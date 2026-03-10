// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mageutil

// The most reliable way to pass data to go tests is through environment variables.
// This file contains the environment variables used by the magefiles and understood by the tests.
const (
	// MageColorEnableEnvVar is the environment variable that enables color in mage.
	MageColorEnableEnvVar = "MAGEFILE_ENABLE_COLOR"
)
