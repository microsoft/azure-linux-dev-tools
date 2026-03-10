// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package magescenario

import (
	"errors"
	"os"
	"path"
	"strings"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/buildtestenv"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/magebuild"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
)

var ErrScenario = errors.New("scenario test failed")

// Scenario runs the scenario tests.
func Scenario() error {
	startTime := time.Now()

	mageutil.MagePrintln(mageutil.MsgStart, "Starting scenario tests...")

	mg.SerialDeps(mg.F(mageScenarioCommon, false))

	mageutil.MagePrintf(mageutil.MsgSuccess, "Scenario tests complete: %v.\n", time.Since(startTime))

	return nil
}

// ScenarioUpdate updates snapshots and runs the scenario tests.
func ScenarioUpdate() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Updating scenario snapshots...")

	mg.SerialDeps(mg.F(mageScenarioCommon, true))

	mageutil.MagePrintln(mageutil.MsgSuccess, "Scenario snapshots updated.")

	return nil
}

func mageScenarioCommon(doSnapshotUpdate bool) error {
	mg.SerialDeps(magebuild.Build)

	mageutil.MagePrintln(mageutil.MsgStart, "Running scenario tests...")

	//nolint:lll // Can't really break up the long URI.
	// Add workaround for go-snaps disablement of color based on $_.
	// Reference: https://github.com/gkampitakis/go-snaps/blob/e004bc15e5166169d41a9d082967275fffb9d2b4/internal/colors/colors.go#L35
	if os.Getenv(mageutil.MageColorEnableEnvVar) == "true" {
		os.Setenv("_", "")
	}

	// mage knows the exact path to the azldev binary, ensure we use it.
	env := map[string]string{
		buildtestenv.TestingAzldevBinPathEnvVar: path.Join(mageutil.BinDir(), "azldev"),
	}

	// If we are updating the snapshots, set the environment variable to only run the snapshot tests, and configure
	// the snapshotter into update mode.
	if doSnapshotUpdate {
		mageutil.MagePrintln(mageutil.MsgInfo, "Will update snapshots.")

		env[buildtestenv.TestingUpdateSnapshotsEnvVar] = buildtestenv.TestingUpdateSnapshotsEnvValue
	}

	// '-count=1' causes the cache to be ignored for scenario tests so they always re-run (it would only rebuild if
	// we changed the test code itself, not the application code).
	output, err := sh.OutputWith(env, mg.GoCmd(), "test", "-tags=scenario", "-timeout=60m", "-count=1", "./scenario/...")
	if err != nil {
		mageutil.MagePrintln(mageutil.MsgError, "Scenario test failed; details follow.")

		for _, line := range strings.Split(output, "\n") {
			mageutil.MagePrintln(mageutil.MsgInfo, line)
		}

		return mageutil.PrintAndReturnError("Scenario test failed (see diagnostic output above).", ErrScenario, err)
	}

	return nil
}
