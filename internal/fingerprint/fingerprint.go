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
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
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
	// OverlayFileHashes maps overlay source file paths to their SHA256 hashes.
	OverlayFileHashes map[string]string `json:"overlayFileHashes,omitempty"`
	// AffectsCommitCount is the number of "Affects: <component>" commits in the project repo.
	AffectsCommitCount int `json:"affectsCommitCount"`
	// Distro is the effective distro name.
	Distro string `json:"distro"`
	// DistroVersion is the effective distro version.
	DistroVersion string `json:"distroVersion"`
}

// IdentityOptions holds additional inputs for computing a component's identity
// that are not part of the component config itself.
type IdentityOptions struct {
	// AffectsCommitCount is the number of "Affects: <component>" commits.
	AffectsCommitCount int
	// SourceIdentity is the opaque identity string from a [sourceproviders.SourceIdentityProvider].
	SourceIdentity string
}

// ComputeIdentity computes the fingerprint for a component from its resolved config
// and additional context. The fs parameter is used to read spec file and overlay
// source file contents for hashing.
func ComputeIdentity(
	fs opctx.FS,
	component projectconfig.ComponentConfig,
	distroRef projectconfig.DistroReference,
	opts IdentityOptions,
) (*ComponentIdentity, error) {
	inputs := ComponentInputs{
		AffectsCommitCount: opts.AffectsCommitCount,
		SourceIdentity:     opts.SourceIdentity,
		Distro:             distroRef.Name,
		DistroVersion:      distroRef.Version,
	}

	// 1. Hash the resolved config struct (excluding fingerprint:"-" fields).
	configHash, err := hashstructure.Hash(component, hashstructure.FormatV2, &hashstructure.HashOptions{
		TagName: hashstructureTagName,
	})
	if err != nil {
		return nil, fmt.Errorf("hashing component config:\n%w", err)
	}

	inputs.ConfigHash = configHash

	// 2. Hash overlay source file contents.
	overlayHashes, err := hashOverlayFiles(fs, component.Overlays)
	if err != nil {
		return nil, fmt.Errorf("hashing overlay files:\n%w", err)
	}

	inputs.OverlayFileHashes = overlayHashes

	// 3. Combine all inputs into the overall fingerprint.
	return &ComponentIdentity{
		Fingerprint: combineInputs(inputs),
		Inputs:      inputs,
	}, nil
}

// hashOverlayFiles computes SHA256 hashes for all overlay source files that reference
// local files. Returns a map of source path to hex hash, or an empty map if no overlay
// source files exist.
func hashOverlayFiles(
	fs opctx.FS,
	overlays []projectconfig.ComponentOverlay,
) (map[string]string, error) {
	hashes := make(map[string]string)

	for _, overlay := range overlays {
		if overlay.Source == "" {
			continue
		}

		fileHash, err := fileutils.ComputeFileHash(fs, fileutils.HashTypeSHA256, overlay.Source)
		if err != nil {
			return nil, fmt.Errorf("hashing overlay source %#q:\n%w", overlay.Source, err)
		}

		hashes[overlay.Source] = fileHash
	}

	return hashes, nil
}

// combineInputs deterministically combines all input hashes into a single SHA256 fingerprint.
func combineInputs(inputs ComponentInputs) string {
	hasher := sha256.New()

	// Write each input in a fixed order with field labels for domain separation.
	writeField(hasher, "config_hash", strconv.FormatUint(inputs.ConfigHash, 10))
	writeField(hasher, "source_identity", inputs.SourceIdentity)
	writeField(hasher, "affects_commit_count", strconv.Itoa(inputs.AffectsCommitCount))
	writeField(hasher, "distro", inputs.Distro)
	writeField(hasher, "distro_version", inputs.DistroVersion)

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
	// Use label=value\n format. Length-prefixing the label prevents
	// collisions between field names that are prefixes of each other.
	fmt.Fprintf(writer, "%d:%s=%s\n", len(label), label, value)
}
