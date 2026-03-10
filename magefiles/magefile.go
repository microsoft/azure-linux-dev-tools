// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build mage

package main

import (
	"os"

	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"

	"github.com/magefile/mage/mg"
	//mage:import
	"github.com/microsoft/azure-linux-dev-tools/magefiles/magebuild"
	//mage:import
	"github.com/microsoft/azure-linux-dev-tools/magefiles/magecheckfix"
	//mage:import
	"github.com/microsoft/azure-linux-dev-tools/magefiles/magescenario"
	//mage:import
	_ "github.com/microsoft/azure-linux-dev-tools/magefiles/completions"
	//mage:import
	_ "github.com/microsoft/azure-linux-dev-tools/magefiles/magesrc"
)

func init() {
	// Borrowed from the main magefile sources in template.go
	// Terminals which  don't support color:
	// 	TERM=vt100
	// 	TERM=cygwin
	// 	TERM=xterm-mono
	var noColorTerms = map[string]bool{
		"vt100":      false,
		"cygwin":     false,
		"xterm-mono": false,
	}

	// This will set the environment variable before we actually run the mage tool since init() is called before main().
	if _, ok := noColorTerms[os.Getenv("TERM")]; !ok {
		// Don't override the value if it's already set.
		if os.Getenv(mageutil.MageColorEnableEnvVar) == "" {
			os.Setenv(mageutil.MageColorEnableEnvVar, "true")
		}
	}
}

// All runs the tests, builds the code, and checks for any issues.
func All() error {
	mg.SerialDeps(magebuild.Build, magebuild.Unit, mg.F(magecheckfix.Check, magecheckfix.TargetAll), magescenario.Scenario)
	return nil
}
