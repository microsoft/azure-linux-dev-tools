// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"

	"dario.cat/mergo"
	"github.com/brunoga/deep"
)

// Defines an image.
type ImageConfig struct {
	// The image's name; not actually present in serialized TOML files.
	Name string `toml:"-" json:"name" table:",sortkey"`

	// Reference to the source config file that this definition came from; not present
	// in serialized files.
	SourceConfigFile *ConfigFile `toml:"-" json:"-" table:"-"`

	// Description of the image.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Description of the image"`

	// Where to find its definition.
	Definition ImageDefinition `toml:"definition,omitempty" json:"definition,omitempty" jsonschema:"title=Definition,description=Identifies where to find the definition for this image"`

	// Capabilities describes the features and properties of this image.
	Capabilities ImageCapabilities `toml:"capabilities,omitempty" json:"capabilities,omitempty" jsonschema:"title=Capabilities,description=Features and properties of this image"`

	// Tests holds the test configuration for this image, including which test suites
	// apply to it.
	Tests ImageTestsConfig `toml:"tests,omitempty" json:"tests,omitempty" jsonschema:"title=Tests,description=Test configuration for this image"`

	// Publish holds the publish settings for this image.
	Publish ImagePublishConfig `toml:"publish,omitempty" json:"publish,omitempty" jsonschema:"title=Publish settings,description=Publishing settings for this image"`
}

// ImagePublishConfig holds publish settings for an image. Unlike packages (which target a
// single channel), images may be published to multiple channels simultaneously.
type ImagePublishConfig struct {
	// Channels lists the publish channels for this image.
	Channels []string `toml:"channels,omitempty" json:"channels,omitempty" validate:"dive,required,ne=.,ne=..,excludesall=/\\" jsonschema:"title=Channels,description=List of publish channels for this image"`
}

// ImageCapabilities describes the features and properties of an image. Boolean fields
// use *bool to distinguish "explicitly true", "explicitly false", and "unspecified"
// (nil). This tristate enables correct merge semantics (unspecified inherits, false
// overrides) and detection of underspecification.
type ImageCapabilities struct {
	// MachineBootable indicates whether the image can be booted on a machine (bare metal,
	// VM, etc.). Images that lack a kernel are not machine-bootable.
	MachineBootable *bool `toml:"machine-bootable,omitempty" json:"machineBootable,omitempty" jsonschema:"title=Machine bootable,description=Whether the image can be booted on a machine (bare metal or VM)"`

	// Container indicates whether the image can be run on an OCI container host.
	Container *bool `toml:"container,omitempty" json:"container,omitempty" jsonschema:"title=Container,description=Whether the image can be run on an OCI container host"`

	// Systemd indicates whether the image runs systemd as its init system.
	Systemd *bool `toml:"systemd,omitempty" json:"systemd,omitempty" jsonschema:"title=Systemd,description=Whether the image runs systemd as its init system"`

	// RuntimePackageManagement indicates whether the image supports installing or
	// removing packages at runtime (e.g., via dnf/tdnf).
	RuntimePackageManagement *bool `toml:"runtime-package-management,omitempty" json:"runtimePackageManagement,omitempty" jsonschema:"title=Runtime package management,description=Whether the image supports installing or removing packages at runtime"`
}

// IsMachineBootable returns true if the image is explicitly marked as machine-bootable.
func (c *ImageCapabilities) IsMachineBootable() bool {
	return c.MachineBootable != nil && *c.MachineBootable
}

// IsContainer returns true if the image is explicitly marked as runnable on
// an OCI container host.
func (c *ImageCapabilities) IsContainer() bool {
	return c.Container != nil && *c.Container
}

// IsSystemd returns true if the image explicitly runs systemd.
func (c *ImageCapabilities) IsSystemd() bool {
	return c.Systemd != nil && *c.Systemd
}

// IsRuntimePackageManagement returns true if the image explicitly supports runtime
// package management.
func (c *ImageCapabilities) IsRuntimePackageManagement() bool {
	return c.RuntimePackageManagement != nil && *c.RuntimePackageManagement
}

// EnabledNames returns the TOML field names of capabilities that are explicitly set to
// true, in a stable order matching the struct field declaration order.
func (c *ImageCapabilities) EnabledNames() []string {
	var names []string

	if c.IsMachineBootable() {
		names = append(names, "machine-bootable")
	}

	if c.IsContainer() {
		names = append(names, "container")
	}

	if c.IsSystemd() {
		names = append(names, "systemd")
	}

	if c.IsRuntimePackageManagement() {
		names = append(names, "runtime-package-management")
	}

	return names
}

// ImageTestsConfig holds the test-related configuration for an image.
type ImageTestsConfig struct {
	// TestSuites is the list of test suite references that apply to this image. Each
	// reference identifies a test suite defined in the top-level [test-suites] section
	// and may carry per-test metadata in the future (e.g., required vs optional).
	TestSuites []TestSuiteRef `toml:"test-suites,omitempty" json:"testSuites,omitempty" jsonschema:"title=Test Suites,description=List of test suite references that apply to this image"`
}

// TestSuiteRef is a reference to a named test suite. Using a structured type (rather than
// a bare string) allows per-test metadata to be added later without a breaking config change.
type TestSuiteRef struct {
	// Name is the key into the top-level [test-suites] map.
	Name string `toml:"name" json:"name" jsonschema:"required,title=Name,description=Name of the test suite (must match a key in [test-suites])"`
}

// TestNames returns the test suite names referenced by this image.
func (i *ImageConfig) TestNames() []string {
	names := make([]string, len(i.Tests.TestSuites))
	for idx, ref := range i.Tests.TestSuites {
		names[idx] = ref.Name
	}

	return names
}

// Defines where to find an image definition.
type ImageDefinition struct {
	// DefinitionType indicates the type of image definition.
	DefinitionType ImageDefinitionType `toml:"type,omitempty" json:"type,omitempty" jsonschema:"title=Type,description=Type of image definition"`

	// Path points to the image definition file.
	Path string `toml:"path,omitempty" json:"path,omitempty" jsonschema:"title=Path,description=Path to the image definition file"`

	// Profile is an optional field that specifies the profile to use when building the image.
	Profile string `toml:"profile,omitempty" json:"profile,omitempty" jsonschema:"title=Profile,description=Optional field that specifies the profile to use when building the image"`
}

// Type of image definition.
type ImageDefinitionType string

const (
	// Default (unspecified) source.
	ImageDefinitionTypeUnspecified ImageDefinitionType = ""
	// kiwi-ng image definition.
	ImageDefinitionTypeKiwi ImageDefinitionType = "kiwi"
)

// Mutates the image config, updating it with overrides present in other.
func (i *ImageConfig) MergeUpdatesFrom(other *ImageConfig) error {
	err := mergo.Merge(i, other, mergo.WithOverride, mergo.WithAppendSlice)
	if err != nil {
		return fmt.Errorf("failed to merge image config:\n%w", err)
	}

	return nil
}

// Returns a copy of the image config with relative file paths converted to absolute
// file paths (relative to referenceDir, not the current working directory).
func (i *ImageConfig) WithAbsolutePaths(referenceDir string) *ImageConfig {
	// Deep copy the input to avoid unexpected sharing. Make sure *not* to deep-copy
	// the SourceConfigFile, as we *do* want to alias that pointer, sharing it across
	// all configs that came from that source config file.
	result := &ImageConfig{
		Name:             i.Name,
		Description:      i.Description,
		SourceConfigFile: i.SourceConfigFile,
		Definition:       deep.MustCopy(i.Definition),
		Capabilities:     deep.MustCopy(i.Capabilities),
		Tests:            deep.MustCopy(i.Tests),
		Publish:          deep.MustCopy(i.Publish),
	}

	// Fix up paths.
	result.Definition.Path = makeAbsolute(referenceDir, result.Definition.Path)

	return result
}
