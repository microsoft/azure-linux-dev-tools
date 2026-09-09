// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/externalcmd"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testBumpspecPath         = "/sources/component.spec"
	testBumpspecHome         = "/work/rpmdev-home"
	testBumpspecOriginalSpec = "Name: component\nVersion: 1.0\nRelease: 4\n"
)

func newBumpspecRequest() RPMDevBumpspecRequest {
	return RPMDevBumpspecRequest{
		SpecPath: testBumpspecPath, Comment: "- rebuilt",
		Identity:  "Azure Linux Packaging Team <azurelinux@microsoft.com>",
		Datestamp: "Mon Jan 06 2025", HomeDir: testBumpspecHome,
		Build: projectconfig.ComponentBuildConfig{
			With: []string{"feature"}, Without: []string{"legacy"},
			Defines: map[string]string{"zeta": "2", "alpha": "1"}, Undefines: []string{"ambient"},
		},
	}
}

func newBumpspecCtx() *testctx.TestCtx {
	osEnv := testctx.NewTestOSEnv()
	osEnv.SetEnv("PATH", "/test/bin")

	ctx := testctx.NewCtx(testctx.WithOSEnv(osEnv))
	for _, binary := range rpmdevBumpspecBinaries() {
		ctx.CmdFactory.RegisterCommandInSearchPath(binary)
	}

	return ctx
}

func writeBumpspecSpec(t *testing.T, ctx *testctx.TestCtx, content string) {
	t.Helper()
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(testBumpspecPath)))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testBumpspecPath, []byte(content), fileperms.PublicFile))
}

func evrOutput(name, epoch, release string) string {
	return strings.Join([]string{name, epoch, "1.0", release}, rpmdevBumpspecEVRSeparator) + "\n"
}

func isEVRQuery(cmd *exec.Cmd) bool {
	return len(cmd.Args) >= 3 && cmd.Args[0] == rpmspecBinary &&
		slices.Contains(cmd.Args, "-q") && slices.Contains(cmd.Args, "--srpm")
}

func isReleaseComparison(cmd *exec.Cmd) bool {
	return len(cmd.Args) >= 2 && cmd.Args[0] == python3Binary && cmd.Args[1] == "-c"
}

func TestRunRPMDevBumpspec_UsesHostEVRAndFixedContext(t *testing.T) {
	ctx := newBumpspecCtx()
	request := newBumpspecRequest()
	before := "Name: component\nVersion: 1.0\nRelease: 4%{?dist}\n%changelog\n"
	after := "Name: component\nVersion: 1.0\nRelease: 5%{?dist}\n%changelog\n- rebuilt\n"

	writeBumpspecSpec(t, ctx, before)

	queries := 0

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		switch {
		case isEVRQuery(cmd):
			queries++

			release := "5"
			if queries == 1 {
				release = "4"
			}

			_, _ = fmt.Fprint(cmd.Stdout, evrOutput("component", "0", release))
		case cmd.Path == RPMDevBumpspecBinary:
			assert.Equal(t, []string{
				RPMDevBumpspecBinary, "-c", "- rebuilt", "-u",
				"Azure Linux Packaging Team <azurelinux@microsoft.com>", "-d", "Mon Jan 06 2025",
				testBumpspecPath,
			}, cmd.Args)
			require.NoError(t, fileutils.WriteFile(ctx.FS(), testBumpspecPath, []byte(after), fileperms.PublicFile))
		case isReleaseComparison(cmd):
			assert.Equal(t, []string{python3Binary, "-c", rpmLabelCompareProgram, "0", "1.0", "4", "0", "1.0", "5"}, cmd.Args)
			_, _ = fmt.Fprintln(cmd.Stdout, "-1")
		default:
			return fmt.Errorf("unexpected command: %#v", cmd.Args)
		}

		return nil
	}

	require.NoError(t, RunRPMDevBumpspec(ctx, request))
	content, err := fileutils.ReadFile(ctx.FS(), testBumpspecPath)
	require.NoError(t, err)
	assert.Equal(t, after, string(content))

	wrapperPath := filepath.Join(testBumpspecHome, rpmdevBumpspecBinDirName, rpmdevBumpspecRPMWrapper)
	wrapper, err := fileutils.ReadFile(ctx.FS(), wrapperPath)
	require.NoError(t, err)
	assert.Contains(t, string(wrapper), "PATH=\"$RPMDEV_BUMPSPEC_ORIGINAL_PATH\"")
	assert.Contains(t, string(wrapper), "'--define' '_sourcedir /sources'")
	assert.NotContains(t, string(wrapper), "'--with'")
	assert.NotContains(t, string(wrapper), "'--without'")
	assert.Contains(t, string(wrapper),
		"'--define' '_with_feature 1' '--define' '_without_legacy 1' '--define' 'alpha 1' '--define' 'zeta 2'")
	assert.Equal(t, 2, queries)
}

func TestRunRPMDevBumpspec_RestoresOriginalBytesOnEveryFailure(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		handler func(*exec.Cmd, *testctx.TestCtx) error
	}{
		{
			name: "pre mutation evaluation failure",
			handler: func(cmd *exec.Cmd, _ *testctx.TestCtx) error {
				if isEVRQuery(cmd) {
					return errors.New("before query failed")
				}

				return errors.New("bumpspec must not run")
			},
		},
		{
			name: "command failure after mutation",
			handler: func(cmd *exec.Cmd, ctx *testctx.TestCtx) error {
				if isEVRQuery(cmd) {
					_, _ = fmt.Fprint(cmd.Stdout, evrOutput("component", "0", "4"))

					return nil
				}
				if cmd.Path == RPMDevBumpspecBinary {
					require.NoError(t, fileutils.WriteFile(ctx.FS(), testBumpspecPath,
						[]byte("Name: component\nVersion: 1.0\nRelease: 5\n"), fileperms.PublicFile))

					return errors.New("bumpspec failed")
				}

				return nil
			},
		},
		{
			name: "post mutation evaluation failure",
			handler: func(cmd *exec.Cmd, ctx *testctx.TestCtx) error {
				if isEVRQuery(cmd) {
					if strings.Contains(stringMustRead(t, ctx, testBumpspecPath), "Release: 5") {
						return errors.New("after query failed")
					}
					_, _ = fmt.Fprint(cmd.Stdout, evrOutput("component", "0", "4"))

					return nil
				}
				if cmd.Path == RPMDevBumpspecBinary {
					return fileutils.WriteFile(ctx.FS(), testBumpspecPath,
						[]byte("Name: component\nVersion: 1.0\nRelease: 5\n"), fileperms.PublicFile)
				}

				return nil
			},
		},
		{
			name: "unchanged malformed or decreased release",
			handler: func(cmd *exec.Cmd, ctx *testctx.TestCtx) error {
				if isEVRQuery(cmd) {
					release := "4"
					if strings.Contains(stringMustRead(t, ctx, testBumpspecPath), "Release: 5") {
						release = ""
					}
					_, _ = fmt.Fprint(cmd.Stdout, evrOutput("component", "0", release))

					return nil
				}
				if cmd.Path == RPMDevBumpspecBinary {
					return fileutils.WriteFile(ctx.FS(), testBumpspecPath,
						[]byte("Name: component\nVersion: 1.0\nRelease: 5\n"), fileperms.PublicFile)
				}

				return nil
			},
		},
		{
			name: "identity changes",
			handler: func(cmd *exec.Cmd, ctx *testctx.TestCtx) error {
				if isEVRQuery(cmd) {
					if strings.Contains(stringMustRead(t, ctx, testBumpspecPath), "Release: 5") {
						_, _ = fmt.Fprint(cmd.Stdout, evrOutput("other", "0", "5"))
					} else {
						_, _ = fmt.Fprint(cmd.Stdout, evrOutput("component", "0", "4"))
					}

					return nil
				}
				if cmd.Path == RPMDevBumpspecBinary {
					return fileutils.WriteFile(ctx.FS(), testBumpspecPath,
						[]byte("Name: component\nVersion: 1.0\nRelease: 5\n"), fileperms.PublicFile)
				}

				return nil
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := newBumpspecCtx()
			request := newBumpspecRequest()
			original := "Name: component\nVersion: 1.0\nRelease: 4\n%changelog\n"
			writeBumpspecSpec(t, ctx, original)
			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error { return testCase.handler(cmd, ctx) }
			require.Error(t, RunRPMDevBumpspec(ctx, request))
			assert.Equal(t, original, stringMustRead(t, ctx, testBumpspecPath))
		})
	}
}

func TestRunRPMDevBumpspec_RequiresRPMReleaseIncrease(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		comparisonOut string
		comparisonErr error
		wantSuccess   bool
	}{
		{name: "newer result", comparisonOut: "-1\n", wantSuccess: true},
		{name: "unchanged result", comparisonOut: "0\n"},
		{name: "older result", comparisonOut: "1\n"},
		{name: "noncanonical negative result", comparisonOut: "-01\n"},
		{name: "noncanonical positive result", comparisonOut: "+1\n"},
		{name: "leading space", comparisonOut: " -1\n"},
		{name: "trailing space", comparisonOut: "-1 \n"},
		{name: "empty result"},
		{name: "extra newline", comparisonOut: "-1\n\n"},
		{name: "CRLF", comparisonOut: "-1\r\n"},
		{name: "arbitrary text", comparisonOut: "not an integer\n"},
		{name: "comparison command failure", comparisonErr: errors.New("python failed")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := newBumpspecCtx()
			request := newBumpspecRequest()
			original := testBumpspecOriginalSpec
			after := "Name: component\nVersion: 1.0\nRelease: 5\n"

			writeBumpspecSpec(t, ctx, original)

			queryCount := 0
			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				switch {
				case isEVRQuery(cmd):
					queryCount++

					release := "4"
					if queryCount == 2 {
						release = "5"
					}

					_, _ = fmt.Fprint(cmd.Stdout, evrOutput("component", "(none)", release))
				case cmd.Path == RPMDevBumpspecBinary:
					return fileutils.WriteFile(ctx.FS(), testBumpspecPath, []byte(after), fileperms.PublicFile)
				case isReleaseComparison(cmd):
					if testCase.comparisonErr != nil {
						return testCase.comparisonErr
					}

					_, _ = fmt.Fprint(cmd.Stdout, testCase.comparisonOut)
				default:
					return fmt.Errorf("unexpected command: %#v", cmd.Args)
				}

				return nil
			}

			err := RunRPMDevBumpspec(ctx, request)
			assert.Equal(t, testCase.wantSuccess, err == nil)

			if !testCase.wantSuccess {
				require.ErrorIs(t, err, ErrRPMDevBumpspecNoMutation)
				assert.Equal(t, original, stringMustRead(t, ctx, testBumpspecPath))
			} else {
				assert.Equal(t, after, stringMustRead(t, ctx, testBumpspecPath))
			}

			assert.Equal(t, 2, queryCount)
		})
	}
}

func TestRunRPMDevBumpspec_RuntimeAndDryRun(t *testing.T) {
	t.Run("missing python is actionable", func(t *testing.T) {
		ctx := testctx.NewCtx()
		for _, binary := range []string{RPMDevBumpspecBinary, rpmdevPackagerBinary, rpmBinary, rpmspecBinary} {
			ctx.CmdFactory.RegisterCommandInSearchPath(binary)
		}

		err := RunRPMDevBumpspec(ctx, newBumpspecRequest())
		require.ErrorIs(t, err, externalcmd.ErrMissingExecutable)
		assert.Contains(t, err.Error(), python3Binary)
	})
	t.Run("dry run delegates only bumpspec", func(t *testing.T) {
		ctx := newBumpspecCtx()
		ctx.DryRunValue = true
		request := newBumpspecRequest()

		writeBumpspecSpec(t, ctx, "Name: component\nVersion: 1.0\nRelease: 4\n")
		ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
			assert.Equal(t, RPMDevBumpspecBinary, cmd.Path)

			return nil
		}
		require.NoError(t, RunRPMDevBumpspec(ctx, request))
	})
	t.Run("cancelled before runtime discovery", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		cancel()

		ctx := testctx.NewCtx(func(ctx *testctx.TestCtx) { ctx.Ctx = base })
		require.ErrorIs(t, RunRPMDevBumpspec(ctx, newBumpspecRequest()), context.Canceled)
	})
}

func TestParseRPMDevBumpspecSourceEVR_RequiresOneCanonicalRecord(t *testing.T) {
	valid := evrOutput("component", "(none)", "4")

	for _, testCase := range []struct {
		name    string
		output  string
		wantEVR RPMDevBumpspecSourceEVR
	}{
		{
			name: "valid record with one terminal newline", output: valid,
			wantEVR: RPMDevBumpspecSourceEVR{Name: "component", Epoch: "0", Version: "1.0", Release: "4"},
		},
		{name: "missing terminal newline", output: strings.TrimSuffix(valid, "\n")},
		{name: "extra terminal newline", output: valid + "\n"},
		{name: "CRLF", output: strings.TrimSuffix(valid, "\n") + "\r\n"},
		{name: "stdout prefix before Name", output: "banner\n" + valid},
		{name: "newline in Name", output: evrOutput("component\nextra", "0", "4")},
		{name: "CR in Name", output: evrOutput("component\rextra", "0", "4")},
		{name: "newline in Epoch", output: evrOutput("component", "0\nextra", "4")},
		{name: "CR in Epoch", output: evrOutput("component", "0\rextra", "4")},
		{name: "newline in Version", output: "component" + rpmdevBumpspecEVRSeparator + "0" +
			rpmdevBumpspecEVRSeparator + "1.0\nextra" + rpmdevBumpspecEVRSeparator + "4\n"},
		{name: "CR in Version", output: "component" + rpmdevBumpspecEVRSeparator + "0" +
			rpmdevBumpspecEVRSeparator + "1.0\rextra" + rpmdevBumpspecEVRSeparator + "4\n"},
		{name: "newline in Release", output: evrOutput("component", "0", "4\nextra")},
		{name: "CR in Release", output: evrOutput("component", "0", "4\rextra")},
		{name: "suffix after record", output: valid + "suffix"},
		{name: "wrong field count", output: "component" + rpmdevBumpspecEVRSeparator + "0\n"},
		{name: "empty field", output: evrOutput("component", "0", "")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			evr, err := parseRPMDevBumpspecSourceEVR(testCase.output)
			if testCase.wantEVR == (RPMDevBumpspecSourceEVR{}) {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantEVR, evr)
		})
	}
}

func TestRunRPMDevBumpspec_RestoresOriginalBytesAfterMalformedEVROutput(t *testing.T) {
	for _, malformedOutput := range []string{
		"banner\n" + evrOutput("component", "0", "5"),
		evrOutput("component", "0", "5") + "\n",
	} {
		t.Run(fmt.Sprintf("%q", malformedOutput), func(t *testing.T) {
			ctx := newBumpspecCtx()
			request := newBumpspecRequest()
			original := testBumpspecOriginalSpec
			after := "Name: component\nVersion: 1.0\nRelease: 5\n"

			writeBumpspecSpec(t, ctx, original)

			queryCount := 0
			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				switch {
				case isEVRQuery(cmd):
					queryCount++
					if queryCount == 1 {
						_, _ = fmt.Fprint(cmd.Stdout, evrOutput("component", "0", "4"))
					} else {
						_, _ = fmt.Fprint(cmd.Stdout, malformedOutput)
					}
				case cmd.Path == RPMDevBumpspecBinary:
					return fileutils.WriteFile(ctx.FS(), testBumpspecPath, []byte(after), fileperms.PublicFile)
				}

				return nil
			}

			require.Error(t, RunRPMDevBumpspec(ctx, request))
			assert.Equal(t, original, stringMustRead(t, ctx, testBumpspecPath))
		})
	}
}

func TestRunRPMDevBumpspec_UsesOneSRPMRecordAndRollsBackMultipleRecords(t *testing.T) {
	t.Run("source record is accepted for a multi-subpackage spec", func(t *testing.T) {
		ctx := newBumpspecCtx()
		request := newBumpspecRequest()
		before := "Name: component\nVersion: 1.0\nRelease: 4\n%package child\nSummary: child\n"
		after := strings.Replace(before, "Release: 4", "Release: 5", 1)
		writeBumpspecSpec(t, ctx, before)

		queryCount := 0
		ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
			switch {
			case isEVRQuery(cmd):
				queryCount++

				release := "4"
				if queryCount == 2 {
					release = "5"
				}

				_, _ = fmt.Fprint(cmd.Stdout, evrOutput("component", "0", release))
			case cmd.Path == RPMDevBumpspecBinary:
				return fileutils.WriteFile(ctx.FS(), testBumpspecPath, []byte(after), fileperms.PublicFile)
			case isReleaseComparison(cmd):
				_, _ = fmt.Fprintln(cmd.Stdout, "-1")
			}

			return nil
		}

		require.NoError(t, RunRPMDevBumpspec(ctx, request))
		assert.Equal(t, after, stringMustRead(t, ctx, testBumpspecPath))
		assert.Equal(t, 2, queryCount)
	})

	t.Run("multiple records are rejected and restored", func(t *testing.T) {
		ctx := newBumpspecCtx()
		request := newBumpspecRequest()
		original := testBumpspecOriginalSpec
		writeBumpspecSpec(t, ctx, original)
		ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
			if isEVRQuery(cmd) {
				_, _ = fmt.Fprint(cmd.Stdout, evrOutput("component", "0", "4")+evrOutput("component-child", "0", "4"))
			}

			return nil
		}

		require.Error(t, RunRPMDevBumpspec(ctx, request))
		assert.Equal(t, original, stringMustRead(t, ctx, testBumpspecPath))
	})
}

func TestRunRPMDevBumpspec_AppliesTargetToQueryAndWrapper(t *testing.T) {
	for _, target := range []string{"x86_64", "aarch64"} {
		t.Run(target, func(t *testing.T) {
			ctx := newBumpspecCtx()
			request := newBumpspecRequest()
			request.TargetArch = target
			original := "Name: component\nVersion: 1.0\nRelease: %{?target}\n"
			writeBumpspecSpec(t, ctx, original)

			queryCount := 0
			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				switch {
				case isEVRQuery(cmd):
					assert.Contains(t, cmd.Args, "--target="+target)

					queryCount++

					release := "9"
					if target == "aarch64" {
						release = "5"
					}

					if queryCount == 2 {
						release += ".1"
					}

					_, _ = fmt.Fprint(cmd.Stdout, evrOutput("component", "0", release))
				case cmd.Path == RPMDevBumpspecBinary:
					return fileutils.WriteFile(ctx.FS(), testBumpspecPath,
						[]byte("Name: component\nVersion: 1.0\nRelease: changed\n"), fileperms.PublicFile)
				case isReleaseComparison(cmd):
					_, _ = fmt.Fprintln(cmd.Stdout, "-1")
				}

				return nil
			}

			require.NoError(t, RunRPMDevBumpspec(ctx, request))
			wrapper, err := fileutils.ReadFile(ctx.FS(),
				filepath.Join(testBumpspecHome, rpmdevBumpspecBinDirName, rpmdevBumpspecRPMWrapper))
			require.NoError(t, err)
			assert.Contains(t, string(wrapper), "'--target="+target+"'")
		})
	}
}

func TestRPMDevBumpspecContextArgs_UsesEffectiveBuildMacros(t *testing.T) {
	request := newBumpspecRequest()
	request.Build.Defines["_with_feature"] = "explicit"
	request.Build.Undefines = append(request.Build.Undefines, "_without_legacy")

	assert.Equal(t, []string{
		"--define", "_sourcedir /sources",
		"--define", "_specdir /sources",
		"--define", "_with_feature explicit",
		"--define", "alpha 1",
		"--define", "zeta 2",
	}, rpmdevBumpspecContextArgs(request))
}

func stringMustRead(t *testing.T, ctx *testctx.TestCtx, path string) string {
	t.Helper()

	content, err := fileutils.ReadFile(ctx.FS(), path)
	require.NoError(t, err)

	return string(content)
}
