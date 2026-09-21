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

// RunLisaTestDefinition runs a new-style [tests.X] LISA test definition locally, generating
// a runbook from the test's configured criteria and invoking LISA. It requires either
// options.LisaDir (an already-cloned LISA checkout) or the test's [tests.X.lisa] subtable
// to include a 'source' (git-url, ref) identifying the LISA framework to clone; tests with
// neither are metadata-only and cannot be run locally (they must be run through external
// orchestration).
func RunLisaTestDefinition(
	env *azldev.Env, resolvedTest projectconfig.ResolvedTest,
	imageConfig *projectconfig.ImageConfig, options *ImageTestOptions,
) error {
	lisa := resolvedTest.Definition.Lisa
	if lisa == nil {
		return fmt.Errorf(
			"test %#q of type %#q has no [tests.%s.lisa] subtable", resolvedTest.Name,
			projectconfig.TestTypeLisa, resolvedTest.Name,
		)
	}

	// A user-provided LISA checkout (--lisa-dir) makes the 'source' (git-url, ref) optional:
	// azldev runs against the given checkout instead of cloning one.
	var framework *projectconfig.GitSourceConfig

	if options.LisaDir == "" {
		var err error

		framework, err = parseLisaSource(lisa, resolvedTest.Name)
		if err != nil {
			return err
		}
	}

	criteria, err := parseLisaCriteriaFromDefinition(lisa, resolvedTest.Name)
	if err != nil {
		return err
	}

	pipPreInstall, err := toOptionalStringSlice(lisa["pip-pre-install"], "pip-pre-install")
	if err != nil {
		return fmt.Errorf("test %#q lisa.%w", resolvedTest.Name, err)
	}

	pipExtras, err := toOptionalStringSlice(lisa["pip-extras"], "pip-extras")
	if err != nil {
		return fmt.Errorf("test %#q lisa.%w", resolvedTest.Name, err)
	}

	extraArgs, err := toOptionalStringSlice(lisa["extra-args"], "extra-args")
	if err != nil {
		return fmt.Errorf("test %#q lisa.%w", resolvedTest.Name, err)
	}

	logFields := []any{
		slog.String("name", resolvedTest.Name),
		slog.Int("criteria", len(criteria)),
		slog.String("image-path", options.ImagePath),
	}

	if framework != nil {
		logFields = append(logFields, slog.String("framework-ref", framework.Ref))
	}

	slog.Info("Running LISA test", logFields...)

	return runLisaLocally(
		env, resolvedTest.Name, framework, criteria,
		pipPreInstall, pipExtras, extraArgs, imageConfig, options,
	)
}

// runLisaLocally sets up the LISA framework and venv, generates a runbook from the given
// criteria, and invokes LISA against the given image. It backs RunLisaTestDefinition
// (new-style [tests] definitions). If options.LisaDir is set, that checkout is used directly
// instead of cloning framework; in that case framework may be nil.
func runLisaLocally(
	env *azldev.Env, name string, framework *projectconfig.GitSourceConfig, criteria []lisaCriteria,
	pipPreInstall, pipExtras, extraArgs []string,
	imageConfig *projectconfig.ImageConfig, options *ImageTestOptions,
) error {
	// Ensure python3 is available; git is only needed when azldev clones the framework
	// itself (i.e., --lisa-dir was not given).
	if err := prereqs.RequireExecutable(env, pythonProgram, nil); err != nil {
		return fmt.Errorf("python3 is required to run LISA tests:\n%w", err)
	}

	workDir := env.WorkDir()
	if workDir == "" {
		// Without a configured work directory we'd otherwise create a relative
		// 'lisa/' tree (keys, framework clones, generated runbooks) under the
		// user's CWD, which is surprising and can pollute repos. Fail fast instead.
		return fmt.Errorf(
			"cannot run LISA test %#q: project work directory is not configured", name,
		)
	}

	lisaBaseDir := filepath.Join(workDir, lisaDirName)

	// Generate an ephemeral admin SSH key pair for VM access; it is removed once the
	// test finishes.
	adminKeyPath, keyCleanup, err := generateAdminKeyPair(env, lisaBaseDir)
	if err != nil {
		return err
	}

	defer keyCleanup()

	// Resolve the LISA framework directory: either a user-provided checkout, or a
	// clone/update of the configured git source.
	frameworkDir, err := resolveLisaFrameworkDir(env, name, lisaBaseDir, framework, options)
	if err != nil {
		return err
	}

	// Set up or reuse the LISA venv and install the framework.
	venvDir, err := ensureLisaVenv(env, name, frameworkDir, pipPreInstall, pipExtras)
	if err != nil {
		return err
	}

	// The generated runbook boots a qcow2 disk on the qemu platform, so the image must
	// already be in qcow2 format; any other format is rejected.
	if err := requireQcow2Image(options.ImagePath); err != nil {
		return err
	}

	// Generate a runbook from the configured criteria and write it into the framework
	// tree so its relative includes resolve.
	runbookPath, err := writeGeneratedRunbook(env, frameworkDir, name, criteria, options.ImagePath, adminKeyPath)
	if err != nil {
		return err
	}

	// Remove the generated runbook from the framework checkout once the test finishes so
	// stale azldev-generated-* files don't accumulate or influence future runs.
	defer func() {
		if removeErr := env.FS().RemoveAll(runbookPath); removeErr != nil {
			slog.Warn("Failed to remove generated LISA runbook",
				slog.String("path", runbookPath), slog.Any("error", removeErr))
		}
	}()

	// Build LISA arguments with placeholder expansion.
	lisaArgs := buildLisaArgs(runbookPath, extraArgs, imageConfig, options)

	return runLisaCommand(env, venvDir, frameworkDir, lisaArgs)
}

// resolveLisaFrameworkDir returns the LISA framework directory to run against: either
// options.LisaDir, an already-cloned checkout provided by the user (validated to exist),
// or a clone/update of the given git source. Using a user-provided checkout avoids
// introducing any git-source metadata requirement for the test. framework may be nil only
// when options.LisaDir is set.
func resolveLisaFrameworkDir(
	env *azldev.Env, name, lisaBaseDir string, framework *projectconfig.GitSourceConfig, options *ImageTestOptions,
) (string, error) {
	if options.LisaDir != "" {
		absDir, err := filepath.Abs(options.LisaDir)
		if err != nil {
			return "", fmt.Errorf("failed to resolve --lisa-dir %#q:\n%w", options.LisaDir, err)
		}

		isDir, err := fileutils.DirExists(env.FS(), absDir)
		if err != nil {
			return "", fmt.Errorf("cannot access --lisa-dir %#q:\n%w", absDir, err)
		}

		if !isDir {
			return "", fmt.Errorf("--lisa-dir %#q does not exist or is not a directory", absDir)
		}

		slog.Info("Using user-provided LISA framework checkout", slog.String("path", absDir))

		return absDir, nil
	}

	if framework == nil {
		return "", fmt.Errorf(
			"cannot run LISA test %#q: no LISA framework source configured; specify --lisa-dir "+
				"to run against an existing checkout", name,
		)
	}

	if err := prereqs.RequireExecutable(env, "git", nil); err != nil {
		return "", fmt.Errorf("git is required to clone LISA repos:\n%w", err)
	}

	frameworkDir, err := ensureGitRepo(env, lisaBaseDir, lisaFrameworkDirName, framework)
	if err != nil {
		return "", fmt.Errorf("failed to set up LISA framework:\n%w", err)
	}

	return frameworkDir, nil
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

// writeGeneratedRunbook generates a LISA runbook from the given criteria and writes it at
// the framework repo root, so that its repo-root-relative tier include
// ('lisa/microsoft/runbook/tiers/tier.yml') resolves against the real tier definitions.
// It returns the absolute path to the written runbook.
func writeGeneratedRunbook(
	env *azldev.Env, frameworkDir, name string, criteria []lisaCriteria,
	imagePath, adminKeyPath string,
) (string, error) {
	if len(criteria) == 0 {
		return "", fmt.Errorf("test %#q has no LISA criteria to run", name)
	}

	absImagePath, err := filepath.Abs(imagePath)
	if err != nil {
		absImagePath = imagePath
	}

	runbookYAML, err := generateRunbookYAMLFromCriteria(name, criteria, absImagePath, adminKeyPath)
	if err != nil {
		return "", err
	}

	// Write the runbook at the framework repo root so its repo-root-relative include resolves.
	runbookPath := filepath.Join(frameworkDir, lisaGeneratedRunbookPrefix+name+".yml")

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

		// Ensure the parent directory exists so the clone doesn't depend on it having
		// been created elsewhere (e.g., by admin-key setup), keeping ensureGitRepo
		// self-contained.
		if err := fileutils.MkdirAll(env.FS(), filepath.Dir(repoDir)); err != nil {
			return "", fmt.Errorf("failed to create repo parent dir for %#q:\n%w", repoDir, err)
		}

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
		if !repoExists {
			return "", fmt.Errorf("failed to checkout %#q:\n%w", source.Ref, err)
		}

		// The existing checkout may be interrupted, externally modified, or predate the
		// pinned ref (e.g., the user updated it to a commit not present when originally
		// cloned). Remove and re-clone once so updated refs work without manual cleanup.
		slog.Warn("Checkout failed in existing repo; removing and re-cloning",
			slog.String("path", repoDir),
			slog.String("ref", source.Ref),
			slog.Any("error", err),
		)

		if removeErr := env.FS().RemoveAll(repoDir); removeErr != nil {
			return "", fmt.Errorf("failed to remove stale repo %#q:\n%w", repoDir, removeErr)
		}

		if cloneErr := gitProvider.Clone(env, source.GitURL, repoDir); cloneErr != nil {
			return "", fmt.Errorf("failed to re-clone %#q:\n%w", source.GitURL, cloneErr)
		}

		if checkoutErr := gitProvider.Checkout(env, repoDir, source.Ref); checkoutErr != nil {
			return "", fmt.Errorf("failed to checkout %#q after re-clone:\n%w", source.Ref, checkoutErr)
		}
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
