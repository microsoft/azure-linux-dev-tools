// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/cobra"
)

const (
	// testRunnerLisa is the only supported test runner.
	testRunnerLisa = "lisa"

	// testImagePrefix is the prefix used for qcow2 images created during image testing.
	testImagePrefix = "azldevtest"
)

// ImageTestOptions holds the options for the 'image test' command.
type ImageTestOptions struct {
	ImagePath           string
	TestRunner          string
	RunbookPath         string
	AdminPrivateKeyPath string
}

func testOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewImageTestCmd())
}

// NewImageTestCmd constructs a [cobra.Command] for the 'image test' command.
func NewImageTestCmd() *cobra.Command {
	options := &ImageTestOptions{}

	cmd := &cobra.Command{
		Use:   "test",
		Short: "Run tests against an Azure Linux image",
		Long: `Run tests against an Azure Linux image using a supported test runner.

Currently only the LISA test runner is supported. The image must be in qcow2,
vhd, or vhdfixed format. If the image is in vhd/vhdfixed format it is
automatically converted to qcow2 before running the tests.

Requirements:
  - lisa (Installation instructions: https://github.com/microsoft/lisa/blob/main/INSTALL.md)
  - runbook file (YAML format defining the tests to run: https://github.com/microsoft/lisa/blob/main/docs/Runbooks.md)
  - qemu-img (for vhd/vhdfixed to qcow2 conversion, if needed)`,
		Example: `  # Run LISA tests against a qcow2 image
  azldev image test --image-path ./out/image.qcow2 --test-runner lisa --runbook-path ./runbooks/smoke.yml

  # Run LISA tests against a vhd image (auto-converted to qcow2)
  azldev image test --image-path ./out/image.vhd --test-runner lisa --runbook-path ./runbooks/smoke.yml`,
		RunE: azldev.RunFuncWithoutRequiredConfig(func(env *azldev.Env) (interface{}, error) {
			return nil, testImage(env, options)
		}),
	}

	cmd.Flags().StringVarP(&options.ImagePath, "image-path", "i", "",
		"Path to the disk image file to test")
	_ = cmd.MarkFlagRequired("image-path")
	_ = cmd.MarkFlagFilename("image-path")

	cmd.Flags().StringVar(&options.TestRunner, "test-runner", "",
		"Test runner to use (currently only 'lisa' is supported)")
	_ = cmd.MarkFlagRequired("test-runner")

	cmd.Flags().StringVarP(&options.RunbookPath, "runbook-path", "r", "",
		"Path to the test runbook file")
	_ = cmd.MarkFlagRequired("runbook-path")
	_ = cmd.MarkFlagFilename("runbook-path")

	cmd.Flags().StringVarP(&options.AdminPrivateKeyPath, "admin-private-key-path", "k", "",
		"Path to the admin SSH private key file passed to LISA")
	_ = cmd.MarkFlagRequired("admin-private-key-path")
	_ = cmd.MarkFlagFilename("admin-private-key-path")

	return cmd
}

// testImage implements the core logic for the 'image test' command.
func testImage(env *azldev.Env, options *ImageTestOptions) error {
	// Check 1: validate test runner.
	if err := CheckTestRunner(options.TestRunner); err != nil {
		return err
	}

	// Check 2: verify lisa is installed.
	if err := checkLisaInstalled(env); err != nil {
		return err
	}

	// Check 3: validate admin private key path.
	if err := validateFileExists(env.FS(), options.AdminPrivateKeyPath); err != nil {
		return fmt.Errorf("--admin-private-key-path:\n%w", err)
	}

	// Check 4: resolve the image to a qcow2 path (converting if necessary).
	qcow2Path, err := ResolveQcow2Image(env, options.ImagePath)
	if err != nil {
		return err
	}

	return runLisa(env, options.RunbookPath, qcow2Path, options.AdminPrivateKeyPath)
}

// CheckTestRunner returns an error if the test runner is not supported.
func CheckTestRunner(runner string) error {
	if !strings.EqualFold(runner, testRunnerLisa) {
		return fmt.Errorf("test runner %#q is not supported; only %#q is supported at this time", runner, testRunnerLisa)
	}

	return nil
}

// checkLisaInstalled verifies that the lisa executable is available on the host.
func checkLisaInstalled(env *azldev.Env) error {
	if !env.CommandInSearchPath(testRunnerLisa) {
		return errors.New("'lisa' is not installed or not found in PATH; " +
			"please install LISA before running image tests")
	}

	return nil
}

// ResolveQcow2Image inspects the image at imagePath and returns a path to a qcow2 image.
// If the image is already qcow2 it is returned as-is. If it is vhd or vhdfixed it is
// converted to qcow2 in a temporary directory. Any other format is an error.
func ResolveQcow2Image(env *azldev.Env, imagePath string) (string, error) {
	format, err := InferImageFormat(imagePath)
	if err != nil {
		return "", err
	}

	switch format {
	case string(ImageFormatQcow2):
		slog.Info("Image is already in qcow2 format, using as-is", slog.String("path", imagePath))

		return imagePath, nil

	case string(ImageFormatVhd):
		return convertToQcow2(env, imagePath)

	default:
		return "", fmt.Errorf(
			"image format %#q is not supported for testing; supported formats: qcow2, vhd, vhdfixed",
			format,
		)
	}
}

// convertToQcow2 converts a vhd/vhdfixed disk image to qcow2 format using qemu-img and
// returns the path to the converted file. The converted image is written alongside the
// source file and is the caller's responsibility to clean up (or accept it as a leftover).
func convertToQcow2(env *azldev.Env, srcPath string) (string, error) {
	if !env.CommandInSearchPath("qemu-img") {
		return "", errors.New("'qemu-img' is not installed or not found in PATH; " +
			"it is required to convert vhd/vhdfixed images to qcow2")
	}

	baseName := strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath))
	destFileName := baseName + ".qcow2"

	// Write the converted image alongside the source file so the path is predictable.
	destPath := filepath.Join(filepath.Dir(srcPath), testImagePrefix+"-"+destFileName)

	slog.Info("Converting image to qcow2",
		slog.String("src", srcPath),
		slog.String("dest", destPath),
	)

	if env.DryRun() {
		slog.Info("Dry-run: would convert image to qcow2",
			slog.String("src", srcPath),
			slog.String("dest", destPath),
		)

		return destPath, nil
	}

	convertCmd := exec.CommandContext(
		env, "qemu-img", "convert", "-O", "qcow2", srcPath, destPath,
	)
	convertCmd.Stdout = os.Stdout
	convertCmd.Stderr = os.Stderr

	cmd, err := env.Command(convertCmd)
	if err != nil {
		return "", fmt.Errorf("failed to create qemu-img command:\n%w", err)
	}

	if err = cmd.Run(env); err != nil {
		return "", fmt.Errorf("failed to convert image %#q to qcow2:\n%w", srcPath, err)
	}

	slog.Info("Conversion complete", slog.String("dest", destPath))

	return destPath, nil
}

// validateFileExists returns an error if the path does not point to an existing regular file.
func validateFileExists(fs opctx.FS, path string) error {
	isDir, err := fileutils.DirExists(fs, path)
	if err != nil {
		return fmt.Errorf("cannot access %#q:\n%w", path, err)
	}

	if isDir {
		return fmt.Errorf("%#q is a directory, expected a file", path)
	}

	exists, err := fileutils.Exists(fs, path)
	if err != nil {
		return fmt.Errorf("cannot access %#q:\n%w", path, err)
	}

	if !exists {
		return fmt.Errorf("file not found: %#q", path)
	}

	return nil
}

// runLisa executes `lisa -r <runbookPath> -v "qcow2:<imagePath>"` and streams its
// stdout and stderr directly to the terminal.
func runLisa(env *azldev.Env, runbookPath, qcow2ImagePath, adminPrivateKeyPath string) error {
	slog.Info("Running LISA tests",
		slog.String("runbook", runbookPath),
		slog.String("image", qcow2ImagePath),
	)

	args := []string{
		"-r", runbookPath,
		"-v", "qcow2:" + qcow2ImagePath,
		"-v", "admin_private_key_file:" + adminPrivateKeyPath,
	}

	lisaCmd := exec.CommandContext(
		env,
		testRunnerLisa,
		args...,
	)
	lisaCmd.Stdout = os.Stdout
	lisaCmd.Stderr = os.Stderr

	cmd, err := env.Command(lisaCmd)
	if err != nil {
		return fmt.Errorf("failed to create lisa command:\n%w", err)
	}

	if err = cmd.Run(env); err != nil {
		return fmt.Errorf("lisa test run failed:\n%w", err)
	}

	return nil
}
