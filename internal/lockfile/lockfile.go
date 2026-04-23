// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package lockfile reads and writes per-component lock files under the locks/
// directory. Each component gets its own <name>.lock TOML file that pins
// resolved upstream commits and tracks component identity for deterministic
// builds. Lock files are managed by [azldev component update].
package lockfile

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
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

// LockPath returns the path to a component's lock file given the lock
// directory and component name.
func LockPath(lockDir, componentName string) (string, error) {
	if err := fileutils.ValidateFilename(componentName); err != nil {
		return "", fmt.Errorf("validating component name %#q for lock file path:\n%w", componentName, err)
	}

	return filepath.Join(lockDir, componentName+lockFileExtension), nil
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

// ValidateUpstreamCommit checks that a lock file is consistent with the
// component's config. Returns the locked commit and an error if:
//   - The lock file does not exist or cannot be loaded
//   - The component has an explicit 'upstream-commit' in config that doesn't match
//     the lock file (lock is stale, run 'component update')
func ValidateUpstreamCommit(
	fs opctx.FS, lockDir, componentName, configUpstreamCommit string,
) (string, error) {
	lockPath, pathErr := LockPath(lockDir, componentName)
	if pathErr != nil {
		return "", fmt.Errorf("getting lock path for %#q:\n%w", componentName, pathErr)
	}

	exists, err := Exists(fs, lockPath)
	if err != nil {
		return "", fmt.Errorf("checking lock for %#q:\n%w", componentName, err)
	}

	if !exists {
		return "", fmt.Errorf(
			"no lock file for upstream component %#q; run 'azldev component update %s' to create one",
			componentName, componentName)
	}

	lock, err := Load(fs, lockPath)
	if err != nil {
		return "", fmt.Errorf("loading lock for %#q:\n%w", componentName, err)
	}

	if lock.UpstreamCommit == "" {
		return "", fmt.Errorf(
			"lock file for %#q has no upstream-commit; run 'azldev component update %s' to populate",
			componentName, componentName)
	}

	if configUpstreamCommit != "" && lock.UpstreamCommit != configUpstreamCommit {
		return "", fmt.Errorf(
			"lock is stale for %#q: config pins %#q but lock has %#q",
			componentName, configUpstreamCommit, lock.UpstreamCommit)
	}

	return lock.UpstreamCommit, nil
}

// validateConsistency checks lock files against resolved component configs.
// For each upstream component, verifies a lock file exists and any explicit
// upstream-commit pin matches. When checkOrphans is true, also detects orphan
// lock files (components removed from config). Orphan detection should only
// be used when validating the full project — on filtered commands it would
// misfire against the subset.
//
// Returns sorted lists of components with missing/stale locks and orphan
// component names. Returns an error if any issues are found.
func validateConsistency(
	fs opctx.FS,
	lockDir string,
	components map[string]projectconfig.ComponentConfig,
	checkOrphans bool,
) (missingOrStale, orphans []string, err error) {
	// Check each component that may need a lock file. Only skip components
	// that are explicitly local — unspecified source type inherits upstream
	// from distro defaults, so those need validation too.
	for name, comp := range components {
		if comp.Spec.SourceType == projectconfig.SpecSourceTypeLocal {
			continue
		}

		if _, validateErr := ValidateUpstreamCommit(
			fs, lockDir, name, comp.Spec.UpstreamCommit,
		); validateErr != nil {
			slog.Warn("Lock validation failed", "component", name, "error", validateErr)

			missingOrStale = append(missingOrStale, name)
		}
	}

	if checkOrphans {
		var orphanErr error

		orphans, orphanErr = FindOrphanLockFiles(fs, lockDir, components)
		if orphanErr != nil {
			return missingOrStale, nil, fmt.Errorf("checking for orphan lock files:\n%w", orphanErr)
		}
	}

	if len(missingOrStale) > 0 || len(orphans) > 0 {
		sort.Strings(missingOrStale)
		sort.Strings(orphans)

		return missingOrStale, orphans, fmt.Errorf(
			"lock file consistency check failed: %d missing/stale, %d orphans",
			len(missingOrStale), len(orphans))
	}

	return nil, nil, nil
}

// FindOrphanLockFiles returns component names that have lock files but no
// corresponding component in config (i.e., the component was removed).
// Returns an error if the locks directory exists but cannot be read.
func FindOrphanLockFiles(
	fs opctx.FS,
	lockDir string,
	components map[string]projectconfig.ComponentConfig,
) ([]string, error) {
	entries, readErr := fileutils.ReadDir(fs, lockDir)
	if readErr != nil {
		// No locks directory is fine — nothing to detect.
		exists, existsErr := fileutils.DirExists(fs, lockDir)
		if existsErr != nil {
			return nil, fmt.Errorf("checking locks directory %#q:\n%w", lockDir, existsErr)
		}

		if !exists {
			return nil, nil
		}

		return nil, fmt.Errorf("reading locks directory %#q:\n%w", lockDir, readErr)
	}

	var orphans []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		entryName := entry.Name()
		if !strings.HasSuffix(entryName, lockFileExtension) || strings.HasPrefix(entryName, ".") {
			continue
		}

		componentName := strings.TrimSuffix(entryName, lockFileExtension)

		if _, exists := components[componentName]; !exists {
			orphans = append(orphans, componentName)
		}
	}

	sort.Strings(orphans)

	return orphans, nil
}
