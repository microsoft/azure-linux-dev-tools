// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testRenderedSpecDir = "/project/specs/u/util-linux"

const (
	testImagePath = "/project/image.qcow2"
	testRPMPath   = "/project/component.rpm"
)

func TestDecodeTMTConfig(t *testing.T) {
	config, err := decodeTMTConfig(map[string]any{
		"source": map[string]any{
			"git-url": "https://example.test/tests.git",
			"ref":     "0123456789012345678901234567890123456789",
		},
		"plan": "/plans/all",
	})

	require.NoError(t, err)
	assert.Equal(t, "https://example.test/tests.git", config.Source.GitURL)
	assert.Equal(t, "/plans/all", config.Plan)
}

func TestDecodeTMTConfigAllowsMissingSourceAndPlan(t *testing.T) {
	config, err := decodeTMTConfig(map[string]any{})

	require.NoError(t, err)
	assert.Empty(t, config.Source.GitURL)
	assert.Empty(t, config.Plan)
}

func TestDecodeTMTConfigRejectsInvalidRefAndPlan(t *testing.T) {
	for name, test := range map[string]struct {
		config        map[string]any
		expectedError string
	}{
		"non-commit ref": {
			config: map[string]any{
				"source": map[string]any{
					"git-url": "https://example.test/tests.git",
					"ref":     "main",
				},
				"plan": "/plans/all",
			},
			expectedError: "'tmt.source'",
		},
		"relative plan": {
			config: map[string]any{
				"source": map[string]any{
					"git-url": "https://example.test/tests.git",
					"ref":     "0123456789012345678901234567890123456789",
				},
				"plan": "plans/all",
			},
			expectedError: "'tmt.plan'",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeTMTConfig(test.config)
			require.ErrorContains(t, err, test.expectedError)
		})
	}
}

func TestValidateTMTProvisionOptions(t *testing.T) {
	require.NoError(t, validateTMTProvisionOptions(tmtProvisionVirtual, "/images/azure.qcow2"))
	require.NoError(t, validateTMTProvisionOptions(tmtProvisionLocal, ""))
	require.ErrorContains(t, validateTMTProvisionOptions(tmtProvisionLocal, "/images/azure.qcow2"), "cannot be used")
	require.ErrorContains(t, validateTMTProvisionOptions("container", ""), "must be either")
}

func TestTMTPipRequirementPinsTMTVersion(t *testing.T) {
	assert.Equal(t, "tmt[provision-virtual]==1.78.0", tmtPipRequirement)
}

func TestSelectTMTTests(t *testing.T) {
	tests := []projectconfig.ResolvedTest{
		{Name: "tmt-one", Definition: projectconfig.TestDefinition{Type: "tmt"}},
		{Name: "lisa-one", Definition: projectconfig.TestDefinition{Type: "lisa"}},
		{Name: "tmt-two", Definition: projectconfig.TestDefinition{Type: "tmt"}},
	}

	assert.Equal(t, []projectconfig.ResolvedTest{tests[0], tests[2]}, selectTMTTests(tests, nil))
	assert.Equal(t, []projectconfig.ResolvedTest{tests[2]}, selectTMTTests(tests, []string{"tmt-two"}))
}

func TestValidateTMTTestName(t *testing.T) {
	for _, name := range []string{"tmt-buildah", "nodejs22-tier1", ".metadata"} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, validateTMTTestName(name))
		})
	}

	for _, name := range []string{"", ".", "..", "../outside", "nested/test", `nested\test`, "/outside"} {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, validateTMTTestName(name), "must be a simple name")
		})
	}
}

func TestFlattenHardwareConstraints(t *testing.T) {
	constraints, err := flattenHardwareConstraints("", map[string]any{
		"memory": ">= 16 GB",
		"cpu": map[string]any{
			"cores":   ">= 4",
			"threads": ">=8",
		},
		"disk": []any{
			map[string]any{"size": ">= 512 GB"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"cpu.cores >= 4",
		"cpu.threads >=8",
		"disk[0].size >= 512 GB",
		"memory >= 16 GB",
	}, constraints)
}

func TestFlattenHardwareConstraintsRejectsBooleanGroups(t *testing.T) {
	_, err := flattenHardwareConstraints("", map[string]any{
		"and": []any{map[string]any{"memory": ">= 16 GB"}},
	})

	require.ErrorContains(t, err, "cannot be safely overridden")
}

func TestResolvedPlanHardwareArgsDryRunSkipsPlanExport(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	dryRunOptions := azldev.NewEnvOptions()
	dryRunOptions.ProjectDir = "/project"
	dryRunOptions.Config = testEnv.Config
	dryRunOptions.DryRunnable = azldev.NewAppDryRunnable(true)
	dryRunOptions.EventListener = testEnv.EventListener
	dryRunOptions.Interfaces = testEnv.TestInterfaces
	dryRunEnv := azldev.NewEnv(t.Context(), dryRunOptions)

	called := false
	testEnv.CmdFactory.RunAndGetOutputHandler = func(*exec.Cmd) (string, error) {
		called = true

		return "", errors.New("plan export must not run during dry-run")
	}

	args, err := resolvedPlanHardwareArgs(
		dryRunEnv, "/work/repo", "/work/tmt", "/plans/smoke",
	)

	require.NoError(t, err)
	assert.False(t, called)
	assert.Equal(t, []string{"boot.method = uefi"}, args)
}

func TestAbsoluteProjectPath(t *testing.T) {
	path, err := absoluteProjectPath("/projects/azurelinux", "base/out/image.qcow2")

	require.NoError(t, err)
	assert.Equal(t, "/projects/azurelinux/base/out/image.qcow2", path)
}

func TestResolveTMTImagePath(t *testing.T) {
	t.Run("accepts a regular file", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)
		require.NoError(t, fileutils.WriteFile(
			testEnv.TestFS, "/project/out/image.qcow2", []byte("qcow2"), fileperms.PrivateFile,
		))

		imagePath, err := resolveTMTImagePath(testEnv.Env, tmtProvisionVirtual, "out/image.qcow2")

		require.NoError(t, err)
		assert.Equal(t, "/project/out/image.qcow2", imagePath)
	})

	t.Run("rejects a directory", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)
		require.NoError(t, testEnv.TestFS.MkdirAll("/project/out/image.qcow2", fileperms.PublicDir))

		_, err := resolveTMTImagePath(testEnv.Env, tmtProvisionVirtual, "out/image.qcow2")

		require.ErrorContains(t, err, "must be a regular file")
	})

	t.Run("rejects a missing path", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)

		_, err := resolveTMTImagePath(testEnv.Env, tmtProvisionVirtual, "out/image.qcow2")

		require.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("local provisioner ignores the image path", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)

		imagePath, err := resolveTMTImagePath(testEnv.Env, tmtProvisionLocal, "")

		require.NoError(t, err)
		assert.Empty(t, imagePath)
	})
}

func TestAbsoluteRegularFilesExpandsGlobsInSortedOrder(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	for _, rpm := range []string{"z-last.rpm", "a-first.rpm"} {
		path := filepath.Join("/project/out", rpm)
		require.NoError(t, fileutils.WriteFile(testEnv.TestFS, path, []byte("rpm"), fileperms.PrivateFile))
	}

	paths, err := absoluteRegularFiles(testEnv.Env, []string{"out/*.rpm"}, "rpm")

	require.NoError(t, err)
	assert.Equal(t, []string{"/project/out/a-first.rpm", "/project/out/z-last.rpm"}, paths)
}

func TestComponentTMTWorkDirDefaultsToCurrentDirectory(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	require.NoError(t, testEnv.TestOSEnv.Chdir("/artifacts"))

	workDir, err := componentTMTWorkDir(testEnv.Env, "")

	require.NoError(t, err)
	assert.Equal(t, "/artifacts", workDir)
}

func TestPrepareTMTEnvironmentDryRunAvoidsFilesystemChanges(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	dryRunOptions := azldev.NewEnvOptions()
	dryRunOptions.ProjectDir = "/project"
	dryRunOptions.Config = testEnv.Config
	dryRunOptions.DryRunnable = azldev.NewAppDryRunnable(true)
	dryRunOptions.EventListener = testEnv.EventListener
	dryRunOptions.Interfaces = testEnv.TestInterfaces
	dryRunEnv := azldev.NewEnv(t.Context(), dryRunOptions)

	workDir, tmtProgramPath, err := prepareTMTEnvironment(dryRunEnv, "artifacts", tmtProvisionVirtual, false)

	require.NoError(t, err)
	assert.Equal(t, "/project/artifacts", workDir)
	assert.Equal(t, "/project/artifacts/tmt/venv/bin/tmt", tmtProgramPath)

	_, err = testEnv.TestFS.Stat(workDir)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestNewComponentTestCmd(t *testing.T) {
	cmd := NewComponentTestCmd()

	require.NotNil(t, cmd)
	assert.Equal(t, "test COMPONENT", cmd.Use)
	assert.NotNil(t, cmd.RunE)

	for _, name := range []string{
		"image-path", "rpm", "test", "work-dir", "provision", "source-dir", "plan", "from-spec",
	} {
		assert.NotNil(t, cmd.Flags().Lookup(name), "%s flag should be registered", name)
	}

	fromSpec := cmd.Flags().Lookup("from-spec")
	require.NotNil(t, fromSpec)
	assert.NotEmpty(t, fromSpec.Deprecated, "--from-spec should be marked deprecated")

	// Removed flags: memory, firmware, connection, user
	for _, name := range []string{"memory", "firmware", "connection", "user"} {
		assert.Nil(t, cmd.Flags().Lookup(name), "%s flag should not be registered", name)
	}
}

func TestResolveSourceRunDir(t *testing.T) {
	t.Run("returns the source dir when an fmf root is present", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)
		sourceDir := testRenderedSpecDir
		require.NoError(t, fileutils.WriteFile(
			testEnv.TestFS, filepath.Join(sourceDir, ".fmf", "version"), []byte("1\n"), fileperms.PrivateFile,
		))

		runDir, err := resolveSourceRunDir(testEnv.Env, sourceDir)

		require.NoError(t, err)
		assert.Equal(t, sourceDir, runDir)
	})

	t.Run("rejects a source dir without an fmf root", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)
		sourceDir := testRenderedSpecDir
		require.NoError(t, testEnv.TestFS.MkdirAll(sourceDir, fileperms.PublicDir))

		_, err := resolveSourceRunDir(testEnv.Env, sourceDir)

		require.ErrorContains(t, err, "no fmf metadata")
		assert.ErrorContains(t, err, "skip-file-filter")
	})

	t.Run("rejects a non-regular fmf version marker", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)
		sourceDir := testRenderedSpecDir
		require.NoError(t, testEnv.TestFS.MkdirAll(filepath.Join(sourceDir, ".fmf", "version"), fileperms.PublicDir))

		_, err := resolveSourceRunDir(testEnv.Env, sourceDir)

		require.ErrorContains(t, err, "must be a regular file")
	})
}

func TestRunOneTMTTestFromSourceDirDoesNotClone(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	sourceDir := testRenderedSpecDir
	require.NoError(t, fileutils.WriteFile(
		testEnv.TestFS, filepath.Join(sourceDir, ".fmf", "version"), []byte("1\n"), fileperms.PrivateFile,
	))

	test := projectconfig.ResolvedTest{
		Name: "tmt-util-linux-ci",
		Definition: projectconfig.TestDefinition{
			Type: "tmt",
			Tmt: map[string]any{
				"plan": "/plans/ci",
			},
		},
	}

	settings := tmtRunSettings{
		WorkDir:        "/project/work",
		TMTProgramPath: "/project/work/tmt/venv/bin/tmt",
		Provision:      tmtProvisionLocal,
		SourceDir:      sourceDir,
	}

	require.NoError(t, runOneTMTTest(testEnv.Env, test, settings))

	ranTMT := false

	for _, args := range testEnv.CommandsExecuted {
		require.NotEmpty(t, args)
		assert.NotEqual(t, "git", filepath.Base(args[0]),
			"--source-dir must not invoke git, but ran: %v", args)

		if filepath.Base(args[0]) == tmtProgram {
			ranTMT = true
		}
	}

	assert.True(t, ranTMT, "expected the tmt run command to be invoked from the source dir")
}

// TestRunOneTMTTestFromSourceDirWithNoCatalogEntryAutoDiscoversPlan mirrors the
// real scenario of running 'azldev component test <pkg> --source-dir <prep-sources
// output> --provision local --rpm <built-rpm>' against a component with no
// catalog [tests.X] entry at all and no --plan: resolveComponentTMTTests
// synthesizes the test, and the resulting tmt invocation omits --name so tmt
// discovers and runs every enabled plan in the prepared source tree.
func TestRunOneTMTTestFromSourceDirWithNoCatalogEntryAutoDiscoversPlan(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	sourceDir := testRenderedSpecDir
	require.NoError(t, fileutils.WriteFile(
		testEnv.TestFS, filepath.Join(sourceDir, ".fmf", "version"), []byte("1\n"), fileperms.PrivateFile,
	))

	// No 'source' and no 'plan': exactly what resolveComponentTMTTests
	// synthesizes for a component with no catalog entry and no --plan.
	test := projectconfig.ResolvedTest{
		Name:       "hostname",
		Definition: projectconfig.TestDefinition{Type: "tmt", Tmt: map[string]any{"plan": ""}},
	}

	settings := tmtRunSettings{
		WorkDir:        "/project/work",
		TMTProgramPath: "/project/work/tmt/venv/bin/tmt",
		Provision:      tmtProvisionLocal,
		SourceDir:      sourceDir,
	}

	require.NoError(t, runOneTMTTest(testEnv.Env, test, settings))

	var tmtArgs []string

	for _, args := range testEnv.CommandsExecuted {
		require.NotEmpty(t, args)
		assert.NotEqual(t, "git", filepath.Base(args[0]), "must not invoke git, but ran: %v", args)

		if filepath.Base(args[0]) == tmtProgram {
			tmtArgs = args[1:]
		}
	}

	require.NotNil(t, tmtArgs, "expected the tmt run command to be invoked")
	// No --name between 'plan' and 'provision': tmt discovers and runs every
	// enabled plan on its own. (The unrelated 'prepare --insert ... --name
	// azldev-candidate-rpms' step further along still has its own --name.)
	planIndex := slices.Index(tmtArgs, "plan")
	require.GreaterOrEqual(t, planIndex, 0, "expected a 'plan' step, got: %v", tmtArgs)
	assert.Equal(t, "provision", tmtArgs[planIndex+1], "no --name should follow 'plan', got: %v", tmtArgs)
}

const testTMTComponentName = "hostname"

// newTMTTestEnv builds a test env with a single local-spec component (so
// component resolution doesn't require a lock file). If tests is non-nil, the
// component is given a '[component.tests]' reference to it; otherwise the
// component has no catalog tests at all.
func newTMTTestEnv(t *testing.T, tests *projectconfig.ComponentTestsConfig) *testutils.TestEnv {
	t.Helper()

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Components[testTMTComponentName] = projectconfig.ComponentConfig{
		Name: testTMTComponentName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/project/" + testTMTComponentName + ".spec",
		},
		Tests: tests,
	}

	return testEnv
}

func TestEffectiveSourceDir(t *testing.T) {
	t.Run("returns an absolute --source-dir unchanged when --from-spec is not set", func(t *testing.T) {
		testEnv := newTMTTestEnv(t, nil)

		sourceDir, err := effectiveSourceDir(testEnv.Env, testTMTComponentName, &ComponentTestOptions{
			SourceDir: "/project/source",
		})

		require.NoError(t, err)
		assert.Equal(t, "/project/source", sourceDir)
	})

	t.Run("resolves a relative --source-dir against the project directory", func(t *testing.T) {
		testEnv := newTMTTestEnv(t, nil)

		sourceDir, err := effectiveSourceDir(testEnv.Env, testTMTComponentName, &ComponentTestOptions{
			SourceDir: "prepared/pkg",
		})

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(testEnv.Env.ProjectDir(), "prepared/pkg"), sourceDir)
	})

	t.Run("leaves --source-dir empty when neither flag is set", func(t *testing.T) {
		testEnv := newTMTTestEnv(t, nil)

		sourceDir, err := effectiveSourceDir(testEnv.Env, testTMTComponentName, &ComponentTestOptions{})

		require.NoError(t, err)
		assert.Empty(t, sourceDir)
	})

	t.Run("deprecated --from-spec resolves the component's rendered spec dir", func(t *testing.T) {
		testEnv := newTMTTestEnv(t, nil)
		testEnv.Config.Project.RenderedSpecsDir = "/project/specs"

		sourceDir, err := effectiveSourceDir(testEnv.Env, testTMTComponentName, &ComponentTestOptions{
			FromSpec: true,
		})

		require.NoError(t, err)
		assert.Equal(t, "/project/specs/h/hostname", sourceDir)
	})

	t.Run("rejects --from-spec combined with --source-dir", func(t *testing.T) {
		testEnv := newTMTTestEnv(t, nil)
		testEnv.Config.Project.RenderedSpecsDir = "/project/specs"

		_, err := effectiveSourceDir(testEnv.Env, testTMTComponentName, &ComponentTestOptions{
			FromSpec:  true,
			SourceDir: "/project/source",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "deprecated")
		assert.Contains(t, err.Error(), "--source-dir")
	})

	t.Run("rejects --from-spec when rendered-specs-dir is not configured", func(t *testing.T) {
		testEnv := newTMTTestEnv(t, nil)
		// Project.RenderedSpecsDir intentionally left unset.

		_, err := effectiveSourceDir(testEnv.Env, testTMTComponentName, &ComponentTestOptions{FromSpec: true})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "rendered-specs-dir")
	})
}

func TestResolveComponentTMTTestsSynthesizesAdHocTestWithNoCatalogEntry(t *testing.T) {
	testEnv := newTMTTestEnv(t, nil)

	resolved, err := resolveComponentTMTTests(testEnv.Env, testTMTComponentName, nil, "/project/source", "")

	require.NoError(t, err)
	require.Len(t, resolved, 1)
	assert.Equal(t, testTMTComponentName, resolved[0].Name)
	assert.Equal(t, "tmt", resolved[0].Definition.Type)
	assert.Empty(t, resolved[0].Definition.Tmt["plan"])
}

func TestResolveComponentTMTTestsRejectsNoCatalogEntryWithoutSourceDir(t *testing.T) {
	testEnv := newTMTTestEnv(t, nil)

	_, err := resolveComponentTMTTests(testEnv.Env, testTMTComponentName, nil, "" /*sourceDir*/, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no TMT tests in the catalog")
	assert.Contains(t, err.Error(), "--source-dir")
}

func TestResolveComponentTMTTestsDoesNotSynthesizeWhenSelectorMatchesNoCatalogTest(t *testing.T) {
	testEnv := newTMTTestEnv(t, &projectconfig.ComponentTestsConfig{
		Tests: []projectconfig.TestRef{{Name: "ci"}},
	})
	testEnv.Config.Tests["ci"] = projectconfig.TestDefinition{
		Type: "tmt",
		Tmt:  map[string]any{"plan": "/plans/ci"},
	}

	// A typo'd --test selector must be reported as an error, not silently
	// synthesize a new ad-hoc test and run every discovered plan.
	_, err := resolveComponentTMTTests(testEnv.Env, testTMTComponentName, []string{"typo"}, "/project/source", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no selected TMT tests")
}

func TestResolveComponentTMTTestsDoesNotSynthesizeWhenSelectorMatchesWithNoCatalogEntryAtAll(t *testing.T) {
	// No '[component.tests]' entry at all, unlike the sibling test above.
	testEnv := newTMTTestEnv(t, nil)

	// An unmatched --test selector must still error, even when the component
	// has zero catalog tmt tests to begin with (not just a non-matching one).
	_, err := resolveComponentTMTTests(testEnv.Env, testTMTComponentName, []string{"typo"}, "/project/source", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no selected TMT tests")
}

func TestResolveComponentTMTTestsReturnsMatchedCatalogTest(t *testing.T) {
	testEnv := newTMTTestEnv(t, &projectconfig.ComponentTestsConfig{
		Tests: []projectconfig.TestRef{{Name: "ci"}},
	})
	testEnv.Config.Tests["ci"] = projectconfig.TestDefinition{
		Type: "tmt",
		Tmt:  map[string]any{"plan": "/plans/ci"},
	}

	resolved, err := resolveComponentTMTTests(testEnv.Env, testTMTComponentName, []string{"ci"}, "", "")

	require.NoError(t, err)
	require.Len(t, resolved, 1)
	assert.Equal(t, "ci", resolved[0].Name)
	assert.Equal(t, "/plans/ci", resolved[0].Definition.Tmt["plan"])
}

func TestPreflightTMTTests(t *testing.T) {
	t.Run("passes a test with a catalog source and no plan under local provisioning", func(t *testing.T) {
		resolved := []projectconfig.ResolvedTest{{
			Name: "ci",
			Definition: projectconfig.TestDefinition{Type: "tmt", Tmt: map[string]any{
				"source": map[string]any{
					"git-url": "https://example.test/tests.git",
					"ref":     "0123456789012345678901234567890123456789",
				},
			}},
		}}

		require.NoError(t, preflightTMTTests(resolved, "" /*sourceDir*/, tmtProvisionLocal))
	})

	t.Run("rejects a missing tmt.source when --source-dir is not set", func(t *testing.T) {
		resolved := []projectconfig.ResolvedTest{{
			Name:       "ci",
			Definition: projectconfig.TestDefinition{Type: "tmt", Tmt: map[string]any{}},
		}}

		err := preflightTMTTests(resolved, "" /*sourceDir*/, tmtProvisionLocal)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "'tmt.source'")
		assert.Contains(t, err.Error(), "--source-dir")
	})

	t.Run("allows a missing tmt.source when --source-dir is set", func(t *testing.T) {
		resolved := []projectconfig.ResolvedTest{{
			Name:       "ci",
			Definition: projectconfig.TestDefinition{Type: "tmt", Tmt: map[string]any{}},
		}}

		require.NoError(t, preflightTMTTests(resolved, "/project/source", tmtProvisionLocal))
	})

	t.Run("rejects a missing plan under --provision virtual", func(t *testing.T) {
		resolved := []projectconfig.ResolvedTest{{
			Name:       "ci",
			Definition: projectconfig.TestDefinition{Type: "tmt", Tmt: map[string]any{}},
		}}

		err := preflightTMTTests(resolved, "/project/source", tmtProvisionVirtual)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "'tmt.plan'")
		assert.Contains(t, err.Error(), "--provision virtual")
	})
}

func TestComponentTestCmdNoMatch(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testImagePath, []byte("image"), fileperms.PrivateFile))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testRPMPath, []byte("rpm"), fileperms.PrivateFile))

	cmd := NewComponentTestCmd()
	cmd.SetArgs([]string{"missing-component", "--image-path", testImagePath, "--rpm", testRPMPath})

	err := cmd.ExecuteContext(testEnv.Env)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "component not found")
}

// TestComponentTestCmdPreflightsBeforePreparingEnvironment proves the fix for
// the deterministic-input-error-after-side-effects issue: a catalog test
// missing 'tmt.source' (and no --source-dir) must fail before azldev creates a
// venv or runs pip install, not partway through runOneTMTTest.
func TestComponentTestCmdPreflightsBeforePreparingEnvironment(t *testing.T) {
	testEnv := newTMTTestEnv(t, &projectconfig.ComponentTestsConfig{
		Tests: []projectconfig.TestRef{{Name: "ci"}},
	})
	testEnv.Config.Tests["ci"] = projectconfig.TestDefinition{Type: "tmt", Tmt: map[string]any{}}

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testImagePath, []byte("image"), fileperms.PrivateFile))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testRPMPath, []byte("rpm"), fileperms.PrivateFile))

	cmd := NewComponentTestCmd()
	cmd.SetArgs([]string{
		testTMTComponentName, "--image-path", testImagePath, "--rpm", testRPMPath, "--plan", "/plans/all",
	})

	err := cmd.ExecuteContext(testEnv.Env)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "'tmt.source'")
	assert.Empty(t, testEnv.CommandsExecuted, "no venv/pip commands should run before the preflight check")
}

func TestDirsOverlap(t *testing.T) {
	for name, test := range map[string]struct {
		a        string
		b        string
		expected bool
	}{
		"identical paths":             {"/project/source", "/project/source", true},
		"b nested under a":            {"/project/source", "/project/source/nested", true},
		"a nested under b":            {"/project/source/nested", "/project/source", true},
		"project root contains work":  {"/project", "/project/work", true},
		"unrelated sibling paths":     {"/project/source", "/project/work", false},
		"similar but distinct prefix": {"/project/source", "/project/source-other", false},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, test.expected, dirsOverlap(test.a, test.b))
		})
	}
}

// TestComponentTestCmdRejectsOverlappingSourceAndWorkDir covers the scenario
// from the review finding: '--source-dir .' from the project root with the
// default (cwd) work directory makes the managed per-test directory a child
// of the supplied source tree.
func TestComponentTestCmdRejectsOverlappingSourceAndWorkDir(t *testing.T) {
	testEnv := newTMTTestEnv(t, nil)
	require.NoError(t, fileutils.WriteFile(
		testEnv.TestFS, filepath.Join("/project", ".fmf", "version"), []byte("1\n"), fileperms.PrivateFile,
	))

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testImagePath, []byte("image"), fileperms.PrivateFile))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testRPMPath, []byte("rpm"), fileperms.PrivateFile))

	cmd := NewComponentTestCmd()
	cmd.SetArgs([]string{
		testTMTComponentName, "--image-path", testImagePath, "--rpm", testRPMPath,
		"--plan", "/plans/all", "--source-dir", ".",
	})

	err := cmd.ExecuteContext(testEnv.Env)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not overlap")
	assert.Empty(t, testEnv.CommandsExecuted, "no venv/pip commands should run before the overlap check")
}

func TestRemovePreviousTMTRepositoryRemovesOnlyRepository(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testDir := "/project/tmt-test"
	repoDir := filepath.Join(testDir, "repo")
	artifactPath := filepath.Join(testDir, "tmt", "results.yaml")
	staleMetadataPath := filepath.Join(repoDir, "stale-metadata")

	require.NoError(t, testEnv.TestFS.MkdirAll(repoDir, fileperms.PublicDir))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, staleMetadataPath, []byte("stale"), fileperms.PrivateFile))
	require.NoError(t, testEnv.TestFS.MkdirAll(filepath.Dir(artifactPath), fileperms.PublicDir))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, artifactPath, []byte("preserve"), fileperms.PrivateFile))

	require.NoError(t, removePreviousTMTRepository(testEnv.Env, repoDir))
	_, err := testEnv.TestFS.Stat(repoDir)
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = testEnv.TestFS.Stat(artifactPath)
	assert.NoError(t, err)
}

func TestRunHostCommandOutputIncludesStderr(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.CmdFactory.RunAndGetOutputHandler = func(command *exec.Cmd) (string, error) {
		require.NotNil(t, command.Stderr)
		_, err := command.Stderr.Write([]byte("tmt export diagnostic"))
		require.NoError(t, err)

		return "", errors.New("command failed")
	}

	_, err := runHostCommandOutput(testEnv.Env, "/project", "tmt", "plan", "export")

	require.Error(t, err)
	require.ErrorContains(t, err, "tmt export diagnostic")
	require.ErrorContains(t, err, "command failed")
}

func TestRunHostCommandOutputOmitsEmptyStderr(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.CmdFactory.RunAndGetOutputHandler = func(*exec.Cmd) (string, error) {
		return "", errors.New("command failed")
	}

	_, err := runHostCommandOutput(testEnv.Env, "/project", "tmt", "plan", "export")

	require.EqualError(t, err, "run host command `tmt`:\ncommand failed")
}

func TestComponentTMTArgsPreservesNativePlanSteps(t *testing.T) {
	args := componentTMTArgs(
		tmtConfig{Plan: "/plans/smoke"},
		"/work/tmt",
		tmtProvisionVirtual,
		"/path/to/image.qcow2",
		[]string{"memory >= 4 GB", "boot.method = uefi"},
		[]string{"/rpms/component.rpm", "/rpms/component-tests.rpm"},
	)

	assert.Equal(t, []string{
		"run", "--all", "--keep", "--workdir-root", "/work/tmt",
		"plan", "--name", "/plans/smoke",
		"provision", "--how", "virtual", "--become", "--image", "/path/to/image.qcow2",
		"--hardware", "memory >= 4 GB", "--hardware", "boot.method = uefi",
		"prepare", "--insert", "--how", "install", "--name", tmtCandidateRPMPrepareStepName,
		"--package", "/rpms/component.rpm",
		"--package", "/rpms/component-tests.rpm",
	}, args)
	assert.NotContains(t, args, "discover")
	assert.NotContains(t, args, "execute")
	assert.NotContains(t, args, "report")
	assert.NotContains(t, args, "finish")
}

func TestComponentTMTArgsVirtualProvisioner(t *testing.T) {
	args := componentTMTArgs(
		tmtConfig{Plan: "/plans/smoke"},
		"/work/tmt",
		tmtProvisionVirtual,
		"/path/to/image.qcow2",
		[]string{"boot.method = uefi"},
		[]string{"/rpms/component.rpm"},
	)

	assert.Equal(t, []string{
		"run", "--all", "--keep", "--workdir-root", "/work/tmt",
		"plan", "--name", "/plans/smoke",
		"provision", "--how", "virtual", "--become", "--image", "/path/to/image.qcow2",
		"--hardware", "boot.method = uefi",
		"prepare", "--insert", "--how", "install", "--name", tmtCandidateRPMPrepareStepName,
		"--package", "/rpms/component.rpm",
	}, args)
}

func TestComponentTMTArgsLocalProvisioner(t *testing.T) {
	// Pass an image path and hardware constraints the local provisioner cannot
	// use, so the assertions below prove they are suppressed rather than merely
	// absent from the input.
	args := componentTMTArgs(
		tmtConfig{Plan: "/plans/smoke"},
		"/work/tmt",
		tmtProvisionLocal,
		"/path/to/image.qcow2",
		[]string{"memory >= 4 GB", "boot.method = uefi"},
		[]string{"/rpms/component.rpm"},
	)

	assert.Equal(t, []string{
		"run", "--all", "--keep", "--workdir-root", "/work/tmt",
		"plan", "--name", "/plans/smoke",
		"provision", "--how", "local", "--become",
		"prepare", "--insert", "--how", "install", "--name", tmtCandidateRPMPrepareStepName,
		"--package", "/rpms/component.rpm",
	}, args)
	assert.NotContains(t, args, "--image")
	assert.NotContains(t, args, "--hardware")
}

func TestComponentTMTArgsOmitsNameWhenPlanIsEmpty(t *testing.T) {
	args := componentTMTArgs(
		tmtConfig{},
		"/work/tmt",
		tmtProvisionLocal,
		"",
		nil,
		[]string{"/rpms/component.rpm"},
	)

	assert.Equal(t, []string{
		"run", "--all", "--keep", "--workdir-root", "/work/tmt",
		"plan",
		"provision", "--how", "local", "--become",
		"prepare", "--insert", "--how", "install", "--name", tmtCandidateRPMPrepareStepName,
		"--package", "/rpms/component.rpm",
	}, args)
}

func TestOverrideTMTPlanCopiesRatherThanMutatesSharedMap(t *testing.T) {
	sharedTmt := map[string]any{"plan": "/plans/ci"}
	tests := []projectconfig.ResolvedTest{
		{Name: "a", Definition: projectconfig.TestDefinition{Type: "tmt", Tmt: sharedTmt}},
		{Name: "b", Definition: projectconfig.TestDefinition{Type: "tmt", Tmt: sharedTmt}},
	}

	result := overrideTMTPlan(tests, "/plans/all")

	assert.Equal(t, "/plans/all", result[0].Definition.Tmt["plan"])
	assert.Equal(t, "/plans/all", result[1].Definition.Tmt["plan"])
	assert.Equal(t, "/plans/ci", sharedTmt["plan"], "original map must not be mutated")
}

func TestTMTCommandEnv(t *testing.T) {
	testCases := map[string]struct {
		pluginDir      string
		provision      string
		bootTimeoutSet string
		expected       []string
	}{
		"local provisioner acknowledges host modification": {
			provision: tmtProvisionLocal,
			expected:  []string{"TMT_BOOT_TIMEOUT=300", "TMT_FEELING_SAFE=1"},
		},
		"virtual provisioner omits the acknowledgement": {
			pluginDir: "/work/test/tmt-plugins",
			provision: tmtProvisionVirtual,
			expected:  []string{"TMT_PLUGINS=/work/test/tmt-plugins", "TMT_BOOT_TIMEOUT=300"},
		},
		"explicit boot timeout is preserved": {
			provision:      tmtProvisionLocal,
			bootTimeoutSet: "900",
			expected:       []string{"TMT_FEELING_SAFE=1"},
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			testEnv := testutils.NewTestEnv(t)
			if testCase.bootTimeoutSet != "" {
				osEnv, ok := testEnv.TestOSEnv.(*testctx.TestOSEnv)
				require.True(t, ok)
				osEnv.SetEnv("TMT_BOOT_TIMEOUT", testCase.bootTimeoutSet)
			}

			vars := tmtCommandEnv(testEnv.Env, testCase.pluginDir, testCase.provision)

			assert.Equal(t, testCase.expected, vars)
		})
	}
}

func TestWriteTestcloudPluginRepairsPermissionsBeforePull(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	pluginDir, err := writeTestcloudPlugin(testEnv.Env, "/project/test")

	require.NoError(t, err)
	plugin, err := fileutils.ReadFile(testEnv.TestFS, filepath.Join(pluginDir, "azldev_testcloud.py"))
	require.NoError(t, err)

	pluginText := string(plugin)
	assert.True(t, strings.HasPrefix(pluginText, "import tmt.steps.provision.testcloud as testcloud\n"))
	assert.Contains(t, pluginText, "\nfrom tmt.guest import GuestSsh\n")
	assert.Contains(t, pluginText, "_azldev_original_pull = GuestSsh.pull")
	assert.Contains(t, pluginText, "GuestSsh.pull = _azldev_pull")
	assert.Less(t,
		strings.Index(pluginText, "Command(\"sudo\", \"chmod\", \"-R\", \"a+rX\", path)"),
		strings.Index(pluginText, "return _azldev_original_pull("),
	)
	// System libvirt connection: SSH only
	assert.Contains(t, pluginText, "firewall-cmd --add-service=ssh || :")
	assert.NotContains(t, pluginText, "firewall-cmd --add-port=10022/tcp || :")
}
