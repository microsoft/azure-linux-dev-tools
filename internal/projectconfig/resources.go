// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/brunoga/deep"
	"github.com/invopop/jsonschema"
)

// RpmRepoType is the type discriminator for an [RpmRepoResource]. New types may be added
// in the future (e.g., "rpm-dir" for unindexed directories of RPMs, or "oci" for delta-RPM
// repos hosted in container registries). For now, only "rpm-md" is supported.
type RpmRepoType string

const (
	// RpmRepoTypeRpmMd designates a standard rpm-md (dnf) repository accessed via a
	// `base-uri` or `metalink`. This is the default when [RpmRepoResource.Type] is unset.
	RpmRepoTypeRpmMd RpmRepoType = "rpm-md"
)

// IsValid reports whether the given RpmRepoType is a value the loader currently understands.
func (t RpmRepoType) IsValid() bool {
	switch t {
	case "", RpmRepoTypeRpmMd:
		return true
	default:
		return false
	}
}

// ResourcesConfig is a top-level container for reusable, named resource definitions
// referenced from elsewhere in the configuration (e.g., from a distro version's
// [DistroVersionDefinition.Inputs]).
//
// The container is intentionally namespaced (e.g., resources.rpm-repos rather than
// rpm-repos) so that future sibling resource types (container registries, signing
// keys, etc.) can live under the same top-level key without crowding the schema.
//
// JSONSchemaExtend uses a value receiver because the invopop/jsonschema library
// only invokes value-receiver methods when reflecting on the type; the rest of
// the methods use pointer receivers because they mutate.
//
//nolint:recvcheck // intentional mixed receivers (see comment above).
type ResourcesConfig struct {
	// RpmRepos is the set of reusable RPM repository definitions, keyed by name.
	RpmRepos map[string]RpmRepoResource `toml:"rpm-repos,omitempty" json:"rpmRepos,omitempty" jsonschema:"title=RPM repositories,description=Reusable named RPM repository definitions"`

	// RpmRepoSetTemplates defines named layout templates that describe a fixed
	// matrix of sub-repos (e.g., the standard Azure Linux base/sdk x main/debuginfo/srpms
	// matrix, or a Koji dist-repo layout). Templates are instantiated by [RpmRepoSet]
	// entries.
	RpmRepoSetTemplates map[string]RpmRepoSetTemplate `toml:"rpm-repo-set-templates,omitempty" json:"rpmRepoSetTemplates,omitempty" jsonschema:"title=RPM repo set templates,description=Named layout templates that describe a fixed matrix of sub-repos"`

	// RpmRepoSets instantiates a [RpmRepoSetTemplate] for a specific deployment by
	// supplying a base URI and shared GPG configuration. Each set expands at validation
	// time into one or more synthesized [RpmRepoResource] entries; consumers reach the
	// expanded repos via [ResourcesConfig.EffectiveRpmRepos].
	RpmRepoSets map[string]RpmRepoSet `toml:"rpm-repo-sets,omitempty" json:"rpmRepoSets,omitempty" jsonschema:"title=RPM repo sets,description=Template instantiations that expand to a group of related RPM repos"`
}

// IsEmpty reports whether the ResourcesConfig contains no entries.
func (r *ResourcesConfig) IsEmpty() bool {
	return r == nil || (len(r.RpmRepos) == 0 && len(r.RpmRepoSetTemplates) == 0 && len(r.RpmRepoSets) == 0)
}

// JSONSchemaExtend tightens the generated schema for the resources maps so editors
// can flag invalid names at edit time. The runtime validator ([validateRpmRepoName])
// is the source of truth; this keeps the schema in sync.
func (ResourcesConfig) JSONSchemaExtend(schema *jsonschema.Schema) {
	if schema.Properties == nil {
		return
	}

	for _, key := range []string{"rpm-repos", "rpm-repo-set-templates", "rpm-repo-sets"} {
		prop, ok := schema.Properties.Get(key)
		if !ok || prop == nil {
			continue
		}

		prop.PropertyNames = &jsonschema.Schema{
			Type:        "string",
			Pattern:     rpmRepoNameRE.String(),
			Description: "Name; projected verbatim into dnf section headers and kiwi --add-repo arguments.",
		}
	}
}

// MergeUpdatesFrom mutates r, updating it with overrides present in other. Maps are
// merged by key with **wholesale replacement** at the entry level: a duplicate name
// in `other` fully replaces the existing entry, including any fields that happen to
// be the Go zero value in the new entry. This avoids subtle bugs where, e.g., a
// later config file intentionally setting `disable-gpg-check = false` (the zero
// value) would otherwise fail to override an earlier `true`.
func (r *ResourcesConfig) MergeUpdatesFrom(other *ResourcesConfig) error {
	if other == nil {
		return nil
	}

	if len(other.RpmRepos) > 0 && r.RpmRepos == nil {
		r.RpmRepos = make(map[string]RpmRepoResource, len(other.RpmRepos))
	}

	for name, repo := range other.RpmRepos {
		r.RpmRepos[name] = repo
	}

	if len(other.RpmRepoSetTemplates) > 0 && r.RpmRepoSetTemplates == nil {
		r.RpmRepoSetTemplates = make(map[string]RpmRepoSetTemplate, len(other.RpmRepoSetTemplates))
	}

	for name, tmpl := range other.RpmRepoSetTemplates {
		r.RpmRepoSetTemplates[name] = tmpl
	}

	if len(other.RpmRepoSets) > 0 && r.RpmRepoSets == nil {
		r.RpmRepoSets = make(map[string]RpmRepoSet, len(other.RpmRepoSets))
	}

	for name, set := range other.RpmRepoSets {
		r.RpmRepoSets[name] = set
	}

	return nil
}

// RpmRepoResource describes a single reusable RPM repository. Per-distro-version
// configuration ([DistroVersionDefinition.Inputs]) selects which repos are made
// available to which build use-cases (rpm-build, image-build).
//
// dnf-side variables ($basearch, $releasever) in `base-uri`/`metalink`/`gpg-key`
// fields are passed through verbatim and expanded by the consuming tool (mock/dnf
// or kiwi).
//
// **GPG checking defaults to enabled** (the safe default). Set
// `disable-gpg-check = true` to opt out. A repo with GPG checking enabled and no
// `gpg-key` is invalid; load-time validation rejects it.
//
// JSONSchemaExtend uses a value receiver because the invopop/jsonschema library
// only invokes value-receiver methods when reflecting on the type; the rest of
// the methods use pointer receivers because they read shared state.
//
//nolint:recvcheck // intentional mixed receivers (see comment above).
type RpmRepoResource struct {
	// Description is a human-readable description of the repository. Used only
	// for `azldev config dump` and similar diagnostics; not projected into dnf
	// or kiwi configuration (avoids newline/encoding pitfalls).
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Human-readable description (diagnostic only)"`

	// Type discriminates the repository's access protocol. Defaults to "rpm-md" when unset.
	Type RpmRepoType `toml:"type,omitempty" json:"type,omitempty" jsonschema:"title=Type,description=Repository access protocol; defaults to rpm-md,enum=rpm-md"`

	// BaseURI is the repository base URI (dnf's `baseurl`). Mutually exclusive with
	// [RpmRepoResource.Metalink]. Either BaseURI or Metalink must be set for type "rpm-md".
	BaseURI string `toml:"base-uri,omitempty" json:"baseUri,omitempty" jsonschema:"format=uri,pattern=^https?://[^\\s]+$,title=Base URI,description=Repository base URI (dnf baseurl). Mutually exclusive with metalink. Must be an http(s) URL."`

	// Metalink is the repository metalink URL. Mutually exclusive with [RpmRepoResource.BaseURI].
	Metalink string `toml:"metalink,omitempty" json:"metalink,omitempty" jsonschema:"format=uri,pattern=^https?://[^\\s]+$,title=Metalink,description=Repository metalink URL. Mutually exclusive with base-uri. Must be an http(s) URL."`

	// DisableGPGCheck disables GPG signature verification for this repo. The zero
	// value (false) means GPG checking is *enabled* — the safe default. Setting
	// this to true is an explicit opt-out and load-time validation requires either
	// `disable-gpg-check = true` or a non-empty `gpg-key`.
	DisableGPGCheck bool `toml:"disable-gpg-check,omitempty" json:"disableGpgCheck,omitempty" jsonschema:"title=Disable GPG check,description=Opt out of GPG signature verification for this repo (zero value = checking enabled)"`

	// GPGKey is a path or URI to the GPG key used to verify signatures.
	//
	// Accepted forms:
	//   * `https://...` or `http://...` URI: passed through verbatim to both consumers.
	//   * `file:///absolute/path`: passed through verbatim. Note that mock evaluates this
	//     *inside* the chroot, while kiwi evaluates it on the host. Prefer http(s)://
	//     for portability across consumers.
	//   * Bare path: resolved at TOML-load time relative to the directory containing the
	//     defining TOML file, then emitted to consumers as a `file://` URI.
	GPGKey string `toml:"gpg-key,omitempty" json:"gpgKey,omitempty" jsonschema:"pattern=^\\S+$,title=GPG key,description=Path or URI to the GPG key file. Accepted URI schemes: http\\, https\\, file. Bare paths are resolved relative to the defining TOML file."`

	// Arches optionally restricts the repository to a specific list of target architectures
	// (e.g., ["x86_64"]). When empty, the repository is available for all architectures.
	Arches []string `toml:"arches,omitempty" json:"arches,omitempty" jsonschema:"title=Arches,description=Restrict to specific target architectures; empty = all"`
}

// EffectiveType returns the repository type, applying the rpm-md default when [RpmRepoResource.Type]
// is unset.
func (r *RpmRepoResource) EffectiveType() RpmRepoType {
	if r.Type == "" {
		return RpmRepoTypeRpmMd
	}

	return r.Type
}

// JSONSchemaExtend tightens the generated schema for an [RpmRepoResource] so that
// editors can flag invalid TOML at edit time. Mirrors the runtime constraints in
// [validateRpmRepo]:
//
//   - exactly one of `base-uri` / `metalink` must be set;
//   - `gpg-key` must be a bare path or an `http://` / `https://` / `file://` URI.
//
// The runtime validator is the source of truth; this keeps the schema in sync.
func (RpmRepoResource) JSONSchemaExtend(schema *jsonschema.Schema) {
	// Exactly one of base-uri/metalink. Encoded as oneOf with `required` on each.
	schema.OneOf = []*jsonschema.Schema{
		{Required: []string{"base-uri"}, Not: &jsonschema.Schema{Required: []string{"metalink"}}},
		{Required: []string{"metalink"}, Not: &jsonschema.Schema{Required: []string{"base-uri"}}},
	}

	// gpg-key: bare path OR http(s)/file URI. The struct tag pattern is just
	// "no whitespace"; this tightens it to one of the supported shapes.
	if schema.Properties != nil {
		if gpg, ok := schema.Properties.Get("gpg-key"); ok && gpg != nil {
			gpg.Pattern = `^((https?|file)://\S+|[^\s:]\S*)$`
		}
	}
}

// IsAvailableForArch reports whether the repository is available for the given target
// architecture. A repository with no [RpmRepoResource.Arches] restriction is available
// for all architectures.
func (r *RpmRepoResource) IsAvailableForArch(arch string) bool {
	if len(r.Arches) == 0 {
		return true
	}

	for _, a := range r.Arches {
		if a == arch {
			return true
		}
	}

	return false
}

// validateRpmRepo checks the structural validity of an [RpmRepoResource] in isolation.
// Cross-resource and cross-section validation (e.g., that names referenced from
// [DistroVersionDefinition.Inputs] resolve, or that bare gpg-key paths are not used
// for rpm-build inputs) is performed elsewhere.
func validateRpmRepo(name string, repo *RpmRepoResource) error {
	if err := validateRpmRepoName(name); err != nil {
		return err
	}

	if !repo.EffectiveType().IsValid() {
		return fmt.Errorf("rpm-repo %#q has unsupported type %#q", name, repo.Type)
	}

	if err := validateNoUnsafeChars("rpm-repo", "description", name, repo.Description); err != nil {
		return err
	}

	if err := validateRpmRepoSource(name, repo); err != nil {
		return err
	}

	return validateRpmRepoGPG(name, repo)
}

// validateRpmRepoSource validates the source-defining fields of a repo (`base-uri`
// and `metalink`) for the active [RpmRepoResource.EffectiveType].
func validateRpmRepoSource(name string, repo *RpmRepoResource) error {
	if repo.EffectiveType() == RpmRepoTypeRpmMd {
		if repo.BaseURI == "" && repo.Metalink == "" {
			return fmt.Errorf("rpm-repo %#q must specify exactly one of `base-uri` or `metalink`", name)
		}

		if repo.BaseURI != "" && repo.Metalink != "" {
			return fmt.Errorf("rpm-repo %#q must not specify both `base-uri` and `metalink`", name)
		}
	}

	if repo.BaseURI != "" {
		if err := validateRemoteURI("rpm-repo", "base-uri", name, repo.BaseURI); err != nil {
			return err
		}
	}

	if repo.Metalink != "" {
		if err := validateRemoteURI("rpm-repo", "metalink", name, repo.Metalink); err != nil {
			return err
		}
	}

	return nil
}

// validateRpmRepoGPG validates the GPG-related fields (`disable-gpg-check`, `gpg-key`).
func validateRpmRepoGPG(name string, repo *RpmRepoResource) error {
	if !repo.DisableGPGCheck && repo.GPGKey == "" {
		return fmt.Errorf(
			"rpm-repo %#q has GPG checking enabled (the default) but no `gpg-key`; "+
				"either set `gpg-key = \"...\"` or opt out with `disable-gpg-check = true`",
			name,
		)
	}

	if repo.GPGKey != "" {
		if err := validateGPGKey("rpm-repo", name, repo.GPGKey); err != nil {
			return err
		}
	}

	return nil
}

// rpmRepoNameRE is the canonical grammar for repo IDs. Conservative: must start with an
// alphanumeric, then a mix of [A-Za-z0-9_.:-]. This is a strict subset of what dnf/kiwi
// accept, but keeps generated config files (dnf section headers, kiwi comma-delimited
// arguments) unambiguous and free of escaping concerns.
var rpmRepoNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)

// URI scheme constants used by the validators.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
	schemeFile  = "file"
)

func validateRpmRepoName(name string) error {
	if name == "" {
		return errors.New("rpm-repo name must not be empty")
	}

	if !rpmRepoNameRE.MatchString(name) {
		return fmt.Errorf(
			"rpm-repo name %#q is invalid; must match %s "+
				"(repo names are projected verbatim into dnf section headers and kiwi --add-repo arguments)",
			name, rpmRepoNameRE.String(),
		)
	}

	return nil
}

// validateNoUnsafeChars rejects values containing characters that, if projected into
// generated dnf/kiwi/python config, would corrupt the surrounding syntax. This is a
// defense-in-depth check: even for trusted TOML sources, unsanitized newlines or NUL
// bytes here produce baffling downstream failures.
func validateNoUnsafeChars(objKind, field, name, value string) error {
	for byteIndex, char := range value {
		switch char {
		case '\r', '\n':
			return fmt.Errorf(
				"%s %#q `%s` must be a single line (no embedded CR/LF at byte %d)",
				objKind, name, field, byteIndex)
		case 0:
			return fmt.Errorf(
				"%s %#q `%s` must not contain NUL bytes (at byte %d)",
				objKind, name, field, byteIndex)
		case '\u2028', '\u2029':
			return fmt.Errorf(
				"%s %#q `%s` must not contain Unicode line separators (at byte %d)",
				objKind, name, field, byteIndex)
		}
	}

	return nil
}

// validateRemoteURI ensures a base-uri/metalink value is a syntactically valid URI with
// an http or https scheme. Local schemes (file://) are deliberately disallowed for the
// repo source: kiwi.AddRemoteRepo only handles remote URIs, and supporting file:// here
// would require split-by-consumer staging that we don't do today.
//
// Also rejects opaque (non-hierarchical) URIs like `https:example.com` (no `//`) and
// requires a non-empty `Host`, so the value is always something downstream tools will
// recognize as `http(s)://...`.
func validateRemoteURI(objKind, field, name, raw string) error {
	if err := validateNoUnsafeChars(objKind, field, name, raw); err != nil {
		return err
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s %#q `%s` is not a valid URI:\n%w", objKind, name, field, err)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "":
		return fmt.Errorf("%s %#q `%s` is missing a scheme (expected an http or https URL)", objKind, name, field)
	case schemeHTTP, schemeHTTPS:
		// fall through to hierarchical-form checks below.
	default:
		return fmt.Errorf(
			"%s %#q `%s` uses unsupported scheme %q; only http and https are accepted",
			objKind, name, field, parsed.Scheme,
		)
	}

	if parsed.Opaque != "" {
		return fmt.Errorf(
			"%s %#q `%s` must use the `scheme://host/path` form (got opaque URI %q)",
			objKind, name, field, raw,
		)
	}

	if parsed.Host == "" {
		return fmt.Errorf("%s %#q `%s` must include a host (got %q)", objKind, name, field, raw)
	}

	return nil
}

// validateGPGKey checks the in-isolation form of a `gpg-key` value. A bare path is
// allowed at this stage (resolved to an absolute file:// URI by [WithAbsolutePaths]);
// rejection of bare paths for rpm-build consumers happens in
// [validateDistroVersionInputs], not here.
//
// For URI-shaped values, the supported schemes are `http`, `https`, and `file`. We
// require hierarchical (`scheme://...`) forms — opaque URIs like `file:relative` or
// `https:example.com` are rejected since they wouldn't behave the way callers expect.
// `http(s)` requires a non-empty `Host`; `file` requires an empty `Host` and an absolute
// `Path` (so it really is a `file:///abs/path` reference rather than e.g.
// `file://server/share`).
func validateGPGKey(objKind, name, raw string) error {
	if err := validateNoUnsafeChars(objKind, "gpg-key", name, raw); err != nil {
		return err
	}

	if !hasURIScheme(raw) {
		// Bare path; will be resolved relative to the defining TOML directory.
		return nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s %#q `gpg-key` is not a valid URI:\n%w", objKind, name, err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case schemeHTTP, schemeHTTPS, schemeFile:
		// fall through to per-scheme shape checks.
	default:
		return fmt.Errorf(
			"%s %#q `gpg-key` uses unsupported scheme %q; expected http, https, or file",
			objKind, name, parsed.Scheme,
		)
	}

	if parsed.Opaque != "" {
		return fmt.Errorf(
			"%s %#q `gpg-key` must use the `scheme://...` form (got opaque URI %q)",
			objKind, name, raw,
		)
	}

	switch scheme {
	case schemeHTTP, schemeHTTPS:
		if parsed.Host == "" {
			return fmt.Errorf("%s %#q `gpg-key` must include a host (got %q)", objKind, name, raw)
		}
	case schemeFile:
		if parsed.Host != "" {
			return fmt.Errorf(
				"%s %#q `gpg-key` must be of the form `file:///absolute/path` (got host %q in %q)",
				objKind, name, parsed.Host, raw,
			)
		}

		if !filepath.IsAbs(parsed.Path) {
			return fmt.Errorf(
				"%s %#q `gpg-key` must be an absolute `file://` path (got %q)",
				objKind, name, raw,
			)
		}
	}

	return nil
}

// IsLocalGPGKey reports whether the repo's gpg-key (if any) refers to a local filesystem
// path (either bare or via a `file://` URI). This is the form that does NOT work for
// rpm-build (mock) consumers because mock evaluates the URI inside the chroot.
func (r *RpmRepoResource) IsLocalGPGKey() bool {
	if r.GPGKey == "" {
		return false
	}

	if !hasURIScheme(r.GPGKey) {
		return true
	}

	parsed, err := url.Parse(r.GPGKey)
	if err != nil {
		return false
	}

	return strings.EqualFold(parsed.Scheme, schemeFile)
}

// WithAbsolutePaths returns a copy of the ResourcesConfig with relative path-shaped
// fields (currently only `gpg-key`) resolved relative to referenceDir and re-emitted
// as absolute `file://` URIs. URI-shaped fields are returned unchanged.
//
// This runs at TOML load time, so the consumers (mock / kiwi) only ever see absolute
// references — no working-directory ambiguity at run time.
func (r *ResourcesConfig) WithAbsolutePaths(referenceDir string) *ResourcesConfig {
	if r == nil {
		return nil
	}

	result := deep.MustCopy(r)

	for name, repo := range result.RpmRepos {
		if repo.GPGKey != "" {
			repo.GPGKey = absolutizeKeyPath(repo.GPGKey, referenceDir)
			result.RpmRepos[name] = repo
		}
	}

	for name, set := range result.RpmRepoSets {
		if set.GPGKey != "" {
			set.GPGKey = absolutizeKeyPath(set.GPGKey, referenceDir)
			result.RpmRepoSets[name] = set
		}
	}

	return result
}

// absolutizeKeyPath resolves a TOML-supplied gpg-key value to a portable form.
// URI-shaped values (with a scheme) are returned unchanged. Bare paths are joined
// against referenceDir (when not already absolute) and emitted as `file://` URIs.
func absolutizeKeyPath(key, referenceDir string) string {
	if hasURIScheme(key) {
		return key
	}

	abs := key
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(referenceDir, abs)
	}

	abs = filepath.Clean(abs)

	return (&url.URL{Scheme: schemeFile, Path: abs}).String()
}

// hasURIScheme reports whether s starts with "<alpha>[<alphanumeric>+-.]*:".
// Cheap and dependency-free; we don't need full RFC 3986 here.
func hasURIScheme(s string) bool {
	for i, char := range s {
		if i == 0 {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') {
				return false
			}

			continue
		}

		switch {
		case char >= 'a' && char <= 'z',
			char >= 'A' && char <= 'Z',
			char >= '0' && char <= '9',
			char == '+', char == '-', char == '.':
			continue
		case char == ':':
			return true
		default:
			return false
		}
	}

	return false
}

// SubrepoKind classifies a sub-repo within an [RpmRepoSetTemplate] by the type
// of RPMs it carries. Consumers (and authors of allowlist filters) use the kind
// when reasoning about a sub-repo's role in a build.
type SubrepoKind string

const (
	// SubrepoKindBinary identifies a sub-repo carrying ordinary binary RPMs.
	// This is the default when [SubrepoSpec.Kind] is unset.
	SubrepoKindBinary SubrepoKind = "binary"
	// SubrepoKindDebug identifies a sub-repo carrying debuginfo / debugsource RPMs.
	SubrepoKindDebug SubrepoKind = "debug"
	// SubrepoKindSource identifies a sub-repo carrying source RPMs (SRPMs).
	SubrepoKindSource SubrepoKind = "source"
)

// IsValid reports whether the given SubrepoKind is one this loader understands.
func (k SubrepoKind) IsValid() bool {
	switch k {
	case "", SubrepoKindBinary, SubrepoKindDebug, SubrepoKindSource:
		return true
	default:
		return false
	}
}

// Default returns the kind, defaulting to [SubrepoKindBinary] when unset.
func (k SubrepoKind) Default() SubrepoKind {
	if k == "" {
		return SubrepoKindBinary
	}

	return k
}

// SubrepoSpec describes one sub-repo entry within an [RpmRepoSetTemplate]. The
// `subpath` is appended to a [RpmRepoSet]'s `base-uri` to form the synthesized
// repo's `base-uri`. `$basearch` (and any other dnf-side variables) are passed
// through verbatim and expanded by the consuming tool.
type SubrepoSpec struct {
	// Name is a stable short identifier for this sub-repo within the template
	// (e.g., "base", "base-debug"). Combined with the [RpmRepoSet]'s
	// `name-prefix` it forms the synthesized repo's ID, so it must satisfy the
	// same grammar as [RpmRepoResource] names.
	Name string `toml:"name" json:"name" jsonschema:"required,title=Name,description=Stable short identifier; combined with the set's name-prefix to form the repo ID"`

	// Subpath is the relative path (under the set's base URI) that hosts the
	// sub-repo's repodata. May contain dnf-side variables such as `$basearch`,
	// which are passed through verbatim.
	Subpath string `toml:"subpath" json:"subpath" jsonschema:"required,title=Sub-path,description=Relative path under the set's base URI; may contain $basearch"`

	// Kind classifies the sub-repo (binary, debug, source). Defaults to "binary".
	// Authors of [RpmRepoSet] entries can filter by kind only indirectly, by
	// listing specific sub-repo names in `subrepos`.
	Kind SubrepoKind `toml:"kind,omitempty" json:"kind,omitempty" jsonschema:"title=Kind,description=Sub-repo classification; defaults to binary,enum=binary,enum=debug,enum=source"`
}

// RpmRepoSetTemplate is a named layout that describes a fixed set of sub-repos
// hosted under a common URL prefix. Templates are reusable across deployments;
// pair a template with a [RpmRepoSet] to produce a concrete bundle of repos.
type RpmRepoSetTemplate struct {
	// Description is a human-readable description of the layout (diagnostic only).
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Human-readable description (diagnostic only)"`

	// Subrepos is the ordered list of sub-repo entries that make up the layout.
	Subrepos []SubrepoSpec `toml:"subrepos" json:"subrepos" jsonschema:"required,title=Sub-repos,description=Ordered list of sub-repos in the layout"`
}

// RpmRepoSet instantiates an [RpmRepoSetTemplate] for a specific deployment by
// supplying a base URI and shared GPG configuration. At validation time each set
// expands into one synthesized [RpmRepoResource] per included sub-repo, keyed by
// `<name-prefix><subrepo.name>`.
type RpmRepoSet struct {
	// Description is a human-readable description (diagnostic only).
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Human-readable description (diagnostic only)"`

	// Template is the name of the [RpmRepoSetTemplate] to instantiate. Must
	// resolve to a defined template at validation time.
	Template string `toml:"template" json:"template" jsonschema:"required,title=Template,description=Name of the rpm-repo-set-template to instantiate"`

	// BaseURI is the URL prefix under which all sub-repos for this deployment
	// live. The synthesized repo's `base-uri` is `BaseURI/<subrepo.subpath>`.
	BaseURI string `toml:"base-uri" json:"baseUri" jsonschema:"required,format=uri,pattern=^https?://[^\\s]+$,title=Base URI,description=URL prefix under which all sub-repos in this set live"`

	// NamePrefix is prepended to each sub-repo's `name` to form the synthesized
	// repo ID. May be empty, in which case bare sub-repo names are used (only
	// safe when no other set or explicit repo collides with those names).
	NamePrefix string `toml:"name-prefix,omitempty" json:"namePrefix,omitempty" jsonschema:"title=Name prefix,description=Prepended to each sub-repo's name to form the repo ID"`

	// GPGKey is the shared GPG key for sub-repos in this set; same shape rules
	// as [RpmRepoResource.GPGKey] apply.
	GPGKey string `toml:"gpg-key,omitempty" json:"gpgKey,omitempty" jsonschema:"pattern=^\\S+$,title=GPG key,description=Path or URI to the GPG key file. Accepted URI schemes: http\\, https\\, file. Bare paths are resolved relative to the defining TOML file."`

	// DisableGPGCheck opts out of GPG signature verification for sub-repos in
	// this set; same default semantics as [RpmRepoResource.DisableGPGCheck].
	DisableGPGCheck bool `toml:"disable-gpg-check,omitempty" json:"disableGpgCheck,omitempty" jsonschema:"title=Disable GPG check,description=Opt out of GPG signature verification for repos in this set"`

	// Arches optionally restricts every synthesized repo in this set to a
	// specific list of target architectures. Empty = all.
	Arches []string `toml:"arches,omitempty" json:"arches,omitempty" jsonschema:"title=Arches,description=Restrict to specific target architectures; empty = all"`

	// Subrepos is an optional allowlist of sub-repo names from the referenced
	// template to instantiate. When unset (or empty), every sub-repo in the
	// template is instantiated. When non-empty, only the listed sub-repos are
	// instantiated. Listed names must match a sub-repo declared in the
	// referenced template.
	Subrepos []string `toml:"subrepos,omitempty" json:"subrepos,omitempty" jsonschema:"title=Sub-repos,description=Allowlist of template sub-repos to instantiate (default: all)"`
}

// EffectiveRpmRepos returns the union of explicitly-defined [RpmRepoResource]
// entries and the entries synthesized by expanding each [RpmRepoSet]. Conflicts
// are reported as errors.
//
// Path-typed inputs (e.g. relative `gpg-key` values) must already be absolutized
// before calling this method — that happens during config load/merge via
// [WithAbsolutePaths]. This method itself only joins each set's `base-uri` with
// each sub-repo's `subpath` and validates the result; it does not resolve any
// remaining relative paths.
//
// Validation rules applied during expansion:
//   - Every set's `template` must resolve to a defined [RpmRepoSetTemplate].
//   - Every name in `subrepos` (the set's allowlist) must match a sub-repo in
//     the referenced template.
//   - Synthesized repo IDs (`<name-prefix><subrepo.name>`) must satisfy the
//     same grammar as explicit repo names, and must not collide with explicit
//     [RpmRepoResource] entries or with other sets' expansions.
//   - The set's `base-uri` must be an http(s) URL; sub-repo subpaths must be
//     relative (no leading `/`, no `..` segments).
func (r *ResourcesConfig) EffectiveRpmRepos() (map[string]RpmRepoResource, error) {
	if r == nil {
		return map[string]RpmRepoResource{}, nil
	}

	effective := make(map[string]RpmRepoResource, len(r.RpmRepos))

	for name, repo := range r.RpmRepos {
		effective[name] = repo
	}

	// Stable expansion order for deterministic error reporting.
	setNames := make([]string, 0, len(r.RpmRepoSets))
	for name := range r.RpmRepoSets {
		setNames = append(setNames, name)
	}

	sort.Strings(setNames)

	for _, setName := range setNames {
		set := r.RpmRepoSets[setName]

		expanded, err := expandRpmRepoSet(setName, &set, r.RpmRepoSetTemplates)
		if err != nil {
			return nil, err
		}

		for _, entry := range expanded {
			if _, ok := effective[entry.Name]; ok {
				return nil, fmt.Errorf(
					"rpm-repo-set %#q expansion produced repo name %#q which already exists "+
						"(either as an explicit entry under `[resources.rpm-repos]` or from "+
						"another set's expansion); choose a different `name-prefix` or rename "+
						"the colliding entry",
					setName, entry.Name,
				)
			}

			effective[entry.Name] = entry.Repo
		}
	}

	return effective, nil
}

// expandedRepo is one entry in a [RpmRepoSet] expansion. The slice form preserves
// the template's declared sub-repo order, which both gives deterministic output and
// avoids a second template lookup at the call sites that need it.
type expandedRepo struct {
	Name string
	Repo RpmRepoResource
}

// expandRpmRepoSet expands a single [RpmRepoSet] into its synthesized
// [RpmRepoResource] entries in template-declared order.
func expandRpmRepoSet(
	setName string, set *RpmRepoSet, templates map[string]RpmRepoSetTemplate,
) ([]expandedRepo, error) {
	if err := validateRpmRepoSet(setName, set); err != nil {
		return nil, err
	}

	tmpl, ok := templates[set.Template]
	if !ok {
		return nil, fmt.Errorf(
			"rpm-repo-set %#q references undefined `template` %#q; "+
				"define a matching key under `[resources.rpm-repo-set-templates]`",
			setName, set.Template,
		)
	}

	allowlist, err := buildSubrepoAllowlist(setName, set, &tmpl)
	if err != nil {
		return nil, err
	}

	out := make([]expandedRepo, 0, len(tmpl.Subrepos))

	for i := range tmpl.Subrepos {
		sub := &tmpl.Subrepos[i]

		if allowlist != nil {
			if _, ok := allowlist[sub.Name]; !ok {
				continue
			}
		}

		baseURI, err := joinSetBaseURI(setName, set.BaseURI, sub.Subpath)
		if err != nil {
			return nil, err
		}

		repoID := set.NamePrefix + sub.Name
		if err := validateRpmRepoName(repoID); err != nil {
			return nil, fmt.Errorf(
				"rpm-repo-set %#q would synthesize an invalid repo ID %#q "+
					"(combining `name-prefix` %#q with sub-repo name %#q):\n%w",
				setName, repoID, set.NamePrefix, sub.Name, err,
			)
		}

		out = append(out, expandedRepo{
			Name: repoID,
			Repo: RpmRepoResource{
				Description:     expandedDescription(set, sub),
				Type:            RpmRepoTypeRpmMd,
				BaseURI:         baseURI,
				DisableGPGCheck: set.DisableGPGCheck,
				GPGKey:          set.GPGKey,
				Arches:          append([]string(nil), set.Arches...),
			},
		})
	}

	return out, nil
}

// expandedDescription preserves the user-supplied set description (if any) and
// annotates it with the originating set/subrepo for diagnostics.
func expandedDescription(set *RpmRepoSet, sub *SubrepoSpec) string {
	if set.Description != "" {
		return fmt.Sprintf("%s — subrepo %q (kind=%s)", set.Description, sub.Name, sub.Kind.Default())
	}

	return fmt.Sprintf("expanded from rpm-repo-set, subrepo %q (kind=%s)", sub.Name, sub.Kind.Default())
}

// buildSubrepoAllowlist resolves the set's optional `subrepos` allowlist into a
// lookup keyed by sub-repo name. Returns (nil, nil) when the allowlist is unset
// or empty (= include every sub-repo from the template).
func buildSubrepoAllowlist(
	setName string, set *RpmRepoSet, tmpl *RpmRepoSetTemplate,
) (map[string]struct{}, error) {
	if len(set.Subrepos) == 0 {
		return nil, nil //nolint:nilnil // nil = "no allowlist"
	}

	known := make(map[string]struct{}, len(tmpl.Subrepos))
	for i := range tmpl.Subrepos {
		known[tmpl.Subrepos[i].Name] = struct{}{}
	}

	allowlist := make(map[string]struct{}, len(set.Subrepos))

	for _, name := range set.Subrepos {
		if _, ok := known[name]; !ok {
			return nil, fmt.Errorf(
				"rpm-repo-set %#q `subrepos` allowlist references %#q "+
					"which is not a sub-repo of template %#q",
				setName, name, set.Template,
			)
		}

		if _, dup := allowlist[name]; dup {
			return nil, fmt.Errorf(
				"rpm-repo-set %#q `subrepos` allowlist lists %#q more than once",
				setName, name,
			)
		}

		allowlist[name] = struct{}{}
	}

	return allowlist, nil
}

// joinSetBaseURI joins a set's base-uri with a sub-repo's subpath, validating
// that the subpath is relative and well-formed and that the base-uri carries no
// query string or fragment (which would otherwise produce a syntactically
// invalid URL when path segments were appended).
func joinSetBaseURI(setName, baseURI, subpath string) (string, error) {
	if subpath == "" {
		return "", fmt.Errorf("rpm-repo-set %#q template has an empty `subpath`", setName)
	}

	if strings.HasPrefix(subpath, "/") {
		return "", fmt.Errorf(
			"rpm-repo-set %#q template `subpath` %#q must be relative (no leading slash)",
			setName, subpath,
		)
	}

	for _, segment := range strings.Split(subpath, "/") {
		if segment == ".." {
			return "", fmt.Errorf(
				"rpm-repo-set %#q template `subpath` %#q must not contain `..` segments",
				setName, subpath,
			)
		}
	}

	parsed, err := url.Parse(baseURI)
	if err != nil {
		return "", fmt.Errorf("rpm-repo-set %#q `base-uri` is not a valid URI:\n%w", setName, err)
	}

	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf(
			"rpm-repo-set %#q `base-uri` must not contain a query string or fragment (got %q)",
			setName, baseURI,
		)
	}

	return strings.TrimRight(baseURI, "/") + "/" + subpath, nil
}

// validateRpmRepoSet performs structural validation of a single set definition
// (independent of whether the referenced template exists).
func validateRpmRepoSet(name string, set *RpmRepoSet) error {
	if set.Template == "" {
		return fmt.Errorf("rpm-repo-set %#q is missing `template`", name)
	}

	if set.BaseURI == "" {
		return fmt.Errorf("rpm-repo-set %#q is missing `base-uri`", name)
	}

	if err := validateRemoteURI("rpm-repo-set", "base-uri", name, set.BaseURI); err != nil {
		return err
	}

	// Each synthesized repo ID (`name-prefix + sub.Name`) is validated against
	// the repo-name grammar in [expandRpmRepoSet] when the set actually expands;
	// no per-prefix structural check here is correct or strict enough on its own.

	if !set.DisableGPGCheck && set.GPGKey == "" {
		return fmt.Errorf(
			"rpm-repo-set %#q has GPG checking enabled (the default) but no `gpg-key`; "+
				"either set `gpg-key = \"...\"` or opt out with `disable-gpg-check = true`",
			name,
		)
	}

	if set.GPGKey != "" {
		if err := validateGPGKey("rpm-repo-set", name, set.GPGKey); err != nil {
			return err
		}
	}

	return nil
}

// validateRpmRepoSetTemplate performs structural validation of a single template.
func validateRpmRepoSetTemplate(name string, tmpl *RpmRepoSetTemplate) error {
	if len(tmpl.Subrepos) == 0 {
		return fmt.Errorf("rpm-repo-set-template %#q must define at least one entry in `subrepos`", name)
	}

	seen := make(map[string]bool, len(tmpl.Subrepos))

	for i := range tmpl.Subrepos {
		sub := &tmpl.Subrepos[i]

		if err := validateRpmRepoName(sub.Name); err != nil {
			return fmt.Errorf("rpm-repo-set-template %#q sub-repo %d:\n%w", name, i, err)
		}

		if seen[sub.Name] {
			return fmt.Errorf(
				"rpm-repo-set-template %#q has duplicate sub-repo name %#q", name, sub.Name,
			)
		}

		seen[sub.Name] = true

		if !sub.Kind.IsValid() {
			return fmt.Errorf(
				"rpm-repo-set-template %#q sub-repo %#q has invalid `kind` %#q "+
					"(expected one of: binary, debug, source)",
				name, sub.Name, sub.Kind,
			)
		}

		if sub.Subpath == "" {
			return fmt.Errorf(
				"rpm-repo-set-template %#q sub-repo %#q is missing `subpath`",
				name, sub.Name,
			)
		}

		if strings.HasPrefix(sub.Subpath, "/") {
			return fmt.Errorf(
				"rpm-repo-set-template %#q sub-repo %#q `subpath` must be relative (no leading slash)",
				name, sub.Name,
			)
		}

		if err := validateNoUnsafeChars("rpm-repo-set-template", "subpath", sub.Name, sub.Subpath); err != nil {
			return fmt.Errorf("rpm-repo-set-template %#q:\n%w", name, err)
		}
	}

	return nil
}

// EffectiveRpmBuildRepos returns the deduplicated, ordered list of effective
// repo names exposed to the rpm-build use-case for this distro version. The
// `inputs.rpm-build` list is processed entry-by-entry in declaration order:
// `repo` entries contribute their name, `set` entries expand into the names of
// every sub-repo the set instantiates. Duplicates across the resulting list
// (whether two direct repos with the same name or a direct repo named the same
// as a set's expansion) are reported as errors so that consumers do not have
// to dedupe.
//
// This method does NOT verify that direct `repo = "..."` entries reference an
// existing [RpmRepoResource]; doing so requires a fully-built effective repo
// map (the union of `[resources.rpm-repos]` and every set's expansion), which
// is constructed once at the project level. Cross-reference validation against
// [ResourcesConfig.EffectiveRpmRepos] happens in [ProjectConfig.Validate] via
// validateDistroVersionInputs.
func (v DistroVersionDefinition) EffectiveRpmBuildRepos(resources *ResourcesConfig) ([]string, error) {
	return effectiveInputRepos("rpm-build", v.Inputs.RpmBuild, resources)
}

// EffectiveImageBuildRepos returns the deduplicated, ordered list of effective
// repo names exposed to the image-build use-case for this distro version. Same
// semantics as [DistroVersionDefinition.EffectiveRpmBuildRepos].
func (v DistroVersionDefinition) EffectiveImageBuildRepos(resources *ResourcesConfig) ([]string, error) {
	return effectiveInputRepos("image-build", v.Inputs.ImageBuild, resources)
}

func effectiveInputRepos(
	useCase string, inputs []DistroVersionInput, resources *ResourcesConfig,
) ([]string, error) {
	out := make([]string, 0, len(inputs))
	seen := make(map[string]bool, cap(out))

	emit := func(repoName, source string) error {
		if seen[repoName] {
			return fmt.Errorf(
				"`inputs.%s` produces repo %#q more than once (most recently from %s); "+
					"remove the duplicate to disambiguate",
				useCase, repoName, source,
			)
		}

		seen[repoName] = true
		out = append(out, repoName)

		return nil
	}

	for idx, entry := range inputs {
		// Report indices as 1-based in error messages so they line up with how
		// users count TOML list entries.
		entryNum := idx + 1

		if err := validateDistroVersionInput(useCase, entryNum, &entry); err != nil {
			return nil, err
		}

		switch {
		case entry.Repo != "":
			if err := emit(entry.Repo, fmt.Sprintf("repo %#q", entry.Repo)); err != nil {
				return nil, err
			}

		case entry.Set != "":
			if resources == nil {
				return nil, fmt.Errorf(
					"`inputs.%s` entry %d references rpm-repo-set %#q but no [resources] section is defined",
					useCase, entryNum, entry.Set,
				)
			}

			set, ok := resources.RpmRepoSets[entry.Set]
			if !ok {
				return nil, fmt.Errorf(
					"`inputs.%s` entry %d references undefined rpm-repo-set %#q", useCase, entryNum, entry.Set,
				)
			}

			expanded, err := expandRpmRepoSet(entry.Set, &set, resources.RpmRepoSetTemplates)
			if err != nil {
				return nil, err
			}

			for _, exp := range expanded {
				if err := emit(exp.Name, fmt.Sprintf("set %#q", entry.Set)); err != nil {
					return nil, err
				}
			}
		}
	}

	return out, nil
}

// validateDistroVersionInput rejects entries that set neither or both of `repo`
// and `set`. The TOML schema cannot express XOR cleanly, so we enforce it here.
// `entryNum` is reported verbatim in error messages and should be 1-based.
func validateDistroVersionInput(useCase string, entryNum int, entry *DistroVersionInput) error {
	hasRepo := entry.Repo != ""
	hasSet := entry.Set != ""

	switch {
	case !hasRepo && !hasSet:
		return fmt.Errorf(
			"`inputs.%s` entry %d sets neither `repo` nor `set`; exactly one is required",
			useCase, entryNum,
		)
	case hasRepo && hasSet:
		return fmt.Errorf(
			"`inputs.%s` entry %d sets both `repo` (%#q) and `set` (%#q); exactly one is required",
			useCase, entryNum, entry.Repo, entry.Set,
		)
	}

	return nil
}
