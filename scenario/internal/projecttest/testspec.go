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
	name      string
	version   string
	release   string
	buildArch string
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

	lines = append(lines, []string{
		"",
		"%description",
		"Test component for, you know, testing.",
		"",
		"%build",
		"echo hello >file.txt",
		"",
		"%install",
		"mkdir -p %{buildroot}/%{_datadir}/test-component",
		"cp file.txt %{buildroot}/%{_datadir}/test-component/file.txt",
		"",
		"%files",
		"%{_datadir}/test-component",
		"",
	}...)

	return strings.Join(lines, "\n")
}
