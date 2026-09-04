// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComponentCommandOptionsDefaultsToLegacy(t *testing.T) {
	assert.Equal(t, spec.EditorLegacy, componentCommandOptions{}.specEditorMode())
}

func TestSpecEditorOptionDefaultsToLegacy(t *testing.T) {
	editor, err := executeSpecEditorTestCommand(t, "render")

	require.NoError(t, err)
	assert.Equal(t, spec.EditorLegacy, editor)
}

func TestSpecEditorOptionParsesLegacy(t *testing.T) {
	editor, err := executeSpecEditorTestCommand(t, "render", "--spec-editor", specEditorLegacy)

	require.NoError(t, err)
	assert.Equal(t, spec.EditorLegacy, editor)
}

func TestSpecEditorOptionParsesExperimental(t *testing.T) {
	editor, err := executeSpecEditorTestCommand(t, "render", "--spec-editor", specEditorExperimental)

	require.NoError(t, err)
	assert.Equal(t, spec.EditorStructural, editor)
}

func TestSpecEditorOptionDoesNotLeakBetweenCommands(t *testing.T) {
	experimental, err := executeSpecEditorTestCommand(t, "render", "--spec-editor", specEditorExperimental)
	require.NoError(t, err)
	assert.Equal(t, spec.EditorStructural, experimental)

	legacy, err := executeSpecEditorTestCommand(t, "render")
	require.NoError(t, err)
	assert.Equal(t, spec.EditorLegacy, legacy)
}

func TestSpecEditorOptionRejectsUnsupportedValuesWithoutFallback(t *testing.T) {
	executed := false
	root := &cobra.Command{Use: "component"}
	addSpecEditorOption(root)
	root.AddCommand(&cobra.Command{
		Use: "render",
		RunE: func(_ *cobra.Command, _ []string) error {
			executed = true

			return nil
		},
	})
	root.SetArgs([]string{"render", "--spec-editor", "structural"})

	err := root.Execute()

	require.Error(t, err)
	require.ErrorContains(t, err, "unsupported RPM spec editor `structural`; expected `legacy` or `experimental`")
	assert.False(t, executed)
}

func TestSpecEditorOptionRoutesToRelevantComponentCommands(t *testing.T) {
	for _, command := range []string{"render", "build", "prepare-sources", "diff-sources"} {
		t.Run(command, func(t *testing.T) {
			editor, err := executeSpecEditorTestCommand(t, command, "--spec-editor", specEditorExperimental)

			require.NoError(t, err)
			assert.Equal(t, spec.EditorStructural, editor)
		})
	}
}

func executeSpecEditorTestCommand(t *testing.T, command string, args ...string) (spec.EditorMode, error) {
	t.Helper()

	var editor spec.EditorMode

	root := &cobra.Command{Use: "component"}
	addSpecEditorOption(root)
	root.AddCommand(&cobra.Command{
		Use: command,
		RunE: func(cmd *cobra.Command, _ []string) error {
			editor = specEditorFromCommand(cmd)

			return nil
		},
	})
	root.SetArgs(append([]string{command}, args...))

	return editor, root.Execute()
}
