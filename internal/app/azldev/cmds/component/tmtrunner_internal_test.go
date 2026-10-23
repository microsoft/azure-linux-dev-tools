// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reportTestEnv(t *testing.T) *testutils.TestEnv {
	t.Helper()
	env := newTMTTestEnv(t, nil)
	env.Env.SetDefaultReportFormat(azldev.ReportFormatJSON)
	env.CmdFactory.RegisterCommandInSearchPath("python3")

	for path, content := range map[string]string{
		testImagePath: "image", testRPMPath: "rpm", "/source/.fmf/version": "1\n",
	} {
		require.NoError(t, fileutils.WriteFile(env.TestFS, path, []byte(content), fileperms.PrivateFile))
	}

	env.CmdFactory.RunAndGetOutputHandler = func(*exec.Cmd) (string, error) {
		return `[{"name":"/plans/all"}]`, nil
	}

	return env
}

func executeReportTest(t *testing.T, env *testutils.TestEnv) ([]azldev.TestRunResult, error) {
	t.Helper()

	var output strings.Builder
	env.Env.SetReportFile(&output)

	cmd := NewComponentTestCmd()
	cmd.SetArgs([]string{
		testTMTComponentName, "--image-path", testImagePath, "--rpm", testRPMPath,
		"--source-dir", "/source", "--work-dir", "/work", "--plan", "/plans/all",
	})
	err := cmd.ExecuteContext(env.Env)

	var results []azldev.TestRunResult

	decoder := json.NewDecoder(strings.NewReader(output.String()))
	require.NoError(t, decoder.Decode(&results), "stdout: %s", output.String())
	require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)

	return results, err
}

func TestComponentTestArtifacts(t *testing.T) {
	for _, failure := range []string{"", "test", "setup", "no-report"} {
		t.Run(failure, func(t *testing.T) {
			env := reportTestEnv(t)
			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				require.Equal(t, 2*time.Second, cmd.WaitDelay)
				_, err := io.WriteString(cmd.Stdout, "noisy stdout\n")
				require.NoError(t, err)
				_, err = io.WriteString(cmd.Stderr, "noisy stderr\n")
				require.NoError(t, err)

				if failure == "setup" {
					return errors.New("setup failed")
				}

				if filepath.Base(cmd.Args[0]) != tmtProgram {
					return nil
				}

				native := cmd.Args[slices.Index(cmd.Args, "--id")+1]
				store := cmd.Args[slices.Index(cmd.Args, "--workdir-root")+1]
				assert.Equal(t, filepath.Join(filepath.Dir(native), "workdir-root"), store)
				assert.NotEqual(t, filepath.Dir(filepath.Dir(native)), store, "do not chmod the private artifact root")
				assert.Less(t, slices.Index(cmd.Args, "--workdir-root"), slices.Index(cmd.Args, "plan"))

				if failure != "no-report" {
					for _, plan := range []string{"", "plans/all"} {
						path := filepath.Join(native, plan, "report", tmtJUnitPhase, "junit.xml")
						require.NoError(t, fileutils.WriteFile(env.TestFS, path, []byte("<testsuites/>"), fileperms.PrivateFile))
					}

					require.NoError(t, fileutils.WriteFile(env.TestFS,
						filepath.Join(native, "report/supplied-junit/junit.xml"), []byte("not ours"), fileperms.PrivateFile))
				}

				if failure != "" {
					return errors.New("runner failed")
				}

				return nil
			}

			results, err := executeReportTest(t, env)
			if failure == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			require.Len(t, results, 1)
			result := results[0]
			assert.Nil(t, result.ExitCode, "mock did not observe an exit code")
			assert.Equal(t, testTMTComponentName, result.Test)

			expectedReports := 2
			if failure == "setup" || failure == "no-report" {
				expectedReports = 0

				assert.NotContains(t, err.Error(), "run.yaml", "do not invent missing-state failures")
			}

			assert.Len(t, result.JUnit, expectedReports)

			for _, path := range result.JUnit {
				require.True(t, filepath.IsLocal(path))
				assert.NotContains(t, path, "supplied-junit")
				_, err := env.TestFS.Stat(filepath.Join(result.ArtifactDir, path))
				require.NoError(t, err)
			}

			log, err := fileutils.ReadFile(env.TestFS, filepath.Join(result.ArtifactDir, "setup.log"))
			require.NoError(t, err)
			assert.Contains(t, string(log), "noisy stdout")
			assert.Contains(t, string(log), "noisy stderr")
		})
	}
}

func TestComponentTestCSVWithNoisyCommands(t *testing.T) {
	env := reportTestEnv(t)
	env.Env.SetDefaultReportFormat(azldev.ReportFormatCSV)

	var output strings.Builder
	env.Env.SetReportFile(&output)
	env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		_, err := io.WriteString(cmd.Stdout, "<diagnostic>not a CSV header</diagnostic>\n")

		return err
	}
	cmd := NewComponentTestCmd()
	cmd.SetArgs([]string{
		testTMTComponentName, "--image-path", testImagePath, "--rpm", testRPMPath,
		"--source-dir", "/source", "--work-dir", "/work", "--plan", "/plans/all",
	})
	require.NoError(t, cmd.ExecuteContext(env.Env))

	rows, err := csv.NewReader(strings.NewReader(output.String())).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"test", "artifactDir"}, rows[0])
	assert.Equal(t, testTMTComponentName, rows[1][0])
	assert.NotContains(t, output.String(), "<diagnostic>")
}

type unreadableArtifactFS struct {
	opctx.FS
	blocked string
	err     error
}

func (fs unreadableArtifactFS) Open(path string) (afero.File, error) {
	if path == fs.blocked {
		return nil, &os.PathError{Op: "open", Path: path, Err: fs.err}
	}

	return fs.FS.Open(path)
}

func (fs unreadableArtifactFS) Stat(path string) (os.FileInfo, error) {
	if strings.HasPrefix(path, fs.blocked+string(filepath.Separator)) {
		return nil, &os.PathError{Op: "stat", Path: path, Err: fs.err}
	}

	return fs.FS.Stat(path)
}

func TestCollectTMTJUnitUnreadableDirectories(t *testing.T) {
	const (
		root   = "/owned"
		native = "/owned/native"
	)
	// Directory names here are legitimate plan hierarchy, not lifecycle steps.
	goodReport := "native/execute/data/report/nested/report/azldev-junit/junit.xml"
	otherReport := "native/other/report/azldev-junit/junit.xml"

	for _, testCase := range []struct {
		name, blocked string
		readErr       error
		want          []string
	}{
		{"private test data", "other/execute/data/private", os.ErrPermission, []string{goodReport, otherReport}},
		{
			"private payload named report", "other/execute/data/report", os.ErrPermission,
			[]string{goodReport, otherReport},
		},
		{"private report directory", "other/report", os.ErrPermission, []string{goodReport}},
		{"private reporter directory", "other/report/azldev-junit", os.ErrPermission, []string{goodReport}},
		{"private run directory", "", os.ErrPermission, []string{}},
		{"payload IO failure", "other/execute/data/private", io.ErrUnexpectedEOF, []string{goodReport, otherReport}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)

			var warnings strings.Builder

			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&warnings, nil)))
			t.Cleanup(func() { slog.SetDefault(prev) })

			for _, path := range []string{goodReport, otherReport} {
				require.NoError(t, fileutils.WriteFile(env.TestFS, filepath.Join(root, path),
					[]byte("<testsuites/>"), fileperms.PrivateFile))
			}

			blocked := filepath.Join(native, testCase.blocked)
			require.NoError(t, env.TestFS.MkdirAll(blocked, fileperms.PrivateDir))

			reports := collectTMTJUnit(unreadableArtifactFS{
				FS: env.TestFS, blocked: blocked, err: testCase.readErr,
			}, root, native)

			assert.Equal(t, testCase.want, reports)
			assert.Contains(t, warnings.String(), "level=WARN")
			assert.Contains(t, warnings.String(), blocked)
			assert.Contains(t, warnings.String(), testCase.readErr.Error())
		})
	}
}

func TestComponentTestFreshArtifactsAndFailFast(t *testing.T) {
	env := reportTestEnv(t)
	comp := env.Config.Components[testTMTComponentName]
	comp.Tests = &projectconfig.ComponentTestsConfig{Tests: []projectconfig.TestRef{{Name: "first"}, {Name: "second"}}}

	env.Config.Components[testTMTComponentName] = comp
	for _, name := range []string{"first", "second"} {
		env.Config.Tests[name] = projectconfig.TestDefinition{Type: "tmt", Tmt: map[string]any{}}
	}

	var roots []string

	env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		if filepath.Base(cmd.Args[0]) == tmtProgram {
			roots = append(roots, cmd.Args[slices.Index(cmd.Args, "--workdir-root")+1])

			return errors.New("runner failed")
		}

		return nil
	}
	first, err := executeReportTest(t, env)
	require.Error(t, err)
	second, err := executeReportTest(t, env)
	require.Error(t, err)
	require.Len(t, first, 1)
	require.Len(t, second, 1)
	require.Len(t, roots, 2)
	assert.NotEqual(t, roots[0], roots[1])
	assert.NotEqual(t, first[0].ArtifactDir, second[0].ArtifactDir)
	assert.Equal(t, "first", first[0].Test, "no rows for unattempted entries")
}

func TestComponentTestDiscoveryPreservesRunnerError(t *testing.T) {
	runnerErr := errors.New("test runner failed")
	for _, runErr := range []error{nil, runnerErr} {
		env := reportTestEnv(t)
		filesystem := &unreadableArtifactFS{FS: env.TestFS, blocked: "/unused", err: os.ErrPermission}
		configureReportTestEnv(t, env, filesystem, false)
		env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
			if filepath.Base(cmd.Args[0]) != tmtProgram {
				return nil
			}

			native := cmd.Args[slices.Index(cmd.Args, "--id")+1]
			require.NoError(t, env.TestFS.MkdirAll(native, fileperms.PrivateDir))
			filesystem.blocked = native

			return runErr
		}

		results, err := executeReportTest(t, env)
		if runErr == nil {
			require.NoError(t, err, "discovery must not turn a successful run into a failure")
		} else {
			require.ErrorIs(t, err, runnerErr, "discovery must not replace the runner error")
		}

		require.Len(t, results, 1)
		assert.NotEmpty(t, results[0].ArtifactDir)
		assert.Empty(t, results[0].JUnit)
	}
}

type reportFSFactory struct{ filesystem opctx.FS }

func (factory reportFSFactory) FS() opctx.FS { return factory.filesystem }

func configureReportTestEnv(t *testing.T, env *testutils.TestEnv, filesystem opctx.FS, dryRun bool) {
	t.Helper()

	options := azldev.NewEnvOptions()
	options.ProjectDir, options.Config = testProjectDir, env.Config
	options.Interfaces = env.TestInterfaces
	options.Interfaces.FileSystemFactory = reportFSFactory{filesystem}
	options.EventListener = env.EventListener
	options.DryRunnable = azldev.NewAppDryRunnable(dryRun)
	env.Env = azldev.NewEnv(t.Context(), options)
	env.Env.SetDefaultReportFormat(azldev.ReportFormatJSON)
}

func TestComponentTestDryRunDoesNotCreateArtifacts(t *testing.T) {
	env := reportTestEnv(t)
	configureReportTestEnv(t, env, env.TestFS, true)
	env.CmdFactory.RunHandler = func(*exec.Cmd) error {
		t.Fatal("dry-run executed command")

		return nil
	}
	results, err := executeReportTest(t, env)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Empty(t, results[0].ArtifactDir)
	assert.Empty(t, results[0].JUnit)
	assert.Nil(t, results[0].ExitCode)

	_, err = env.TestFS.Stat("/work")
	require.ErrorIs(t, err, os.ErrNotExist)
}

type reportSymlinkFS struct {
	opctx.FS
	link string
}

type reportSymlinkInfo struct{ os.FileInfo }

func (reportSymlinkInfo) Mode() os.FileMode { return os.ModeSymlink | fileperms.PublicFile }

func (fs reportSymlinkFS) LstatIfPossible(path string) (os.FileInfo, bool, error) {
	info, err := fs.Stat(path)
	if err == nil && path == fs.link {
		info = reportSymlinkInfo{info}
	}

	return info, true, err
}

func TestRetainedTMTReportBoundaries(t *testing.T) {
	env := testutils.NewTestEnv(t)
	require.NoError(t, fileutils.WriteFile(
		env.TestFS, "/owned/native/junit.xml", []byte("<testsuites/>"), fileperms.PrivateFile,
	))

	for _, link := range []string{"/owned", "/owned/native", "/owned/native/junit.xml"} {
		_, err := retainedTMTReport(reportSymlinkFS{FS: env.TestFS, link: link}, "/owned", "/owned/native/junit.xml")
		require.ErrorContains(t, err, "symlink")
	}

	for _, path := range []string{"/outside", "/owned/../outside", "/owned/native", "/owned"} {
		_, err := retainedTMTReport(env.TestFS, "/owned", path)
		require.Error(t, err)
	}

	rel, err := retainedTMTReport(env.TestFS, "/owned", "/owned/native/junit.xml")
	require.NoError(t, err)
	assert.Equal(t, "native/junit.xml", rel)
}

type transcriptFailureFS struct {
	opctx.FS
	closeFailure bool
}

func (fs transcriptFailureFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	file, err := fs.FS.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}

	return transcriptFailureFile{File: file, closeFailure: fs.closeFailure}, nil
}

type transcriptFailureFile struct {
	afero.File
	closeFailure bool
}

func (file transcriptFailureFile) Write(data []byte) (int, error) {
	if !file.closeFailure {
		return 0, io.ErrShortWrite
	}

	return file.File.Write(data)
}

func (file transcriptFailureFile) Close() error {
	return errors.Join(file.File.Close(), io.ErrClosedPipe)
}

func TestComponentTestCaptureFailures(t *testing.T) {
	for _, closeFailure := range []bool{false, true} {
		env := reportTestEnv(t)
		configureReportTestEnv(t, env, transcriptFailureFS{FS: env.TestFS, closeFailure: closeFailure}, false)
		results, err := executeReportTest(t, env)
		require.Error(t, err)
		require.Len(t, results, 1)
		assert.Empty(t, results[0].JUnit)
		assert.NotEmpty(t, results[0].ArtifactDir)
	}
}
