// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package buildenv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
)

// MockRootFactory is an implementation of [Factory] that can create new [MockRoot]
// instances of build environments.
type MockRootFactory struct {
	ctx            opctx.Ctx
	mockConfigPath string
}

// NewMockRootFactory creates a new instance of [mockRootFactory] with the given config file.
func NewMockRootFactory(ctx opctx.Ctx, mockConfigPath string) (*MockRootFactory, error) {
	// Get set up with mock.
	return &MockRootFactory{
		ctx:            ctx,
		mockConfigPath: mockConfigPath,
	}, nil
}

// CreateEnv creates a new instance of a build environment (e.g., a specific mock root).
func (f *MockRootFactory) CreateEnv(options CreateOptions) (BuildEnv, error) {
	return f.CreateMockRoot(options)
}

// CreateRPMAwareEnv creates a new instance of an RPM-aware build environment (e.g., a specific mock root).
func (f *MockRootFactory) CreateRPMAwareEnv(options CreateOptions) (RPMAwareBuildEnv, error) {
	return f.CreateMockRoot(options)
}

// CreateMockRoot creates a new instance of a mock root. Unlike [CreateEnv], this method
// returns a [MockRoot] directly for callers that specifically want to interact with our concrete type.
func (f *MockRootFactory) CreateMockRoot(options CreateOptions) (*MockRoot, error) {
	// Create and configure a mock runner using the mock package.
	runner := mock.NewRunner(f.ctx, f.mockConfigPath)

	if len(options.ConfigOpts) > 0 {
		runner.WithConfigOpts(options.ConfigOpts)
	}

	return &MockRoot{
		mockRunner: runner,
	}, nil
}

// MockRoot is an implementation of the [BuildEnv] interface that represents a mock root build
// environment.
type MockRoot struct {
	mockRunner *mock.Runner
}

// GetRunner returns the [mock.Runner] that may be used to run commands within this mock root build environment.
func (r *MockRoot) GetRunner() *mock.Runner {
	return r.mockRunner.Clone()
}

// CreateCmd builds a new [opctx.Cmd] for running the given command within the build environment.
func (r *MockRoot) CreateCmd(ctx context.Context, args []string, options RunOptions) (
	cmd opctx.Cmd, err error,
) {
	// We clone the template mock runner object so we can specialize it with the specific [RunOptions] for
	// this request. This avoids leaking these configuration changes into subsequent calls of methods on
	// this type.
	mockRunner := r.mockRunner.Clone()

	if options.EnableNetworking {
		mockRunner.EnableNetwork()
	}

	for _, bindMount := range options.BindMounts {
		mockRunner.AddBindMount(bindMount.PathInHost, bindMount.PathInBuildEnv)
	}

	cmd, err = mockRunner.CmdInChroot(ctx, args, options.Interactive)
	if err != nil {
		return nil, fmt.Errorf("failed to create command to run in mock root:\n%w", err)
	}

	return cmd, nil
}

// BuildSRPM uses this build environment to build a source RPM package.
func (r *MockRoot) BuildSRPM(
	ctx context.Context, specPath, sourceDirPath, outputDirPath string, options SRPMBuildOptions,
) error {
	//nolint:wrapcheck // This is intentionally as pass-through.
	return r.mockRunner.BuildSRPM(ctx, specPath, sourceDirPath, outputDirPath, options)
}

// BuildRPM uses this build environment to build binary RPM packages from the provided SRPM package.
func (r *MockRoot) BuildRPM(ctx context.Context, srpmPath, outputDirPath string, options RPMBuildOptions) error {
	//nolint:wrapcheck // This is intentionally as pass-through.
	return r.mockRunner.BuildRPM(ctx, srpmPath, outputDirPath, options)
}

// TryGetFailureDetails makes a best-effort attempt to extract details from build logs that may be
// relevant to understanding the cause of a build failure. This is intended to be called after a build
// failure to glean any insights we can from logs about why the failure might have occurred.
func (r *MockRoot) TryGetFailureDetails(fs opctx.FS, outputDirPath string) (details *RPMBuildLogDetails) {
	return r.mockRunner.TryGetFailureDetails(fs, outputDirPath)
}

func (r *MockRoot) GetInfo() BuildEnvInfo {
	// Return stubbed info for now. When we support parallel mock with separate roots, we will fill this out
	// correctly.
	return BuildEnvInfo{
		Type: EnvTypeMock,
	}
}

// Destroy permanently (and irreversibly) destroys the build environment, removing its files from the filesystem.
func (r *MockRoot) Destroy(ctx opctx.Ctx) (err error) {
	slog.Debug("Destroying mock root")

	err = r.mockRunner.ScrubRoot(ctx)
	if err != nil {
		return fmt.Errorf("failed to scrub mock root:\n%w", err)
	}

	slog.Debug("Finished destroying mock root")

	return nil
}

// OpenMockRoot opens an existing mock root build environment.
func OpenMockRoot(ctx opctx.Ctx, info BuildEnvInfo) (mockRoot *MockRoot, err error) {
	if info.Type != EnvTypeMock {
		return nil, errors.New("cannot open mock root: not a mock build environment")
	}

	return nil, errors.New("opening a mock root from its info is not yet implemented")
}
