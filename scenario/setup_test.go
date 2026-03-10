// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"fmt"
	"os"
	"testing"

	"github.com/opencontainers/selinux/go-selinux"
)

// TestMain is the entry point for all scenario tests.
// It checks SELinux mode and fails fast if it's in Enforcing mode.
func TestMain(m *testing.M) {
	// Check SELinux mode
	mode := selinux.EnforceMode()

	// Fail fast if SELinux is in Enforcing mode
	if mode == selinux.Enforcing {
		fmt.Fprintf(os.Stderr, `Scenario tests require SELinux to be in Permissive mode or disabled.
Current SELinux mode: Enforcing

To set SELinux to Permissive mode temporarily, run:
  sudo setenforce 0

To make it permanent, edit /etc/selinux/config and set:
  SELINUX=permissive
`)
		os.Exit(1)
	}

	// Log the current SELinux mode for informational purposes
	if mode == selinux.Permissive {
		fmt.Printf("SELinux is in Permissive mode - tests will run.\n")
	} else {
		fmt.Printf("SELinux is disabled - tests will run.\n")
	}

	// Run the tests
	os.Exit(m.Run())
}

