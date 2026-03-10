// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package buildtestenv provides the environment variables used by testing and build tools. These are generally not
// used in the core code, but allows the build tooling to pass information to the code under test more easily.
package buildtestenv

const (
	// TestingAzldevBinPathEnvVar is the path the scenario tests use to find the azldev binary. If this is not set,
	// the tests will look for the azldev binary in the $PATH. This is set by the magefiles when running the tests.
	TestingAzldevBinPathEnvVar = "TESTING_AZLDEV_BIN_PATH"

	// TestingDisableNetworkTestsEnvVar will cause all scenario tests that require network access to be skipped.
	TestingDisableNetworkTestsEnvVar = "TESTING_DISABLE_NETWORK_TESTS"

	// TestingUpdateSnapshotsEnvVar is the environment variable that causes snapshots to be updated.
	TestingUpdateSnapshotsEnvVar = "UPDATE_SNAPS"

	// TestingUpdateSnapshotsEnvValue is the value that the TestingUpdateSnapshotsEnvVar should be set to in order to
	// update snapshots.
	TestingUpdateSnapshotsEnvValue = "true"
)
