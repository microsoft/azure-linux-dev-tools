// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../../../../tools/mockgen/go.mod mockgen -source=spec.go -destination=specs_testutils/spec_mocks.go -package=specs_testutils --copyright_file=../../../../../.license-preamble

package specs

import (
	"fmt"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/workdir"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
)

// ComponentSpec provides abstract access to the build specification of a software component.
type ComponentSpec interface {
	// Parse parses the spec and returns the component's information. The spec is parsed in an
	// isolated environment set up to match the component's configuration. Note that this function
	// may be computationally intensive and/or time-consuming.
	Parse() (specInfo *ComponentSpecDetails, err error)
	// GetPath ensures that the spec is locally available, and then provides a path to it. Invoking this
	// function may fail if an error occurs while preparing the spec for local access.
	GetPath() (path string, err error)
}

// ComponentSpecDetails encapsulates detailed information extracted from a component's specification.
type ComponentSpecDetails struct {
	rpm.SpecInfo
}

type componentSpec struct {
	env             *azldev.Env
	componentConfig projectconfig.ComponentConfig
}

func NewSpec(env *azldev.Env, componentConfig projectconfig.ComponentConfig) ComponentSpec {
	return &componentSpec{
		env:             env,
		componentConfig: componentConfig,
	}
}

func (s *componentSpec) GetPath() (path string, err error) {
	if s.componentConfig.Spec.SourceType != projectconfig.SpecSourceTypeLocal {
		return "", fmt.Errorf("component %q has invalid spec source type: %v",
			s.componentConfig.Name,
			s.componentConfig.Spec.SourceType,
		)
	}

	if s.componentConfig.Spec.Path == "" {
		return "", fmt.Errorf("component %q does not have a spec path defined", s.componentConfig.Name)
	}

	// Make sure the file exists.
	_, err = s.env.FS().Stat(s.componentConfig.Spec.Path)
	if err != nil {
		return "", fmt.Errorf("spec file for component %#q missing at %#q:\n%w",
			s.componentConfig.Name, s.componentConfig.Spec.Path, err)
	}

	return s.componentConfig.Spec.Path, nil
}

func (s *componentSpec) Parse() (specInfo *ComponentSpecDetails, err error) {
	// Make sure we can get a specPath to a locally-accessible spec file first.
	specPath, err := s.GetPath()
	if err != nil {
		return nil, err
	}

	evt := s.env.StartEvent(fmt.Sprintf("Querying spec %q in isolated environment...", filepath.Base(specPath)))
	defer evt.End()

	workDirFactory, err := workdir.NewFactory(s.env.FS(), s.env.WorkDir(), s.env.ConstructionTime())
	if err != nil {
		return nil, fmt.Errorf("failed to create work dir factory:\n%w", err)
	}

	// Create a working build environment so we can run rpmspec in the target distro's context
	// (with all its macros, etc.).
	buildEnv, err := workdir.MkComponentBuildEnvironment(s.env, workDirFactory, &s.componentConfig, "spec-parse", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create build environment for component %q:\n%w", s.componentConfig.Name, err)
	}

	// Clean up the build environment before we return.
	defer defers.HandleDeferError(func() error { return buildEnv.Destroy(s.env) }, &err)

	// Extract the build options from the component; we'll need to honor them even in rpmspec
	// in order to get the correct spec information.
	buildOptions := rpm.BuildOptions{
		With:    s.componentConfig.Build.With,
		Without: s.componentConfig.Build.Without,
		Defines: s.componentConfig.Build.Defines,
	}

	// Create the spec querier and query it!
	queriedSpecInfo, err := rpm.NewSpecQuerier(buildEnv, buildOptions).QuerySpec(s.env, specPath)
	if err != nil {
		return nil, fmt.Errorf("failed to query spec for component %q:\n%w", s.componentConfig.Name, err)
	}

	return &ComponentSpecDetails{
		SpecInfo: *queriedSpecInfo,
	}, nil
}
