// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
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
type ResourcesConfig struct {
	// RpmRepos is the set of reusable RPM repository definitions, keyed by name.
	RpmRepos map[string]RpmRepoResource `toml:"rpm-repos,omitempty" json:"rpmRepos,omitempty" jsonschema:"title=RPM repositories,description=Reusable named RPM repository definitions"`
}

// IsEmpty reports whether the ResourcesConfig contains no entries.
func (r *ResourcesConfig) IsEmpty() bool {
	return r == nil || len(r.RpmRepos) == 0
}

// JSONSchemaExtend tightens the generated schema for the rpm-repos map so editors
// can flag invalid repo names at edit time. The runtime validator
// ([validateRpmRepoName]) is the source of truth; this keeps the schema in sync.
func (ResourcesConfig) JSONSchemaExtend(s *jsonschema.Schema) {
	if s.Properties == nil {
		return
	}

	repos, ok := s.Properties.Get("rpm-repos")
	if !ok || repos == nil {
		return
	}

	repos.PropertyNames = &jsonschema.Schema{
		Type:        "string",
		Pattern:     rpmRepoNameRE.String(),
		Description: "Repo name; projected verbatim into dnf section headers and kiwi --add-repo arguments.",
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
	GPGKey string `toml:"gpg-key,omitempty" json:"gpgKey,omitempty" jsonschema:"pattern=^\\S+$,title=GPG key,description=Path or URI to the GPG key file. Bare paths are resolved relative to the defining TOML file. Accepted URI schemes: http, https, file."`

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

	if err := validateNoUnsafeChars("description", name, repo.Description); err != nil {
		return err
	}

	switch repo.EffectiveType() {
	case RpmRepoTypeRpmMd:
		if repo.BaseURI == "" && repo.Metalink == "" {
			return fmt.Errorf("rpm-repo %#q must specify exactly one of `base-uri` or `metalink`", name)
		}

		if repo.BaseURI != "" && repo.Metalink != "" {
			return fmt.Errorf("rpm-repo %#q must not specify both `base-uri` and `metalink`", name)
		}
	}

	if repo.BaseURI != "" {
		if err := validateRemoteURI("base-uri", name, repo.BaseURI); err != nil {
			return err
		}
	}

	if repo.Metalink != "" {
		if err := validateRemoteURI("metalink", name, repo.Metalink); err != nil {
			return err
		}
	}

	if !repo.DisableGPGCheck && repo.GPGKey == "" {
		return fmt.Errorf(
			"rpm-repo %#q has GPG checking enabled (the default) but no `gpg-key`; "+
				"either set `gpg-key = \"...\"` or opt out with `disable-gpg-check = true`",
			name,
		)
	}

	if repo.GPGKey != "" {
		if err := validateGPGKey(name, repo.GPGKey); err != nil {
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
func validateNoUnsafeChars(field, repoName, value string) error {
	for i, r := range value {
		switch {
		case r == '\r' || r == '\n':
			return fmt.Errorf("rpm-repo %#q `%s` must be a single line (no embedded CR/LF at byte %d)", repoName, field, i)
		case r == 0:
			return fmt.Errorf("rpm-repo %#q `%s` must not contain NUL bytes (at byte %d)", repoName, field, i)
		case r == '\u2028' || r == '\u2029':
			return fmt.Errorf("rpm-repo %#q `%s` must not contain Unicode line separators (at byte %d)", repoName, field, i)
		}
	}

	return nil
}

// validateRemoteURI ensures a base-uri/metalink value is a syntactically valid URI with
// an http or https scheme. Local schemes (file://) are deliberately disallowed for the
// repo source: kiwi.AddRemoteRepo only handles remote URIs, and supporting file:// here
// would require split-by-consumer staging that we don't do today.
func validateRemoteURI(field, repoName, raw string) error {
	if err := validateNoUnsafeChars(field, repoName, raw); err != nil {
		return err
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("rpm-repo %#q `%s` is not a valid URI: %w", repoName, field, err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return nil
	case "":
		return fmt.Errorf("rpm-repo %#q `%s` is missing a scheme; expected http(s)://...", repoName, field)
	default:
		return fmt.Errorf("rpm-repo %#q `%s` uses unsupported scheme %q; only http and https are accepted", repoName, field, u.Scheme)
	}
}

// validateGPGKey checks the in-isolation form of a `gpg-key` value. A bare path is
// allowed at this stage (resolved to an absolute file:// URI by [WithAbsolutePaths]);
// rejection of bare paths for rpm-build consumers happens in
// [validateDistroVersionInputs], not here.
func validateGPGKey(repoName, raw string) error {
	if err := validateNoUnsafeChars("gpg-key", repoName, raw); err != nil {
		return err
	}

	if !hasURIScheme(raw) {
		// Bare path; will be resolved relative to the defining TOML directory.
		return nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("rpm-repo %#q `gpg-key` is not a valid URI: %w", repoName, err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https", "file":
		return nil
	default:
		return fmt.Errorf("rpm-repo %#q `gpg-key` uses unsupported scheme %q; expected http, https, or file", repoName, u.Scheme)
	}
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

	u, err := url.Parse(r.GPGKey)
	if err != nil {
		return false
	}

	return strings.EqualFold(u.Scheme, "file")
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

	return (&url.URL{Scheme: "file", Path: abs}).String()
}

// hasURIScheme reports whether s starts with "<alpha>[<alphanumeric>+-.]*:".
// Cheap and dependency-free; we don't need full RFC 3986 here.
func hasURIScheme(s string) bool {
	for i, r := range s {
		if i == 0 {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				return false
			}

			continue
		}

		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '+', r == '-', r == '.':
			continue
		case r == ':':
			return true
		default:
			return false
		}
	}

	return false
}
