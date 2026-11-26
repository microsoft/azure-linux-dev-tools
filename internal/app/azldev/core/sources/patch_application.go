// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"bytes"
	"fmt"
	"io"
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

		patchFS, patchPath := destFS, name

		exists, statErr := fileutils.Exists(patchFS, patchPath)
		if statErr == nil && !exists && p.dryRunnable.DryRun() {
			// Dry-run file copies leave only the original overlay input available.
			patchFS, patchPath = p.fs, overlay.Source
			exists, statErr = fileutils.Exists(patchFS, patchPath)
		}

		if statErr != nil {
			return fmt.Errorf("checking final patch %#q for diagnostics:\n%w", name, statErr)
		}

		if exists {
			if err := fileutils.ValidateFileExists(patchFS, patchPath); err != nil {
				return fmt.Errorf("checking final patch %#q for diagnostics:\n%w", name, err)
			}
		}

		patches = append(patches, spec.AddedPatch{Filename: name, FileMissing: !exists})
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
