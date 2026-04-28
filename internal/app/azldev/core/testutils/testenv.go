// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package testutils

import (
	"context"
	"log/slog"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"github.com/thejerf/slogassert"
)

// Test environment, useful for unit-testing azldev CLI commands. Contains
// an [azldev.Env] constructed with injected dependencies that redirect
// filesystem and OS environmental access to included test objects.
type TestEnv struct {
	Env    *azldev.Env
	Config *projectconfig.ProjectConfig

	TestInterfaces azldev.SystemInterfaces

	// Test implementations
	CmdFactory       *testctx.TestCmdFactory
	DryRunnable      opctx.DryRunnable
	EventListener    opctx.EventListener
	TestFS           opctx.FS
	TestOSEnv        opctx.OSEnv
	CommandsExecuted [][]string
}

// Ensure that [TestEnv.Env] implements [opctx.Ctx].
var _ opctx.Ctx = TestEnv{}.Env

// Ensure that [TestEnv.Env] implements [context.Context].
var _ context.Context = TestEnv{}.Env

// Constructs a new [TestEnv].
func NewTestEnv(t *testing.T) *TestEnv {
	t.Helper()

	const (
		testProjectDir     = "/project"
		testMockConfigPath = testProjectDir + "/mock.cfg"
	)

	testEnv := newTestEnv(testMockConfigPath)

	populateTestProjectFiles(t, testEnv, testProjectDir, testMockConfigPath)

	setCmdFactory(testEnv)

	setEnvDependencies(t, testEnv)

	setEnv(t, testEnv, testProjectDir)

	return testEnv
}

// newTestEnv creates a new [TestEnv] with a test project config
// and mock implementations of [opctx.FS] and [opctx.OSEnv].
func newTestEnv(testMockConfigPath string) *TestEnv {
	return &TestEnv{
		Config:           constructProjectConfig(testMockConfigPath),
		CommandsExecuted: [][]string{},
		TestFS:           afero.NewMemMapFs(),
		TestOSEnv:        testctx.NewTestOSEnv(),
	}
}

// setEnvDependencies sets interface dependencies shared by [azldev.Env].
func setEnvDependencies(t *testing.T, testEnv *TestEnv) {
	t.Helper()

	setUpEventListener(t, testEnv)

	testEnv.TestInterfaces = azldev.SystemInterfaces{
		CmdFactory:        testEnv.CmdFactory,
		FileSystemFactory: testEnv,
		OSEnvFactory:      testEnv,
	}
	testEnv.DryRunnable = azldev.NewAppDryRunnable(false)
}

func setUpEventListener(t *testing.T, testEnv *TestEnv) {
	t.Helper()

	testLogHandler := slogassert.New(t, slog.LevelDebug, nil)
	testEventLogger := slog.New(testLogHandler)
	testEventListener, err := azldev.NewEventListener(testEventLogger, false)
	require.NoError(t, err)

	testEnv.EventListener = testEventListener
}

// populateTestProjectFiles creates simple mock config and project files
// in the test project directory.
func populateTestProjectFiles(t *testing.T, testEnv *TestEnv, testProjectDir string, testMockConfigPath string) {
	t.Helper()

	// Create the project dir.
	require.NoError(t, testEnv.TestOSEnv.Chdir(testProjectDir))

	// Create an empty mock config file.
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testMockConfigPath, []byte{}, fileperms.PrivateFile))

	// Create an empty project (.toml) config file.
	_, err := testEnv.TestFS.Create(filepath.Join(testProjectDir, projectconfig.DefaultConfigFileName))
	require.NoError(t, err)
}

// setEnv sets the [azldev.Env] for the test environment, using the provided
// project directory and the test environment's configuration.
func setEnv(t *testing.T, testEnv *TestEnv, testProjectDir string) {
	t.Helper()

	envOptions := azldev.NewEnvOptions()
	envOptions.DryRunnable = testEnv.DryRunnable
	envOptions.EventListener = testEnv.EventListener
	envOptions.Interfaces = testEnv.TestInterfaces
	envOptions.ProjectDir = testProjectDir
	envOptions.Config = testEnv.Config

	testEnv.Env = azldev.NewEnv(t.Context(), envOptions)
}

// setCmdFactory sets the test version of [testctx.CmdFactory] for the test environment.
func setCmdFactory(testEnv *TestEnv) {
	testEnv.CmdFactory = testctx.NewTestCmdFactory()

	testEnv.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		testEnv.CommandsExecuted = append(testEnv.CommandsExecuted, cmd.Args)

		return nil
	}

	testEnv.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		testEnv.CommandsExecuted = append(testEnv.CommandsExecuted, cmd.Args)

		return "", nil
	}
}

// Construct a [projectconfig.ProjectConfig].
func constructProjectConfig(testMockConfigPath string) *projectconfig.ProjectConfig {
	config := projectconfig.NewProjectConfig()
	config.Project.WorkDir = "/work"
	config.Project.LogDir = "/logs"
	config.Project.OutputDir = "/output"
	config.Project.DefaultDistro.Name = "test-distro"
	config.Project.DefaultDistro.Version = "1.0"

	distro := projectconfig.DistroDefinition{
		Versions:         make(map[string]projectconfig.DistroVersionDefinition),
		LookasideBaseURI: "https://example.com/lookaside/$pkg/$filename/$hashtype/$hash/$filename",
		DistGitBaseURI:   "https://example.com/upstream/$pkg.git",
	}

	distro.Versions["1.0"] = projectconfig.DistroVersionDefinition{
		MockConfigPath: testMockConfigPath,
		DistGitBranch:  "main",
		ReleaseVer:     "3.0",
	}

	config.Distros["test-distro"] = distro

	return &config
}

// FS implements the [opctx.FileSystemFactory] interface.
func (e *TestEnv) FS() opctx.FS {
	return e.TestFS
}

// FS implements the [opctx.OSEnvFactory] interface.
func (e *TestEnv) OSEnv() opctx.OSEnv {
	return e.TestOSEnv
}
