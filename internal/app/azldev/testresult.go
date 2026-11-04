// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev

// TestRunResult identifies a test runner's retained artifacts.
// JUnit paths are relative to [TestRunResult.ArtifactDir].
// [TestRunResult.ExitCode] is nil if no normal process exit was observed.
type TestRunResult struct {
	Test        string   `json:"test"`
	ExitCode    *int     `json:"exitCode"`
	ArtifactDir string   `json:"artifactDir"`
	JUnit       []string `json:"junit"`
}
