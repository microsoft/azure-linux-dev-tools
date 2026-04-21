// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/cobra"
)

func diffIdentityOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(NewDiffIdentityCommand())
}

// diffIdentityArgCount is the number of positional arguments required by the diff-identity command.
const diffIdentityArgCount = 2

// NewDiffIdentityCommand constructs a [cobra.Command] for "component diff-identity".
func NewDiffIdentityCommand() *cobra.Command {
	var options struct {
		ChangedOnly bool
	}

	cmd := &cobra.Command{
		Use:   "diff-identity <base.json> <head.json>",
		Short: "Compare two identity files and report changed components",
		Long: `Compare two component identity JSON files (produced by 'component identity -a -O json')
and report which components have changed, been added, or been removed.

CI uses the 'changed' and 'added' lists to determine the build queue.`,
		Example: `  # Compare base and head identity files
  azldev component diff-identity base-identity.json head-identity.json

  # JSON output for CI
  azldev component diff-identity base.json head.json -O json`,
		Args: cobra.ExactArgs(diffIdentityArgCount),
		RunE: azldev.RunFuncWithoutRequiredConfigWithExtraArgs(
			func(env *azldev.Env, args []string) (interface{}, error) {
				return DiffIdentities(env, args[0], args[1], options.ChangedOnly)
			},
		),
	}

	cmd.Flags().BoolVarP(&options.ChangedOnly, "changed-only", "c", false,
		"Only show changed and added components (the build queue)")

	return cmd
}

// IdentityDiffStatus represents the change status of a component.
type IdentityDiffStatus string

const (
	// IdentityDiffChanged indicates the component's fingerprint changed.
	IdentityDiffChanged IdentityDiffStatus = "changed"
	// IdentityDiffAdded indicates the component is new in the head.
	IdentityDiffAdded IdentityDiffStatus = "added"
	// IdentityDiffRemoved indicates the component was removed in the head.
	IdentityDiffRemoved IdentityDiffStatus = "removed"
	// IdentityDiffUnchanged indicates the component's fingerprint is identical.
	IdentityDiffUnchanged IdentityDiffStatus = "unchanged"
)

// IdentityDiffResult is the per-component row in table output.
type IdentityDiffResult struct {
	Component string             `json:"component" table:",sortkey"`
	Status    IdentityDiffStatus `json:"status"`
}

// IdentityDiffReport is the structured output for JSON format.
type IdentityDiffReport struct {
	Changed   []string `json:"changed"`
	Added     []string `json:"added"`
	Removed   []string `json:"removed"`
	Unchanged []string `json:"unchanged"`
}

// DiffIdentities reads two identity JSON files and computes the diff.
func DiffIdentities(env *azldev.Env, basePath string, headPath string, changedOnly bool) (interface{}, error) {
	baseIdentities, err := readIdentityFile(env, basePath)
	if err != nil {
		return nil, fmt.Errorf("reading base identity file %#q:\n%w", basePath, err)
	}

	headIdentities, err := readIdentityFile(env, headPath)
	if err != nil {
		return nil, fmt.Errorf("reading head identity file %#q:\n%w", headPath, err)
	}

	report := ComputeDiff(baseIdentities, headIdentities, changedOnly)

	// Return table-friendly results for table/CSV format, or the report for JSON.
	if env.DefaultReportFormat() == azldev.ReportFormatJSON {
		return report, nil
	}

	return buildTableResults(report), nil
}

// readIdentityFile reads and parses a component identity JSON file into a map of
// component name to fingerprint.
func readIdentityFile(
	env *azldev.Env, filePath string,
) (map[string]string, error) {
	data, err := fileutils.ReadFile(env.FS(), filePath)
	if err != nil {
		return nil, fmt.Errorf("reading file:\n%w", err)
	}

	var entries []ComponentIdentityResult

	err = json.Unmarshal(data, &entries)
	if err != nil {
		return nil, fmt.Errorf("parsing JSON:\n%w", err)
	}

	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		result[entry.Component] = entry.Fingerprint
	}

	return result, nil
}

// ComputeDiff compares base and head identity maps and produces a diff report.
// When changedOnly is true, the Removed and Unchanged lists are left empty.
func ComputeDiff(base map[string]string, head map[string]string, changedOnly bool) *IdentityDiffReport {
	// Initialize all slices so JSON serialization produces [] instead of null.
	report := &IdentityDiffReport{
		Changed:   make([]string, 0),
		Added:     make([]string, 0),
		Removed:   make([]string, 0),
		Unchanged: make([]string, 0),
	}

	// Check base components against head.
	for name, baseFP := range base {
		headFP, exists := head[name]

		switch {
		case !exists:
			if !changedOnly {
				report.Removed = append(report.Removed, name)
			}
		case baseFP != headFP:
			report.Changed = append(report.Changed, name)
		default:
			if !changedOnly {
				report.Unchanged = append(report.Unchanged, name)
			}
		}
	}

	// Check for new components in head.
	for name := range head {
		if _, exists := base[name]; !exists {
			report.Added = append(report.Added, name)
		}
	}

	// Sort all lists for deterministic output.
	sort.Strings(report.Changed)
	sort.Strings(report.Added)
	sort.Strings(report.Removed)
	sort.Strings(report.Unchanged)

	return report
}

// buildTableResults converts the diff report into a slice for table output.
func buildTableResults(report *IdentityDiffReport) []IdentityDiffResult {
	results := make([]IdentityDiffResult, 0,
		len(report.Changed)+len(report.Added)+len(report.Removed)+len(report.Unchanged))

	for _, name := range report.Changed {
		results = append(results, IdentityDiffResult{Component: name, Status: IdentityDiffChanged})
	}

	for _, name := range report.Added {
		results = append(results, IdentityDiffResult{Component: name, Status: IdentityDiffAdded})
	}

	for _, name := range report.Removed {
		results = append(results, IdentityDiffResult{Component: name, Status: IdentityDiffRemoved})
	}

	for _, name := range report.Unchanged {
		results = append(results, IdentityDiffResult{Component: name, Status: IdentityDiffUnchanged})
	}

	return results
}
