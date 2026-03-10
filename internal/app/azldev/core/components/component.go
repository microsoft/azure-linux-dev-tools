// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../../../../tools/mockgen/go.mod mockgen -source=component.go -destination=components_testutils/component_mocks.go -package=components_testutils --copyright_file=../../../../../.license-preamble

package components

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/specs"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// Component provides abstract access to a *software component*; software components are buildable
// entities that can be used in a project, and which typically produce 1 or more packages when built.
type Component interface {
	// GetName returns the name of the component.
	GetName() string
	// GetConfig returns the static configuration for the component.
	GetConfig() *projectconfig.ComponentConfig
	// GetSpec returns an abstract object that represents the specification for building the component.
	GetSpec() specs.ComponentSpec
	// GetDetails inspects the component and retrieves more detailed information; non-trivial
	// computation may be required to collect these details.
	GetDetails() (info *ComponentDetails, err error)
}

// ComponentDetails encapsulates detailed information about a component, including both
// configuration information as well as detailed information that may be computationally intensive
// to collect. This information can be separately retrieved from the [Component] but this type
// provides a convenient data-oriented structure to encapsulate all details about a component
// in one place.
type ComponentDetails struct {
	// We embed the details retrieved from the component's spec as well.
	specs.ComponentSpecDetails

	// Config holds the static configuration for the component, including its name.
	Config projectconfig.ComponentConfig
}

// ComponentGroup defines a component group. Component groups are used to group components, and optionally
// apply shared configuration or policy to them.
type ComponentGroup struct {
	// The group's name.
	Name string
	// A list of the group's members.
	Components []ComponentGroupMember
}

// ComponentGroupMember defines a member of a [ComponentGroup].
type ComponentGroupMember struct {
	// Name of the component.
	ComponentName string
	// Path to the component's spec (optional).
	SpecPath string
}
