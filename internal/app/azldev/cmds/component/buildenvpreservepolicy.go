// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"

	"github.com/spf13/pflag"
)

// BuildEnvPreservePolicy defines policy for when to preserve auto-created build environments.
type BuildEnvPreservePolicy string

const (
	// BuildEnvPreserveOnFailure indicates build environments should only be preserved for failed builds.
	BuildEnvPreserveOnFailure BuildEnvPreservePolicy = "on-failure"
	// BuildEnvPreserveAlways indicates all build environments should be preserved.
	BuildEnvPreserveAlways BuildEnvPreservePolicy = "always"
	// BuildEnvPreserveNever indicates build environments should *never* be preserved (i.e., always destroyed).
	BuildEnvPreserveNever BuildEnvPreservePolicy = "never"
)

// Assert that BuildEnvPreservePolicy implements the [pflag.Value] interface.
var _ pflag.Value = (*BuildEnvPreservePolicy)(nil)

func (f *BuildEnvPreservePolicy) String() string {
	return string(*f)
}

// Set parses the format from a string; used by command-line parser.
func (f *BuildEnvPreservePolicy) Set(value string) error {
	switch value {
	case string(BuildEnvPreserveOnFailure):
		*f = BuildEnvPreserveOnFailure
	case string(BuildEnvPreserveAlways):
		*f = BuildEnvPreserveAlways
	case string(BuildEnvPreserveNever):
		*f = BuildEnvPreserveNever
	case "":
		*f = BuildEnvPreserveOnFailure
	default:
		// Default to "on failure" but still return an error.
		*f = BuildEnvPreserveOnFailure

		return fmt.Errorf("unsupported build environment preserve policy: %s", value)
	}

	return nil
}

// Type returns a descriptive string used in command-line help.
func (f *BuildEnvPreservePolicy) Type() string {
	return "policy"
}

// ShouldPreserve is a helper to decide whether to preserve a build environment, based on the policy
// and the actual results of the build.
func (f *BuildEnvPreservePolicy) ShouldPreserve(buildSucceeded bool) bool {
	switch *f {
	case BuildEnvPreserveOnFailure:
		return !buildSucceeded
	case BuildEnvPreserveAlways:
		return true
	case BuildEnvPreserveNever:
		return false
	default:
		return false
	}
}
