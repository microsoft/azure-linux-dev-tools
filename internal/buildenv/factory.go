// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package buildenv

//go:generate go tool -modfile=../../tools/mockgen/go.mod mockgen -source=factory.go -destination=buildenv_testutils/factory_mocks.go -package=buildenv_testutils --copyright_file=../../.license-preamble

// Factory is an abstract interface for a factory that can create new [BuildEnv] instances.
type Factory interface {
	// CreateEnv creates a new instance of a build environment (e.g., a specific mock root);
	// note that creation is decoupled from executing operations within the build environment.
	CreateEnv(options CreateOptions) (buildEnv BuildEnv, err error)
}

// RPMAwareFactory is an abstract interface for a factory that can create new [RPMAwareBuildEnv]
// instances.
type RPMAwareFactory interface {
	// CreateEnv creates a new instance of a build environment (e.g., a specific mock root);
	// note that creation is decoupled from executing operations within the build environment.
	CreateRPMAwareEnv(options CreateOptions) (buildEnv RPMAwareBuildEnv, err error)
}

// CreateOptions encapsulate options that may be specified at creation time
// of a [BuildEnv]. Note that there may be some environment options that are only
// configurable when the build environment is *run* (see: [RunOptions]).
type CreateOptions struct {
	// Name is the user-referenceable name of the build environment.
	Name string
	// Dir is the directory under which the build environment's files should be stored.
	Dir string
	// UserCreated indicates whether the build environment was explicitly created by the
	// user for direct use (as opposed to being created as part of a build operation).
	UserCreated bool
	// Description optionally provides a human-readable description for the purpose of
	// this environment.
	Description string
	// ConfigOpts is an optional set of key-value pairs that will be passed through to the
	// underlying build environment backend as configuration overrides (e.g., mock's --config-opts).
	ConfigOpts map[string]string
}
