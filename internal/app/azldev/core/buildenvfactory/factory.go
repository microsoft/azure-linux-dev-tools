// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package buildenvfactory

import (
	"errors"
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/buildenv"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// NewFactoryForEnv creates a new factory that can produce a build environment of the given type.
func NewFactoryForEnv(env *azldev.Env, buildEnvType buildenv.EnvType) (buildenv.Factory, error) {
	switch buildEnvType {
	case buildenv.EnvTypeMock:
		return NewMockRootFactoryForEnv(env)
	default:
		return nil, fmt.Errorf("unknown build environment type '%s'", buildEnvType)
	}
}

// NewRPMAwareFactoryForEnv creates a new RPM-aware factory that can produce build environments of
// the given type.
func NewRPMAwareFactoryForEnv(env *azldev.Env, buildEnvType buildenv.EnvType) (buildenv.RPMAwareFactory, error) {
	switch buildEnvType {
	case buildenv.EnvTypeMock:
		return NewMockRootFactoryForEnv(env)
	default:
		return nil, fmt.Errorf("unknown build environment type '%s'", buildEnvType)
	}
}

// NewMockRootFactory creates a new instance of [mockRootFactory] with the given environment.
func NewMockRootFactoryForEnv(env *azldev.Env) (*buildenv.MockRootFactory, error) {
	var distroVerDef projectconfig.DistroVersionDefinition

	// Get the distro we're building for.
	_, distroVerDef, err := env.Distro()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve distro for build:\n%w", err)
	}

	// Find the specified config path.
	mockConfigPath := distroVerDef.MockConfigPath

	// Make sure it *was* specified.
	if mockConfigPath == "" {
		return nil, errors.New("no mock config file available for project")
	}

	// Make sure the mock config file exists and is accessible by stat'ing it.
	// It's always possible something could change that before we run mock, but
	// this hopefully catches some basic issues earlier in the process, and with
	// a more meaningful error message.
	if _, statErr := env.FS().Stat(mockConfigPath); statErr != nil {
		return nil, fmt.Errorf("failed to access configured mock config file '%s':\n%w", mockConfigPath, statErr)
	}

	// Get set up with mock.
	factory, err := buildenv.NewMockRootFactory(env, mockConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create mock root factory:\n%w", err)
	}

	return factory, nil
}
