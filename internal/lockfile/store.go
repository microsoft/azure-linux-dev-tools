// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lockfile

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// LockReader provides read-only access to per-component lock files. Use this
// interface for commands that consume lock state but should not modify it
// (e.g., render, build, validation).
type LockReader interface {
	// Get returns the lock for a component. Returns an error if the lock file
	// does not exist or cannot be parsed.
	Get(componentName string) (*ComponentLock, error)
	// Exists checks whether a lock file exists for the given component.
	Exists(componentName string) (bool, error)
	// ValidateConsistency checks lock files against the resolved component
	// configs. Returns sorted lists of components with missing/stale locks
	// and orphan component names.
	ValidateConsistency(
		components map[string]projectconfig.ComponentConfig,
		checkOrphans bool,
	) (missingOrStale, orphans []string, err error)
}

// LockWriter extends [LockReader] with write operations. Use this interface
// for commands that create or update lock files (e.g., component update).
type LockWriter interface {
	LockReader
	// GetOrNew returns the lock for a component, creating a new empty lock if
	// none exists on disk. Returns an error if the lock file exists but is
	// corrupt/unreadable.
	GetOrNew(componentName string) (*ComponentLock, error)
	// Save writes the lock for a component to disk.
	Save(componentName string, lock *ComponentLock) error
	// Remove deletes a component's lock file from disk.
	Remove(componentName string) error
}

// Compile-time check that Store satisfies both interfaces.
var (
	_ LockReader = (*Store)(nil)
	_ LockWriter = (*Store)(nil)
)

// Store provides cached access to per-component lock files. It wraps the
// low-level Load/Save/Exists functions with a lazy-loading cache, avoiding
// repeated disk reads when commands touch the same component multiple times
// or when parallel goroutines resolve different components concurrently.
//
// All methods are safe for concurrent use. The cache uses [sync.Map] for
// lock-free reads on different keys. Concurrent first-time loads of the same
// key may result in a redundant disk read, but the result is identical and
// harmless.
type Store struct {
	fs      opctx.FS
	lockDir string
	cache   sync.Map // map[string]*ComponentLock
}

// NewStore creates a new lock store that reads/writes lock files in lockDir.
func NewStore(fs opctx.FS, lockDir string) *Store {
	return &Store{
		fs:      fs,
		lockDir: lockDir,
	}
}

// lockPath returns the path for a component's lock file within this store.
func (s *Store) lockPath(componentName string) (string, error) {
	return LockPath(s.lockDir, componentName)
}

// Get returns the lock for a component, loading it from disk on first access.
// Returns a copy — callers may mutate the returned value without affecting
// cached state. Returns an error if the lock file does not exist or cannot
// be parsed.
func (s *Store) Get(componentName string) (*ComponentLock, error) {
	if cached, ok := s.cache.Load(componentName); ok {
		lock, typeOK := cached.(*ComponentLock)
		if !typeOK {
			return nil, fmt.Errorf("cache corruption for %#q: unexpected type", componentName)
		}

		cp := *lock

		return &cp, nil
	}

	// Not cached — load from disk.
	lockPath, err := s.lockPath(componentName)
	if err != nil {
		return nil, fmt.Errorf("getting lock path for component %#q:\n%w", componentName, err)
	}

	lock, err := Load(s.fs, lockPath)
	if err != nil {
		return nil, err
	}

	// Store in cache. If another goroutine raced us, the duplicate is harmless
	// (same file contents).
	s.cache.Store(componentName, lock)

	cp := *lock

	return &cp, nil
}

// GetOrNew returns the lock for a component, creating a new empty lock if
// the lock file does not exist on disk. Returns an error if the lock file
// exists but cannot be loaded (e.g., corrupt TOML, unsupported version).
func (s *Store) GetOrNew(componentName string) (*ComponentLock, error) {
	lock, err := s.Get(componentName)
	if err != nil {
		// Distinguish "not found" from other errors. Only create a new lock
		// if the file doesn't exist; corrupt/unreadable files should be
		// surfaced as errors to avoid silently losing data.
		exists, existsErr := s.Exists(componentName)
		if existsErr != nil {
			return nil, fmt.Errorf("checking lock for %#q:\n%w", componentName, existsErr)
		}

		if exists {
			return nil, fmt.Errorf("loading existing lock for %#q:\n%w", componentName, err)
		}

		return New(), nil
	}

	return lock, nil
}

// Save writes the lock for a component to disk and updates the cache.
// Caches a defensive copy so caller mutations after Save don't affect
// cached state.
func (s *Store) Save(componentName string, lock *ComponentLock) error {
	if lock == nil {
		return fmt.Errorf("cannot save nil lock for component %#q", componentName)
	}

	lockPath, err := s.lockPath(componentName)
	if err != nil {
		return fmt.Errorf("getting lock path for component %#q:\n%w", componentName, err)
	}

	if err := lock.Save(s.fs, lockPath); err != nil {
		return err
	}

	// Cache a copy so caller can't mutate cached state after Save.
	cp := *lock
	s.cache.Store(componentName, &cp)

	return nil
}

// Exists checks whether a lock file exists for the given component.
func (s *Store) Exists(componentName string) (bool, error) {
	if _, ok := s.cache.Load(componentName); ok {
		return true, nil
	}

	lockPath, err := s.lockPath(componentName)
	if err != nil {
		return false, fmt.Errorf("getting lock path for component %#q:\n%w", componentName, err)
	}

	return Exists(s.fs, lockPath)
}

// Remove deletes a component's lock file from disk and evicts it from cache.
func (s *Store) Remove(componentName string) error {
	lockPath, err := s.lockPath(componentName)
	if err != nil {
		return fmt.Errorf("getting lock path for component %#q:\n%w", componentName, err)
	}

	if err := Remove(s.fs, lockPath); err != nil {
		return err
	}

	s.cache.Delete(componentName)

	return nil
}

// ValidateConsistency checks lock files against the resolved component configs.
// For each non-local component, verifies a lock file exists and any explicit
// upstream-commit pin matches. When checkOrphans is true, also detects orphan
// lock files (only appropriate when validating the full project).
//
// Returns sorted lists of components with missing/stale locks and orphan
// component names. Returns an error if any issues are found.
func (s *Store) ValidateConsistency(
	components map[string]projectconfig.ComponentConfig,
	checkOrphans bool,
) (missingOrStale, orphans []string, err error) {
	return validateConsistency(s.fs, s.lockDir, components, checkOrphans)
}

// FindOrphanLockFiles returns component names that have lock files but no
// corresponding component in the given config map.
func (s *Store) FindOrphanLockFiles(
	components map[string]projectconfig.ComponentConfig,
) ([]string, error) {
	return FindOrphanLockFiles(s.fs, s.lockDir, components)
}

// PruneOrphans removes lock files for components that no longer exist in
// config. Returns the number of files removed and an error if any removals
// failed. Also evicts pruned entries from the cache.
func (s *Store) PruneOrphans(
	components map[string]projectconfig.ComponentConfig,
) (int, error) {
	orphans, findErr := s.FindOrphanLockFiles(components)
	if findErr != nil {
		return 0, fmt.Errorf("finding orphan lock files:\n%w", findErr)
	}

	if len(orphans) == 0 {
		return 0, nil
	}

	var (
		pruned int
		errs   []error
	)

	for _, componentName := range orphans {
		slog.Info("Removing orphan lock file", "component", componentName)

		if removeErr := s.Remove(componentName); removeErr != nil {
			errs = append(errs, fmt.Errorf("removing lock for %#q:\n%w", componentName, removeErr))

			continue
		}

		pruned++
	}

	if len(errs) > 0 {
		return pruned, fmt.Errorf("failed to remove %d orphan lock file(s):\n  %w",
			len(errs), errors.Join(errs...))
	}

	return pruned, nil
}
