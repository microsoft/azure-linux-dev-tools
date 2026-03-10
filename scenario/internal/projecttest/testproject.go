// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projecttest

import "testing"

// TestProject is implemented by types that can produce a serial azldev project directory.
type TestProject interface {
	// Serialize writes the project to the given directory.
	Serialize(t *testing.T, projectDir string)
}
