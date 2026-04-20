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

	"github.com/bmatcuk/doublestar/v4"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
)

const (
	// pythonProgram is the Python interpreter used to create venvs and run pytest.
	pythonProgram = "python3"

	// venvDirName is the name of the venv directory created under the azldev work dir.
	venvDirName = "pytest-venv"

	// imagePlaceholder is the placeholder token for the image path.
	imagePlaceholder = "{image-path}"
	// imageNamePlaceholder is the placeholder token for the image name.
	imageNamePlaceholder = "{image-name}"
	// capabilitiesPlaceholder is the placeholder token for the comma-delimited capabilities.
	capabilitiesPlaceholder = "{capabilities}"
)

// RunPytestSuite runs a pytest-based test suite natively using a Python venv.
func RunPytestSuite(
	env *azldev.Env, suiteConfig *projectconfig.TestSuiteConfig,
	imageConfig *projectconfig.ImageConfig, options *ImageTestOptions,
) error {
	pytestConfig := suiteConfig.Pytest
	if pytestConfig == nil {
		return fmt.Errorf("test suite %#q is missing pytest configuration", suiteConfig.Name)
	}

	slog.Info("Running pytest test suite",
		slog.String("name", suiteConfig.Name),
		slog.String("working-dir", pytestConfig.WorkingDir),
		slog.String("image-path", options.ImagePath),
	)

	// Validate that the working directory exists.
	if pytestConfig.WorkingDir != "" {
		workingDirExists, err := fileutils.DirExists(env.FS(), pytestConfig.WorkingDir)
		if err != nil {
			return fmt.Errorf("cannot access working directory %#q:\n%w", pytestConfig.WorkingDir, err)
		}

		if !workingDirExists {
			return fmt.Errorf("working directory not found: %#q", pytestConfig.WorkingDir)
		}
	}

	// Ensure python3 is available.
	if err := prereqs.RequireExecutable(env, pythonProgram, nil); err != nil {
		return fmt.Errorf("python3 is required to run pytest tests:\n%w", err)
	}

	// Set up or reuse the venv.
	venvDir, err := ensurePytestVenv(env, suiteConfig.Name, pytestConfig)
	if err != nil {
		return err
	}

	// Build the pytest command: expand test paths, substitute placeholders in extra args.
	pytestArgs := BuildNativePytestArgs(pytestConfig, imageConfig, options)

	slog.Info("Running pytest", slog.Any("args", pytestArgs))

	venvPython := filepath.Join(venvDir, "bin", pythonProgram)

	cmdArgs := append([]string{"-m", "pytest"}, pytestArgs...)

	if env.Verbose() {
		cmdArgs = append(cmdArgs, "--log-cli-level=DEBUG")
	}

	pytestCmd := exec.CommandContext(env, venvPython, cmdArgs...)
	pytestCmd.Dir = pytestConfig.WorkingDir
	pytestCmd.Stdout = os.Stdout
	pytestCmd.Stderr = os.Stderr

	cmd, err := env.Command(pytestCmd)
	if err != nil {
		return fmt.Errorf("failed to create pytest command:\n%w", err)
	}

	if err := cmd.Run(env); err != nil {
		return fmt.Errorf("pytest run failed:\n%w", err)
	}

	return nil
}

// ensurePytestVenv creates or reuses a Python venv for the given test suite and installs
// dependencies according to the configured install mode. The venv is created under the
// project's work directory.
func ensurePytestVenv(
	env *azldev.Env, testName string, pytestConfig *projectconfig.PytestConfig,
) (string, error) {
	venvDir := filepath.Join(env.WorkDir(), venvDirName, testName)

	venvPython := filepath.Join(venvDir, "bin", pythonProgram)

	venvExists, err := fileutils.Exists(env.FS(), venvPython)
	if err != nil {
		return "", fmt.Errorf("cannot check venv at %#q:\n%w", venvDir, err)
	}

	if !venvExists {
		if err := createPythonVenv(env, venvDir); err != nil {
			return "", err
		}
	} else {
		slog.Info("Reusing existing Python venv", slog.String("path", venvDir))
	}

	// Install dependencies according to the configured mode.
	if err := installPytestDependencies(env, venvPython, pytestConfig); err != nil {
		return "", err
	}

	return venvDir, nil
}

// createPythonVenv creates a new Python virtual environment at venvDir.
func createPythonVenv(env *azldev.Env, venvDir string) error {
	slog.Info("Creating Python venv", slog.String("path", venvDir))

	venvCmd := exec.CommandContext(env, pythonProgram, "-m", "venv", venvDir)
	venvCmd.Stdout = os.Stdout
	venvCmd.Stderr = os.Stderr

	cmd, err := env.Command(venvCmd)
	if err != nil {
		return fmt.Errorf("failed to create venv command:\n%w", err)
	}

	if err := cmd.Run(env); err != nil {
		return fmt.Errorf("failed to create Python venv at %#q:\n%w", venvDir, err)
	}

	return nil
}

// installPytestDependencies installs Python dependencies into the venv according to the
// configured [projectconfig.PytestInstallMode].
func installPytestDependencies(
	env *azldev.Env, venvPython string, pytestConfig *projectconfig.PytestConfig,
) error {
	mode := pytestConfig.EffectiveInstallMode()

	if mode == projectconfig.PytestInstallNone {
		slog.Info("Skipping dependency installation (install mode 'none')")

		return nil
	}

	if pytestConfig.WorkingDir == "" {
		slog.Debug("No working directory configured; skipping dependency installation")

		return nil
	}

	switch mode {
	case projectconfig.PytestInstallPyproject:
		return installFromPyproject(env, venvPython, pytestConfig.WorkingDir)
	case projectconfig.PytestInstallRequirements:
		return installFromRequirements(env, venvPython, pytestConfig.WorkingDir)
	case projectconfig.PytestInstallNone:
		// Already handled above, but listed for exhaustiveness.
		return nil
	default:
		return fmt.Errorf("unsupported install mode %#q", mode)
	}
}

// installFromPyproject installs dependencies from pyproject.toml using editable mode.
// If pyproject.toml is not found, a warning is logged and installation is skipped.
func installFromPyproject(env *azldev.Env, venvPython string, workingDir string) error {
	pyprojectPath := filepath.Join(workingDir, "pyproject.toml")

	pyprojectExists, err := fileutils.Exists(env.FS(), pyprojectPath)
	if err != nil {
		return fmt.Errorf("cannot check for pyproject.toml at %#q:\n%w", pyprojectPath, err)
	}

	if !pyprojectExists {
		slog.Warn("No pyproject.toml found; skipping dependency installation",
			slog.String("working-dir", workingDir),
		)

		return nil
	}

	slog.Info("Installing dependencies from pyproject.toml",
		slog.String("pyproject", pyprojectPath),
	)

	pipCmd := exec.CommandContext(
		env, venvPython, "-m", "pip", "install", "--quiet", "-e", workingDir,
	)
	pipCmd.Stdout = os.Stdout
	pipCmd.Stderr = os.Stderr

	cmd, err := env.Command(pipCmd)
	if err != nil {
		return fmt.Errorf("failed to create pip install command:\n%w", err)
	}

	if err := cmd.Run(env); err != nil {
		return fmt.Errorf("failed to install dependencies from %#q:\n%w", pyprojectPath, err)
	}

	return nil
}

// installFromRequirements installs dependencies from requirements.txt.
// Returns an error if the file is not found.
func installFromRequirements(env *azldev.Env, venvPython string, workingDir string) error {
	requirementsPath := filepath.Join(workingDir, "requirements.txt")

	requirementsExists, err := fileutils.Exists(env.FS(), requirementsPath)
	if err != nil {
		return fmt.Errorf("cannot check for requirements.txt at %#q:\n%w", requirementsPath, err)
	}

	if !requirementsExists {
		return fmt.Errorf(
			"requirements.txt not found at %#q (required by install mode %#q)",
			requirementsPath, projectconfig.PytestInstallRequirements,
		)
	}

	slog.Info("Installing dependencies from requirements.txt",
		slog.String("requirements", requirementsPath),
	)

	pipCmd := exec.CommandContext(
		env, venvPython, "-m", "pip", "install", "--quiet", "-r", requirementsPath,
	)
	pipCmd.Stdout = os.Stdout
	pipCmd.Stderr = os.Stderr

	cmd, err := env.Command(pipCmd)
	if err != nil {
		return fmt.Errorf("failed to create pip install command:\n%w", err)
	}

	if err := cmd.Run(env); err != nil {
		return fmt.Errorf("failed to install dependencies from %#q:\n%w", requirementsPath, err)
	}

	return nil
}

// BuildNativePytestArgs constructs the full pytest argument list from the config.
// Test paths are glob-expanded relative to the working directory. Extra args are passed
// verbatim after placeholder substitution. The --junit-xml flag is appended automatically
// when requested via CLI.
func BuildNativePytestArgs(
	pytestConfig *projectconfig.PytestConfig,
	imageConfig *projectconfig.ImageConfig,
	options *ImageTestOptions,
) []string {
	absImagePath, err := filepath.Abs(options.ImagePath)
	if err != nil {
		absImagePath = options.ImagePath
	}

	// Build a replacer for all known placeholders.
	replacer := strings.NewReplacer(
		imagePlaceholder, absImagePath,
		imageNamePlaceholder, options.ImageName,
		capabilitiesPlaceholder, strings.Join(imageConfig.Capabilities.EnabledNames(), ","),
	)

	args := make([]string, 0, len(pytestConfig.TestPaths)+len(pytestConfig.ExtraArgs))

	// Expand test paths (glob patterns resolved relative to working dir).
	for _, testPath := range pytestConfig.TestPaths {
		args = append(args, expandGlob(testPath, pytestConfig.WorkingDir)...)
	}

	// Substitute placeholders in extra args (never glob-expanded).
	for _, arg := range pytestConfig.ExtraArgs {
		args = append(args, replacer.Replace(arg))
	}

	// Append --junit-xml when requested via CLI.
	if options.JUnitXMLPath != "" {
		args = append(args, "--junit-xml", options.JUnitXMLPath)
	}

	return args
}

// expandGlob expands a glob pattern relative to workingDir using doublestar, which supports
// recursive ** patterns. If the pattern matches no files, the original pattern is returned
// unchanged (letting pytest report the error).
func expandGlob(pattern string, workingDir string) []string {
	absPattern := pattern
	if workingDir != "" && !filepath.IsAbs(pattern) {
		absPattern = filepath.Join(workingDir, pattern)
	}

	// Use WithFilesOnly so directory entries are excluded from glob results — pytest handles
	// directory args directly (without globs). Use WithFailOnIOErrors to surface real I/O
	// problems instead of silently returning empty. Follow symlinks (the default) since test
	// trees may use them.
	matches, err := doublestar.FilepathGlob(absPattern,
		doublestar.WithFilesOnly(),
		doublestar.WithFailOnIOErrors(),
	)
	if err != nil {
		slog.Warn("Failed to expand glob pattern",
			slog.String("pattern", pattern),
			slog.Any("error", err),
		)

		return []string{pattern}
	}

	if len(matches) == 0 {
		return []string{pattern}
	}

	// Convert back to paths relative to the working directory so pytest sees them
	// the same way it would with shell expansion.
	result := make([]string, 0, len(matches))

	for _, match := range matches {
		if workingDir != "" {
			rel, relErr := filepath.Rel(workingDir, match)
			if relErr == nil {
				result = append(result, rel)

				continue
			}
		}

		result = append(result, match)
	}

	return result
}
