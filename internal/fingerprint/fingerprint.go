// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/mitchellh/hashstructure/v2"
)

// hashstructureTagName is the struct tag name used by hashstructure to determine
// field inclusion. Fields tagged with `fingerprint:"-"` are excluded.
const hashstructureTagName = "fingerprint"

// ComponentIdentity holds the computed fingerprint for a single component plus
// a breakdown of individual input hashes for debugging.
type ComponentIdentity struct {
	// Fingerprint is the overall SHA256 hash combining all inputs.
	Fingerprint string `json:"fingerprint"`
	// Inputs provides the individual input hashes that were combined.
	Inputs ComponentInputs `json:"inputs"`
}

// ComponentInputs contains the individual input hashes that comprise a component's
// fingerprint.
type ComponentInputs struct {
	// ConfigHash is the hash of the resolved component config fields (uint64 from hashstructure).
	ConfigHash uint64 `json:"configHash"`
	// SourceIdentity is the opaque identity string for the component's source.
	// For local specs this is a content hash; for upstream specs this is a commit hash.
	SourceIdentity string `json:"sourceIdentity,omitempty"`
	// OverlayFileHashes maps overlay index (as string) to a combined hash of the
	// source file's basename and content. Keyed by index rather than path to avoid
	// checkout-location dependence.
	OverlayFileHashes map[string]string `json:"overlayFileHashes,omitempty"`
	// ManualBump is the manual rebuild counter from the lock file. Almost always 0;
	// used for mass-rebuild scenarios.
	ManualBump int `json:"manualBump"`
	// ReleaseVer is the distro's formal releasever (e.g., "4.0"), which feeds into
	// RPM macros like %{dist}. Different release versions produce different package
	// NEVRAs even with identical specs.
	ReleaseVer string `json:"releaseVer"`
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
	inputs := ComponentInputs{
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

	// 3. Hash the resolved config struct (excluding fingerprint:"-" fields).
	configHash, err := hashstructure.Hash(component, hashstructure.FormatV2, &hashstructure.HashOptions{
		TagName: hashstructureTagName,
	})
	if err != nil {
		return nil, fmt.Errorf("hashing component config:\n%w", err)
	}

	inputs.ConfigHash = configHash

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

	// 5. Combine all inputs into the overall fingerprint.
	return &ComponentIdentity{
		Fingerprint: combineInputs(inputs),
		Inputs:      inputs,
	}, nil
}

// combineInputs deterministically combines all input hashes into a single SHA256 fingerprint.
func combineInputs(inputs ComponentInputs) string {
	hasher := sha256.New()

	// Write each input in a fixed order with field labels for domain separation.
	writeField(hasher, "config_hash", strconv.FormatUint(inputs.ConfigHash, 10))
	writeField(hasher, "source_identity", inputs.SourceIdentity)
	writeField(hasher, "manual_bump", strconv.Itoa(inputs.ManualBump))
	writeField(hasher, "release_ver", inputs.ReleaseVer)

	// Overlay file hashes in sorted key order for determinism.
	if len(inputs.OverlayFileHashes) > 0 {
		keys := make([]string, 0, len(inputs.OverlayFileHashes))
		for key := range inputs.OverlayFileHashes {
			keys = append(keys, key)
		}

		sort.Strings(keys)

		for _, key := range keys {
			writeField(hasher, "overlay:"+key, inputs.OverlayFileHashes[key])
		}
	}

	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
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

	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}
