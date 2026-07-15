// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
	"github.com/pelletier/go-toml/v2"
)

// ImageTestOptions holds the options for the 'image test' command.
type ImageTestOptions struct {
	// ImageName is the name of the image (positional argument), used to look up its
	// test suites and optionally resolve the image artifact path.
	ImageName string

	// TestSuites optionally selects specific test names or test-group names to run.
	// When empty, all tests associated with the image are run.
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
		Long: `Run tests against an Azure Linux image using test definitions declared in the
project configuration.

Images may reference tests directly via [images.NAME.tests.tests] entries, or via
named [test-groups]. Legacy [test-suites] references are still supported.

By default, all tests associated with the named image are run. Use
--test-suite to select specific test names or test-group names (may be repeated).

The image artifact can be specified explicitly with --image-path, or resolved
automatically from the image name in the output directory.

For pytest tests, azldev creates a Python virtual environment, installs
dependencies from pyproject.toml in the working directory, and runs pytest
with the configured test paths and extra arguments. Use {image-path} in
extra-args to insert the image path. Glob patterns (including **) in
test-paths are expanded automatically.`,
		Example: `  # Run all tests for an image (artifact auto-resolved from output dir)
  azldev image test vm-base

  # Run all test suites with an explicit image path
  azldev image test vm-base --image-path ./out/images/vm-base/image.raw

	# Run a specific test
	azldev image test vm-base --test-suite static-image-checks

	# Run multiple tests or a test-group
	azldev image test vm-base --test-suite static-image-checks --test-suite vm-base-functional

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
		"Name of a test or test-group to run (may be repeated; defaults to all tests for the image)")

	cmd.Flags().StringVarP(&options.ImagePath, "image-path", "i", "",
		"Path to the disk image file (resolved from image name if not specified)")
	_ = cmd.MarkFlagFilename("image-path")

	cmd.Flags().StringVar(&options.JUnitXMLPath, "junit-xml", "",
		"Path for writing JUnit XML output")
	_ = cmd.MarkFlagFilename("junit-xml")

	return cmd
}

// runImageTest resolves which tests to run and dispatches each one.
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

	resolvedTests, legacySuiteNames, err := resolveImageTestsToRun(cfg, imageConfig, options.TestSuites)
	if err != nil {
		return err
	}

	if len(resolvedTests) == 0 && len(legacySuiteNames) == 0 {
		slog.Warn("No tests to run for image", slog.String("image", options.ImageName))

		return nil
	}

	var testFailures []string

	for _, resolvedTest := range resolvedTests {
		if err := runResolvedTest(env, resolvedTest, imageConfig, options); err != nil {
			slog.Error("Test failed",
				slog.String("test", resolvedTest.Name),
				slog.Any("error", err),
			)

			testFailures = append(testFailures, resolvedTest.Name)
		}
	}

	for _, suiteName := range legacySuiteNames {
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
		total := len(resolvedTests) + len(legacySuiteNames)
		return fmt.Errorf("%d of %d test(s) failed: %s",
			len(testFailures), total, strings.Join(testFailures, ", "))
	}

	return nil
}


func resolveImageTestsToRun(
	cfg *projectconfig.ProjectConfig,
	imageConfig *projectconfig.ImageConfig,
	explicitSelectors []string,
) ([]projectconfig.ResolvedTest, []string, error) {
	if imageConfig.Tests != nil && len(imageConfig.Tests.Tests) > 0 {
		if len(explicitSelectors) > 0 {
			resolvedTests, err := cfg.ResolveTestSelectors(explicitSelectors)
			return resolvedTests, nil, err
		}

		resolvedTests, err := cfg.ResolveImageTests(imageConfig)
		return resolvedTests, nil, err
	}

	if len(explicitSelectors) > 0 {
		return nil, explicitSelectors, nil
	}

	return nil, imageConfig.TestNames(), nil
}

func runResolvedTest(
	env *azldev.Env,
	resolvedTest projectconfig.ResolvedTest,
	imageConfig *projectconfig.ImageConfig,
	options *ImageTestOptions,
) error {
	switch resolvedTest.Definition.Type {
	case string(projectconfig.TestTypePytest):
		suiteConfig, err := testDefinitionToSuiteConfig(resolvedTest)
		if err != nil {
			return err
		}

		return RunPytestSuite(env, suiteConfig, imageConfig, options)

	case string(projectconfig.TestTypeLisa):
		return fmt.Errorf("LISA tests cannot be run locally via 'azldev image test'; test %#q must be run through the LISA infrastructure", resolvedTest.Name)

	case "tmt":
		return fmt.Errorf("TMT tests cannot be run locally via 'azldev image test'; test %#q is metadata-only for external orchestration", resolvedTest.Name)

	default:
		return fmt.Errorf("unsupported test type %#q for test %#q", resolvedTest.Definition.Type, resolvedTest.Name)
	}
}

func testDefinitionToSuiteConfig(resolvedTest projectconfig.ResolvedTest) (*projectconfig.TestSuiteConfig, error) {
	pytestConfig, err := decodePytestConfig(resolvedTest.Definition.Pytest)
	if err != nil {
		return nil, fmt.Errorf("decode pytest config for test %#q:\n%w", resolvedTest.Name, err)
	}

	suiteConfig := &projectconfig.TestSuiteConfig{
		Name:        resolvedTest.Name,
		Description: resolvedTest.Definition.Description,
		Type:        projectconfig.TestTypePytest,
		Pytest:      pytestConfig,
	}

	if err := suiteConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid pytest test %#q:\n%w", resolvedTest.Name, err)
	}

	return suiteConfig, nil
}

func decodePytestConfig(raw map[string]any) (*projectconfig.PytestConfig, error) {
	if raw == nil {
		return nil, fmt.Errorf("missing [pytest] subtable")
	}

	bytes, err := toml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal pytest config:\n%w", err)
	}

	pytestConfig := &projectconfig.PytestConfig{}
	if err := toml.Unmarshal(bytes, pytestConfig); err != nil {
		return nil, fmt.Errorf("unmarshal pytest config:\n%w", err)
	}

	return pytestConfig, nil
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
		return fmt.Errorf("LISA test suites cannot be run locally via 'azldev image test'; "+
			"test suite %#q must be run through the LISA infrastructure", suiteConfig.Name)

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
