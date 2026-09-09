// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"context"
	"os/exec"
	"slices"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateKiwiRunnerDistroConfigOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		configOverridePath   string
		createDistroOverride bool
		createOverrideDir    bool
		wantConfigPath       string
		wantError            string
	}{
		{
			name:                 "uses distro override",
			configOverridePath:   "/project/distro/kiwi/azl4/stage1/azurelinux-4.0-kiwi-override.yml",
			createDistroOverride: true,
			wantConfigPath:       "/project/distro/kiwi/azl4/stage1/azurelinux-4.0-kiwi-override.yml",
		},
		{
			name: "omits config without distro override",
		},
		{
			name:               "rejects inaccessible distro override",
			configOverridePath: "/project/distro/kiwi/missing.yml",
			wantError:          "file not found",
		},
		{
			name:               "rejects directory as distro override",
			configOverridePath: "/project/distro/kiwi/override.yml",
			createOverrideDir:  true,
			wantError:          "is a directory, expected a file",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			testEnv := testutils.NewTestEnv(t)
			require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, "/work"))

			distroName := testEnv.Config.Project.DefaultDistro.Name
			distro := testEnv.Config.Distros[distroName]
			versionName := testEnv.Config.Project.DefaultDistro.Version
			version := distro.Versions[versionName]
			version.KiwiConfigOverridePath = testCase.configOverridePath
			distro.Versions[versionName] = version
			testEnv.Config.Distros[distroName] = distro

			if testCase.createDistroOverride {
				require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, "/project/distro/kiwi/azl4/stage1"))
				require.NoError(t, fileutils.WriteFile(
					testEnv.TestFS,
					testCase.configOverridePath,
					[]byte("custom: true\n"),
					fileperms.PrivateFile,
				))
			} else if testCase.createOverrideDir {
				require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, testCase.configOverridePath))
			}

			var buildArgs []string

			testEnv.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				buildArgs = cmd.Args

				return nil
			}

			runner, err := createKiwiRunner(
				testEnv.Env,
				&projectconfig.ImageConfig{
					Definition: projectconfig.ImageDefinition{Path: "/project/image/config.kiwi"},
				},
				"/work/output",
				&ImageBuildOptions{},
			)
			if testCase.wantError != "" {
				require.ErrorContains(t, err, testCase.wantError)

				return
			}

			require.NoError(t, err)
			require.NoError(t, runner.Build(context.Background()))

			configFlagIndex := slices.Index(buildArgs, "--config")
			if testCase.wantConfigPath == "" {
				assert.Equal(t, -1, configFlagIndex)
			} else {
				require.NotEqual(t, -1, configFlagIndex)
				require.Less(t, configFlagIndex+1, len(buildArgs))
				assert.Equal(t, testCase.wantConfigPath, buildArgs[configFlagIndex+1])
			}
		})
	}
}

func TestValidateBuildArchitecture(t *testing.T) {
	imageConfig := &projectconfig.ImageConfig{
		Name:          "gen1",
		Architectures: []string{projectconfig.ImageArchitectureX86_64},
	}

	require.NoError(t, validateBuildArchitecture(
		imageConfig,
		ImageArchX86_64,
		"arm64",
	))
	require.NoError(t, validateBuildArchitecture(
		imageConfig,
		ImageArchDefault,
		"amd64",
	))

	err := validateBuildArchitecture(imageConfig, ImageArchAarch64, "amd64")
	require.ErrorContains(t, err, "image `gen1` does not support architecture `aarch64`")

	err = validateBuildArchitecture(imageConfig, ImageArchDefault, "arm64")
	require.ErrorContains(t, err, "image `gen1` does not support architecture `aarch64`")

	err = validateBuildArchitecture(imageConfig, ImageArchDefault, "riscv64")
	require.ErrorContains(t, err, "unsupported host architecture `riscv64`")
}

func TestValidateBuildArchitecture_UnrestrictedWhenUnset(t *testing.T) {
	// An image with no declared Architectures (e.g. from an images.toml written
	// before this field existed) must remain unrestricted.
	imageConfig := &projectconfig.ImageConfig{Name: "legacy"}

	require.NoError(t, validateBuildArchitecture(imageConfig, ImageArchX86_64, "amd64"))
	require.NoError(t, validateBuildArchitecture(imageConfig, ImageArchAarch64, "amd64"))
}

func TestValidateBuildArchitecture_RejectsUnsupportedExplicitTarget(t *testing.T) {
	// An unrestricted image (no declared Architectures) must still reject an
	// explicit --arch value that bypasses ImageArch.Set (e.g. set directly rather
	// than via flag parsing), instead of silently accepting any string.
	// SupportsArchitecture rejects it because "riscv64" isn't a recognized
	// architecture at all, regardless of the image's declared support. The error
	// must report the recognized architecture set, not the image's empty
	// declared list (which would misleadingly suggest it supports none).
	imageConfig := &projectconfig.ImageConfig{Name: "legacy"}

	err := validateBuildArchitecture(imageConfig, ImageArch("riscv64"), "amd64")
	require.ErrorContains(t, err, "image `legacy` does not support architecture `riscv64`")
	require.ErrorContains(t, err, `supported architectures: ["x86_64" "aarch64"]`)
}
