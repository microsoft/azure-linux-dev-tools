// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage,nolintlint // Tests access unexported spec-editor routing helpers.
package component

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/dirdiff"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComponentCommandOptionsDefaultsToLegacy(t *testing.T) {
	assert.Equal(t, spec.EditorLegacy, componentCommandOptions{}.specEditorMode())
}

func TestSpecEditorOptionDefaultsToLegacy(t *testing.T) {
	editor, err := executeSpecEditorProductionCommand(t, "render")

	require.NoError(t, err)
	assert.Equal(t, spec.EditorLegacy, editor)
}

func TestSpecEditorOptionParsesLegacy(t *testing.T) {
	editor, err := executeSpecEditorProductionCommand(t, "render", "--spec-editor", specEditorLegacy)

	require.NoError(t, err)
	assert.Equal(t, spec.EditorLegacy, editor)
}

func TestSpecEditorOptionParsesExperimental(t *testing.T) {
	editor, err := executeSpecEditorProductionCommand(t, "render", "--spec-editor", specEditorExperimental)

	require.NoError(t, err)
	assert.Equal(t, spec.EditorStructural, editor)
}

func TestSpecEditorOptionDoesNotLeakBetweenCommands(t *testing.T) {
	experimental, err := executeSpecEditorProductionCommand(t, "render", "--spec-editor", specEditorExperimental)
	require.NoError(t, err)
	assert.Equal(t, spec.EditorStructural, experimental)

	legacy, err := executeSpecEditorProductionCommand(t, "render")
	require.NoError(t, err)
	assert.Equal(t, spec.EditorLegacy, legacy)
}

func TestSpecEditorOptionIsInheritedByNonConsumingCommands(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	cmd := newComponentCmd(&azldev.App{})
	cmd.SetArgs([]string{"list", "--spec-editor", specEditorExperimental})

	require.NoError(t, cmd.ExecuteContext(testEnv.Env))
}

func TestSpecEditorOptionRejectsUnsupportedValuesWithoutFallback(t *testing.T) {
	executed := false
	originalNewSourcePreparer := newSourcePreparer
	newSourcePreparer = func(
		sourceManager sourceproviders.SourceManager,
		fileSystem opctx.FS,
		eventListener opctx.EventListener,
		dryRunnable opctx.DryRunnable,
		options ...sources.PreparerOption,
	) (sources.SourcePreparer, error) {
		executed = true

		return originalNewSourcePreparer(sourceManager, fileSystem, eventListener, dryRunnable, options...)
	}

	t.Cleanup(func() {
		newSourcePreparer = originalNewSourcePreparer
	})

	cmd := newComponentCmd(&azldev.App{})
	cmd.SetArgs([]string{"render", "--spec-editor", "structural"})

	err := cmd.Execute()

	require.Error(t, err)
	require.ErrorContains(t, err, "unsupported RPM spec editor `structural`; expected `legacy` or `experimental`")
	assert.False(t, executed)
}

func TestSpecEditorOptionRoutesToRelevantComponentCommands(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		args     []string
		expected spec.EditorMode
	}{
		{
			name:     "omitted flag uses legacy",
			expected: spec.EditorLegacy,
		},
		{
			name:     "explicit legacy uses legacy",
			args:     []string{"--spec-editor", specEditorLegacy},
			expected: spec.EditorLegacy,
		},
		{
			name:     "experimental uses structural",
			args:     []string{"--spec-editor", specEditorExperimental},
			expected: spec.EditorStructural,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, command := range []string{"render", "build", "prepare-sources", "diff-sources"} {
				t.Run(command, func(t *testing.T) {
					editor, err := executeSpecEditorProductionCommand(t, command, testCase.args...)

					if command == "render" {
						require.NoError(t, err)
					} else {
						require.ErrorIs(t, err, errSpecEditorObserved)
					}

					assert.Equal(t, testCase.expected, editor)
				})
			}
		})
	}
}

var errSpecEditorObserved = errors.New("source preparer observed spec editor")

type specEditorObservingPreparer struct {
	sources.SourcePreparer
	fileSystem opctx.FS
	editor     *spec.EditorMode
}

func (p *specEditorObservingPreparer) PrepareSources(
	ctx context.Context, component components.Component, outputDir string, applyOverlays bool,
) error {
	err := p.SourcePreparer.PrepareSources(ctx, component, outputDir, applyOverlays)
	if observeErr := p.observePreparedSpec(component.GetName(), outputDir); observeErr != nil {
		return observeErr
	}

	if err != nil {
		return err
	}

	return errSpecEditorObserved
}

func (p *specEditorObservingPreparer) DiffSources(
	ctx context.Context, component components.Component, baseDir string,
) (*dirdiff.DiffResult, error) {
	result, err := p.SourcePreparer.DiffSources(ctx, component, baseDir)
	if err != nil {
		return nil, err
	}

	diff := result.String()
	if !strings.Contains(diff, "Name: selected-by-overlay") {
		return nil, errors.New("spec editor observation was not present in the source diff")
	}

	if strings.Contains(diff, "-Name: macro-body") {
		*p.editor = spec.EditorLegacy
	} else {
		*p.editor = spec.EditorStructural
	}

	return nil, errSpecEditorObserved
}

func (p *specEditorObservingPreparer) observePreparedSpec(componentName, outputDir string) error {
	contents, err := fileutils.ReadFile(p.fileSystem, filepath.Join(outputDir, componentName+".spec"))
	if err != nil {
		return err
	}

	if strings.Contains(string(contents), "\nName: macro-body\n") {
		*p.editor = spec.EditorStructural
	} else {
		*p.editor = spec.EditorLegacy
	}

	return nil
}

func executeSpecEditorProductionCommand(t *testing.T, command string, args ...string) (spec.EditorMode, error) {
	t.Helper()

	var editor spec.EditorMode

	testEnv := testutils.NewTestEnv(t)
	addSpecEditorTestComponent(t, testEnv)

	originalNewSourcePreparer := newSourcePreparer
	newSourcePreparer = func(
		sourceManager sourceproviders.SourceManager,
		fileSystem opctx.FS,
		eventListener opctx.EventListener,
		dryRunnable opctx.DryRunnable,
		options ...sources.PreparerOption,
	) (sources.SourcePreparer, error) {
		preparer, err := originalNewSourcePreparer(sourceManager, fileSystem, eventListener, dryRunnable, options...)
		if err != nil {
			return nil, err
		}

		return &specEditorObservingPreparer{
			SourcePreparer: preparer,
			fileSystem:     fileSystem,
			editor:         &editor,
		}, nil
	}

	t.Cleanup(func() {
		newSourcePreparer = originalNewSourcePreparer
	})

	cmd := newComponentCmd(&azldev.App{})
	cmd.SetArgs(append([]string{command}, append(specEditorCommandArgs(command), args...)...))

	return editor, cmd.ExecuteContext(testEnv.Env)
}

func specEditorCommandArgs(command string) []string {
	switch command {
	case "build":
		return []string{"--without-git", "spec-editor-fixture"}
	case "render":
		return []string{"--output-dir", "/rendered", "spec-editor-fixture"}
	case "prepare-sources":
		return []string{"--without-git", "--output-dir", "/prepared", "spec-editor-fixture"}
	case "diff-sources":
		return []string{"spec-editor-fixture"}
	default:
		panic("unsupported command: " + command)
	}
}

func addSpecEditorTestComponent(t *testing.T, testEnv *testutils.TestEnv) {
	t.Helper()

	const (
		componentName = "spec-editor-fixture"
		specPath      = "/project/specs/spec-editor-fixture/spec-editor-fixture.spec"
	)

	specContents := []string{
		"%global hidden() \\",
		"Name: macro-body",
		"Name: spec-editor-fixture",
		"Version: 1",
		"Release: 1",
		"Summary: fixture",
		"%description",
		"fixture",
	}

	require.NoError(t, fileutils.WriteFile(
		testEnv.TestFS, specPath, []byte(strings.Join(specContents, "\n")+"\n"), fileperms.PublicFile))

	testEnv.Config.Components[componentName] = projectconfig.ComponentConfig{
		Name: componentName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       specPath,
		},
		Overlays: []projectconfig.ComponentOverlay{
			{
				Type:  projectconfig.ComponentOverlayUpdateSpecTag,
				Tag:   "Name",
				Value: "selected-by-overlay",
			},
		},
	}
}
