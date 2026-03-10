// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

// Provides source information for locating the spec for a component.
type SpecSource struct {
	// SourceType indicates the type of source for the spec.
	SourceType SpecSourceType `toml:"type" json:"type,omitempty" validate:"omitempty,oneof=local upstream" jsonschema:"required,enum=local,enum=upstream,enum=,title=Source Type,description=The type of the spec source"`

	// Path indicates the path to the spec file; only relevant for local specs.
	Path string `toml:"path,omitempty" json:"path,omitempty" validate:"excluded_unless=SourceType local,required_if=SourceType local" jsonschema:"title=Path,description=Path to the spec (if available locally),example=specs/mycomponent.spec"`

	// UpstreamDistro indicates the upstream distro providing the spec; only relevant for upstream specs.
	UpstreamDistro DistroReference `toml:"upstream-distro,omitempty" json:"upstreamDistro,omitempty" jsonschema:"title=Upstream distro,description=Reference to the upstream distro providing the spec"`

	// UpstreamName indicates the name of the component in the upstream distro; only relevant for upstream specs.
	UpstreamName string `toml:"upstream-name,omitempty" json:"upstreamName,omitempty" validate:"excluded_unless=SourceType upstream" jsonschema:"title=Upstream component name,description=Name of the component in the upstream distro,example=different-name"`

	// UpstreamCommit pins the upstream spec to a specific git commit hash; only relevant for upstream specs.
	// When set, this takes priority over the snapshot date-time on the distro reference.
	UpstreamCommit string `toml:"upstream-commit,omitempty" json:"upstreamCommit,omitempty" validate:"excluded_unless=SourceType upstream,omitempty,hexadecimal,min=7,max=40" jsonschema:"title=Upstream commit,description=Git commit hash to pin the upstream spec to. Takes priority over snapshot.,minLength=7,maxLength=40,pattern=^[0-9a-fA-F]+$,example=abc1234def5678"`
}

// Implements the [Stringer] interface.
func (s *SpecSource) String() string {
	switch s.SourceType {
	case SpecSourceTypeUnspecified:
		fallthrough
	case SpecSourceTypeLocal:
		return s.Path
	case SpecSourceTypeUpstream:
		result := "Upstream: " + s.UpstreamDistro.String()
		if s.UpstreamCommit != "" {
			result += " @" + s.UpstreamCommit
		}

		return result
	default:
		return ""
	}
}

// Type of source for a spec.
type SpecSourceType string

const (
	// Default (unspecified) source.
	SpecSourceTypeUnspecified SpecSourceType = ""
	// Local source: the spec is present in the local filesystem.
	SpecSourceTypeLocal SpecSourceType = "local"
	// Upstream source: the spec is present in an upstream source (may not local).
	SpecSourceTypeUpstream SpecSourceType = "upstream"
)
