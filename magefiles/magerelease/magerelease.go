// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package magerelease

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/magefile/mage/sh"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
)

var (
	ErrChangelog = errors.New("changelog generation failed")
	ErrRelease   = errors.New("release tagging failed")
)

// Changelog generates a draft changelog section for the next release from the Conventional Commit
// history and prepends it to 'CHANGELOG.md', using git-cliff and the repo's 'cliff.toml'.
//
// The output is a *draft*: review and curate it into user-facing notes before releasing. See
// docs/developer/how-to/releasing.md.
//
// git-cliff must be installed and on PATH; this target does not install it. The same target runs
// locally and in CI (CI just installs git-cliff first), so there is a single changelog flow.
func Changelog() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Generating changelog draft with git-cliff...")

	// git-cliff is an external (non-Go) tool installed separately, so invoke it by name and let
	// PATH resolution happen at execution time (the repo forbids exec.LookPath for tool checks).
	// Probe it once up front so a missing binary surfaces an actionable install hint.
	const cliff = "git-cliff"
	if _, err := sh.Output(cliff, "--version"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return mageutil.PrintAndReturnError(gitCliffInstallHint(), ErrChangelog, err)
		}

		return mageutil.PrintAndReturnError("git-cliff is installed but failed to run.", ErrChangelog, err)
	}

	projectDir := mageutil.AzldevProjectDir()
	configPath := filepath.Join(projectDir, "cliff.toml")
	changelogPath := filepath.Join(projectDir, "CHANGELOG.md")

	// Guard against double-prepending: if CHANGELOG.md already starts with the version git-cliff
	// would bump to, the draft is already present. Re-running would add a duplicate section.
	bumped := bumpedVersion(cliff, configPath)

	top, topErr := latestChangelogVersion()
	if bumped != "" && topErr == nil && top == bumped {
		mageutil.MagePrintf(mageutil.MsgInfo,
			"CHANGELOG.md already has a draft for v%s; nothing to do "+
				"(run 'git restore CHANGELOG.md' to regenerate).\n", bumped)

		return nil
	}

	// --bump computes the next version from the commits; --unreleased limits to commits since the
	// last release tag; --prepend inserts the new section at the top of the existing changelog.
	err := sh.Run(cliff, "--config", configPath, "--bump", "--unreleased", "--prepend", changelogPath)
	if err != nil {
		return mageutil.PrintAndReturnError("git-cliff failed to generate the changelog.", ErrChangelog, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess,
		"Updated %#q. Review and curate the new section before releasing.\n", changelogPath)

	return nil
}

// Release creates the release tag from CHANGELOG.md. It reads the version from the top
// `## [X.Y.Z]` heading and creates a matching annotated git tag (vX.Y.Z) on HEAD, so the tag and
// the changelog can never disagree. It does not push: pushing the tag is the point of no return
// for a release, so that stays an explicit step (manual locally, or a dedicated CI step).
//
// It is idempotent: if the changelog version is already tagged it does nothing, so it is safe to
// trigger on every merge to 'main' and only tags when the changelog carries a new version. Run it
// after the changelog PR has merged (see docs/developer/how-to/releasing.md).
func Release() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Preparing release tag from CHANGELOG.md...")

	version, err := latestChangelogVersion()
	if err != nil {
		return mageutil.PrintAndReturnError("Could not read the release version from CHANGELOG.md.", ErrRelease, err)
	}

	tag := "v" + version

	exists, err := tagExists(tag)
	if err != nil {
		return mageutil.PrintAndReturnError("Could not check existing tags.", ErrRelease, err)
	}

	if exists {
		mageutil.MagePrintf(mageutil.MsgInfo, "Tag %#q already exists; nothing to release.\n", tag)

		return nil
	}

	err = sh.Run("git", "tag", "-a", tag, "-m", tag)
	if err != nil {
		return mageutil.PrintAndReturnError(fmt.Sprintf("Failed to create tag %#q.", tag), ErrRelease, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess, "Created annotated tag %#q (matching CHANGELOG.md).\n", tag)
	mageutil.MagePrintf(mageutil.MsgInfo, "Push it to publish the release: git push origin %s\n", tag)
	mageutil.MagePrintf(mageutil.MsgInfo, "The tag is local only; to remove it before pushing: git tag -d %s\n", tag)

	return nil
}

// latestChangelogVersion returns the version from the first `## [X.Y.Z]` heading in CHANGELOG.md,
// which is the release being prepared.
func latestChangelogVersion() (string, error) {
	changelogPath := filepath.Join(mageutil.AzldevProjectDir(), "CHANGELOG.md")

	content, err := os.ReadFile(changelogPath)
	if err != nil {
		return "", fmt.Errorf("failed to read %#q:\n%w", changelogPath, err)
	}

	// Match a Keep a Changelog version heading, e.g. "## [0.2.0] - 2026-06-25".
	headingRe := regexp.MustCompile(`(?m)^##\s+\[(\d+\.\d+\.\d+)\]`)

	match := headingRe.FindStringSubmatch(string(content))
	if match == nil {
		return "", fmt.Errorf("no '## [X.Y.Z]' version heading found in %#q", changelogPath)
	}

	return match[1], nil
}

// tagExists reports whether the given git tag already exists locally.
func tagExists(tag string) (bool, error) {
	out, err := sh.Output("git", "tag", "--list", tag)
	if err != nil {
		return false, fmt.Errorf("failed to list git tags:\n%w", err)
	}

	return strings.TrimSpace(out) != "", nil
}

// gitCliffInstallHint builds the "git-cliff is missing" message, naming the version pinned in
// tools/git-cliff/Cargo.toml when it can be read.
func gitCliffInstallHint() string {
	const base = "git-cliff not found on PATH. The pin lives in tools/git-cliff/Cargo.toml."

	if version := pinnedGitCliffVersion(); version != "" {
		return fmt.Sprintf("%s Install it (e.g. 'cargo binstall git-cliff@%s' or 'brew install git-cliff'), "+
			"then re-run.", base, version)
	}

	return base + " Install it (e.g. 'cargo binstall git-cliff' or 'brew install git-cliff'), then re-run."
}

// bumpedVersion asks git-cliff for the version it would bump to next (without the leading "v"),
// or "" if it can't be determined (e.g. no eligible commits, or git-cliff fails).
func bumpedVersion(cliff, configPath string) string {
	out, err := sh.Output(cliff, "--config", configPath, "--bumped-version")
	if err != nil {
		return ""
	}

	return strings.TrimPrefix(strings.TrimSpace(out), "v")
}

// pinnedGitCliffVersion returns the git-cliff version pinned in tools/git-cliff/Cargo.toml, or ""
// if it can't be determined. The pin lives there (not in Go) so Dependabot and security scanners
// can track and update it.
func pinnedGitCliffVersion() string {
	cargoPath := filepath.Join(mageutil.AzldevProjectDir(), "tools", "git-cliff", "Cargo.toml")

	content, err := os.ReadFile(cargoPath)
	if err != nil {
		return ""
	}

	match := regexp.MustCompile(`(?m)^\s*git-cliff\s*=\s*"=?(\d+\.\d+\.\d+)"`).FindStringSubmatch(string(content))
	if match == nil {
		return ""
	}

	return match[1]
}
