// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../tools/mockgen/go.mod mockgen -source=buildenv.go -destination=buildenv_testutils/buildenv_mocks.go -package=buildenv_testutils --copyright_file=../../.license-preamble

package buildenv

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
)

// BuildEnv defines an abstract interface for interacting with a specific instance of a build
// environment. A build environment is a self-contained, intentionally constructed  environment that
// can be used to run build-related operations. A mock root instance is an example of a build environment.
type BuildEnv interface {
	// GetInfo returns a [BuildEnvInfo] structure that contains information about the build
	// environment. This information can be used to identify the build environment and its properties.
	GetInfo() BuildEnvInfo

	// CreateCmd builds a new [exec.Cmd] for running the given command within the build environment.
	CreateCmd(ctx context.Context, args []string, options RunOptions) (cmd opctx.Cmd, err error)

	// Destroy permanently (and irreversibly) destroys the build environment, removing its files from
	// the filesystem.
	Destroy(ctx opctx.Ctx) error
}

// SRPMBuildOptions encapsulates options that may be specified when building source RPM packages
// using an [RPMAwareBuildEnv].
type SRPMBuildOptions = mock.SRPMBuildOptions

// RPMBuildOptions encapsulates options that may be specified when building binary RPM packages
// using an [RPMAwareBuildEnv].
type RPMBuildOptions = mock.RPMBuildOptions

// RPMBuildLogDetails encapsulates details extracted from RPM build logs that may be relevant to
// understanding the cause of a build failure.
type RPMBuildLogDetails = mock.BuildLogDetails

// RPMAwareBuildEnv is an interface that extends the [BuildEnv] interface for the subset of build
// environments that provide native support RPM build operations.
type RPMAwareBuildEnv interface {
	BuildEnv

	// BuildSRPM uses this build environment to build a source RPM package.
	BuildSRPM(ctx context.Context, specPath, sourceDirPath, outputDirPath string, options SRPMBuildOptions) error

	// BuildRPM uses this build environment to build binary RPM packages from the provided SRPM package.
	BuildRPM(ctx context.Context, srpmPath, outputDirPath string, options RPMBuildOptions) error

	// TryGetFailureDetails makes a best-effort attempt to extract details from build logs that may be
	// relevant to understanding the cause of a build failure. This is intended to be called after a build
	// failure to glean any insights we can from logs about why the failure might have occurred.
	TryGetFailureDetails(fs opctx.FS, outputDirPath string) (details *RPMBuildLogDetails)
}

// RunOptions encapsulate options that may be specified at runtime when executing
// an operation within an created instance of a [BuildEnv]. Note that there may be some
// environment options that are only configurable when the build environment is *created*
// (see: [CreateOptions]).
type RunOptions struct {
	// EnableNetworking indicates whether the build environment should allow networking operations.
	// If set to false, the build environment will be entirely isolated from the network.
	EnableNetworking bool

	// Interactive indicates whether the command should be run interactively. If set to true, the
	// command will be run in an interactive terminal, allowing the user to interact with it via
	// stdin.
	Interactive bool

	// BindMounts is a list of bind mounts that should be created for the build environment, allowing
	// mapping of host paths into the build environment.
	BindMounts []BindMount
}

type BindMount struct {
	PathInHost     string
	PathInBuildEnv string
}

// EnvType identifies the type of a build environment. This is used to determine which
// technology can be used to interact with the build environment.
type EnvType string

const (
	// EnvTypeMock is the type of build environment that uses the mock technology.
	EnvTypeMock EnvType = "mock"
)

// String returns the string representation of the build environment type.
func (f *EnvType) String() string {
	return string(*f)
}

// Parses the type from a string; usable by a command-line parser. Required by cobra.Command
// to be able to parse an [EnvType] from a command-line string.
func (f *EnvType) Set(value string) error {
	switch value {
	case "mock":
		*f = EnvTypeMock
	default:
		return fmt.Errorf("unsupported build environment type: %s", value)
	}

	return nil
}

// Returns a descriptive string usable in usage information.
func (f *EnvType) Type() string {
	return "buildenv type"
}

// BuildEnvInfo is a simple data structure that contains information about a [BuildEnv].
type BuildEnvInfo struct {
	// Name is the human-readable name moniker for the build environment.
	Name string `json:"name"`

	// UserCreated indicates whether the build environment was created on behalf of a direct user request.
	// If this is false, the build environment was created automatically as part of a larger operation.
	UserCreated bool `json:"userCreated"`

	// Type is the type of build environment. This is used to determine which technology can be used
	// to interact with the build environment.
	Type EnvType `json:"type"`

	// CreationTime is the time when the build environment was created. This can be useful for users to
	// better understanding their environments.
	CreationTime time.Time `json:"creationTime"`

	// Dir is the directory under which the build environment's files are stored.
	Dir string `json:"dir"`

	// Description optionally provides a human-readable description of the environment's purpose.
	Description string `json:"description"`
}

func (i *BuildEnvInfo) Serialize() (data []byte, err error) {
	data, err = json.MarshalIndent(i, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize build environment info:\n%w", err)
	}

	return data, nil
}
