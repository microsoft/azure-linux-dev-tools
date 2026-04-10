// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package lockfile reads and writes azldev.lock files, which pin resolved
// upstream commit hashes for deterministic builds. The lock file is a TOML
// file at the project root, managed by [azldev component update].
package lockfile

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	toml "github.com/pelletier/go-toml/v2"
)

// FileName is the lock file name, placed at the project root.
const FileName = "azldev.lock"

// currentVersion is the lock file format version.
const currentVersion = 1

// LockFile holds the parsed contents of an azldev.lock file.
type LockFile struct {
	// Version is the lock file format version.
	Version int `toml:"version" comment:"azldev.lock - Managed by azldev component update. Do not edit manually."`
	// Components maps component name → locked state.
	Components map[string]ComponentLock `toml:"components"`
}

// ComponentLock holds the locked state for a single component.
// Upstream components have [ComponentLock.UpstreamCommit] set to the resolved
// commit hash. Local components have an entry but with an empty commit field.
type ComponentLock struct {
	// UpstreamCommit is the resolved full commit hash from the upstream dist-git.
	// Empty for local components.
	UpstreamCommit string `toml:"upstream-commit,omitempty"`
}

// New creates an empty lock file with the current format version.
func New() *LockFile {
	return &LockFile{
		Version:    currentVersion,
		Components: make(map[string]ComponentLock),
	}
}

// Load reads and parses a lock file from the given path. Returns an error if the
// file cannot be read or parsed, or if the format version is unsupported.
func Load(fs opctx.FS, path string) (*LockFile, error) {
	data, err := fileutils.ReadFile(fs, path)
	if err != nil {
		return nil, fmt.Errorf("reading lock file %#q:\n%w", path, err)
	}

	var lockFile LockFile
	if err := toml.Unmarshal(data, &lockFile); err != nil {
		return nil, fmt.Errorf("parsing lock file %#q:\n%w", path, err)
	}

	if lockFile.Version != currentVersion {
		return nil, fmt.Errorf(
			// Backwards compatibility is a future consideration if we need to make non-compatible changes.
			// For now, we can just error on unsupported versions.
			"unsupported lock file version %d in %#q (expected %d)",
			lockFile.Version, path, currentVersion)
	}

	if lockFile.Components == nil {
		lockFile.Components = make(map[string]ComponentLock)
	}

	return &lockFile, nil
}

// Save writes the lock file to the given path. [toml.Marshal] sorts map keys
// alphabetically, producing deterministic output.
func (lockFile *LockFile) Save(fs opctx.FS, path string) error {
	data, err := toml.Marshal(lockFile)
	if err != nil {
		return fmt.Errorf("marshaling lock file:\n%w", err)
	}

	if err := fileutils.WriteFile(fs, path, data, fileperms.PublicFile); err != nil {
		return fmt.Errorf("writing lock file %#q:\n%w", path, err)
	}

	return nil
}

// SetUpstreamCommit sets the locked upstream commit for a component.
func (lockFile *LockFile) SetUpstreamCommit(componentName, commitHash string) {
	if lockFile.Components == nil {
		lockFile.Components = make(map[string]ComponentLock)
	}

	entry := lockFile.Components[componentName]
	entry.UpstreamCommit = commitHash
	lockFile.Components[componentName] = entry
}

// GetUpstreamCommit returns the locked upstream commit for a component.
// Returns empty string and false if the component has no lock entry or
// if the entry has an empty upstream commit.
func (lockFile *LockFile) GetUpstreamCommit(componentName string) (string, bool) {
	entry, ok := lockFile.Components[componentName]
	if !ok || entry.UpstreamCommit == "" {
		return "", false
	}

	return entry.UpstreamCommit, true
}
