// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// The following structs model the subset of a LISA runbook that azldev generates. The
// YAML field names are dictated by LISA's runbook schema (snake_case), which we do not
// control.
//

type lisaRunbook struct {
	Name     string         `yaml:"name"`
	Include  []lisaInclude  `yaml:"include"`
	Testcase []lisaTestcase `yaml:"testcase"`
	Notifier []lisaNotifier `yaml:"notifier"`
	Platform []lisaPlatform `yaml:"platform"`
}

type lisaInclude struct {
	Path string `yaml:"path"`
}

type lisaTestcase struct {
	Criteria lisaCriteria `yaml:"criteria"`
}

type lisaCriteria struct {
	Name string `yaml:"name"`
}

type lisaNotifier struct {
	Type string `yaml:"type"`
}

//nolint:tagliatelle // External schema (LISA runbook) dictates the field names.
type lisaPlatform struct {
	Type                string              `yaml:"type"`
	AdminPrivateKeyFile string              `yaml:"admin_private_key_file"`
	KeepEnvironment     string              `yaml:"keep_environment"`
	Qemu                lisaPlatformQemu    `yaml:"qemu"`
	Requirement         lisaPlatformReqRoot `yaml:"requirement"`
}

//nolint:tagliatelle // External schema (LISA runbook) dictates the field names.
type lisaPlatformQemu struct {
	NetworkBootTimeout int `yaml:"network_boot_timeout"`
}

type lisaPlatformReqRoot struct {
	Qemu lisaPlatformReqQemu `yaml:"qemu"`
}

type lisaPlatformReqQemu struct {
	Qcow2 string `yaml:"qcow2"`
}

const (
	// runbookTierIncludePath is the path (relative to the generated runbook) to the shared
	// tier definitions in the LISA tree. LISA resolves includes relative to the runbook file's
	// directory, so this resolves correctly only when the generated runbook is written at the
	// framework repo root (see writeGeneratedRunbook).
	runbookTierIncludePath = "lisa/microsoft/runbook/tiers/tier.yml"
	// runbookBootTimeoutSeconds is the QEMU network boot timeout used in the generated runbook.
	runbookBootTimeoutSeconds = 300
)

// generateRunbookYAML builds a LISA runbook that runs the given test cases on a QEMU VM
// booted from imagePath, authenticating with adminKeyPath. All values (image path, admin
// key path) are inlined as concrete values. keep_environment is "no" so LISA tears down the
// VM environment after the run.
func generateRunbookYAML(suiteName string, testCases []string, imagePath, adminKeyPath string) ([]byte, error) {
	runbook := lisaRunbook{
		Name:    suiteName,
		Include: []lisaInclude{{Path: runbookTierIncludePath}},
		Testcase: []lisaTestcase{
			{Criteria: lisaCriteria{Name: strings.Join(testCases, "|")}},
		},
		Notifier: []lisaNotifier{{Type: "html"}},
		Platform: []lisaPlatform{
			{
				Type:                "qemu",
				AdminPrivateKeyFile: adminKeyPath,
				KeepEnvironment:     "no",
				Qemu:                lisaPlatformQemu{NetworkBootTimeout: runbookBootTimeoutSeconds},
				Requirement: lisaPlatformReqRoot{
					Qemu: lisaPlatformReqQemu{
						Qcow2: imagePath,
					},
				},
			},
		},
	}

	data, err := yaml.Marshal(&runbook)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal generated LISA runbook:\n%w", err)
	}

	return data, nil
}
