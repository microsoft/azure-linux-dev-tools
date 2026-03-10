// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// The fileperms package contains a set of enums for commonly used linux file permissions.
package fileperms

import (
	"os"
)

const (
	// No permission bits set.
	None os.FileMode = 0
	// All permission bits set.
	All os.FileMode = os.ModePerm

	// File permissions: user read/write.
	PrivateFile os.FileMode = 0o600
	// File permissions: user read/write; group and others read.
	PublicFile os.FileMode = 0o644
	// File permissions: user read/write/execute; group and others read/execute.
	PublicExecutable os.FileMode = 0o755

	// Directory permissions: user read/write/execute.
	PrivateDir os.FileMode = 0o700
	// Directory permissions: user read/write/execute; group read/execute.
	PublicDir os.FileMode = 0o755
)
