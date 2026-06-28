// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint

import (
	"crypto/sha256"
	"fmt"
	"io"
	"strconv"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// fingerprintTagName is the struct tag name that marks field inclusion. Fields
// tagged with `fingerprint:"-"` are excluded from the projection.
const fingerprintTagName = "fingerprint"

// ComponentIdentity holds the computed fingerprint for a single component.
type ComponentIdentity struct {
	// Fingerprint is the atomic "v<N>:sha256:..." content token combining the
	// canonical config projection with the non-config inputs.
	Fingerprint string `json:"fingerprint"`
}

// componentInputs holds the non-config inputs that ComputeIdentity folds into the
// fingerprint document alongside the canonical config projection. It is never
// serialized (the document is assembled as an explicit map), so its fields carry
// no json tags.
type componentInputs struct {
	// SourceIdentity is the opaque identity string for the component's source.
	// For local specs this is a content hash; for upstream specs this is a commit hash.
	SourceIdentity string
	// OverlayFileHashes maps overlay index (as string) to a combined hash of the
	// source file's basename and content. Keyed by index rather than path to avoid
	// checkout-location dependence.
	OverlayFileHashes map[string]string
	// ManualBump is the manual rebuild counter from the lock file. Almost always 0;
	// used for mass-rebuild scenarios.
	ManualBump int
	// ReleaseVer is the distro's formal releasever (e.g., "4.0"), which feeds into
	// RPM macros like %{dist}. Different release versions produce different package
	// NEVRAs even with identical specs.
	ReleaseVer string
}

// IdentityOptions holds additional inputs for computing a component's identity
// that are not part of the component config itself.
type IdentityOptions struct {
	// ManualBump is the manual rebuild counter from the component's lock file.
	ManualBump int
	// SourceIdentity is the opaque identity string from a [sourceproviders.SourceIdentityProvider].
	// For upstream components this is the resolved commit hash; for local components this is a
	// content hash of the spec directory.
	//
	// This is caller-provided because resolving it requires network access (upstream clone) or
	// filesystem traversal (local content hash). [ComputeIdentity] is a pure combiner — it does
	// not perform I/O beyond reading overlay files. Callers should resolve source identity via
	// SourceManager.ResolveSourceIdentity before calling [ComputeIdentity].
	SourceIdentity string
}

// ComputeIdentity computes the fingerprint for a component from its resolved config
// and additional context. The fs parameter is used to read overlay source file
// contents for hashing; spec content identity is provided via [IdentityOptions.SourceIdentity].
//
// This function is a deterministic combiner: given the same resolved inputs it always
// produces the same fingerprint. It does not resolve source identity or count commits —
// those are expected to be pre-resolved by the caller and passed via opts.
func ComputeIdentity(
	fs opctx.FS,
	component projectconfig.ComponentConfig,
	releaseVer string,
	opts IdentityOptions,
) (*ComponentIdentity, error) {
	inputs := componentInputs{
		ManualBump:     opts.ManualBump,
		SourceIdentity: opts.SourceIdentity,
		ReleaseVer:     releaseVer,
	}

	// 1. Require source identity when the component has a spec source that
	//    contributes content. Without it the fingerprint cannot detect spec
	//    content changes (Spec.Path is excluded from the config hash).
	if opts.SourceIdentity == "" && component.Spec.SourceType != "" {
		return nil, fmt.Errorf(
			"source identity is required for component with source type %#q; "+
				"resolve it via SourceManager.ResolveSourceIdentity before calling ComputeIdentity",
			component.Spec.SourceType)
	}

	// 2. Verify all source files have a hash. Without a hash the fingerprint
	//    cannot detect content changes, so we refuse to compute one.
	for i := range component.SourceFiles {
		if component.SourceFiles[i].Hash == "" {
			return nil, fmt.Errorf(
				"source file %#q has no hash; cannot compute a deterministic fingerprint",
				component.SourceFiles[i].Filename,
			)
		}
	}

	// 3. Project the resolved config to a canonical JSON tree (excluding
	//    fingerprint:"-" fields). The omit predicate treats a nil-or-empty scalar
	//    slice as a zero value inside the hash boundary, so nil-vs-empty differences
	//    collapse regardless of how the caller obtained the config.
	projection, err := projectV1(component)
	if err != nil {
		return nil, fmt.Errorf("projecting component config:\n%w", err)
	}

	if projection == nil {
		projection = map[string]any{}
	}

	// 4. Hash overlay source file contents. Each overlay owns its identity
	//    computation via [projectconfig.ComponentOverlay.SourceContentIdentity].
	overlayHashes := make(map[string]string)

	for idx := range component.Overlays {
		identity, overlayErr := component.Overlays[idx].SourceContentIdentity(fs)
		if overlayErr != nil {
			return nil, fmt.Errorf("hashing overlay %d source:\n%w", idx, overlayErr)
		}

		if identity != "" {
			overlayHashes[strconv.Itoa(idx)] = identity
		}
	}

	inputs.OverlayFileHashes = overlayHashes

	// 5. Assemble the single canonical document - the config projection plus the
	//    non-config inputs, each under its own object key for domain separation -
	//    canonicalize it with RFC 8785, sha256 it, and stamp the atomic
	//    "v<version>:sha256:..." content token. Version and digest are written
	//    together as one string so they can never desync (RFC D3).
	document := map[string]any{
		"config":         projection,
		"sourceIdentity": inputs.SourceIdentity,
		"manualBump":     inputs.ManualBump,
		"releaseVer":     inputs.ReleaseVer,
	}

	if len(inputs.OverlayFileHashes) > 0 {
		document["overlays"] = inputs.OverlayFileHashes
	}

	digest, err := canonicalDigest(document)
	if err != nil {
		return nil, fmt.Errorf("computing fingerprint digest:\n%w", err)
	}

	return &ComponentIdentity{
		Fingerprint: fmt.Sprintf("v%d:%s", currentContentVersion, digest),
	}, nil
}

// writeField writes a labeled value to the hasher for domain separation.
func writeField(writer io.Writer, label string, value string) {
	// Length-prefix both label and value to prevent injection of fake field records
	// via values containing newlines.
	fmt.Fprintf(writer, "%d:%s=%d:%s\n", len(label), label, len(value), value)
}

// UpstreamCommitResolutionInputs holds the effective inputs that determine which upstream
// commit gets resolved. These must be the *resolved* values after inheritance
// and fallback — not the raw component spec fields.
type UpstreamCommitResolutionInputs struct {
	// Snapshot is the resolved snapshot timestamp.
	Snapshot string
	// DistroName is the resolved distro name.
	DistroName string
	// DistroVersion is the resolved distro version.
	DistroVersion string
	// DistGitBranch is the resolved dist-git branch (e.g., "f43").
	DistGitBranch string
	// DistGitBaseURI is the resolved dist-git base URI template.
	DistGitBaseURI string
	// UpstreamCommitPin is an explicit commit hash pin (overrides snapshot).
	UpstreamCommitPin string
	// UpstreamName is the upstream package name (if different from component).
	UpstreamName string
}

// ComputeResolutionHash produces a deterministic hash of the effective inputs
// that affect upstream commit resolution. When this hash matches the stored
// value in a lock file, re-resolving the upstream commit can be skipped — the
// resolution inputs haven't changed so the same commit would be produced.
//
// This prevents two classes of problems:
//   - Unnecessary re-resolution when only build inputs changed (e.g., overlay edit)
//   - Snapshot instability where upstream branches receive new commits (mass
//     rebuilds, cherry-picks) that change what 'git rev-list --before' resolves
//     to, even though the snapshot timestamp itself is unchanged
//
// Callers must resolve distro inheritance/fallbacks before calling this — the
// hash must reflect the *actual* values used during resolution, not the raw
// per-component config which may be empty when defaults apply.
func ComputeResolutionHash(inputs UpstreamCommitResolutionInputs) string {
	hasher := sha256.New()

	writeField(hasher, "snapshot", inputs.Snapshot)
	writeField(hasher, "distro_name", inputs.DistroName)
	writeField(hasher, "distro_version", inputs.DistroVersion)
	writeField(hasher, "dist_git_branch", inputs.DistGitBranch)
	writeField(hasher, "dist_git_base_uri", inputs.DistGitBaseURI)
	writeField(hasher, "upstream_commit_pin", inputs.UpstreamCommitPin)
	writeField(hasher, "upstream_name", inputs.UpstreamName)

	return sha256Hex(hasher.Sum(nil))
}
