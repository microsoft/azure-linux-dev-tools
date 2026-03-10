// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package hostinfo provides utilities to query capabilities and characteristics of the
// host system that this code is running on.
package hostinfo

import "os"

// IsWSL checks if the current host is running under Windows Subsystem for Linux (WSL).
func IsWSL() bool {
	// We perform a very simple environment check for a well-known variable injected by WSL.
	return os.Getenv("WSL_DISTRO_NAME") != ""
}
