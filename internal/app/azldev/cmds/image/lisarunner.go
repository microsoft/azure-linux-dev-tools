// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
)

const (
	// lisaDirName is the parent directory under the project work dir for LISA-related state.
	lisaDirName = "lisa"
	// lisaVenvDirName is the name of the venv subdirectory for LISA.
	lisaVenvDirName = "venv"
	// lisaFrameworkDirName is the subdirectory for cloned LISA framework repos.
	lisaFrameworkDirName = "framework"
	// lisaProgram is the LISA executable name inside the venv.
	lisaProgram = "lisa"
	// shortSHALength is the number of characters to use from a SHA for directory names.
	shortSHALength = 12
	// lisaGeneratedRunbookPrefix is prepended to generated runbook filenames.
	lisaGeneratedRunbookPrefix = "azldev-generated-"
)

// RunLisaSuite runs a LISA-based test suite by cloning the framework repo, setting up a
// venv, generating a runbook from the configured test cases, and invoking LISA.
func RunLisaSuite(
	env *azldev.Env, suiteConfig *projectconfig.TestSuiteConfig,
	imageConfig *projectconfig.ImageConfig, options *ImageTestOptions,
) error {
	lisaConfig := suiteConfig.Lisa
	if lisaConfig == nil {
		return fmt.Errorf("test suite %#q is missing lisa configuration", suiteConfig.Name)
	}

	slog.Info("Running LISA test suite",
		slog.String("name", suiteConfig.Name),
		slog.String("framework-ref", lisaConfig.Framework.Ref),
		slog.Int("test-cases", len(lisaConfig.TestCases)),
		slog.String("image-path", options.ImagePath),
	)

	// Ensure python3 and git are available.
	if err := prereqs.RequireExecutable(env, pythonProgram, nil); err != nil {
		return fmt.Errorf("python3 is required to run LISA tests:\n%w", err)
	}

	if err := prereqs.RequireExecutable(env, "git", nil); err != nil {
		return fmt.Errorf("git is required to clone LISA repos:\n%w", err)
	}

	lisaBaseDir := filepath.Join(env.WorkDir(), lisaDirName)

	// Generate an ephemeral admin SSH key pair for VM access; it is removed once the
	// suite finishes.
	adminKeyPath, keyCleanup, err := generateAdminKeyPair(env, lisaBaseDir)
	if err != nil {
		return err
	}

	defer keyCleanup()

	// Clone/update the LISA framework.
	frameworkDir, err := ensureGitRepo(
		env, lisaBaseDir, lisaFrameworkDirName, &lisaConfig.Framework,
	)
	if err != nil {
		return fmt.Errorf("failed to set up LISA framework:\n%w", err)
	}

	// Set up or reuse the LISA venv and install the framework.
	venvDir, err := ensureLisaVenv(env, suiteConfig.Name, frameworkDir, lisaConfig.PipPreInstall, lisaConfig.PipExtras)
	if err != nil {
		return err
	}

	// The generated runbook boots a qcow2 disk on the qemu platform, so the image must
	// already be in qcow2 format; any other format is rejected.
	if err := requireQcow2Image(options.ImagePath); err != nil {
		return err
	}

	// Generate a runbook from the configured test cases and write it into the framework
	// tree so its relative includes resolve.
	runbookPath, err := writeGeneratedRunbook(
		env, frameworkDir, suiteConfig.Name, lisaConfig.TestCases, options.ImagePath, adminKeyPath,
	)
	if err != nil {
		return err
	}

	// Remove the generated runbook from the framework checkout once the suite finishes so
	// stale azldev-generated-* files don't accumulate or influence future runs.
	defer func() {
		if removeErr := env.FS().RemoveAll(runbookPath); removeErr != nil {
			slog.Warn("Failed to remove generated LISA runbook",
				slog.String("path", runbookPath), slog.Any("error", removeErr))
		}
	}()

	// Build LISA arguments with placeholder expansion.
	lisaArgs := buildLisaArgs(runbookPath, lisaConfig.ExtraArgs, imageConfig, options)

	return runLisaCommand(env, venvDir, frameworkDir, lisaArgs)
}

// runLisaCommand invokes the LISA executable from the framework's venv with the given args,
// streaming its output. LISA is run from the framework's lisa/ directory so relative
// extension paths (e.g., microsoft/testsuites) resolve correctly.
func runLisaCommand(env *azldev.Env, venvDir, frameworkDir string, lisaArgs []string) error {
	slog.Info("Running LISA", slog.Any("args", lisaArgs))

	lisaBin := filepath.Join(venvDir, "bin", lisaProgram)
	lisaWorkDir := filepath.Join(frameworkDir, "lisa")

	lisaCmd := exec.CommandContext(env, lisaBin, lisaArgs...)
	lisaCmd.Dir = lisaWorkDir
	lisaCmd.Stdout = os.Stdout
	lisaCmd.Stderr = os.Stderr

	cmd, err := env.Command(lisaCmd)
	if err != nil {
		return fmt.Errorf("failed to create LISA command:\n%w", err)
	}

	if err := cmd.Run(env); err != nil {
		return fmt.Errorf("LISA test run failed:\n%w", err)
	}

	return nil
}

// writeGeneratedRunbook generates a LISA runbook from the given test cases and writes it at
// the framework repo root, so that its repo-root-relative tier include
// ('lisa/microsoft/runbook/tiers/tier.yml') resolves against the real tier definitions.
// It returns the absolute path to the written runbook.
func writeGeneratedRunbook(
	env *azldev.Env, frameworkDir, suiteName string, testCases []string,
	imagePath, adminKeyPath string,
) (string, error) {
	if len(testCases) == 0 {
		return "", fmt.Errorf("test suite %#q has no lisa test-cases to run", suiteName)
	}

	absImagePath, err := filepath.Abs(imagePath)
	if err != nil {
		absImagePath = imagePath
	}

	runbookYAML, err := generateRunbookYAML(suiteName, testCases, absImagePath, adminKeyPath)
	if err != nil {
		return "", err
	}

	// Write the runbook at the framework repo root so its repo-root-relative include resolves.
	runbookPath := filepath.Join(frameworkDir, lisaGeneratedRunbookPrefix+suiteName+".yml")

	if err := fileutils.WriteFile(env.FS(), runbookPath, runbookYAML, fileperms.PrivateFile); err != nil {
		return "", fmt.Errorf("failed to write generated runbook %#q:\n%w", runbookPath, err)
	}

	slog.Info("Generated LISA runbook", slog.String("path", runbookPath))

	return runbookPath, nil
}

// requireQcow2Image verifies that the given disk image is in qcow2 format, which the
// generated LISA runbook requires (LISA's qemu platform boots qcow2 disks). Any other
// format is rejected.
func requireQcow2Image(imagePath string) error {
	format, err := InferImageFormat(imagePath)
	if err != nil {
		return err
	}

	if format != string(ImageFormatQcow2) {
		return fmt.Errorf(
			"unsupported image format %#q for %#q: LISA requires a qcow2 image",
			format, imagePath,
		)
	}

	return nil
}

// generateAdminKeyPair generates an ephemeral RSA SSH key pair under lisaBaseDir for LISA to
// use when accessing the booted VM. It returns the absolute path to the private key and a
// cleanup function that deletes the key pair once the suite finishes.
func generateAdminKeyPair(
	env *azldev.Env, lisaBaseDir string,
) (keyPath string, cleanup func(), err error) {
	if err := prereqs.RequireExecutable(env, "ssh-keygen", nil); err != nil {
		return "", nil, fmt.Errorf("ssh-keygen is required to generate an admin SSH key:\n%w", err)
	}

	if err := fileutils.MkdirAll(env.FS(), lisaBaseDir); err != nil {
		return "", nil, fmt.Errorf("failed to create LISA base dir %#q:\n%w", lisaBaseDir, err)
	}

	keysDir, err := fileutils.MkdirTemp(env.FS(), lisaBaseDir, "admin-key-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temporary admin key dir:\n%w", err)
	}

	privateKeyPath := filepath.Join(keysDir, "id_rsa")

	slog.Info("Generating ephemeral admin SSH key pair", slog.String("path", privateKeyPath))

	keygenCmd := exec.CommandContext(
		env, "ssh-keygen", "-t", "rsa", "-b", "4096", "-f", privateKeyPath, "-N", "", "-q",
	)

	cmd, err := env.Command(keygenCmd)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create ssh-keygen command:\n%w", err)
	}

	if err := cmd.Run(env); err != nil {
		return "", nil, fmt.Errorf("failed to generate admin SSH key pair:\n%w", err)
	}

	cleanup = func() {
		slog.Info("Removing ephemeral admin SSH key pair", slog.String("path", keysDir))

		if removeErr := env.FS().RemoveAll(keysDir); removeErr != nil {
			slog.Warn("Failed to remove ephemeral admin SSH key pair",
				slog.String("path", keysDir), slog.Any("error", removeErr))
		}
	}

	return privateKeyPath, cleanup, nil
}

// ensureGitRepo clones a git repo (if not already present) and checks out the specified
// commit SHA. Returns the path to the cloned repo directory.
func ensureGitRepo(
	env *azldev.Env, baseDir string, category string, source *projectconfig.GitSourceConfig,
) (string, error) {
	if len(source.Ref) < shortSHALength {
		return "", fmt.Errorf(
			"invalid git ref %#q: must be at least %d characters", source.Ref, shortSHALength,
		)
	}

	shortSHA := source.Ref[:shortSHALength]
	repoDir := filepath.Join(baseDir, category, shortSHA)

	repoExists, err := fileutils.DirExists(env.FS(), repoDir)
	if err != nil {
		return "", fmt.Errorf("cannot check repo dir at %#q:\n%w", repoDir, err)
	}

	gitProvider, err := git.NewGitProviderImpl(env, env)
	if err != nil {
		return "", fmt.Errorf("failed to create git provider:\n%w", err)
	}

	if !repoExists {
		slog.Info("Cloning git repo",
			slog.String("url", source.GitURL),
			slog.String("ref", source.Ref),
			slog.String("dest", repoDir),
		)

		if err := gitProvider.Clone(env, source.GitURL, repoDir); err != nil {
			return "", fmt.Errorf("failed to clone %#q:\n%w", source.GitURL, err)
		}
	} else {
		slog.Info("Reusing existing git repo",
			slog.String("path", repoDir),
			slog.String("ref", source.Ref),
		)
	}

	// Always checkout the pinned ref, even when reusing an existing checkout, so an
	// interrupted or externally-modified clone still runs against the correct revision.
	if err := gitProvider.Checkout(env, repoDir, source.Ref); err != nil {
		return "", fmt.Errorf("failed to checkout %#q:\n%w", source.Ref, err)
	}

	return repoDir, nil
}

// ensureLisaVenv creates or reuses a Python venv for LISA and installs the framework via
// pip install -e. If pipExtras are specified, they are appended as pip extras
// (e.g., pip install -e ".[azure,legacy]").
func ensureLisaVenv(
	env *azldev.Env, suiteName string, frameworkDir string,
	pipPreInstall []string, pipExtras []string,
) (string, error) {
	venvDir := filepath.Join(env.WorkDir(), lisaDirName, lisaVenvDirName, suiteName)

	venvPython := filepath.Join(venvDir, "bin", pythonProgram)

	venvExists, err := fileutils.Exists(env.FS(), venvPython)
	if err != nil {
		return "", fmt.Errorf("cannot check LISA venv at %#q:\n%w", venvDir, err)
	}

	if !venvExists {
		slog.Info("Creating LISA Python venv", slog.String("path", venvDir))

		if err := createPythonVenv(env, venvDir); err != nil {
			return "", err
		}
	} else {
		slog.Info("Reusing existing LISA venv", slog.String("path", venvDir))
	}

	// Install pre-install packages before the framework (to override version pins).
	if len(pipPreInstall) > 0 {
		slog.Info("Installing pre-install packages", slog.Any("packages", pipPreInstall))

		preInstallArgs := append([]string{"-m", "pip", "install", "--quiet"}, pipPreInstall...)

		preInstallCmd := exec.CommandContext(env, venvPython, preInstallArgs...)
		preInstallCmd.Stdout = os.Stdout
		preInstallCmd.Stderr = os.Stderr

		cmd, err := env.Command(preInstallCmd)
		if err != nil {
			return "", fmt.Errorf("failed to create pip pre-install command:\n%w", err)
		}

		if err := cmd.Run(env); err != nil {
			return "", fmt.Errorf("failed to install pre-install packages:\n%w", err)
		}
	}

	// Always refresh LISA installation from the framework directory.
	slog.Info("Installing LISA framework",
		slog.String("framework", frameworkDir),
	)

	// Build the pip install target, appending extras if specified.
	pipTarget := frameworkDir
	if len(pipExtras) > 0 {
		pipTarget = frameworkDir + "[" + strings.Join(pipExtras, ",") + "]"
	}

	pipCmd := exec.CommandContext(
		env, venvPython, "-m", "pip", "install", "--quiet", "-e", pipTarget,
	)
	pipCmd.Stdout = os.Stdout
	pipCmd.Stderr = os.Stderr

	cmd, err := env.Command(pipCmd)
	if err != nil {
		return "", fmt.Errorf("failed to create pip install command:\n%w", err)
	}

	if err := cmd.Run(env); err != nil {
		return "", fmt.Errorf("failed to install LISA framework:\n%w", err)
	}

	return venvDir, nil
}

// buildLisaArgs constructs the LISA command-line arguments. The runbook path is passed
// via -r, and extra-args are appended after placeholder expansion.
func buildLisaArgs(
	runbookPath string,
	extraArgs []string,
	imageConfig *projectconfig.ImageConfig,
	options *ImageTestOptions,
) []string {
	absImagePath, err := filepath.Abs(options.ImagePath)
	if err != nil {
		absImagePath = options.ImagePath
	}

	replacer := strings.NewReplacer(
		imagePlaceholder, absImagePath,
		imageNamePlaceholder, options.ImageName,
		capabilitiesPlaceholder, strings.Join(imageConfig.Capabilities.EnabledNames(), ","),
	)

	baseArgs := []string{"-r", runbookPath}
	args := make([]string, 0, len(baseArgs)+len(extraArgs))
	args = append(args, baseArgs...)

	for _, arg := range extraArgs {
		args = append(args, replacer.Replace(arg))
	}

	return args
}
