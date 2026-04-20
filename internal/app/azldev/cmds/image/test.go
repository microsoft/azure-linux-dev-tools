// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
)

// ImageTestOptions holds the options for the 'image test' command.
type ImageTestOptions struct {
	// ImageName is the name of the image (positional argument), used to look up its
	// test suites and optionally resolve the image artifact path.
	ImageName string

	// TestSuites optionally selects specific test suites to run. When empty, all test
	// suites associated with the image are run.
	TestSuites []string

	// ImagePath is an optional explicit path to the image file. When empty, the image
	// artifact is resolved from the image name in the output directory.
	ImagePath string

	// JUnitXMLPath is an optional path for writing JUnit XML output.
	JUnitXMLPath string
}

func testOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewImageTestCmd())
}

// NewImageTestCmd constructs a [cobra.Command] for the 'image test' command.
func NewImageTestCmd() *cobra.Command {
	options := &ImageTestOptions{}

	cmd := &cobra.Command{
		Use:   "test IMAGE_NAME",
		Short: "Run tests against an Azure Linux image",
		Long: `Run tests against an Azure Linux image using test suites defined in the
project configuration.

Test suites are defined in the [test-suites] section of azldev.toml and referenced
by images via the [images.NAME.tests] subtable. Each test suite specifies a type
(pytest or lisa) and framework-specific configuration in a matching subtable.

By default, all test suites associated with the named image are run. Use
--test-suite to select specific suites (may be repeated).

The image artifact can be specified explicitly with --image-path, or resolved
automatically from the image name in the output directory.

For pytest tests, azldev creates a Python virtual environment, installs
dependencies from pyproject.toml in the working directory, and runs pytest
with the configured test paths and extra arguments. Use {image-path} in
extra-args to insert the image path. Glob patterns (including **) in
test-paths are expanded automatically.

For LISA tests, the test runner executes on the host and boots the image in a
QEMU VM.`,
		Example: `  # Run all test suites for an image (artifact auto-resolved from output dir)
  azldev image test vm-base

  # Run all test suites with an explicit image path
  azldev image test vm-base --image-path ./out/images/vm-base/image.raw

  # Run a specific test suite
  azldev image test vm-base --test-suite common-vm-checks

  # Run multiple specific test suites
  azldev image test vm-base --test-suite common-vm-checks --test-suite vm-base-checks

  # Generate JUnit XML output
  azldev image test vm-base --junit-xml results.xml`,
		Args: cobra.ExactArgs(1),
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ImageName = args[0]

			return nil, runImageTest(env, options)
		}),
		ValidArgsFunction: generateImageNameCompletions,
	}

	cmd.Flags().StringSliceVar(&options.TestSuites, "test-suite", nil,
		"Name of a test suite to run (may be repeated; defaults to all suites for the image)")

	cmd.Flags().StringVarP(&options.ImagePath, "image-path", "i", "",
		"Path to the disk image file (resolved from image name if not specified)")
	_ = cmd.MarkFlagFilename("image-path")

	cmd.Flags().StringVar(&options.JUnitXMLPath, "junit-xml", "",
		"Path for writing JUnit XML output")
	_ = cmd.MarkFlagFilename("junit-xml")

	return cmd
}

// runImageTest resolves which test suites to run and dispatches each one.
func runImageTest(env *azldev.Env, options *ImageTestOptions) error {
	cfg := env.Config()
	if cfg == nil {
		return errors.New("no project configuration loaded")
	}

	// Resolve the image config from the positional argument.
	imageConfig, err := ResolveImageByName(env, options.ImageName)
	if err != nil {
		return err
	}

	// Resolve image path: explicit --image-path takes precedence, otherwise resolve
	// from the image name in the output directory.
	imagePath := options.ImagePath
	if imagePath == "" {
		var resolveErr error

		imagePath, _, resolveErr = findImageArtifact(env, options.ImageName, "", AllImageFormats())
		if resolveErr != nil {
			return resolveErr
		}

		slog.Info("Resolved image artifact",
			slog.String("image", options.ImageName),
			slog.String("path", imagePath),
		)
	}

	// Validate that the image file exists.
	if err := validateFileExists(env.FS(), imagePath); err != nil {
		return fmt.Errorf("image path:\n%w", err)
	}

	options.ImagePath = imagePath

	// Absolutize JUnitXMLPath against the user's CWD so pytest writes to the location the
	// user expected — pytest itself resolves relative paths against its own working
	// directory (the test suite's working-dir), which is rarely what the user intended.
	if options.JUnitXMLPath != "" && !filepath.IsAbs(options.JUnitXMLPath) {
		absJUnitPath, err := filepath.Abs(options.JUnitXMLPath)
		if err != nil {
			return fmt.Errorf("failed to resolve --junit-xml path %#q:\n%w", options.JUnitXMLPath, err)
		}

		options.JUnitXMLPath = absJUnitPath
	}

	// Determine which test suites to run.
	suiteNames := resolveTestSuiteNames(imageConfig, options.TestSuites)

	// Warn when explicitly requested suites are not referenced by the image config.
	if len(options.TestSuites) > 0 {
		warnUnassociatedSuites(options.ImageName, imageConfig, options.TestSuites)
	}

	if len(suiteNames) == 0 {
		slog.Warn("No test suites to run for image", slog.String("image", options.ImageName))

		return nil
	}

	// Resolve and run each test suite, continuing past failures so all suites get a chance
	// to run. Config/resolution errors abort immediately since they indicate a broken setup.
	var testFailures []string

	for _, suiteName := range suiteNames {
		suiteConfig, err := resolveTestSuiteByName(cfg, suiteName)
		if err != nil {
			return err
		}

		if err := runTestSuite(env, suiteConfig, imageConfig, options); err != nil {
			slog.Error("Test suite failed",
				slog.String("suite", suiteName),
				slog.Any("error", err),
			)

			testFailures = append(testFailures, suiteName)
		}
	}

	if len(testFailures) > 0 {
		return fmt.Errorf("%d of %d test suite(s) failed: %s",
			len(testFailures), len(suiteNames), strings.Join(testFailures, ", "))
	}

	return nil
}

// resolveTestSuiteNames determines which test suites to run. If explicit names are
// provided, they are used as-is. Otherwise, all test suites associated with the image
// are returned.
func resolveTestSuiteNames(
	imageConfig *projectconfig.ImageConfig, explicitSuites []string,
) []string {
	if len(explicitSuites) > 0 {
		return explicitSuites
	}

	return imageConfig.TestNames()
}

// warnUnassociatedSuites logs a warning for each explicitly requested test suite
// that is not referenced by the image's test configuration.
func warnUnassociatedSuites(
	imageName string, imageConfig *projectconfig.ImageConfig, explicitSuites []string,
) {
	imageTestNames := imageConfig.TestNames()

	for _, name := range explicitSuites {
		if !slices.Contains(imageTestNames, name) {
			slog.Warn("Test suite is not associated with image",
				slog.String("suite", name),
				slog.String("image", imageName),
			)
		}
	}
}

// resolveTestSuiteByName looks up a test suite by name in the project configuration.
func resolveTestSuiteByName(
	cfg *projectconfig.ProjectConfig, suiteName string,
) (*projectconfig.TestSuiteConfig, error) {
	suiteConfig, ok := cfg.TestSuites[suiteName]
	if !ok {
		availableSuites := lo.Keys(cfg.TestSuites)
		sort.Strings(availableSuites)

		if len(availableSuites) == 0 {
			return nil, fmt.Errorf(
				"test suite %#q not found; no test suites defined in project configuration", suiteName)
		}

		return nil, fmt.Errorf(
			"test suite %#q not found; available test suites: %s",
			suiteName, strings.Join(availableSuites, ", "),
		)
	}

	return &suiteConfig, nil
}

// runTestSuite dispatches a single test suite to the appropriate runner.
func runTestSuite(
	env *azldev.Env, suiteConfig *projectconfig.TestSuiteConfig,
	imageConfig *projectconfig.ImageConfig, options *ImageTestOptions,
) error {
	switch suiteConfig.Type {
	case projectconfig.TestTypePytest:
		return RunPytestSuite(env, suiteConfig, imageConfig, options)

	case projectconfig.TestTypeLisa:
		return RunLisaSuite(env, suiteConfig, imageConfig, options)

	default:
		return fmt.Errorf("unsupported test type %#q for test suite %#q", suiteConfig.Type, suiteConfig.Name)
	}
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
