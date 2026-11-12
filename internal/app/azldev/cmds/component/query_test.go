// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareSuccessfulBatchQuery(
	t *testing.T, testEnv *testutils.TestEnv, componentName, specPath string,
) {
	t.Helper()

	const renderedSpecsDir = "/project/specs"

	testEnv.Config.Project.RenderedSpecsDir = renderedSpecsDir

	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), specPath, []byte("test spec content"), fileperms.PublicFile,
	))

	renderedSpecDir, err := components.RenderedSpecDir(renderedSpecsDir, componentName)
	require.NoError(t, err)
	require.NoError(t, fileutils.MkdirAll(testEnv.FS(), renderedSpecDir))
	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), filepath.Join(renderedSpecDir, componentName+".spec"),
		[]byte("test rendered spec content"), fileperms.PublicFile,
	))

	type queryResult struct {
		Name    string  `json:"name"`
		SrpmOut string  `json:"srpmOut"`
		BinOut  string  `json:"binOut"`
		Error   *string `json:"error"`
	}

	resultJSON, err := json.Marshal([]queryResult{{
		Name:    componentName,
		SrpmOut: "name=" + componentName + "\nepoch=0\nversion=1.0.0\nrelease=1.azl4\n",
	}})
	require.NoError(t, err)

	testEnv.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)
	testEnv.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		if !slices.Contains(cmd.Args, "--chroot") {
			return nil
		}

		scratchDirs, globErr := fileutils.Glob(
			testEnv.FS(), filepath.Join(testEnv.Env.WorkDir(), "azldev-query-scratch-*"),
		)
		if globErr != nil {
			return globErr
		}

		if len(scratchDirs) != 1 {
			return fmt.Errorf("expected one query scratch directory, found %d", len(scratchDirs))
		}

		return fileutils.WriteFile(
			testEnv.FS(), filepath.Join(scratchDirs[0], "results.json"), resultJSON, fileperms.PublicFile,
		)
	}
}

func TestNewComponentQueryCommand(t *testing.T) {
	cmd := component.NewComponentQueryCommand()
	require.NotNil(t, cmd)
	assert.Equal(t, "query", cmd.Use)
}

type queryDistGitFixture struct {
	cloneDir                           string
	filtered                           bool
	fullClones, sharedClones, repacked int
}

func mockQueryDistGit(
	t *testing.T, env *testutils.TestEnv, failure string, sentinel error,
) *queryDistGitFixture {
	t.Helper()

	fixture := &queryDistGitFixture{}
	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		if cmd.Args[0] != "git" {
			return "", nil
		}

		switch cmd.Args[3] {
		case "rev-parse":
			if cmd.Args[4] == "--git-common-dir" {
				return distGitTestMetadata, nil
			}

			return "/project", nil
		case distGitTestClone:
			if failure == distGitTestClone {
				return "", sentinel
			}

			if slices.Contains(cmd.Args, "--bare") {
				fixture.fullClones++

				assert.Equal(t, []string{"--single-branch", "--no-local", "--bare", "--"}, cmd.Args[4:8])

				return "", nil
			}

			fixture.sharedClones++

			assert.Equal(t, []string{"--shared", "--no-checkout", "--single-branch", "--"}, cmd.Args[4:8])
			fixture.cloneDir = cmd.Args[len(cmd.Args)-1]
			assert.Equal(t, filepath.Join(filepath.Dir(filepath.Dir(fixture.cloneDir)), "project.git"),
				cmd.Args[len(cmd.Args)-2])
			require.NoError(t, fileutils.WriteFile(env.FS(),
				filepath.Join(fixture.cloneDir, ".git", "objects", "info", "alternates"),
				[]byte("shared objects"), fileperms.PublicFile))
		case distGitTestFilterRepo:
			if failure == distGitTestFilterRepo {
				return "", sentinel
			}

			if slices.Contains(cmd.Args, "--subdirectory-filter") {
				if failure == "filter" {
					return "", sentinel
				}

				name := filepath.Base(cmd.Args[2])
				assert.Equal(t, "specs/"+strings.ToLower(name[:1])+"/"+name, cmd.Args[len(cmd.Args)-1])

				fixture.filtered = true

				require.NoError(t, fileutils.WriteFile(env.FS(), filepath.Join(cmd.Args[2], name+".spec"),
					[]byte("committed spec"), fileperms.PublicFile))
			}
		case "repack":
			if failure == "repack" {
				return "", sentinel
			}

			fixture.repacked++

			assert.Equal(t, []string{"-a", "-d"}, cmd.Args[4:])
		}

		return "", nil
	}

	return fixture
}

func TestQueryComponents_LocklessCommittedDistGit(t *testing.T) {
	for _, testCase := range []struct {
		name, failure string
		mixed         bool
		multiple      bool
	}{
		{name: "lockless"},
		{name: "mixed", mixed: true},
		{name: "multiple lockless components", multiple: true},
		{name: "missing filter-repo", failure: distGitTestFilterRepo},
		{name: "snapshot clone failure", failure: distGitTestClone},
		{name: "filter failure", failure: "filter"},
		{name: "repack failure", failure: "repack"},
		{name: "processing failure", failure: "process"},
		{name: "query failure", failure: "query"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			failure := testCase.failure
			env := newDistGitRepoTestEnv(t, testComponentName)
			env.Env.SetConcurrency(1)

			names := []string{testComponentName}
			if testCase.multiple {
				names = append(names, "rust-asyncband")
				env.Config.Components["rust-asyncband"] = projectconfig.ComponentConfig{Name: "rust-asyncband"}
			}

			env.Config.LocklessComponents = make(map[string]bool, len(names))
			for _, name := range names {
				env.Config.LocklessComponents[name] = true
			}

			if testCase.mixed {
				env.Config.Components["bash"] = projectconfig.ComponentConfig{
					Name: "bash",
					Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal, Path: "/project/bash.spec"},
				}
				prepareSuccessfulBatchQuery(t, env, "bash", "/project/bash.spec")
			}

			lockedQueryHandler := env.CmdFactory.RunHandler
			env.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)

			sentinel := errors.New("injected failure")

			fixture := mockQueryDistGit(t, env, failure, sentinel)

			var processed, queried bool

			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				args := strings.Join(cmd.Args, " ")
				if !strings.Contains(args, "--chroot") {
					return nil
				}

				if strings.Contains(args, "render_process.py") {
					if failure == "process" {
						return sentinel
					}

					processed = true

					assert.True(t, fixture.filtered)

					var records []map[string]any

					for _, name := range names {
						dir := filepath.Join(filepath.Dir(fixture.cloneDir), name)
						content, err := fileutils.ReadFile(env.FS(), filepath.Join(dir, name+".spec"))
						require.NoError(t, err)
						assert.Equal(t, "committed spec", string(content))

						exists, err := fileutils.Exists(env.FS(), filepath.Join(dir, ".git", "objects", "info", "alternates"))
						require.NoError(t, err)
						assert.False(t, exists, "component repositories must not depend on host-only shared paths")

						records = append(records, map[string]any{"name": name, "specFiles": "", "error": nil})
					}

					data, err := json.Marshal(records)
					require.NoError(t, err)

					return fileutils.WriteFile(env.FS(), filepath.Join(filepath.Dir(fixture.cloneDir), "results.json"),
						data, fileperms.PublicFile)
				}

				if strings.Contains(args, "query_process.py") {
					if testCase.mixed && !fixture.filtered {
						return lockedQueryHandler(cmd)
					}

					if failure == "query" {
						return sentinel
					}

					queried = true

					assert.True(t, processed)

					var records []map[string]any
					for _, name := range names {
						records = append(records, map[string]any{
							"name": name, "srpmOut": "name=" + name + "\nepoch=0\nversion=1\nrelease=3\n",
							"binOut": "", "error": nil,
						})
					}

					data, err := json.Marshal(records)
					require.NoError(t, err)

					return fileutils.WriteFile(env.FS(), filepath.Join(filepath.Dir(filepath.Dir(fixture.cloneDir)), "results.json"),
						data, fileperms.PublicFile)
				}

				return nil
			}

			filter := components.ComponentFilter{ComponentNamePatterns: names}
			if testCase.mixed {
				filter.IncludeAllComponents = true
			}

			results, queryErr := component.QueryComponents(env.Env, &component.QueryComponentsOptions{
				ComponentFilter: filter,
			})
			dirs, err := fileutils.Glob(env.FS(), filepath.Join(env.Env.WorkDir(), "azldev-query-scratch-*"))
			require.NoError(t, err)
			assert.Empty(t, dirs, "temporary clones must be removed after success or failure")

			if failure != "" {
				require.ErrorIs(t, queryErr, sentinel)

				if failure == distGitTestFilterRepo {
					assert.Contains(t, queryErr.Error(), "must be installed")
				}

				return
			}

			require.NoError(t, queryErr)

			wantLen := len(names)
			if testCase.mixed {
				wantLen++
			}

			require.Len(t, results, wantLen)
			assert.True(t, fixture.filtered && processed && queried)
			assert.Equal(t, 1, fixture.fullClones, "clone project objects only once for the whole query")
			assert.Equal(t, len(names), fixture.sharedClones)
			assert.Equal(t, len(names), fixture.repacked)

			releases := map[string]string{testComponentName: "3", "rust-asyncband": "3", "bash": "1.azl4"}
			for _, result := range results {
				assert.Equal(t, releases[result.Name], result.Version.Release())
			}
		})
	}
}

func TestComponentQueryCmd_NoMatch(t *testing.T) {
	const testComponentName = "test-component"

	testEnv := testutils.NewTestEnv(t)

	cmd := component.NewComponentQueryCommand()
	cmd.SetArgs([]string{testComponentName})

	err := cmd.ExecuteContext(testEnv.Env)

	// We expect an error because we haven't set up any components.
	require.Error(t, err)
}

func TestQueryComponents_MissingRenderedSpecsDir(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	// Test env constructProjectConfig leaves RenderedSpecsDir empty.
	options := component.QueryComponentsOptions{
		ComponentFilter: components.ComponentFilter{
			ComponentNamePatterns: []string{"any"},
		},
	}

	_, err := component.QueryComponents(testEnv.Env, &options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rendered-specs-dir is not configured")
}

func TestQueryComponents_RenderedSpecsDirDoesNotExist(t *testing.T) {
	const renderedSpecsDir = "/project/specs"

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Project.RenderedSpecsDir = renderedSpecsDir

	// Do NOT create the directory on the test filesystem.
	options := component.QueryComponentsOptions{
		ComponentFilter: components.ComponentFilter{
			ComponentNamePatterns: []string{"any"},
		},
	}

	_, err := component.QueryComponents(testEnv.Env, &options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

// Smoke test: when filter matches no components, the resolver surfaces an
// error before any rendered-spec validation runs.
func TestQueryComponents_NoComponentsSelected(t *testing.T) {
	const renderedSpecsDir = "/project/specs"

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Project.RenderedSpecsDir = renderedSpecsDir

	require.NoError(t, fileutils.MkdirAll(testEnv.FS(), renderedSpecsDir))

	// No components configured at all.
	options := component.QueryComponentsOptions{
		ComponentFilter: components.ComponentFilter{
			ComponentNamePatterns: []string{"nonexistent"},
		},
	}

	_, err := component.QueryComponents(testEnv.Env, &options)
	require.Error(t, err)
}

func TestQueryComponents_ResolvesComponentTests(t *testing.T) {
	const (
		testComponentName = "test-component"
		testSpecPath      = "/path/to/spec"
	)

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Components[testComponentName] = projectconfig.ComponentConfig{
		Name: testComponentName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       testSpecPath,
		},
		Tests: &projectconfig.ComponentTestsConfig{
			Tests: []projectconfig.TestRef{{Group: "runtime"}},
		},
	}
	testEnv.Config.Tests = map[string]projectconfig.TestDefinition{
		"runtime-a": {Type: "pytest", Pytest: map[string]any{"test-paths": []string{"tests/a.py"}}},
		"runtime-b": {Type: "pytest", Pytest: map[string]any{"test-paths": []string{"tests/b.py"}}},
	}
	testEnv.Config.TestGroups = map[string]projectconfig.TestGroup{
		"runtime": {Tests: []projectconfig.TestRef{{Name: "runtime-a"}, {Name: "runtime-b"}}},
	}

	prepareSuccessfulBatchQuery(t, testEnv, testComponentName, testSpecPath)

	options := component.QueryComponentsOptions{
		ComponentFilter: components.ComponentFilter{
			ComponentNamePatterns: []string{testComponentName},
		},
	}

	results, err := component.QueryComponents(testEnv.Env, &options)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, []string{"runtime-a", "runtime-b"}, results[0].ResolvedTests)
}

func TestQueryComponents_InvalidComponentTestRef(t *testing.T) {
	const (
		testComponentName = "test-component"
		testSpecPath      = "/path/to/spec"
	)

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Components[testComponentName] = projectconfig.ComponentConfig{
		Name: testComponentName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       testSpecPath,
		},
		Tests: &projectconfig.ComponentTestsConfig{
			Tests: []projectconfig.TestRef{{Name: "missing-test"}},
		},
	}

	prepareSuccessfulBatchQuery(t, testEnv, testComponentName, testSpecPath)

	options := component.QueryComponentsOptions{
		ComponentFilter: components.ComponentFilter{
			ComponentNamePatterns: []string{testComponentName},
		},
	}

	_, err := component.QueryComponents(testEnv.Env, &options)
	require.Error(t, err)
	require.ErrorContains(t, err, "failed to resolve tests for component")
	require.ErrorContains(t, err, "missing-test")
}
