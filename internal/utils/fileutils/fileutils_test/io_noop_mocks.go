// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils_test

import "io"

// NoOpReadCloser implements the [io.ReadCloser] interface but does not perform any operations.
type NoOpReadCloser struct{}

// Ensure NoOpReadCloser implements [io.ReadCloser].
var _ io.ReadCloser = (*NoOpReadCloser)(nil)

// NewNoOpReadCloser creates a new instance of NoOpReadCloser.
func NewNoOpReadCloser() *NoOpReadCloser {
	return &NoOpReadCloser{}
}

// Read returns 0 bytes read and no error, simulating a no-op read operation.
func (m *NoOpReadCloser) Read(p []byte) (n int, err error) {
	return 0, nil
}

// Close returns nil, simulating a no-op close operation.
func (m *NoOpReadCloser) Close() error {
	return nil
}
