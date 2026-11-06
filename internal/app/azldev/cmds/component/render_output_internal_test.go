// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/dirdiff"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const (
	testRenderOutputDir       = "/project/SPECS/c/curl"
	renderTestDiffCommand     = "diff"
	renderTestResetCommand    = "reset"
	renderTestCheckoutCommand = "checkout"
	renderTestOverwrite       = "overwrite"
	renderTestSpecDiff        = "SPECS/c/curl/curl.spec\n"
)

func TestLocklessRenderNEVRBeforeReplacement(t *testing.T) {
	for _, testCase := range []struct {
		name, existing, rendered, newName string
		reject, queryFails, toolMissing   bool
	}{
		{name: "numeric release increase", existing: "1-9", rendered: "1-10"},
		{name: "equal", existing: "1-9", rendered: "1-9", reject: true},
		{name: "version downgrade", existing: "2-1", rendered: "1-99", reject: true},
		{name: "epoch increase", existing: "9-9", rendered: "1:1-1"},
		{name: "epoch decrease", existing: "1:1-1", rendered: "9-9", reject: true},
		{name: "prerelease downgrade", existing: "1-1", rendered: "1~rc1-2", reject: true},
		{name: "name change", existing: "1-1", rendered: "2-1", newName: "other", reject: true},
		{name: "query failure", existing: "1-1", rendered: "2-1", queryFails: true, reject: true},
		{name: "missing rpmspec", existing: "1-1", rendered: "2-1", toolMissing: true, reject: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			setupRenderOutput(t, env.TestFS, true)

			if !testCase.toolMissing {
				env.CmdFactory.RegisterCommandInSearchPath("rpmspec")
			}

			prep := &preparedComponent{
				lockless: true, specFilename: "curl.spec", compOutputDir: testRenderOutputDir,
			}
			existing, err := rpm.NewVersion(testCase.existing)
			require.NoError(t, err)
			rendered, err := rpm.NewVersion(testCase.rendered)
			require.NoError(t, err)

			newName := "curl"
			if testCase.newName != "" {
				newName = testCase.newName
			}

			queries := 0
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				queries++

				assert.False(t, prep.outputTouched, "NEVR checks must precede removal and recovery state")
				assertOriginalRenderOutput(t, env.TestFS, true)

				dir := testRenderOutputDir

				name, version := "curl", existing
				if queries == 2 {
					dir, name, version = "/staging/curl", newName, rendered
				}

				assert.Equal(t, dir, cmd.Dir)
				assert.Equal(t, []string{
					"rpmspec", "-q", "--srpm", "--define", "_sourcedir " + dir,
					"--define", "_specdir " + dir, "--queryformat", rpm.SrpmQueryFormat,
					filepath.Join(dir, "curl.spec"),
				}, cmd.Args)

				if testCase.queryFails {
					return "", errors.New("rpmspec failed")
				}

				return "name=" + name + "\nepoch=" + strconv.Itoa(version.Epoch()) +
					"\nversion=" + version.Version() + "\nrelease=" + version.Release() + "\n", nil
			}

			err = moveRenderedOutput(env.Env, "/staging/curl", prep, true)
			if testCase.reject {
				require.ErrorIs(t, err, errRenderNEVRValidation)
				assertOriginalRenderOutput(t, env.TestFS, true)
				assert.False(t, prep.outputTouched)

				if !testCase.queryFails && !testCase.toolMissing {
					require.ErrorContains(t, err, "existing `curl-"+testCase.existing+"`")
					require.ErrorContains(t, err, "new `"+newName+"-"+testCase.rendered+"`")
				}

				for _, failOnError := range []bool{false, true} {
					require.ErrorIs(t, checkRenderErrors([]*RenderResult{{
						Component: "curl", Status: renderStatusError, renderErr: err,
					}}, failOnError), errRenderNEVRValidation)
				}
			} else {
				require.NoError(t, err)
				assert.True(t, prep.outputTouched)

				content, readErr := fileutils.ReadFile(env.TestFS, filepath.Join(testRenderOutputDir, "sidecar"))
				require.NoError(t, readErr)
				assert.Equal(t, "new sidecar", string(content))
			}

			switch {
			case testCase.toolMissing:
				assert.Zero(t, queries)
			case testCase.queryFails:
				assert.Equal(t, 1, queries)
			default:
				assert.Equal(t, 2, queries)
			}
		})
	}
}

func TestRenderNEVRSkipsFirstAndLockedRenders(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(strconv.FormatBool(existing), func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			setupRenderOutput(t, env.TestFS, existing)
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				t.Fatalf("must not query NEVR for first or locked output: %v", cmd.Args)

				return "", nil
			}
			require.NoError(t, moveRenderedOutput(env.Env, "/staging/curl", &preparedComponent{
				lockless: !existing, specFilename: "curl.spec", compOutputDir: testRenderOutputDir,
			}, true))
		})
	}
}

func stubIncreasingRenderNEVR(t *testing.T, env *testutils.TestEnv) {
	t.Helper()

	env.CmdFactory.RegisterCommandInSearchPath("rpmspec")
	original := env.CmdFactory.RunAndGetOutputHandler
	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		if cmd.Args[0] == "rpmspec" {
			release := "1"
			if cmd.Dir == "/staging/curl" {
				release = "2"
			}

			return "name=curl\nepoch=0\nversion=1\nrelease=" + release + "\n", nil
		}

		if original != nil {
			return original(cmd)
		}

		t.Fatalf("unexpected command: %v", cmd.Args)

		return "", nil
	}
}

func writeOriginalRenderOutput(t *testing.T, fileSystem afero.Fs, root string) {
	t.Helper()

	for name, content := range map[string]string{
		"curl.spec": "old spec", "extras/stale.patch": "old sidecar", ".no-change-rebuild": "old timestamp",
	} {
		require.NoError(t, fileutils.WriteFile(fileSystem, filepath.Join(root, name),
			[]byte(content), fileperms.PublicFile))
	}
}

func setupRenderOutput(t *testing.T, fileSystem afero.Fs, existing bool) {
	t.Helper()

	for name, content := range map[string]string{
		"curl.spec": "Name: curl\nVersion: 1\nRelease: 1\n", "sidecar": "new sidecar", ".git/HEAD": "upstream",
	} {
		require.NoError(t, fileutils.WriteFile(fileSystem, filepath.Join("/staging/curl", name),
			[]byte(content), fileperms.PublicFile))
	}

	if existing {
		writeOriginalRenderOutput(t, fileSystem, testRenderOutputDir)
		writeOriginalRenderOutput(t, fileSystem, "/before")
	}
}

func assertOriginalRenderOutput(t *testing.T, fileSystem afero.Fs, existing bool) {
	t.Helper()

	exists, err := fileutils.DirExists(fileSystem, testRenderOutputDir)
	require.NoError(t, err)
	assert.Equal(t, existing, exists)

	if existing {
		diff, err := dirdiff.DiffDirs(fileSystem, "/before", testRenderOutputDir)
		require.NoError(t, err)
		assert.Empty(t, diff.Files)
	}
}

func mockRenderRecovery(t *testing.T, env *testutils.TestEnv) *[]string {
	t.Helper()

	var commands []string

	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		exists, err := fileutils.Exists(env.TestFS, testRenderOutputDir)
		require.NoError(t, err)
		assert.False(t, exists, "failed output must be cleared before Git recovery")

		command := cmd.Args[3]
		commands = append(commands, command)

		switch command {
		case renderTestResetCommand:
			assert.Equal(t, []string{"git", "-C", "/project", renderTestResetCommand, "--quiet", "--", "SPECS/c/curl"}, cmd.Args)
		case renderTestCheckoutCommand:
			assert.Equal(t, []string{"git", "-C", "/project", renderTestCheckoutCommand, "HEAD", "--", "SPECS/c/curl"}, cmd.Args)
			writeOriginalRenderOutput(t, env.TestFS, testRenderOutputDir)
		default:
			require.FailNow(t, "unexpected Git command", "%v", cmd.Args)
		}

		return "", nil
	}

	return &commands
}

func TestMoveLocklessOutput_PublicationFailure(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, failure := range []string{"processing", renderTestOverwrite, "remove", "move"} {
			if (failure == renderTestOverwrite || failure == "remove") && !existing {
				continue
			}

			t.Run(failure+"/"+strconv.FormatBool(existing), func(t *testing.T) {
				env := testutils.NewTestEnv(t)
				setupRenderOutput(t, env.TestFS, existing)
				env.TestFS = &renderOutputFaultFS{Fs: env.TestFS, failure: failure}
				commands := mockRenderRecovery(t, env)
				comp := components_testutils.NewMockComponent(gomock.NewController(t))
				comp.EXPECT().GetName().AnyTimes().Return("curl")
				prep := &preparedComponent{
					lockless: true, comp: comp, specFilename: "curl.spec", compOutputDir: testRenderOutputDir,
				}

				mockResult := &sources.ComponentMockResult{}
				if failure == "processing" {
					mockResult.Error = errors.New("injected processing failure")
				}

				stubIncreasingRenderNEVR(t, env)
				result := finishOneComponent(env.Env, prep,
					map[string]*sources.ComponentMockResult{"curl": mockResult},
					"/staging", failure != renderTestOverwrite, false, false)
				require.Error(t, result.renderErr)
				commitLocklessRenders(env.Env, []*preparedComponent{prep}, []*RenderResult{result}, false)
				writeFailureMarkers(env.TestFS, []*RenderResult{result}, true, false)
				assertOriginalRenderOutput(t, env.TestFS, existing)

				switch {
				case failure == "processing" || failure == renderTestOverwrite:
					assert.False(t, prep.outputTouched)
					assert.Empty(t, *commands)
				case existing:
					assert.Equal(t, []string{renderTestResetCommand, renderTestCheckoutCommand}, *commands)
				default:
					assert.Equal(t, []string{renderTestResetCommand}, *commands)
				}
			})
		}
	}
}

func TestCommitLocklessRenders_RestoresFailedOutput(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, failure := range []string{"add", renderTestDiffCommand, "commit", ""} {
			t.Run(failure+"/"+strconv.FormatBool(existing), func(t *testing.T) {
				env := testutils.NewTestEnv(t)
				setupRenderOutput(t, env.TestFS, existing)
				comp := components_testutils.NewMockComponent(gomock.NewController(t))
				comp.EXPECT().GetName().AnyTimes().Return("curl")
				prepared := []*preparedComponent{{
					lockless: true, comp: comp, specFilename: "curl.spec", compOutputDir: testRenderOutputDir,
					update: &locklessRenderUpdate{
						repoRoot: "/project", path: "SPECS/c/curl", subject: "updated curl",
					},
				}}

				env.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")
				env.CmdFactory.RunHandler = func(_ *exec.Cmd) error { return nil }
				stubIncreasingRenderNEVR(t, env)
				result := finishOneComponent(env.Env, prepared[0],
					map[string]*sources.ComponentMockResult{"curl": {}}, "/staging", true, false, false)
				require.NoError(t, result.renderErr)

				results := []*RenderResult{result}
				commands := mockRenderRecovery(t, env)
				recoverCommand := env.CmdFactory.RunAndGetOutputHandler
				env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
					if cmd.Args[3] == failure {
						return "", errors.New("injected Git failure")
					}

					if cmd.Args[3] == renderTestDiffCommand {
						return renderTestSpecDiff, nil
					}

					if cmd.Args[3] == renderTestResetCommand || cmd.Args[3] == renderTestCheckoutCommand {
						return recoverCommand(cmd)
					}

					return "", nil
				}

				commitLocklessRenders(env.Env, prepared, results, false)
				writeFailureMarkers(env.TestFS, results, true, false)

				if failure != "" {
					assert.Equal(t, renderStatusError, results[0].Status)
					assert.Contains(t, results[0].Error, "injected Git failure")
					assertOriginalRenderOutput(t, env.TestFS, existing)

					if existing {
						assert.Equal(t, []string{renderTestResetCommand, renderTestCheckoutCommand}, *commands)
					} else {
						assert.Equal(t, []string{renderTestResetCommand}, *commands)
					}
				} else {
					assert.Equal(t, renderStatusOK, results[0].Status)
					assert.Empty(t, *commands)

					content, err := fileutils.ReadFile(env.TestFS, filepath.Join(testRenderOutputDir, "curl.spec"))
					require.NoError(t, err)
					assert.Equal(t, "Name: curl\nVersion: 1\nRelease: 1\n", string(content))

					for _, name := range []string{".git", "extras", renderErrorMarkerFile} {
						exists, err := fileutils.Exists(env.TestFS, filepath.Join(testRenderOutputDir, name))
						require.NoError(t, err)
						assert.False(t, exists)
					}
				}
			})
		}
	}
}

func TestCommitLocklessRenders_ReportsRecoveryFailure(t *testing.T) {
	for _, failure := range []string{renderTestResetCommand, renderTestCheckoutCommand} {
		t.Run(failure, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			setupRenderOutput(t, env.TestFS, true)
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				if cmd.Args[3] == failure {
					return "", errors.New("injected recovery failure")
				}

				return "", nil
			}
			prepared := []*preparedComponent{{
				lockless: true, outputTouched: true, outputExisted: true, compOutputDir: testRenderOutputDir,
			}}
			results := []*RenderResult{{Status: renderStatusError, Error: "original render failure", lockless: true}}
			commitLocklessRenders(env.Env, prepared, results, false)
			assert.Contains(t, results[0].Error, "original render failure")
			assert.Contains(t, results[0].Error, "injected recovery failure")
			assert.Equal(t, renderStatusError, results[0].Status)
		})
	}
}

func TestWriteFailureMarkers_LocklessPreservesOutput(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, checkOnly := range []bool{false, true} {
			t.Run(strconv.FormatBool(existing)+"/"+strconv.FormatBool(checkOnly), func(t *testing.T) {
				env := testutils.NewTestEnv(t)
				setupRenderOutput(t, env.TestFS, existing)

				results := []*RenderResult{{
					Component: "curl", OutputDir: testRenderOutputDir, Status: renderStatusError, lockless: true,
				}}

				writeFailureMarkers(env.TestFS, results, true, checkOnly)
				assertOriginalRenderOutput(t, env.TestFS, existing)
				assert.Equal(t, checkOnly, results[0].Changed)
			})
		}
	}
}

func TestPrepareOneComponent_LocklessFailurePreservesOutput(t *testing.T) {
	original := newSourcePreparer
	newSourcePreparer = func(
		_ sourceproviders.SourceManager, _ opctx.FS, _ opctx.EventListener, _ opctx.DryRunnable,
		_ ...sources.PreparerOption,
	) (sources.SourcePreparer, error) {
		return nil, errors.New("injected source preparation failure")
	}

	t.Cleanup(func() { newSourcePreparer = original })

	for _, existing := range []bool{false, true} {
		t.Run(strconv.FormatBool(existing), func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			setupRenderOutput(t, env.TestFS, existing)
			comp := components_testutils.NewMockComponent(gomock.NewController(t))
			comp.EXPECT().GetName().AnyTimes().Return("curl")
			comp.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
				Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream},
			})

			prep := prepareOneComponent(env.Env, nil, comp, "/staging", "/project/SPECS",
				spec.EditorLegacy, false, true)
			require.NotNil(t, prep.result)
			assert.Contains(t, prep.result.Error, "injected source preparation failure")
			assert.True(t, prep.result.lockless)
			writeFailureMarkers(env.TestFS, []*RenderResult{prep.result}, true, false)
			assertOriginalRenderOutput(t, env.TestFS, existing)
		})
	}
}

type renderOutputFaultFS struct {
	afero.Fs
	failure string
}

func (fs *renderOutputFaultFS) Rename(oldName, newName string) error {
	if fs.failure == "move" && oldName == "/staging/curl/sidecar" {
		return errors.New("injected rename failure")
	}

	return fs.Fs.Rename(oldName, newName)
}

func (fs *renderOutputFaultFS) RemoveAll(path string) error {
	if fs.failure == "remove" && path == testRenderOutputDir {
		fs.failure = ""
		if err := fs.Fs.RemoveAll(filepath.Join(path, "curl.spec")); err != nil {
			return err
		}

		return errors.New("injected partial removal failure")
	}

	return fs.Fs.RemoveAll(path)
}
