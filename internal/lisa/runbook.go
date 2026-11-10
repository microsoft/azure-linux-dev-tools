// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lisa

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

// lisaCriteria models a single LISA runbook testcase criteria block. All rules within
// one criteria are AND-ed together by LISA; multiple criteria (multiple lisaTestcase
// entries) are OR-ed. Name carries either an explicit name pattern or one or more test
// case names joined with '|' (azldev's 'testcase-name'/'testcase-names' selectors).
type lisaCriteria struct {
	Name     string   `yaml:"name,omitempty"`
	Area     string   `yaml:"area,omitempty"`
	Category string   `yaml:"category,omitempty"`
	Priority any      `yaml:"priority,omitempty"`
	Tags     []string `yaml:"tags,omitempty"`
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
	criteria := []lisaCriteria{{Name: strings.Join(testCases, "|")}}

	return generateRunbookYAMLFromCriteria(suiteName, criteria, imagePath, adminKeyPath)
}

// generateRunbookYAMLFromCriteria builds a LISA runbook selecting test cases via one or
// more criteria blocks (name, area, category, priority, tags) on a QEMU VM booted from
// imagePath, authenticating with adminKeyPath. Each criteria produces its own testcase
// entry; LISA ORs test cases matched across entries. keep_environment is "no" so LISA
// tears down the VM environment after the run.
func generateRunbookYAMLFromCriteria(
	suiteName string, criteria []lisaCriteria, imagePath, adminKeyPath string,
) ([]byte, error) {
	testcases := make([]lisaTestcase, 0, len(criteria))
	for _, c := range criteria {
		testcases = append(testcases, lisaTestcase{Criteria: c})
	}

	runbook := lisaRunbook{
		Name:     suiteName,
		Include:  []lisaInclude{{Path: runbookTierIncludePath}},
		Testcase: testcases,
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
