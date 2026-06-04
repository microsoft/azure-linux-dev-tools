// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projecttest

import (
	"strings"
)

// TestSpecOption is a function that can be used to modify a [TestSpec] in-place.
type TestSpecOption func(*TestSpec)

// NoArch is a constant representing the "noarch" architecture for RPMs.
const NoArch = "noarch"

// TestSpec represents an RPM spec being composed for testing purposes.
type TestSpec struct {
	name          string
	version       string
	release       string
	buildArch     string
	exclusiveArch []string
	excludeArch   []string
	subpackages   []string
}

// NewSpec creates a new [TestSpec] with the specified options.
func NewSpec(options ...TestSpecOption) *TestSpec {
	// Start with defaults.
	spec := &TestSpec{
		name:      "test-component",
		version:   "1.2.3",
		release:   "4.rel",
		buildArch: "",
	}

	for _, option := range options {
		option(spec)
	}

	return spec
}

// GetName returns the name of the component defined by the spec.
func (s *TestSpec) GetName() string {
	return s.name
}

// GetVersion returns the version of the component defined by the spec.
func (s *TestSpec) GetVersion() string {
	return s.version
}

// GetRelease returns the release of the component defined by the spec.
func (s *TestSpec) GetRelease() string {
	return s.release
}

// WithName sets the name of the component defined by the spec.
func WithName(name string) TestSpecOption {
	return func(s *TestSpec) {
		s.name = name
	}
}

// WithVersion sets the version of the component defined by the spec.
func WithVersion(version string) TestSpecOption {
	return func(s *TestSpec) {
		s.version = version
	}
}

// WithRelease sets the release of the component defined by the spec.
func WithRelease(release string) TestSpecOption {
	return func(s *TestSpec) {
		s.release = release
	}
}

// WithBuildArch sets the build architecture of the component defined by the spec.
func WithBuildArch(arch string) TestSpecOption {
	return func(s *TestSpec) {
		s.buildArch = arch
	}
}

// WithExclusiveArch appends to the ExclusiveArch tag on the spec, limiting
// the architectures for which the spec is considered buildable. Pass one or
// more arch tokens (e.g. "x86_64", "aarch64"). Repeated calls accumulate.
func WithExclusiveArch(arches ...string) TestSpecOption {
	return func(s *TestSpec) {
		s.exclusiveArch = append(s.exclusiveArch, arches...)
	}
}

// WithExcludeArch appends to the ExcludeArch tag on the spec, marking the
// listed architectures as unsupported. Repeated calls accumulate.
func WithExcludeArch(arches ...string) TestSpecOption {
	return func(s *TestSpec) {
		s.excludeArch = append(s.excludeArch, arches...)
	}
}

// WithSubpackage appends an additional binary subpackage (named
// "<spec name>-<suffix>") to the spec. The subpackage owns its own
// installed file under the same directory as the main package.
func WithSubpackage(suffix string) TestSpecOption {
	return func(s *TestSpec) {
		s.subpackages = append(s.subpackages, suffix)
	}
}

// Render generates the spec file content as a string.
func (s *TestSpec) Render() string {
	lines := []string{
		"Name: " + s.name,
		"Version: " + s.version,
		"Release: " + s.release,
		"Summary: A test component",
		"License: MIT",
	}

	if s.buildArch != "" {
		lines = append(lines, "BuildArch: "+s.buildArch)
	}

	if len(s.exclusiveArch) > 0 {
		lines = append(lines, "ExclusiveArch: "+strings.Join(s.exclusiveArch, " "))
	}

	if len(s.excludeArch) > 0 {
		lines = append(lines, "ExcludeArch: "+strings.Join(s.excludeArch, " "))
	}

	lines = append(lines, []string{
		"",
		"%description",
		"Test component for, you know, testing.",
		"",
	}...)

	for _, sub := range s.subpackages {
		lines = append(lines, []string{
			"%package " + sub,
			"Summary: A test subpackage",
			"",
			"%description " + sub,
			"Subpackage " + sub + " for testing.",
			"",
		}...)
	}

	lines = append(lines, []string{
		"%build",
		"echo hello >file.txt",
		"",
		"%install",
		"mkdir -p %{buildroot}/%{_datadir}/" + s.name,
		"cp file.txt %{buildroot}/%{_datadir}/" + s.name + "/file.txt",
	}...)
	for _, sub := range s.subpackages {
		lines = append(lines, "echo "+sub+" >%{buildroot}/%{_datadir}/"+s.name+"/"+sub+".txt")
	}

	lines = append(lines, []string{
		"",
		"%files",
		"%dir %{_datadir}/" + s.name,
		"%{_datadir}/" + s.name + "/file.txt",
		"",
	}...)

	for _, sub := range s.subpackages {
		lines = append(lines, []string{
			"%files " + sub,
			"%{_datadir}/" + s.name + "/" + sub + ".txt",
			"",
		}...)
	}

	return strings.Join(lines, "\n")
}
