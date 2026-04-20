// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/image"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testImageConfig returns a minimal [projectconfig.ImageConfig] for use in tests.
func testImageConfig() *projectconfig.ImageConfig {
	return &projectconfig.ImageConfig{
		Name: "test-image",
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func TestBuildNativePytestArgs_BasicTestPaths(t *testing.T) {
	pytestConfig := &projectconfig.PytestConfig{
		TestPaths: []string{"cases/", "other/"},
		ExtraArgs: []string{"--image-path", "{image-path}"},
	}
	options := &image.ImageTestOptions{
		ImagePath: "/images/test.raw",
	}

	args := image.BuildNativePytestArgs(pytestConfig, testImageConfig(), options)

	assert.Equal(t, []string{"cases/", "other/", "--image-path", "/images/test.raw"}, args)
}

func TestBuildNativePytestArgs_GlobExpansion(t *testing.T) {
	tmpDir := t.TempDir()
	casesDir := filepath.Join(tmpDir, "cases")
	require.NoError(t, os.MkdirAll(casesDir, 0o755))

	for _, name := range []string{"test_alpha.py", "test_beta.py", "helper.py"} {
		require.NoError(t, os.WriteFile(filepath.Join(casesDir, name), []byte("# test"), 0o600))
	}

	pytestConfig := &projectconfig.PytestConfig{
		WorkingDir: tmpDir,
		TestPaths:  []string{"cases/test_*.py"},
		ExtraArgs:  []string{"--image-path", "{image-path}"},
	}
	options := &image.ImageTestOptions{
		ImagePath: "/images/test.raw",
	}

	args := image.BuildNativePytestArgs(pytestConfig, testImageConfig(), options)

	assert.Contains(t, args, filepath.Join("cases", "test_alpha.py"))
	assert.Contains(t, args, filepath.Join("cases", "test_beta.py"))
	assert.NotContains(t, args, filepath.Join("cases", "helper.py"))
	assert.Contains(t, args, "--image-path")
	assert.Contains(t, args, "/images/test.raw")
}

func TestBuildNativePytestArgs_GlobNoMatch(t *testing.T) {
	tmpDir := t.TempDir()

	pytestConfig := &projectconfig.PytestConfig{
		WorkingDir: tmpDir,
		TestPaths:  []string{"cases/test_*.py"},
	}
	options := &image.ImageTestOptions{
		ImagePath: "/images/test.raw",
	}

	args := image.BuildNativePytestArgs(pytestConfig, testImageConfig(), options)

	// Original pattern preserved when no matches.
	assert.Equal(t, []string{"cases/test_*.py"}, args)
}

func TestBuildNativePytestArgs_ExtraArgsNeverGlobExpanded(t *testing.T) {
	pytestConfig := &projectconfig.PytestConfig{
		ExtraArgs: []string{"--pattern", "test_*.py"},
	}
	options := &image.ImageTestOptions{
		ImagePath: "/images/test.raw",
	}

	args := image.BuildNativePytestArgs(pytestConfig, testImageConfig(), options)

	// Glob chars in extra-args should be passed verbatim.
	assert.Equal(t, []string{"--pattern", "test_*.py"}, args)
}

func TestBuildNativePytestArgs_JUnitXMLAppended(t *testing.T) {
	pytestConfig := &projectconfig.PytestConfig{
		TestPaths: []string{"cases/"},
		ExtraArgs: []string{"--image-path", "{image-path}"},
	}
	options := &image.ImageTestOptions{
		ImagePath:    "/images/test.raw",
		JUnitXMLPath: "/output/results.xml",
	}

	args := image.BuildNativePytestArgs(pytestConfig, testImageConfig(), options)

	assert.Equal(t, []string{
		"cases/",
		"--image-path", "/images/test.raw",
		"--junit-xml", "/output/results.xml",
	}, args)
}

func TestBuildNativePytestArgs_NoJUnitXMLWhenNotRequested(t *testing.T) {
	pytestConfig := &projectconfig.PytestConfig{
		TestPaths: []string{"cases/"},
	}
	options := &image.ImageTestOptions{
		ImagePath: "/images/test.raw",
	}

	args := image.BuildNativePytestArgs(pytestConfig, testImageConfig(), options)

	assert.NotContains(t, args, "--junit-xml")
}

func TestBuildNativePytestArgs_EmptyConfig(t *testing.T) {
	pytestConfig := &projectconfig.PytestConfig{}
	options := &image.ImageTestOptions{
		ImagePath: "/images/test.raw",
	}

	args := image.BuildNativePytestArgs(pytestConfig, testImageConfig(), options)
	assert.Empty(t, args)
}

func TestBuildNativePytestArgs_PlaceholderNotInTestPaths(t *testing.T) {
	// {image-path} in test-paths should NOT be substituted (it's only for extra-args).
	pytestConfig := &projectconfig.PytestConfig{
		TestPaths: []string{"{image-path}"},
	}
	options := &image.ImageTestOptions{
		ImagePath: "/images/test.raw",
	}

	args := image.BuildNativePytestArgs(pytestConfig, testImageConfig(), options)

	assert.Equal(t, []string{"{image-path}"}, args)
}

func TestBuildNativePytestArgs_ImageNamePlaceholder(t *testing.T) {
	pytestConfig := &projectconfig.PytestConfig{
		ExtraArgs: []string{"--image-name", "{image-name}"},
	}
	options := &image.ImageTestOptions{
		ImageName: "vm-base",
		ImagePath: "/images/test.raw",
	}

	args := image.BuildNativePytestArgs(pytestConfig, testImageConfig(), options)

	assert.Equal(t, []string{"--image-name", "vm-base"}, args)
}

func TestBuildNativePytestArgs_CapabilitiesPlaceholder(t *testing.T) {
	imgConfig := &projectconfig.ImageConfig{
		Name: "vm-base",
		Capabilities: projectconfig.ImageCapabilities{
			MachineBootable:          boolPtr(true),
			Container:                boolPtr(false),
			Systemd:                  boolPtr(true),
			RuntimePackageManagement: boolPtr(true),
		},
	}
	pytestConfig := &projectconfig.PytestConfig{
		ExtraArgs: []string{"--capabilities", "{capabilities}"},
	}
	options := &image.ImageTestOptions{
		ImagePath: "/images/test.raw",
	}

	args := image.BuildNativePytestArgs(pytestConfig, imgConfig, options)

	assert.Equal(t, []string{"--capabilities", "machine-bootable,systemd,runtime-package-management"}, args)
}

func TestBuildNativePytestArgs_CapabilitiesEmpty(t *testing.T) {
	imgConfig := &projectconfig.ImageConfig{
		Name: "distroless",
		Capabilities: projectconfig.ImageCapabilities{
			MachineBootable:          boolPtr(false),
			Container:                boolPtr(true),
			RuntimePackageManagement: boolPtr(false),
		},
	}
	pytestConfig := &projectconfig.PytestConfig{
		ExtraArgs: []string{"--capabilities", "{capabilities}"},
	}
	options := &image.ImageTestOptions{
		ImagePath: "/images/test.raw",
	}

	args := image.BuildNativePytestArgs(pytestConfig, imgConfig, options)

	assert.Equal(t, []string{"--capabilities", "container"}, args)
}

func TestRunPytestSuite_MissingPytestConfig(t *testing.T) {
	suiteConfig := &projectconfig.TestSuiteConfig{
		Name: "smoke",
		Type: projectconfig.TestTypePytest,
	}

	options := &image.ImageTestOptions{
		ImagePath: "/images/test.raw",
	}

	err := image.RunPytestSuite(nil, suiteConfig, testImageConfig(), options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing pytest configuration")
}
