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
	Tests *ImageTestsConfig `toml:"tests,omitempty" json:"tests,omitempty" jsonschema:"title=Tests,description=Test configuration for this image"`

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

	// WSL indicates whether the image runs under the Windows Subsystem for Linux
	// runtime, rather than being booted as a VM/bare-metal machine.
	WSL *bool `toml:"wsl,omitempty" json:"wsl,omitempty" jsonschema:"title=WSL,description=Whether the image runs under the Windows Subsystem for Linux runtime"`

	// InstallerMedia indicates whether the image is installer media (e.g. an ISO) that
	// installs another OS, rather than a directly runnable/bootable end-state image.
	InstallerMedia *bool `toml:"installer-media,omitempty" json:"installerMedia,omitempty" jsonschema:"title=Installer media,description=Whether the image is installer media that installs another OS rather than a directly runnable end-state image"`

	// FipsEnabled indicates whether the image is built/configured to run in FIPS mode.
	FipsEnabled *bool `toml:"fips-enabled,omitempty" json:"fipsEnabled,omitempty" jsonschema:"title=FIPS enabled,description=Whether the image is built or configured to run in FIPS mode"`

	// CVM indicates whether the image supports running as a Confidential VM (CVM).
	CVM *bool `toml:"cvm,omitempty" json:"cvm,omitempty" jsonschema:"title=Confidential VM,description=Whether the image supports running as a Confidential VM (CVM)"`
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

// IsWSL returns true if the image explicitly runs under the WSL runtime.
func (c *ImageCapabilities) IsWSL() bool {
	return c.WSL != nil && *c.WSL
}

// IsInstallerMedia returns true if the image is explicitly marked as installer media.
func (c *ImageCapabilities) IsInstallerMedia() bool {
	return c.InstallerMedia != nil && *c.InstallerMedia
}

// IsFipsEnabled returns true if the image is explicitly marked as FIPS-enabled.
func (c *ImageCapabilities) IsFipsEnabled() bool {
	return c.FipsEnabled != nil && *c.FipsEnabled
}

// IsCVM returns true if the image explicitly supports running as a Confidential VM.
func (c *ImageCapabilities) IsCVM() bool {
	return c.CVM != nil && *c.CVM
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

	if c.IsWSL() {
		names = append(names, "wsl")
	}

	if c.IsInstallerMedia() {
		names = append(names, "installer-media")
	}

	if c.IsFipsEnabled() {
		names = append(names, "fips-enabled")
	}

	if c.IsCVM() {
		names = append(names, "cvm")
	}

	return names
}

// ImageTestsConfig holds the test-related configuration for an image.
type ImageTestsConfig struct {
	// Tests is the list of test or test-group references that apply to this
	// image. References must resolve to entries in the project-level [tests] or
	// [test-groups] maps; resolution is the responsibility of the test layer.
	Tests []TestRef `toml:"tests,omitempty" json:"tests,omitempty" jsonschema:"title=Tests,description=List of test or test-group references that apply to this image"`
}

// TestNames returns the test and test-group reference labels for this image, for
// display/summary purposes. Group references are prefixed with "group:".
func (i *ImageConfig) TestNames() []string {
	if i.Tests == nil {
		return nil
	}

	names := make([]string, 0, len(i.Tests.Tests))

	for _, ref := range i.Tests.Tests {
		switch {
		case ref.Name != "":
			names = append(names, ref.Name)
		case ref.Group != "":
			names = append(names, "group:"+ref.Group)
		}
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
