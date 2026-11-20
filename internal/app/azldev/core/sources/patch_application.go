// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

func (p *sourcePreparerImpl) inspectAddedPatchApplications(
	componentName, sourcesDir, specPath string, overlays []projectconfig.ComponentOverlay,
) error {
	additions := addedPatchOverlays(overlays)
	if len(additions) == 0 {
		return nil
	}

	specContent, err := fileutils.ReadFile(p.fs, specPath)
	if err != nil {
		return fmt.Errorf("reading final spec %#q for patch diagnostics:\n%w", specPath, err)
	}

	opened, err := spec.OpenSpec(bytes.NewReader(specContent), spec.WithEditor(p.specEditor))
	if err != nil {
		return fmt.Errorf("opening final spec %#q for patch diagnostics:\n%w", specPath, err)
	}

	destFS, err := newDestFS(p.fs, sourcesDir)
	if err != nil {
		return fmt.Errorf("opening sources directory for patch diagnostics:\n%w", err)
	}

	defer func() {
		if closer, ok := destFS.(io.Closer); ok {
			_ = closer.Close()
		}
	}()

	patches := make([]spec.AddedPatch, 0, len(additions))
	for _, overlay := range additions {
		name := overlay.EffectiveSourceName()

		content, readErr := fileutils.ReadFile(destFS, name)
		if errors.Is(readErr, fs.ErrNotExist) && p.dryRunnable.DryRun() {
			// Dry-run file copies leave only the original overlay input available.
			content, readErr = fileutils.ReadFile(p.fs, overlay.Source)
		}

		if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
			return fmt.Errorf("reading final patch %#q for diagnostics:\n%w", name, readErr)
		}

		patches = append(patches, spec.AddedPatch{Filename: name, Content: content, FileMissing: readErr != nil})
	}

	diagnostics, err := opened.AnalyzePatchApplications(patches)
	if err != nil {
		return fmt.Errorf("checking added patch application in %#q:\n%w", specPath, err)
	}

	p.reportPatchDiagnostics(componentName, specPath, diagnostics)

	return nil
}

func (p *sourcePreparerImpl) reportPatchDiagnostics(
	componentName, specPath string, diagnostics []spec.PatchApplicationDiagnostic,
) {
	if p.patchDiagnostics != nil {
		p.patchDiagnostics(diagnostics)

		return
	}

	for _, diagnostic := range diagnostics {
		slog.Warn("Added patch application could not be verified",
			"component", componentName, "patch", diagnostic.Filename,
			"patchNumber", diagnostic.PatchNumber, "spec", specPath,
			"command", diagnostic.Command,
			"reason", diagnostic.Reason)
	}
}

func addedPatchOverlays(overlays []projectconfig.ComponentOverlay) []projectconfig.ComponentOverlay {
	var additions []projectconfig.ComponentOverlay

	seen := make(map[string]bool)

	for _, overlay := range overlays {
		if overlay.Type != projectconfig.ComponentOverlayAddPatch {
			continue
		}

		name := overlay.EffectiveSourceName()
		if !seen[name] {
			additions = append(additions, overlay)
			seen[name] = true
		}
	}

	return additions
}
