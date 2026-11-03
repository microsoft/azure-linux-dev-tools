// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Testing unexported parseBatchJSON.
package sources

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockProcessorTestCtx returns a test context that reports the mock binary as
// present in the search path so MockProcessor calls reach the stubbed command
// factory instead of failing the mock precondition check.
func newMockProcessorTestCtx() *testctx.TestCtx {
	ctx := testctx.NewCtx()
	ctx.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)

	return ctx
}

// Sentinel errors returned by the stubbed command factory so CalculateRelease
// error tests can assert the causes are preserved through %w wrapping.
var (
	errInitBoom        = errors.New("boom")
	errRpmautospecExit = errors.New("exit status 1")
)

func TestParseBatchJSON_Success(t *testing.T) {
	t.Parallel()

	stdout := `[{"name":"curl","specFiles":"Source0: curl-8.5.0.tar.xz\nPatch0: fix.patch","error":null}]`
	inputs := []ComponentInput{{Name: "curl", SpecFilename: "curl.spec"}}

	results, err := parseBatchJSON(stdout, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "curl", results[0].Name)
	require.NoError(t, results[0].Error)
	assert.Equal(t, []string{"curl-8.5.0.tar.xz", "fix.patch"}, results[0].SpecFiles)
}

func TestParseBatchJSON_RpmautospecFailed(t *testing.T) {
	t.Parallel()

	stdout := `[{"name":"broken","specFiles":"","error":"rpmautospec failed: could not process spec"}]`
	inputs := []ComponentInput{{Name: "broken", SpecFilename: "broken.spec"}}

	results, err := parseBatchJSON(stdout, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Error(t, results[0].Error)
	assert.Contains(t, results[0].Error.Error(), "rpmautospec failed")
	assert.Contains(t, results[0].Error.Error(), "could not process spec")
}

func TestParseBatchJSON_SpectoolFailed(t *testing.T) {
	t.Parallel()

	stdout := `[{"name":"badspec","specFiles":"","error":"spectool failed: query of specfile failed"}]`
	inputs := []ComponentInput{{Name: "badspec", SpecFilename: "badspec.spec"}}

	results, err := parseBatchJSON(stdout, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Error(t, results[0].Error)
	assert.Contains(t, results[0].Error.Error(), "spectool failed")
}

func TestParseBatchJSON_MissingComponent(t *testing.T) {
	t.Parallel()

	// JSON doesn't include a result for "ghost".
	stdout := `[]`
	inputs := []ComponentInput{{Name: "ghost", SpecFilename: "ghost.spec"}}

	results, err := parseBatchJSON(stdout, inputs)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Error(t, results[0].Error)
	assert.Contains(t, results[0].Error.Error(), "no result returned")
}

func TestParseBatchJSON_MultipleComponents(t *testing.T) {
	t.Parallel()

	stdout := `[
		{"name":"good","specFiles":"Source0: good-1.0.tar.gz","error":null},
		{"name":"bad","specFiles":"","error":"rpmautospec failed: boom"}
	]`

	inputs := []ComponentInput{
		{Name: "good", SpecFilename: "good.spec"},
		{Name: "bad", SpecFilename: "bad.spec"},
	}

	results, err := parseBatchJSON(stdout, inputs)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.NoError(t, results[0].Error)
	assert.Equal(t, []string{"good-1.0.tar.gz"}, results[0].SpecFiles)
	require.Error(t, results[1].Error)
	assert.Contains(t, results[1].Error.Error(), "boom")
}

func TestParseBatchJSON_InvalidJSON(t *testing.T) {
	t.Parallel()

	inputs := []ComponentInput{{Name: "any", SpecFilename: "any.spec"}}

	_, err := parseBatchJSON("not json{{{", inputs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing batch results JSON")
}

func TestValidateInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		inputs  []ComponentInput
		wantErr bool
		errMsg  string
	}{
		{"valid single", []ComponentInput{{Name: "curl", SpecFilename: "curl.spec"}}, false, ""},
		{"valid multiple", []ComponentInput{
			{Name: "curl", SpecFilename: "curl.spec"},
			{Name: "wget", SpecFilename: "wget.spec"},
		}, false, ""},
		{"empty name", []ComponentInput{{Name: "", SpecFilename: "a.spec"}}, true, "invalid component name"},
		{"dot name", []ComponentInput{{Name: ".", SpecFilename: "a.spec"}}, true, "invalid component name"},
		{"slash in name", []ComponentInput{{Name: "foo/bar", SpecFilename: "a.spec"}}, true, "invalid component name"},
		{"backslash in name", []ComponentInput{{Name: "foo\\bar", SpecFilename: "a.spec"}}, true, "invalid component name"},
		{"dotdot in name", []ComponentInput{{Name: "..", SpecFilename: "a.spec"}}, true, "invalid component name"},
		{"absolute name", []ComponentInput{{Name: "/tmp/evil", SpecFilename: "a.spec"}}, true, "invalid component name"},
		{"null in name", []ComponentInput{{Name: "has\x00null", SpecFilename: "a.spec"}}, true, "invalid component name"},
		{"space in name", []ComponentInput{{Name: "has space", SpecFilename: "a.spec"}}, true, "invalid component name"},
		{"tab in name", []ComponentInput{{Name: "has\ttab", SpecFilename: "a.spec"}}, true, "invalid component name"},
		{"empty spec", []ComponentInput{{Name: "curl", SpecFilename: ""}}, true, "invalid spec filename"},
		{"dot spec", []ComponentInput{{Name: "curl", SpecFilename: "."}}, true, "invalid spec filename"},
		{"dotdot spec", []ComponentInput{{Name: "curl", SpecFilename: ".."}}, true, "invalid spec filename"},
		{"spec with path", []ComponentInput{{Name: "curl", SpecFilename: "sub/curl.spec"}}, true, "invalid spec filename"},
		{
			"spec with backslash",
			[]ComponentInput{{Name: "curl", SpecFilename: "sub\\curl.spec"}},
			true, "invalid spec filename",
		},
		{"spec with null", []ComponentInput{{Name: "curl", SpecFilename: "has\x00null.spec"}}, true, "invalid spec filename"},
		{"duplicate names", []ComponentInput{
			{Name: "curl", SpecFilename: "curl.spec"},
			{Name: "curl", SpecFilename: "curl.spec"},
		}, true, "duplicate component name"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := validateInputs(testCase.inputs)
			if testCase.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNewMockProcessor_IsolatedBaseDir(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	processor := NewMockProcessor(ctx, "/mock/config", WithIsolatedMockBaseDir("/work/provenance"))
	assert.Equal(t, "/work/provenance", processor.runner.BaseDir(),
		"an isolated base dir must be applied so the provenance root cannot collide with the build chroot")
}

func TestNewMockProcessor_DefaultBaseDir(t *testing.T) {
	t.Parallel()

	ctx := testctx.NewCtx()

	// No option, and a blank isolated base dir, both preserve mock's default location.
	assert.Empty(t, NewMockProcessor(ctx, "/mock/config").runner.BaseDir())
	assert.Empty(t, NewMockProcessor(ctx, "/mock/config", WithIsolatedMockBaseDir("")).runner.BaseDir())
}

func TestCalculateRelease_Success(t *testing.T) {
	t.Parallel()

	ctx := newMockProcessorTestCtx()

	// The rpmautospec chroot command is the only call routed through
	// RunAndGetOutput; capture its argv so we can assert on how it was built.
	var chrootArgs []string

	const rawOutput = "Calculated release number: 7\n"

	ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		chrootArgs = cmd.Args

		return rawOutput, nil
	}

	processor := NewMockProcessor(ctx, "/mock/config")

	out, err := processor.CalculateRelease(ctx, "/host/fwupd-efi", "fwupd-efi.spec")
	require.NoError(t, err)

	// The caller is responsible for parsing; CalculateRelease must return the
	// command output verbatim (including trailing newline).
	assert.Equal(t, rawOutput, out)

	joined := strings.Join(chrootArgs, " ")
	assert.Contains(t, joined, "--chroot", "release must be calculated with mock --chroot")
	assert.Contains(t, joined, "--unpriv", "the chroot command must drop privileges to the mockbuild user")
	assert.Contains(t, joined, `bind_mount:dirs=[("/host/fwupd-efi", "/tmp/provenance")]`,
		"the spec directory must be bind-mounted at the provenance mount point")
	assert.Contains(t, joined, "rpmautospec calculate-release --complete-release /tmp/provenance/fwupd-efi.spec",
		"rpmautospec must run against the spec at its in-chroot path")
	assert.Contains(t, joined, "safe.directory",
		"git safe.directory must be configured before rpmautospec reads the .git history")
}

func TestCalculateRelease_InitFailurePropagates(t *testing.T) {
	t.Parallel()

	ctx := newMockProcessorTestCtx()

	// Fail the chroot initialization (`mock --init`) and confirm the error
	// surfaces without ever attempting the rpmautospec command.
	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		if strings.Contains(strings.Join(cmd.Args, " "), "--init") {
			return errInitBoom
		}

		return nil
	}

	rpmautospecCalled := false
	ctx.CmdFactory.RunAndGetOutputHandler = func(_ *exec.Cmd) (string, error) {
		rpmautospecCalled = true

		return "", nil
	}

	processor := NewMockProcessor(ctx, "/mock/config")

	_, err := processor.CalculateRelease(ctx, "/host/fwupd-efi", "fwupd-efi.spec")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize mock chroot")
	require.ErrorIs(t, err, errInitBoom, "the underlying init error must be preserved via %w wrapping")
	assert.False(t, rpmautospecCalled, "rpmautospec must not run when chroot init fails")
}

func TestCalculateRelease_RpmautospecFailureWrapped(t *testing.T) {
	t.Parallel()

	ctx := newMockProcessorTestCtx()

	ctx.CmdFactory.RunAndGetOutputHandler = func(_ *exec.Cmd) (string, error) {
		return "some stderr", errRpmautospecExit
	}

	processor := NewMockProcessor(ctx, "/mock/config")

	out, err := processor.CalculateRelease(ctx, "/host/fwupd-efi", "fwupd-efi.spec")
	require.Error(t, err)
	assert.Empty(t, out, "no release should be returned when rpmautospec fails")
	assert.Contains(t, err.Error(), "rpmautospec calculate-release failed in mock chroot")
	require.ErrorIs(t, err, errRpmautospecExit, "the underlying rpmautospec error must be preserved via %w wrapping")
}

func TestCalculateRelease_InitializesChrootOnce(t *testing.T) {
	t.Parallel()

	ctx := newMockProcessorTestCtx()

	initCount := 0
	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		if strings.Contains(strings.Join(cmd.Args, " "), "--init") {
			initCount++
		}

		return nil
	}
	ctx.CmdFactory.RunAndGetOutputHandler = func(_ *exec.Cmd) (string, error) {
		return "Calculated release number: 1\n", nil
	}

	processor := NewMockProcessor(ctx, "/mock/config")

	// Two resolutions share the same lazily-initialized chroot.
	_, err := processor.CalculateRelease(ctx, "/host/a", "a.spec")
	require.NoError(t, err)

	_, err = processor.CalculateRelease(ctx, "/host/b", "b.spec")
	require.NoError(t, err)

	assert.Equal(t, 1, initCount, "the shared mock chroot must be initialized only once across calls")
}

func TestCalculateRelease_InitErrorCached(t *testing.T) {
	t.Parallel()

	ctx := newMockProcessorTestCtx()

	// Fail initialization and count how many times `mock --init` is attempted.
	// initOnce memoizes the failure, so a second CalculateRelease must return
	// the cached error without retrying the known-broken chroot.
	initCount := 0
	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		if strings.Contains(strings.Join(cmd.Args, " "), "--init") {
			initCount++

			return errInitBoom
		}

		return nil
	}

	processor := NewMockProcessor(ctx, "/mock/config")

	_, firstErr := processor.CalculateRelease(ctx, "/host/a", "a.spec")
	require.Error(t, firstErr)
	require.ErrorIs(t, firstErr, errInitBoom)

	_, secondErr := processor.CalculateRelease(ctx, "/host/b", "b.spec")
	require.Error(t, secondErr)
	require.ErrorIs(t, secondErr, errInitBoom)

	assert.Equal(t, 1, initCount, "a failed mock init must be cached and never retried")
}
