// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lockfile

import (
	"sync"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

// Store provides cached access to per-component lock files. It wraps the
// low-level Load/Save/Exists functions with a lazy-loading cache.
//
// Store is safe for concurrent read access (Get/Exists). Write operations
// (Save/Remove) should be serialized by the caller.
type Store struct {
	fs         opctx.FS
	projectDir string

	mu    sync.RWMutex
	cache map[string]*ComponentLock
}

// NewStore creates a new lock store for the given project.
func NewStore(fs opctx.FS, projectDir string) *Store {
	return &Store{
		fs:         fs,
		projectDir: projectDir,
		cache:      make(map[string]*ComponentLock),
	}
}

// Get returns the lock for a component, loading it from disk on first access.
// Returns nil and an error if the lock file does not exist or cannot be parsed.
func (s *Store) Get(componentName string) (*ComponentLock, error) {
	s.mu.RLock()

	if lock, ok := s.cache[componentName]; ok {
		s.mu.RUnlock()

		return lock, nil
	}

	s.mu.RUnlock()

	// Not cached — load from disk.
	lockPath := LockPath(s.projectDir, componentName)

	lock, err := Load(s.fs, lockPath)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cache[componentName] = lock
	s.mu.Unlock()

	return lock, nil
}

// GetOrNew returns the lock for a component, creating a new empty lock if
// none exists on disk. Useful for update flows that create locks for new
// components.
func (s *Store) GetOrNew(componentName string) *ComponentLock {
	lock, err := s.Get(componentName)
	if err != nil {
		lock = New()
	}

	return lock
}

// Save writes the lock for a component to disk and updates the cache.
func (s *Store) Save(componentName string, lock *ComponentLock) error {
	lockPath := LockPath(s.projectDir, componentName)

	if err := lock.Save(s.fs, lockPath); err != nil {
		return err
	}

	s.mu.Lock()
	s.cache[componentName] = lock
	s.mu.Unlock()

	return nil
}

// Exists checks whether a lock file exists for the given component.
func (s *Store) Exists(componentName string) (bool, error) {
	s.mu.RLock()

	if _, ok := s.cache[componentName]; ok {
		s.mu.RUnlock()

		return true, nil
	}

	s.mu.RUnlock()

	return Exists(s.fs, LockPath(s.projectDir, componentName))
}

// Remove deletes a component's lock file from disk and evicts it from cache.
func (s *Store) Remove(componentName string) error {
	lockPath := LockPath(s.projectDir, componentName)

	if err := Remove(s.fs, lockPath); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.cache, componentName)
	s.mu.Unlock()

	return nil
}

// ProjectDir returns the project directory this store operates on.
func (s *Store) ProjectDir() string {
	return s.projectDir
}

// FS returns the filesystem this store operates on.
func (s *Store) FS() opctx.FS {
	return s.fs
}
