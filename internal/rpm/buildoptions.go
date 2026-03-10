// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package rpm

// BuildOptions encapsulates standard options for build-related RPM commands that may run in a mock environment.
type BuildOptions struct {
	// 'with' flags.
	With []string
	// 'without' flags.
	Without []string
	// Custom macro defines.
	Defines map[string]string
}
