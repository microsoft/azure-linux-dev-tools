// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build e2e

// Package scenario_tests — end-to-end (e2e) tests that exercise azldev against
// real, large upstream repositories. These tests are heavier than the standard
// scenario tests (they require network access, clone hundreds of MB of git
// history, and may exercise mock chroots across thousands of components), so
// they are gated behind the dedicated 'e2e' build tag and run from the
// 'mage e2e' target rather than the default 'mage scenario'.
//
// They reuse the scenario framework (containerized cmdtest) but skip the
// in-memory project synthesis layer because the project under test is the
// upstream repository itself.
package scenario_tests

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kballard/go-shellquote"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/stretchr/testify/require"
)

const (
	// azureLinuxRepoURL is the upstream microsoft/azurelinux git repo used as
	// the substrate for the e2e idempotency tests. Pinned to a public HTTPS
	// URL so no credentials are required.
	azureLinuxRepoURL = "https://github.com/microsoft/azurelinux"

	// azureLinuxBranch is the canonical azldev-managed branch in the upstream
	// repo. HEAD of this branch is expected to be in a steady state with
	// respect to 'azldev component update -a' and 'azldev component render -a
	// --clean-stale': running either should be a no-op against a fresh clone.
	// If these tests start failing, the most likely cause is that the upstream
	// branch has drifted and needs a refresh — not an azldev regression — but
	// the failure messages below help disambiguate.
	azureLinuxBranch = "4.0"
)

// runAzureLinuxIdempotencyTest clones the upstream microsoft/azurelinux
// repository at [azureLinuxBranch] inside a privileged, networked scenario
// container, runs `azldev <azldevArgs...>` from the clone root, and asserts
// that the working tree is clean afterwards (no modified, deleted, or
// untracked files).
//
// The script also performs lightweight pre-flight sanity checks against the
// upstream layout, prints the resolved upstream commit SHA for traceability,
// and emits a verbose diagnostic block (full status, diff, untracked listing)
// before exiting non-zero on failure. This makes the difference between an
// azldev regression and an upstream-branch drift easy to spot in CI logs.
//
// fullHistory controls clone depth. 'azldev component render' walks the
// project repo's git log to count lock-file fingerprint changes (used to
// determine the synthetic-commit count and the Release-tag bump); a shallow
// (--depth=1) clone hides that history and produces a smaller bump than the
// upstream rendered output recorded, so render idempotency tests must clone
// with full history. 'component update' does not walk history and is fine
// with a shallow clone.
func runAzureLinuxIdempotencyTest(t *testing.T, azldevArgs []string, fullHistory bool) {
	t.Helper()

	// Quote once so the argv ends up safely embedded in the bash script and
	// also reproduced verbatim in the failure message.
	azldevCmdLine := shellquote.Join(azldevArgs...)

	// --single-branch keeps metadata minimal in either mode. The depth flag
	// is conditional: render needs full history (see fullHistory doc above),
	// update can take the much faster shallow path.
	cloneDepthFlag := "--depth=1"
	if fullHistory {
		cloneDepthFlag = ""
	}

	script := fmt.Sprintf(`
set -ex

# Clone the upstream branch. See [runAzureLinuxIdempotencyTest] for why depth
# differs between update and render tests.
git clone %[4]s --single-branch --branch=%[1]s %[2]s azurelinux
cd azurelinux

echo "Resolved upstream commit:"
git rev-parse HEAD

# Pre-flight sanity checks: fail loudly with a clear message if the upstream
# layout has drifted in a way that would otherwise produce a confusing
# downstream azldev error.
test -f azldev.toml         || { echo "FAIL: missing azldev.toml at clone root"; exit 1; }
test -f base/project.toml   || { echo "FAIL: missing base/project.toml"; exit 1; }
test -d specs               || { echo "FAIL: missing rendered-specs directory 'specs/'"; exit 1; }

# Pre-create the build dir (azldev/mock will materialize work/logs subdirs
# inside it). base/project.toml sets
#   work-dir = 'build/work'   log-dir = 'build/logs'
# which azldev resolves relative to the *defining* config file, so the
# effective parent is base/build, NOT <repo-root>/build/. Note: this MUST be
# a real directory, not a symlink to /var/lib/mock — git's .gitignore pattern
# 'build/' (with trailing slash) only matches directories; a symlink is
# treated as a file and would show up as untracked. azldev passes --rootdir
# to mock so the chroot lives under base/build, no /var/lib/mock dependency.
mkdir -p base/build

# Run the command under test. 'set -e' above ensures a non-zero azldev exit
# fails the test immediately.
echo "Running: azldev %[3]s"
azldev %[3]s

# Check the worktree. --porcelain produces stable, machine-readable output and
# includes untracked files by default. Anything reported here means the
# command was not idempotent against a freshly-checked-out upstream.
status_output=$(git status --porcelain)
if [[ -n "$status_output" ]]; then
	echo
	echo "================================================================="
	echo "FAILURE: 'azldev %[3]s' left the working tree dirty."
	echo "================================================================="
	echo "Most likely causes:"
	echo "  1. azldev regression: the command is no longer idempotent."
	echo "  2. Upstream drift: %[2]s @ %[1]s has fallen out of"
	echo "     sync with what 'azldev %[3]s' produces. Refresh the upstream"
	echo "     branch and try again."
	echo
	echo "--- git status --porcelain ---"
	echo "$status_output"
	echo
	echo "--- git status (full) ---"
	git status
	echo
	echo "--- git diff (tracked changes) ---"
	git --no-pager diff
	echo
	echo "--- git diff --cached ---"
	git --no-pager diff --cached
	echo
	echo "--- untracked files ---"
	git ls-files --others --exclude-standard
	exit 1
fi

echo "OK: working tree is clean after 'azldev %[3]s'"
`, azureLinuxBranch, azureLinuxRepoURL, azldevCmdLine, cloneDepthFlag)

	results, err := cmdtest.NewScenarioTest().
		WithScript(strings.NewReader(script)).
		InContainer().
		WithPrivilege().
		WithNetwork().
		Run(t)

	require.NoError(t, err)

	t.Logf("Standard output:\n%s", results.Stdout)
	t.Logf("Standard error:\n%s", results.Stderr)

	results.AssertZeroExitCode(t)
}

// TestAzureLinuxComponentUpdateIsIdempotent verifies that running
// 'azldev component update -a' against an upstream microsoft/azurelinux
// checkout at HEAD of [azureLinuxBranch] leaves no modifications in the
// working tree. In other words, the lock files committed in the upstream
// branch are already fresh against the resolved upstream snapshots, and
// 'update -a' is a no-op.
//
// Intentionally not parallel: see also [TestAzureLinuxComponentRenderIsIdempotent].
// Both tests clone hundreds of MB and may exercise mock; running them
// concurrently on a single GitHub runner doubles disk/network/mock pressure.
func TestAzureLinuxComponentUpdateIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test")
	}

	runAzureLinuxIdempotencyTest(t, []string{"component", "update", "-a"}, false)
}

// TestAzureLinuxComponentRenderSubsetIsIdempotent is a smaller, faster variant
// of [TestAzureLinuxComponentRenderIsIdempotent] that uses --check-only over a
// hand-picked subset of components. It is meant as a smoke test for the e2e
// framework itself (clone, container, mock initialization, render plumbing)
// and as a fast iteration target while diagnosing regressions: a single
// failing component here will reproduce in seconds-to-a-minute rather than
// the many minutes a full --all render across thousands of specs takes.
//
// --check-only renders into a staging area and exits non-zero on drift
// without ever touching the working tree, so the post-run 'git status' check
// inherited from [runAzureLinuxIdempotencyTest] still applies but is largely
// redundant: the real failure signal comes from the azldev exit code.
//
// The component selection is deliberately small and stable (well-known core
// packages: bash + curl). Adding more components is fine but each one
// extends the runtime by another mock-render pass.
func TestAzureLinuxComponentRenderSubsetIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test")
	}

	// Render needs full history; see [runAzureLinuxIdempotencyTest].
	runAzureLinuxIdempotencyTest(t,
		[]string{"component", "render", "--check-only", "bash", "curl"},
		true)
}

// TestAzureLinuxComponentRenderIsIdempotent verifies that running
// 'azldev component render -a --clean-stale' against an upstream
// microsoft/azurelinux checkout at HEAD of [azureLinuxBranch] leaves no
// modifications in the working tree. In other words, the rendered specs
// committed in the upstream branch match what 'render -a' would produce now
// (and there are no orphan rendered-spec directories that --clean-stale would
// prune).
//
// Intentionally not parallel — see [TestAzureLinuxComponentUpdateIsIdempotent].
func TestAzureLinuxComponentRenderIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test")
	}

	runAzureLinuxIdempotencyTest(t,
		[]string{"component", "render", "-a", "--clean-stale"},
		true)
}
