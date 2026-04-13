// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package lockfile reads and writes per-component lock files under the locks/
// directory. Each component gets its own <name>.lock TOML file that pins
// resolved upstream commits and tracks component identity for deterministic
// builds. Lock files are managed by [azldev component update] and
// [azldev component bump].
package lockfile

import (
	"fmt"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	toml "github.com/pelletier/go-toml/v2"
)

// LockDir is the directory under the project root where per-component lock
// files are stored.
const LockDir = "locks"

// lockFileExtension is the file extension for lock files.
const lockFileExtension = ".lock"

// currentVersion is the lock file format version.
const currentVersion = 1

// ComponentLock holds the locked state for a single component.
type ComponentLock struct {
	// Version is the lock file format version.
	Version int `toml:"version" comment:"Managed by azldev component update. Do not edit manually."`

	// ImportCommit is the upstream commit hash at the time of initial import
	// (fork point). Upstream changelog up to this commit is inherited verbatim.
	// Write-once: set on first import, never changed afterwards.
	// Empty for local components.
	ImportCommit string `toml:"import-commit,omitempty"`

	// UpstreamCommit is the current resolved upstream commit hash.
	// Updated by 'component update' when upstream sources are re-resolved.
	// Empty for local components.
	UpstreamCommit string `toml:"upstream-commit,omitempty"`

	// ManualBump is an extra rebuild counter for mass-rebuild scenarios.
	// Almost always 0. Incrementing this changes the component's fingerprint,
	// triggering a new release without any other input change.
	ManualBump int `toml:"manual-bump,omitempty"`

	// InputFingerprint is the hash of all render inputs (config, overlays,
	// upstream-commit, manual-bump, distro release version). Recomputed on
	// every update. Used to detect when inputs have changed.
	InputFingerprint string `toml:"input-fingerprint,omitempty"`

	// ResolutionInputHash is a hash of the config inputs that affect upstream
	// commit resolution (snapshot timestamp, distro reference, explicit pin).
	// Used for offline staleness detection: if the current config's resolution
	// inputs produce a different hash than what's stored, the lock may be stale
	// and `component update` will need to re-resolve the upstream commit via
	// network.
	//
	// This enables a fast offline check without re-resolving upstream commits:
	//   - Hash matches → resolution inputs unchanged, lock is probably fresh
	//   - Hash differs → resolution inputs changed, run update to re-resolve
	//
	// Not yet populated in v1 — reserved for future use.
	ResolutionInputHash string `toml:"resolution-input-hash,omitempty"`
}

// New creates a new empty component lock with the current format version.
func New() *ComponentLock {
	return &ComponentLock{
		Version: currentVersion,
	}
}

// LockPath returns the path to a component's lock file given the project root
// and component name.
func LockPath(projectDir, componentName string) string {
	return filepath.Join(projectDir, LockDir, componentName+lockFileExtension)
}

// Load reads and parses a per-component lock file from the given path.
// Returns an error if the file cannot be read or parsed, or if the format
// version is unsupported.
func Load(fs opctx.FS, path string) (*ComponentLock, error) {
	data, err := fileutils.ReadFile(fs, path)
	if err != nil {
		return nil, fmt.Errorf("reading lock file %#q:\n%w", path, err)
	}

	var lock ComponentLock
	if err := toml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parsing lock file %#q:\n%w", path, err)
	}

	if lock.Version != currentVersion {
		return nil, fmt.Errorf(
			"unsupported lock file version %d in %#q (expected %d)",
			lock.Version, path, currentVersion)
	}

	return &lock, nil
}

// Save writes the component lock file to the given path, creating parent
// directories as needed.
func (lock *ComponentLock) Save(fs opctx.FS, path string) error {
	dir := filepath.Dir(path)
	if err := fileutils.MkdirAll(fs, dir); err != nil {
		return fmt.Errorf("creating lock directory %#q:\n%w", dir, err)
	}

	data, err := toml.Marshal(lock)
	if err != nil {
		return fmt.Errorf("marshaling lock file:\n%w", err)
	}

	if err := fileutils.WriteFile(fs, path, data, fileperms.PublicFile); err != nil {
		return fmt.Errorf("writing lock file %#q:\n%w", path, err)
	}

	return nil
}

// Exists checks whether a lock file exists at the given path.
func Exists(fs opctx.FS, path string) (bool, error) {
	exists, err := fileutils.Exists(fs, path)
	if err != nil {
		return false, fmt.Errorf("checking lock file %#q:\n%w", path, err)
	}

	return exists, nil
}

// Remove deletes a lock file at the given path.
func Remove(fs opctx.FS, path string) error {
	if err := fs.Remove(path); err != nil {
		return fmt.Errorf("removing lock file %#q:\n%w", path, err)
	}

	return nil
}
