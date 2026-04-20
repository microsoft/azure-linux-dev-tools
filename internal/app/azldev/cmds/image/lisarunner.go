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
	// lisaRunbookDirName is the subdirectory for cloned runbook repos.
	lisaRunbookDirName = "runbook"
	// lisaKeysDirName is the subdirectory for auto-generated SSH key pairs.
	lisaKeysDirName = "keys"
	// lisaProgram is the LISA executable name inside the venv.
	lisaProgram = "lisa"
	// shortSHALength is the number of characters to use from a SHA for directory names.
	shortSHALength = 12
	// adminKeyPlaceholder is the placeholder for the auto-generated admin SSH private key path.
	adminKeyPlaceholder = "{admin-key-path}"
)

// RunLisaSuite runs a LISA-based test suite by cloning the framework and runbook repos,
// setting up a venv, and invoking LISA.
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
		slog.String("runbook-ref", lisaConfig.Runbook.Ref),
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

	// Clone/update the runbook repo and resolve the runbook path.
	runbookPath, err := resolveRunbookPath(env, lisaBaseDir, lisaConfig, frameworkDir)
	if err != nil {
		return err
	}

	// Generate (or reuse) an SSH key pair for LISA to use for VM access.
	adminKeyPath, err := ensureAdminKeyPair(env, lisaBaseDir)
	if err != nil {
		return err
	}

	// Build LISA arguments with placeholder expansion.
	lisaArgs := buildLisaArgs(runbookPath, lisaConfig.ExtraArgs, imageConfig, options, adminKeyPath)

	slog.Info("Running LISA", slog.Any("args", lisaArgs))

	lisaBin := filepath.Join(venvDir, "bin", lisaProgram)

	// Run LISA from the framework's lisa/ directory so that relative extension paths
	// (e.g., microsoft/testsuites) resolve correctly.
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

// resolveRunbookPath clones the runbook repo (or reuses the framework clone if they match)
// and returns the absolute path to the runbook YAML file.
func resolveRunbookPath(
	env *azldev.Env, lisaBaseDir string, lisaConfig *projectconfig.LisaConfig, frameworkDir string,
) (string, error) {
	// Reuse the framework clone if the runbook is in the same repo and at the same ref.
	runbookRepoDir := frameworkDir

	if lisaConfig.Runbook.GitURL != lisaConfig.Framework.GitURL ||
		lisaConfig.Runbook.Ref != lisaConfig.Framework.Ref {
		var err error

		runbookRepoDir, err = ensureGitRepo(
			env, lisaBaseDir, lisaRunbookDirName, &lisaConfig.Runbook.GitSourceConfig,
		)
		if err != nil {
			return "", fmt.Errorf("failed to set up LISA runbook repo:\n%w", err)
		}
	} else {
		slog.Info("Runbook is in the same repo as framework; reusing clone")
	}

	runbookPath := filepath.Join(runbookRepoDir, lisaConfig.Runbook.Path)

	runbookExists, err := fileutils.Exists(env.FS(), runbookPath)
	if err != nil {
		return "", fmt.Errorf("cannot check runbook at %#q:\n%w", runbookPath, err)
	}

	if !runbookExists {
		return "", fmt.Errorf("runbook not found at %#q in cloned repo", runbookPath)
	}

	return runbookPath, nil
}

// ensureGitRepo clones a git repo (if not already present) and checks out the specified
// commit SHA. Returns the path to the cloned repo directory.
func ensureGitRepo(
	env *azldev.Env, baseDir string, category string, source *projectconfig.GitSourceConfig,
) (string, error) {
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

		if err := gitProvider.Checkout(env, repoDir, source.Ref); err != nil {
			return "", fmt.Errorf("failed to checkout %#q:\n%w", source.Ref, err)
		}
	} else {
		slog.Info("Reusing existing git repo",
			slog.String("path", repoDir),
			slog.String("ref", source.Ref),
		)
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

// ensureAdminKeyPair generates (or reuses) an RSA SSH key pair for LISA VM access.
// The key pair is stored in the LISA work directory and reused across runs.
func ensureAdminKeyPair(env *azldev.Env, lisaBaseDir string) (string, error) {
	keysDir := filepath.Join(lisaBaseDir, lisaKeysDirName)
	privateKeyPath := filepath.Join(keysDir, "id_rsa")

	keyExists, err := fileutils.Exists(env.FS(), privateKeyPath)
	if err != nil {
		return "", fmt.Errorf("cannot check admin key at %#q:\n%w", privateKeyPath, err)
	}

	if keyExists {
		slog.Info("Reusing existing admin SSH key pair", slog.String("path", privateKeyPath))

		return privateKeyPath, nil
	}

	slog.Info("Generating admin SSH key pair", slog.String("path", privateKeyPath))

	if err := fileutils.MkdirAll(env.FS(), keysDir); err != nil {
		return "", fmt.Errorf("failed to create keys directory %#q:\n%w", keysDir, err)
	}

	keygenCmd := exec.CommandContext(
		env, "ssh-keygen", "-t", "rsa", "-b", "4096", "-f", privateKeyPath, "-N", "", "-q",
	)

	cmd, err := env.Command(keygenCmd)
	if err != nil {
		return "", fmt.Errorf("failed to create ssh-keygen command:\n%w", err)
	}

	if err := cmd.Run(env); err != nil {
		return "", fmt.Errorf("failed to generate admin SSH key pair:\n%w", err)
	}

	return privateKeyPath, nil
}

// buildLisaArgs constructs the LISA command-line arguments. The runbook path is passed
// via -r, and extra-args are appended after placeholder expansion.
func buildLisaArgs(
	runbookPath string,
	extraArgs []string,
	imageConfig *projectconfig.ImageConfig,
	options *ImageTestOptions,
	adminKeyPath string,
) []string {
	absImagePath, err := filepath.Abs(options.ImagePath)
	if err != nil {
		absImagePath = options.ImagePath
	}

	replacer := strings.NewReplacer(
		imagePlaceholder, absImagePath,
		imageNamePlaceholder, options.ImageName,
		capabilitiesPlaceholder, strings.Join(imageConfig.Capabilities.EnabledNames(), ","),
		adminKeyPlaceholder, adminKeyPath,
	)

	args := []string{"-r", runbookPath}

	for _, arg := range extraArgs {
		args = append(args, replacer.Replace(arg))
	}

	return args
}
