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
	// tests and optionally resolve the image artifact path.
	ImageName string

	// Tests optionally selects specific tests to run. When empty, all tests
	// associated with the image (directly or via test-groups) are run.
	Tests []string

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
		Long: `Run tests against an Azure Linux image using tests defined in the
project configuration.

Tests are defined in the [tests] section of azldev.toml and referenced by images
via the [images.NAME.tests] subtable, either directly by name or through a
test-group. Each test specifies a type and framework-specific configuration in
a matching subtable.

By default, all tests associated with the named image (directly or via a
test-group) are run. Use --test to select specific tests (may be repeated).

The image artifact can be specified explicitly with --image-path, or resolved
automatically from the image name in the output directory.

For pytest tests, azldev creates a Python virtual environment, installs
dependencies from pyproject.toml in the working directory, and runs pytest
with the configured test paths and extra arguments. Use {image-path} in
extra-args to insert the image path. Glob patterns (including **) in
test-paths are expanded automatically.`,
		Example: `  # Run all tests for an image (artifact auto-resolved from output dir)
  azldev image test vm-base

  # Run all tests with an explicit image path
  azldev image test vm-base --image-path ./out/images/vm-base/image.raw

  # Run a specific test
  azldev image test vm-base --test common-vm-checks

  # Run multiple specific tests
  azldev image test vm-base --test common-vm-checks --test vm-base-checks

  # Generate JUnit XML output
  azldev image test vm-base --junit-xml results.xml`,
		Args: cobra.ExactArgs(1),
		RunE: azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			options.ImageName = args[0]

			return nil, runImageTest(env, options)
		}),
		ValidArgsFunction: generateImageNameCompletions,
	}

	cmd.Flags().StringSliceVar(&options.Tests, "test", nil,
		"Name of a test to run (may be repeated; defaults to all tests for the image)")

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
	// directory (the test's working-dir), which is rarely what the user intended.
	if options.JUnitXMLPath != "" && !filepath.IsAbs(options.JUnitXMLPath) {
		absJUnitPath, err := filepath.Abs(options.JUnitXMLPath)
		if err != nil {
			return fmt.Errorf("failed to resolve --junit-xml path %#q:\n%w", options.JUnitXMLPath, err)
		}

		options.JUnitXMLPath = absJUnitPath
	}

	// Determine which tests to run.
	imageTestNames := expandImageTestRefs(imageConfig, cfg)
	testNames := resolveTestNames(imageTestNames, options.Tests)

	// Warn when explicitly requested tests are not referenced by the image config.
	if len(options.Tests) > 0 {
		warnUnassociatedTests(options.ImageName, imageTestNames, options.Tests)
	}

	if len(testNames) == 0 {
		slog.Warn("No tests to run for image", slog.String("image", options.ImageName))

		return nil
	}

	// Resolve and run each test, continuing past failures so all tests get a chance
	// to run. Config/resolution errors abort immediately since they indicate a broken setup.
	var testFailures []string

	for _, testName := range testNames {
		testConfig, err := resolveTestByName(cfg, testName)
		if err != nil {
			return err
		}

		if err := runTest(env, testConfig, imageConfig, options); err != nil {
			slog.Error("Test failed",
				slog.String("test", testName),
				slog.Any("error", err),
			)

			testFailures = append(testFailures, testName)
		}
	}

	if len(testFailures) > 0 {
		return fmt.Errorf("%d of %d test(s) failed: %s",
			len(testFailures), len(testNames), strings.Join(testFailures, ", "))
	}

	return nil
}

// expandImageTestRefs returns the deduplicated set of test names associated with
// an image — combining direct test refs and the membership of every referenced
// test-group. Group refs that do not resolve in the project config are silently
// skipped (already rejected by project validation; this is a defensive guard).
// Ordering is preserved (direct refs first, then group members in group order).
func expandImageTestRefs(
	imageConfig *projectconfig.ImageConfig, cfg *projectconfig.ProjectConfig,
) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)

	add := func(name string) {
		if _, ok := seen[name]; ok {
			return
		}

		seen[name] = struct{}{}
		result = append(result, name)
	}

	for _, name := range imageConfig.TestRefNames() {
		add(name)
	}

	for _, groupName := range imageConfig.TestRefGroups() {
		group, ok := cfg.TestGroups[groupName]
		if !ok {
			continue
		}

		for _, member := range group.Tests {
			add(member)
		}
	}

	return result
}

// resolveTestNames determines which tests to run. If explicit names are provided,
// they are used as-is (so users can deliberately invoke a test that is not yet
// associated with the image — useful for one-off debugging). Otherwise, all tests
// associated with the image are returned.
func resolveTestNames(imageTestNames, explicitTests []string) []string {
	if len(explicitTests) > 0 {
		return explicitTests
	}

	return imageTestNames
}

// warnUnassociatedTests logs a warning for each explicitly requested test that is
// not part of the image's expanded test set (direct refs + group members).
func warnUnassociatedTests(imageName string, imageTestNames, explicitTests []string) {
	for _, name := range explicitTests {
		if !slices.Contains(imageTestNames, name) {
			slog.Warn("Test is not associated with image",
				slog.String("test", name),
				slog.String("image", imageName),
			)
		}
	}
}

// resolveTestByName looks up a test by name in the project configuration.
func resolveTestByName(
	cfg *projectconfig.ProjectConfig, testName string,
) (*projectconfig.TestConfig, error) {
	testConfig, ok := cfg.Tests[testName]
	if !ok {
		availableTests := lo.Keys(cfg.Tests)
		sort.Strings(availableTests)

		if len(availableTests) == 0 {
			return nil, fmt.Errorf(
				"test %#q not found; no tests defined in project configuration", testName)
		}

		return nil, fmt.Errorf(
			"test %#q not found; available tests: %s",
			testName, strings.Join(availableTests, ", "),
		)
	}

	return &testConfig, nil
}

// runTest dispatches a single test to the appropriate runner.
func runTest(
	env *azldev.Env, testConfig *projectconfig.TestConfig,
	imageConfig *projectconfig.ImageConfig, options *ImageTestOptions,
) error {
	switch testConfig.Type {
	case projectconfig.TestTypePytest:
		return RunPytest(env, testConfig, imageConfig, options)

	case projectconfig.TestTypeLisa:
		return fmt.Errorf("LISA tests cannot be run locally via 'azldev image test'; "+
			"test %#q must be run through the LISA infrastructure", testConfig.Name)

	default:
		return fmt.Errorf("unsupported test type %#q for test %#q", testConfig.Type, testConfig.Name)
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
