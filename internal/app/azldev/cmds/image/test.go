// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/imagebuilder"
	"github.com/microsoft/azure-linux-dev-tools/internal/lisa"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/pytest"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/imageformat"
	"github.com/spf13/cobra"
)

// ImageTestOptions holds the options for the 'image test' command.
type ImageTestOptions struct {
	// ImageName is the name of the image (positional argument), used to look up its
	// tests and optionally resolve the image artifact path.
	ImageName string

	// TestSelectors optionally selects specific test names or test-group names to run.
	// When empty, all tests associated with the image are run.
	TestSelectors []string

	// ImagePath is an optional explicit path to the image file. When empty, the image
	// artifact is resolved from the image name in the output directory.
	ImagePath string

	// LisaDir optionally points to an already-cloned LISA framework checkout. When set,
	// LISA tests run against this checkout directly instead of azldev cloning the
	// framework's configured git source.
	LisaDir string

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

Images reference tests via [images.NAME.tests.tests] entries, which resolve to
project-level [tests.X] definitions or named [test-groups].

By default, all tests associated with the named image are run. Use
--test-suite to select specific test names or test-group names (may be repeated).

The image artifact can be specified explicitly with --image-path, or resolved
automatically from the image name in the output directory.

For pytest tests, azldev creates a Python virtual environment, installs
dependencies from pyproject.toml in the working directory, and runs pytest
with the configured test paths and extra arguments. Use {image-path} in
extra-args to insert the image path. Glob patterns (including **) in
test-paths are expanded automatically.

For LISA tests, the test runner executes on the host and boots the image in a
QEMU VM. azldev clones the LISA framework, generates a runbook from the test's
configured criteria, and runs it against the image. azldev generates an
ephemeral SSH key pair to access the booted VM and removes it once the test
finishes. [tests.X] LISA definitions require a [tests.X.lisa.source] (git-url,
ref) to run locally; without one, the test is metadata-only and must be run
through the LISA infrastructure. Use --lisa-dir to run against an already-cloned
LISA checkout instead of cloning; this also allows running LISA tests that have
no [tests.X.lisa.source] configured.`,
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

	cmd.Flags().StringSliceVar(&options.TestSelectors, "test-suite", nil,
		"Name of a test or test-group to run (may be repeated; defaults to all tests for the image)")

	cmd.Flags().StringVarP(&options.ImagePath, "image-path", "i", "",
		"Path to the disk image file (resolved from image name if not specified)")
	_ = cmd.MarkFlagFilename("image-path")

	cmd.Flags().StringVar(&options.LisaDir, "lisa-dir", "",
		"Path to an already-cloned LISA framework checkout to run against, instead of "+
			"cloning the framework's configured git source")
	_ = cmd.MarkFlagDirname("lisa-dir")

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

	imageConfig, err := prepareImageTest(env, options)
	if err != nil {
		return err
	}

	resolvedTests, err := resolveImageTestsToRun(cfg, imageConfig, options.TestSelectors)
	if err != nil {
		return err
	}

	if len(resolvedTests) == 0 {
		slog.Warn("No tests to run for image", slog.String("image", options.ImageName))

		return nil
	}

	return runImageTests(env, imageConfig, options, resolvedTests)
}

func prepareImageTest(env *azldev.Env, options *ImageTestOptions) (*projectconfig.ImageConfig, error) {
	imageConfig, err := imagebuilder.ResolveImageByName(env, options.ImageName)
	if err != nil {
		//nolint:wrapcheck // Preserve the existing command-facing error unchanged.
		return nil, err
	}

	imagePath, err := resolveImageTestPath(env, options)
	if err != nil {
		return nil, err
	}

	if err := fileutils.ValidateFileExists(env.FS(), imagePath); err != nil {
		return nil, fmt.Errorf("image path:\n%w", err)
	}

	options.ImagePath = imagePath

	if options.JUnitXMLPath != "" && !filepath.IsAbs(options.JUnitXMLPath) {
		absJUnitPath, err := filepath.Abs(options.JUnitXMLPath)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve --junit-xml path %#q:\n%w", options.JUnitXMLPath, err)
		}

		options.JUnitXMLPath = absJUnitPath
	}

	return imageConfig, nil
}

func resolveImageTestPath(env *azldev.Env, options *ImageTestOptions) (string, error) {
	if options.ImagePath != "" {
		return options.ImagePath, nil
	}

	imagePath, _, err := findImageArtifact(env, options.ImageName, "", imageformat.All())
	if err != nil {
		return "", err
	}

	slog.Info("Resolved image artifact",
		slog.String("image", options.ImageName),
		slog.String("path", imagePath),
	)

	return imagePath, nil
}

func runImageTests(
	env *azldev.Env,
	imageConfig *projectconfig.ImageConfig,
	options *ImageTestOptions,
	resolvedTests []projectconfig.ResolvedTest,
) error {
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

	if len(testFailures) > 0 {
		return fmt.Errorf("%d of %d test(s) failed: %s",
			len(testFailures), len(resolvedTests), strings.Join(testFailures, ", "))
	}

	return nil
}

func resolveImageTestsToRun(
	cfg *projectconfig.ProjectConfig,
	imageConfig *projectconfig.ImageConfig,
	explicitSelectors []string,
) ([]projectconfig.ResolvedTest, error) {
	if len(explicitSelectors) > 0 {
		resolvedTests, err := cfg.ResolveTestSelectors(explicitSelectors)
		if err != nil {
			return nil, fmt.Errorf("resolve test selectors: %w", err)
		}

		return resolvedTests, nil
	}

	resolvedTests, err := cfg.ResolveImageTests(imageConfig)
	if err != nil {
		return nil, fmt.Errorf("resolve image tests: %w", err)
	}

	return resolvedTests, nil
}

func runResolvedTest(
	env *azldev.Env,
	resolvedTest projectconfig.ResolvedTest,
	imageConfig *projectconfig.ImageConfig,
	options *ImageTestOptions,
) error {
	switch resolvedTest.Definition.Type {
	case string(projectconfig.TestTypePytest):
		//nolint:wrapcheck // Preserve the existing command-facing error unchanged.
		return pytest.RunTestDefinition(env, resolvedTest, imageConfig, &pytest.Options{
			ImageName:    options.ImageName,
			ImagePath:    options.ImagePath,
			JUnitXMLPath: options.JUnitXMLPath,
		})

	case string(projectconfig.TestTypeLisa):
		//nolint:wrapcheck // Preserve the existing command-facing diagnostic unchanged.
		return lisa.RunTestDefinition(env, resolvedTest, imageConfig, &lisa.Options{
			ImageName: options.ImageName,
			ImagePath: options.ImagePath,
			LisaDir:   options.LisaDir,
		})

	case "tmt":
		return fmt.Errorf(
			"TMT tests cannot be run locally via 'azldev image test'; "+
				"test %#q is metadata-only for external orchestration",
			resolvedTest.Name,
		)

	default:
		return fmt.Errorf("unsupported test type %#q for test %#q", resolvedTest.Definition.Type, resolvedTest.Name)
	}
}
