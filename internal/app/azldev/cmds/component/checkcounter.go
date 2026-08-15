// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// checkEVRCounterSelection is one component whose Release tag a render would bump through a
// release counter, together with the counter that render would use.
type checkEVRCounterSelection struct {
	// Name is the component name used to resolve the rendered spec path.
	Name string
	// Config is the resolved component configuration supplying build options.
	Config *projectconfig.ComponentConfig
	// Counter is the configured counter, or the built-in counter when the component relies on
	// the default static Release-tag pattern.
	Counter projectconfig.ReleaseCounter
	// SelectionError records why a component could not be classified. Such a component is kept
	// so the gate reports it instead of silently treating it as never bumped.
	SelectionError string
}

// checkEVRCounterSelections keeps every component whose Release a render would bump through a
// release counter. That is the honest answer to "would a rebuild produce new EVRs?", so it
// deliberately covers more than the components with an explicit 'release.counter': components
// using 'release.calculation = "auto"' fall back to the built-in Release-tag counter unless
// their rendered spec uses %autorelease. Components owned by rpmautospec ('autorelease') or by
// the maintainer ('manual') are excluded because no counter drives their Release.
func checkEVRCounterSelections(fs opctx.FS, comps []components.Component) []checkEVRCounterSelection {
	selections := make([]checkEVRCounterSelection, 0, len(comps))

	for _, comp := range comps {
		if selection, selected := newCheckEVRCounterSelection(fs, comp); selected {
			selections = append(selections, selection)
		}
	}

	return selections
}

func newCheckEVRCounterSelection(fs opctx.FS, comp components.Component) (checkEVRCounterSelection, bool) {
	config := comp.GetConfig()
	if config == nil {
		return checkEVRCounterSelection{}, false
	}

	selection := checkEVRCounterSelection{Name: comp.GetName(), Config: config}

	switch config.Release.Calculation {
	case projectconfig.ReleaseCalculationManual, projectconfig.ReleaseCalculationAutorelease:
		return checkEVRCounterSelection{}, false

	case projectconfig.ReleaseCalculationStatic:
		selection.Counter = counterForReleaseCheck(config.Release)

		return selection, true

	case projectconfig.ReleaseCalculationAuto:
		return autoCheckEVRCounterSelection(fs, selection)

	default:
		// An unset calculation is normalized to 'auto' during component resolution and an
		// unknown value is rejected by config validation, so both reach the same fallback.
		return autoCheckEVRCounterSelection(fs, selection)
	}
}

// autoCheckEVRCounterSelection resolves the counter a render would use for an 'auto' component.
// An explicit counter always wins; otherwise render reads the rendered Release tag and skips
// only %autorelease specs, so that tag is the deciding signal.
func autoCheckEVRCounterSelection(
	fs opctx.FS,
	selection checkEVRCounterSelection,
) (checkEVRCounterSelection, bool) {
	if selection.Config.Release.Counter != nil {
		selection.Counter = *selection.Config.Release.Counter

		return selection, true
	}

	selection.Counter = sources.DefaultReleaseCounter()

	bumped, err := autoReleaseUsesBuiltInCounter(fs, selection.Name, selection.Config)
	if err != nil {
		selection.SelectionError = err.Error()

		return selection, true
	}

	return selection, bumped
}

// autoReleaseUsesBuiltInCounter reports whether an 'auto' component without an explicit counter
// falls back to the built-in Release-tag counter at render time. A rendered spec that cannot be
// read is an error rather than a silent exclusion: guessing "not counter-managed" would drop the
// component from the gate entirely.
func autoReleaseUsesBuiltInCounter(
	fs opctx.FS,
	componentName string,
	config *projectconfig.ComponentConfig,
) (bool, error) {
	if config.RenderedSpecDir == "" {
		return false, errors.New("component has no rendered spec directory configured")
	}

	specPath := filepath.Join(config.RenderedSpecDir, componentName+".spec")

	releaseValue, err := sources.GetReleaseTagValue(fs, specPath)
	if err != nil {
		return false, fmt.Errorf("reading Release tag from rendered spec:\n%w", err)
	}

	return !sources.ReleaseUsesAutorelease(releaseValue), nil
}

func counterForReleaseCheck(release projectconfig.ReleaseConfig) projectconfig.ReleaseCounter {
	if release.Counter != nil {
		return *release.Counter
	}

	return sources.DefaultReleaseCounter()
}
