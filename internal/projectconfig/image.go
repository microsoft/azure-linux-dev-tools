// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"
	"slices"

	"dario.cat/mergo"
	"github.com/brunoga/deep"
)

const (
	// ImageArchitectureX86_64 is the canonical x86-64 architecture name used by image builders.
	ImageArchitectureX86_64 = "x86_64"
	// ImageArchitectureAarch64 is the canonical 64-bit Arm architecture name used by image builders.
	ImageArchitectureAarch64 = "aarch64"
)

// SupportedImageArchitectures returns the architecture names azldev recognizes as
// valid for use in an image's Architectures list.
func SupportedImageArchitectures() []string {
	return []string{ImageArchitectureX86_64, ImageArchitectureAarch64}
}

// SKUArchForImageArchitecture maps an image architecture name (x86_64/aarch64)
// to its SKU-group architecture token (amd64/arm64). It is one half of the
// single source of truth bridging the two architecture naming conventions in the
// config: image builders use the kernel uname names while SKU groups use the
// Azure/Go GOARCH tokens. Keep it in sync with ImageArchitectureForSKUArch,
// SupportedImageArchitectures, and the SKUArch* constants. found is false when
// imageArch is not a recognized image architecture.
func SKUArchForImageArchitecture(imageArch string) (skuArch string, found bool) {
	switch imageArch {
	case ImageArchitectureX86_64:
		return SKUArchAMD64, true
	case ImageArchitectureAarch64:
		return SKUArchARM64, true
	default:
		return "", false
	}
}

// ImageArchitectureForSKUArch maps a SKU-group architecture token (amd64/arm64)
// to its image architecture name (x86_64/aarch64). found is false when skuArch
// is not a recognized SKU architecture.
func ImageArchitectureForSKUArch(skuArch string) (imageArch string, found bool) {
	switch skuArch {
	case SKUArchAMD64:
		return ImageArchitectureX86_64, true
	case SKUArchARM64:
		return ImageArchitectureAarch64, true
	default:
		return "", false
	}
}

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

	// Capabilities describes the features supported by this image.
	Capabilities ImageCapabilities `toml:"capabilities,omitempty" json:"capabilities,omitempty" jsonschema:"title=Capabilities,description=Features supported by this image"`

	// Tests holds the test configuration for this image, including which test suites
	// apply to it.
	Tests *ImageTestsConfig `toml:"tests,omitempty" json:"tests,omitempty" jsonschema:"title=Tests,description=Test configuration for this image"`

	// Publish holds the publish settings for this image.
	Publish ImagePublishConfig `toml:"publish,omitempty" json:"publish,omitempty" jsonschema:"title=Publish settings,description=Publishing settings for this image"`

	// Architectures lists the architectures this image supports. Optional: an
	// unset or empty list means the image is unrestricted (supports all
	// architectures azldev recognizes), preserving compatibility with
	// images.toml files written before this field existed.
	Architectures []string `toml:"architectures,omitempty" json:"architectures,omitempty" jsonschema:"title=Architectures,description=Architectures supported by this image (optional; unset means unrestricted),enum=x86_64,enum=aarch64"`

	// Properties holds extensible string metadata describing the image.
	Properties map[string]string `toml:"properties,omitempty" json:"properties,omitempty" jsonschema:"title=Properties,description=Extensible string properties describing this image"`
}

// SupportsArchitecture reports whether the image supports arch. An image with no
// declared Architectures is treated as unrestricted, for compatibility with
// images.toml files that predate this field, but arch must still be one of the
// architectures azldev recognizes (see SupportedImageArchitectures); an
// unrecognized architecture is never supported, restricted or not.
func (i *ImageConfig) SupportsArchitecture(arch string) bool {
	if !slices.Contains(SupportedImageArchitectures(), arch) {
		return false
	}

	if len(i.Architectures) == 0 {
		return true
	}

	return slices.Contains(i.Architectures, arch)
}

// ImagePublishConfig holds publish settings for an image. Unlike packages (which target a
// single channel), images may be published to multiple channels simultaneously.
type ImagePublishConfig struct {
	// Channels lists the publish channels for this image.
	Channels []string `toml:"channels,omitempty" json:"channels,omitempty" validate:"dive,required,ne=.,ne=..,excludesall=/\\" jsonschema:"title=Channels,description=List of publish channels for this image"`
}

// ImageCapabilities describes the features supported by an image. Pointer fields
// distinguish explicit values from unspecified values so configuration merging can
// preserve inheritance and explicit overrides.
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
		Architectures:    deep.MustCopy(i.Architectures),
		Properties:       deep.MustCopy(i.Properties),
	}

	// Fix up paths.
	result.Definition.Path = makeAbsolute(referenceDir, result.Definition.Path)

	return result
}
