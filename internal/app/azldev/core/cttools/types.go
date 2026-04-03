// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package cttools provides utilities for parsing and resolving Azure Linux distro configuration.
//
//nolint:tagliatelle // JSON output must use hyphenated keys to match the distro-config schema.
package cttools

// DistroConfig is the top-level structure for the fully parsed/merged Azure Linux distro configuration.
type DistroConfig struct {
	Distros              map[string]Distro                  `toml:"distros"                json:"distros,omitempty"                yaml:"distros,omitempty"`
	KojiTargetsTemplates map[string]map[string][]KojiTarget `toml:"koji-targets-templates" json:"koji-targets-templates,omitempty" yaml:"koji-targets-templates,omitempty"`
	MockOptionsTemplates map[string]MockOptionsTemplate     `toml:"mock-options-templates" json:"mock-options-templates,omitempty" yaml:"mock-options-templates,omitempty"`
	BuildRootTemplates   map[string]BuildRootTemplate       `toml:"build-root-templates"   json:"build-root-templates,omitempty"   yaml:"build-root-templates,omitempty"`
	Environments         map[string]Environment             `toml:"environments"           json:"environments,omitempty"           yaml:"environments,omitempty"`
}

// Distro represents a distro definition (e.g. "azurelinux").
type Distro struct {
	Description      string             `toml:"description"       json:"description"                 yaml:"description"`
	ShadowAllowlists []ShadowAllowlist  `toml:"shadow-allowlists" json:"shadow-allowlists,omitempty" yaml:"shadow-allowlists,omitempty"`
	Versions         map[string]Version `toml:"versions"          json:"versions,omitempty"          yaml:"versions,omitempty"`
}

// ShadowAllowlist is a tag-name entry in a distro's shadow allowlist.
type ShadowAllowlist struct {
	TagName string `toml:"tag-name" json:"tag-name" yaml:"tag-name"`
}

// Version represents a distro version definition (e.g. "4.0-dev").
type Version struct {
	Description           string                      `toml:"description"              json:"description"                yaml:"description"`
	ReleaseVer            string                      `toml:"release-ver"              json:"release-ver"                yaml:"release-ver"`
	EnvironmentPrefix     string                      `toml:"environment-prefix"       json:"environment-prefix"         yaml:"environment-prefix"`
	RPMMacroDist          string                      `toml:"rpm-macro-dist"           json:"rpm-macro-dist"             yaml:"rpm-macro-dist"`
	RPMMacroDistBootstrap string                      `toml:"rpm-macro-dist-bootstrap" json:"rpm-macro-dist-bootstrap"   yaml:"rpm-macro-dist-bootstrap"`
	GitSourceRepos        map[string][]GitSourceRepo  `toml:"git-source-repos"         json:"git-source-repos,omitempty" yaml:"git-source-repos,omitempty"`
	BuildChannels         map[string][]BuildChannel   `toml:"build-channels"           json:"build-channels,omitempty"   yaml:"build-channels,omitempty"`
	PublishChannels       map[string][]PublishChannel `toml:"publish-channels"         json:"publish-channels,omitempty" yaml:"publish-channels,omitempty"`
}

// GitSourceRepo represents a git source repository definition within a distro version.
type GitSourceRepo struct {
	Ref                  string               `toml:"ref"                      json:"ref"                             yaml:"ref"`
	DefaultBranch        string               `toml:"default-branch"           json:"default-branch"                  yaml:"default-branch"`
	DefaultKojiRPMTarget string               `toml:"default-koji-rpms-target" json:"default-koji-rpms-target"        yaml:"default-koji-rpms-target"`
	KojiTargets          string               `toml:"koji-targets"             json:"koji-targets"                    yaml:"koji-targets"`
	RepoPrefix           string               `toml:"repo-prefix"              json:"repo-prefix,omitempty"           yaml:"repo-prefix,omitempty"`
	ParentPrefix         string               `toml:"parent-prefix"            json:"parent-prefix"                   yaml:"parent-prefix"`
	ResolvedKojiTargets  []ResolvedKojiTarget `toml:"resolved-koji-targets"    json:"resolved-koji-targets,omitempty" yaml:"resolved-koji-targets,omitempty"`
}

// ResolvedKojiTarget is a fully resolved koji target with all prefixes applied.
type ResolvedKojiTarget struct {
	Name          string              `toml:"name"           json:"name"                     yaml:"name"`
	OutputTag     string              `toml:"output-tag"     json:"output-tag"               yaml:"output-tag"`
	ParentTag     string              `toml:"parent-tag"     json:"parent-tag,omitempty"     yaml:"parent-tag,omitempty"`
	BuildRoots    []ResolvedBuildRoot `toml:"build-roots"    json:"build-roots"              yaml:"build-roots"`
	MockOptions   []string            `toml:"mock-options"   json:"mock-options"             yaml:"mock-options"`
	MockDistTag   string              `toml:"mock-dist-tag"  json:"mock-dist-tag,omitempty"  yaml:"mock-dist-tag,omitempty"`
	ExternalRepos []ExternalRepo      `toml:"external-repos" json:"external-repos,omitempty" yaml:"external-repos,omitempty"`
}

// ResolvedBuildRoot is a build root entry with the template expanded to a package list.
type ResolvedBuildRoot struct {
	Type     string   `toml:"type"     json:"type"     yaml:"type"`
	Packages []string `toml:"packages" json:"packages" yaml:"packages"`
}

// KojiTarget is a koji build target definition from a template.
type KojiTarget struct {
	OutputTag       string         `toml:"output-tag"        json:"output-tag"               yaml:"output-tag"`
	ParentTag       string         `toml:"parent-tag"        json:"parent-tag,omitempty"     yaml:"parent-tag,omitempty"`
	BuildRoots      []BuildRootRef `toml:"build-roots"       json:"build-roots"              yaml:"build-roots"`
	MockOptionsBase string         `toml:"mock-options-base" json:"mock-options-base"        yaml:"mock-options-base"`
	MockDistTag     string         `toml:"mock-dist-tag"     json:"mock-dist-tag,omitempty"  yaml:"mock-dist-tag,omitempty"`
	ExternalRepos   []ExternalRepo `toml:"external-repos"    json:"external-repos,omitempty" yaml:"external-repos,omitempty"`
}

// BuildRootRef references a build-root template by name.
type BuildRootRef struct {
	Type  string `toml:"type"  json:"type"  yaml:"type"`
	Value string `toml:"value" json:"value" yaml:"value"`
}

// ExternalRepo is an external repository definition on a koji target.
type ExternalRepo struct {
	Name      string `toml:"name"       json:"name"       yaml:"name"`
	URL       string `toml:"url"        json:"url"        yaml:"url"`
	MergeMode string `toml:"merge-mode" json:"merge-mode" yaml:"merge-mode"`
}

// MockOptionsTemplate defines a reusable set of mock/rpm options.
type MockOptionsTemplate struct {
	Options []string `toml:"options" json:"options" yaml:"options"`
}

// BuildRootTemplate defines a reusable package list for koji build roots.
type BuildRootTemplate struct {
	Packages []string `toml:"packages" json:"packages" yaml:"packages"`
}

// Environment is a Control Tower environment definition.
type Environment struct {
	Resources map[string][]map[string]any `toml:"resources" json:"resources" yaml:"resources"`
}

// BuildChannel specifies a koji target for routing builds.
type BuildChannel struct {
	KojiTarget string `toml:"koji-target" json:"koji-target" yaml:"koji-target"`
}

// PublishChannel specifies a resource for publishing artifacts.
type PublishChannel struct {
	PublishResource string `toml:"publish-resource" json:"publish-resource" yaml:"publish-resource"`
}
