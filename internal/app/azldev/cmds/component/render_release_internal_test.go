// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRenderReleaseAction(t *testing.T) {
	tests := []struct {
		name        string
		sourceType  projectconfig.SpecSourceType
		calculation projectconfig.ReleaseCalculation
		release     string
		expected    renderReleaseAction
		errorText   string
	}{
		{
			name:       "local auto autorelease is preserved",
			sourceType: projectconfig.SpecSourceTypeLocal,
			release:    "%autorelease",
			expected:   renderReleaseActionNone,
		},
		{
			name:        "local manual is preserved",
			sourceType:  projectconfig.SpecSourceTypeLocal,
			calculation: projectconfig.ReleaseCalculationManual,
			release:     "7%{?dist}",
			expected:    renderReleaseActionNone,
		},
		{
			name:        "local static is rejected",
			sourceType:  projectconfig.SpecSourceTypeLocal,
			calculation: projectconfig.ReleaseCalculationStatic,
			release:     "7%{?dist}",
			errorText:   "local component",
		},
		{
			name:        "upstream manual is preserved",
			sourceType:  projectconfig.SpecSourceTypeUpstream,
			calculation: projectconfig.ReleaseCalculationManual,
			release:     "7%{?dist}",
			expected:    renderReleaseActionNone,
		},
		{
			name:        "upstream explicit autorelease initializes",
			sourceType:  projectconfig.SpecSourceTypeUpstream,
			calculation: projectconfig.ReleaseCalculationAutorelease,
			release:     "7%{?dist}",
			expected:    renderReleaseActionInitializeAutorelease,
		},
		{
			name:       "upstream auto detects autorelease",
			sourceType: projectconfig.SpecSourceTypeUpstream,
			release:    "%autorelease",
			expected:   renderReleaseActionInitializeAutorelease,
		},
		{
			name:       "unspecified auto detects autorelease",
			sourceType: projectconfig.SpecSourceTypeUnspecified,
			release:    "%{autorelease}",
			expected:   renderReleaseActionInitializeAutorelease,
		},
		{
			name:       "upstream auto bumps static spec",
			sourceType: projectconfig.SpecSourceTypeUpstream,
			release:    "7%{?dist}",
			expected:   renderReleaseActionBumpSpec,
		},
		{
			name:        "upstream static is rejected",
			sourceType:  projectconfig.SpecSourceTypeUpstream,
			calculation: projectconfig.ReleaseCalculationStatic,
			release:     "7%{?dist}",
			errorText:   "cannot use 'release.calculation = \"static\"'",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testEnv := testutils.NewTestEnvWithoutLockfile(t)
			specPath := filepath.Join(testEnv.Env.ProjectDir(), "test.spec")
			require.NoError(t, fileutils.WriteFile(
				testEnv.TestFS,
				specPath,
				[]byte("Name: test\nRelease: "+test.release+"\n"),
				fileperms.PublicFile,
			))

			config := &projectconfig.ComponentConfig{
				Name: "test",
				Spec: projectconfig.SpecSource{
					SourceType: test.sourceType,
				},
				Release: projectconfig.ReleaseConfig{
					Calculation: test.calculation,
				},
			}

			action, err := resolveRenderReleaseAction(config, specPath, testEnv.Env)
			if test.errorText != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.errorText)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.expected, action)
		})
	}
}

func TestValidateWithoutLockfileRenderReleaseConfig(t *testing.T) {
	for _, sourceType := range []projectconfig.SpecSourceType{
		projectconfig.SpecSourceTypeLocal,
		projectconfig.SpecSourceTypeUpstream,
		projectconfig.SpecSourceTypeUnspecified,
	} {
		config := &projectconfig.ComponentConfig{
			Name: "test",
			Spec: projectconfig.SpecSource{
				SourceType: sourceType,
			},
			Release: projectconfig.ReleaseConfig{
				Calculation: projectconfig.ReleaseCalculationStatic,
			},
		}

		err := validateWithoutLockfileRenderReleaseConfig(config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot use 'release.calculation = \"static\"'")
	}
}

func TestGenerateAndFinalizeAutoreleaseRender(t *testing.T) {
	testEnv := testutils.NewTestEnvWithoutLockfile(t)
	testEnv.CmdFactory.RegisterCommandInSearchPath("rpmautospec")
	testEnv.CmdFactory.RunHandler = func(command *exec.Cmd) error {
		assert.Equal(t, []string{"rpmautospec", "generate-changelog", "test.spec"}, command.Args)
		assert.Equal(t, "/staging/test", command.Dir)

		pristineSpec, err := fileutils.ReadFile(testEnv.TestFS, "/staging/test/test.spec")
		require.NoError(t, err)
		assert.NotContains(t, string(pristineSpec), "BuildRequires: overlay-dependency")

		_, err = command.Stdout.Write(
			[]byte("* Wed Sep 10 2026 Test User <test@example.com> - 1.0-1\n- Initial\n"),
		)

		return err
	}

	componentDir := "/staging/test"
	specPath := filepath.Join(componentDir, "test.spec")
	require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, componentDir))
	require.NoError(t, fileutils.WriteFile(
		testEnv.TestFS,
		specPath,
		[]byte("Name: test\nRelease: %autorelease\n\n%changelog\nold entry\n"),
		fileperms.PublicFile,
	))

	changelogData, err := generateAutoreleaseChangelog(
		t.Context(), testEnv.Env, componentDir, specPath,
	)
	require.NoError(t, err)

	require.NoError(t, fileutils.WriteFile(
		testEnv.TestFS,
		specPath,
		[]byte("Name: test\nRelease: %autorelease\nBuildRequires: overlay-dependency\n\n%changelog\nold entry\n"),
		fileperms.PublicFile,
	))

	err = finalizeAutoreleaseRender(testEnv.Env, componentDir, specPath, changelogData)
	require.NoError(t, err)

	changelog, err := fileutils.ReadFile(testEnv.TestFS, filepath.Join(componentDir, "changelog"))
	require.NoError(t, err)
	assert.Equal(t,
		"* Wed Sep 10 2026 Test User <test@example.com> - 1.0-1\n- Initial\n",
		string(changelog))

	updatedSpec, err := fileutils.ReadFile(testEnv.TestFS, specPath)
	require.NoError(t, err)
	assert.Equal(t,
		"Name: test\nRelease: %autorelease\nBuildRequires: overlay-dependency\n\n%changelog\n%autochangelog\n",
		string(updatedSpec))
}

func TestGenerateAutoreleaseChangelogCommandFailure(t *testing.T) {
	testEnv := testutils.NewTestEnvWithoutLockfile(t)
	testEnv.CmdFactory.RegisterCommandInSearchPath("rpmautospec")
	testEnv.CmdFactory.RunHandler = func(command *exec.Cmd) error {
		_, _ = command.Stderr.Write([]byte("bad spec"))

		return errors.New("exit status 1")
	}

	_, err := generateAutoreleaseChangelog(
		t.Context(), testEnv.Env, "/staging/test", "/staging/test/test.spec",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad spec")
}

func TestRunRPMDevBumpSpec(t *testing.T) {
	testEnv := testutils.NewTestEnvWithoutLockfile(t)
	testEnv.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")
	testEnv.CmdFactory.RunHandler = func(command *exec.Cmd) error {
		assert.Equal(t, []string{
			"rpmdev-bumpspec",
			"--userstring", "Test User <test@example.com>",
			"--datestamp", "Thu Jan 01 1970",
			"test.spec",
		}, command.Args)
		assert.Equal(t, "/staging/test", command.Dir)

		return nil
	}

	err := runRPMDevBumpSpec(
		testEnv.Env,
		"/staging/test",
		"/staging/test/test.spec",
		"Test User <test@example.com>",
		"Thu Jan 01 1970",
	)
	require.NoError(t, err)
}

func TestBumpSpecMetadata(t *testing.T) {
	userString, datestamp, err := bumpSpecMetadata(object.Signature{
		Name:  " Test User ",
		Email: " test@example.com ",
		When:  time.Date(2026, time.September, 11, 23, 30, 0, 0, time.FixedZone("test", 2*60*60)),
	})
	require.NoError(t, err)
	assert.Equal(t, "Test User <test@example.com>", userString)
	assert.Equal(t, "Fri Sep 11 2026", datestamp)
}

func TestRenderedDirExistsInHeadTreeIgnoresWorkingTree(t *testing.T) {
	repoRoot := t.TempDir()

	repo, err := gogit.PlainInit(repoRoot, false)
	require.NoError(t, err)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	trackedDir := filepath.Join(repoRoot, "SPECS", "t", "tracked")
	require.NoError(t, os.MkdirAll(trackedDir, fileperms.PublicDir))
	require.NoError(t, os.WriteFile(
		filepath.Join(trackedDir, "tracked.spec"),
		[]byte("Name: tracked\n"),
		fileperms.PublicFile,
	))

	_, err = worktree.Add(filepath.ToSlash(filepath.Join("SPECS", "t", "tracked", "tracked.spec")))
	require.NoError(t, err)

	_, err = worktree.Commit("add tracked component", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test User",
			Email: "test@example.com",
			When:  time.Unix(0, 0).UTC(),
		},
	})
	require.NoError(t, err)

	untrackedDir := filepath.Join(repoRoot, "SPECS", "u", "untracked")
	require.NoError(t, os.MkdirAll(untrackedDir, fileperms.PublicDir))
	require.NoError(t, os.WriteFile(
		filepath.Join(untrackedDir, "untracked.spec"),
		[]byte("Name: untracked\n"),
		fileperms.PublicFile,
	))

	tracked, err := renderedDirExistsInHeadTree(repo, repoRoot, trackedDir)
	require.NoError(t, err)
	assert.True(t, tracked)

	untracked, err := renderedDirExistsInHeadTree(repo, repoRoot, untrackedDir)
	require.NoError(t, err)
	assert.False(t, untracked)
}
