// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectgen"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/docker"
)

const (
	logFileTimeFormat           = "20060102-150405"
	imageCustomizerCustomizeCmd = "customize"
	imageCustomizerInjectCmd    = "inject-files"
	// The Image Customizer log always goes to a debug-level log file - regardless
	// of the log level set for azldev.
	// The log level set for azldev controls what is shown on the console.
	defaultLogLevel = "debug"
)

type imageCustomizerOptions struct {
	imageFile                string
	imageTag                 string
	imageConfigFile          string
	outputImageFormat        string
	outputPath               string
	rpmSources               []string
	disableBaseImageRpmRepos bool
	packageSnapshotTime      string
}

func getImageCustomizerImageFormats() []string {
	// This is temporarily hardcoded until there is a dynamic way of getting the supported formats.
	// Tracking Item: https://dev.azure.com/mariner-org/polar/_workitems/edit/15309
	imageCustomizerSupportedFormats := []string{
		"vhd", "vhd-fixed", "vhdx", "qcow2", "raw", "iso", "pxe-dir", "pxe-tar", "cosi",
	}

	return imageCustomizerSupportedFormats
}

func getImageCustomizerImageFormatsString() string {
	return strings.Join(getImageCustomizerImageFormats(), ", ")
}

func buildDockerArgs(
	options *imageCustomizerOptions, buildDir string, logsDir string, rpmSources []rpmSourceInfo,
) []string {
	configDir := path.Dir(options.imageConfigFile)
	outputPathDir := path.Dir(options.outputPath)

	args := []string{
		"run", "--rm",
		docker.InteractiveFlag, // Ensure we get a TTY (not the default when run with --privileged)
		docker.PrivilegedFlag,
		"-v", buildDir + ":" + buildDir + docker.MountRWOption,
		"-v", configDir + ":" + configDir + docker.MountROOption,
		"-v", logsDir + ":" + logsDir + docker.MountRWOption,
		"-v", outputPathDir + ":" + outputPathDir + docker.MountRWOption,
		"-v", "/dev:/dev",
	}

	// The Image Customizer tool needs write access to the RPM source because
	// it create an RPM repo there.
	for _, rpmSource := range rpmSources {
		args = append(args, "-v", rpmSource.dir+":"+rpmSource.dir+docker.MountRWOption)
	}

	if options.imageFile != "" {
		inputImageDir := path.Dir(options.imageFile)
		args = append(args, "-v", inputImageDir+":"+inputImageDir+docker.MountROOption)
	}

	return args
}

type rpmSourceInfo struct {
	source string
	dir    string
}

func buildRpmSourcesInfo(rpmSources []string) []rpmSourceInfo {
	if len(rpmSources) == 0 {
		return nil
	}

	processedSources := make([]rpmSourceInfo, 0, len(rpmSources))

	for _, rpmSource := range rpmSources {
		var rpmSourceDir string
		if strings.HasSuffix(rpmSource, ".repo") {
			rpmSourceDir = path.Dir(rpmSource)
		} else {
			rpmSourceDir = rpmSource
		}

		processedSources = append(processedSources, rpmSourceInfo{
			source: rpmSource,
			dir:    rpmSourceDir,
		})
	}

	return processedSources
}

func buildImageCustomizerArgs(
	options *imageCustomizerOptions, imageCustomizerCommand, buildDir, logFile, logLevel, logColor string,
	rpmSources []rpmSourceInfo,
) []string {
	args := []string{}

	args = append(args, imageCustomizerCommand)

	if options.imageFile != "" {
		args = append(args, "--image-file", options.imageFile)
	} else if options.imageTag != "" {
		args = append(args, "--image", "azurelinux:minimal-os:"+options.imageTag)
		args = append(args, "--image-cache-dir", "/azldev/image-cache")
	}

	args = append(args, []string{
		"--build-dir", buildDir,
		"--config-file", options.imageConfigFile,
		"--output-image-format", options.outputImageFormat,
		"--output-path", options.outputPath,
		"--log-level", logLevel,
		"--log-color", logColor,
		"--log-file", logFile,
	}...)

	for _, rpmSource := range rpmSources {
		args = append(args, "--rpm-source", rpmSource.source)
	}

	if options.disableBaseImageRpmRepos {
		args = append(args, "--disable-base-image-rpm-repos")
	}

	if options.packageSnapshotTime != "" {
		args = append(args, "--package-snapshot-time", options.packageSnapshotTime)
	}

	return args
}

func runImageCustomizerContainer(
	env *azldev.Env, imageCustomizerCommand string, options *imageCustomizerOptions,
) error {
	if env.DryRun() {
		return fmt.Errorf("dry-run mode is not supported for the 'image %s' command", imageCustomizerCommand)
	}

	containerTag := env.Config().Tools.ImageCustomizer.ContainerTag
	if containerTag == "" {
		return errors.New("the Image Customizer container tag is not set in the project configuration")
	}

	// Because buildDir and logsDir will be mapped to the container, they
	// cannot be empty.
	buildDir := env.WorkDir()
	if buildDir == "" {
		buildDir = filepath.Join(env.ProjectDir(), projectgen.DefaultWorkDir)
	}

	logsDir := env.LogsDir()
	if logsDir == "" {
		logsDir = filepath.Join(env.ProjectDir(), projectgen.DefaultLogDir)
	}

	timestamp := time.Now().Format(logFileTimeFormat)
	logFile := path.Join(logsDir, fmt.Sprintf("image-customizer-%s.log", timestamp))
	logMarkers := getImageCustomizerInfoMarkers()

	env.Event("Saving detailed logs to: " + logFile)

	rpmSourcesInfo := buildRpmSourcesInfo(options.rpmSources)

	dockerArgs := buildDockerArgs(options, buildDir, logsDir, rpmSourcesInfo)

	imageCustomizerArgs := buildImageCustomizerArgs(
		options, imageCustomizerCommand, buildDir, logFile, defaultLogLevel, string(env.ColorMode()),
		rpmSourcesInfo)

	_, err := docker.RunDocker(env.Context(), env, dockerArgs, containerTag, imageCustomizerArgs, logFile,
		func(_ context.Context, line string) {
			filterImageCustomizerOutput(env, line, logMarkers)
		},
	)
	if err != nil {
		return fmt.Errorf("failed to customize image: %w", err)
	}

	return nil
}
