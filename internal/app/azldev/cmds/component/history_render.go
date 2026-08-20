// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
)

// sortHistoryResults orders results "most-customized first": highest
// customization count first, then fingerprint-changes, then alphabetical by name.
// Customizations is the most direct signal of human attention paid to a
// component (and it's deterministic / fast); fingerprint-changes and the name
// tie-break it for stable output.
func sortHistoryResults(results []HistoryResult) {
	sort.SliceStable(results, func(left, right int) bool {
		if results[left].Customizations != results[right].Customizations {
			return results[left].Customizations > results[right].Customizations
		}

		if results[left].FingerprintChanges != results[right].FingerprintChanges {
			return results[left].FingerprintChanges > results[right].FingerprintChanges
		}

		return results[left].Name < results[right].Name
	})
}

// shouldRenderCardView decides whether to print the per-component "card"
// view instead of falling through to the default table renderer. We only
// switch to the card for exactly one result and only for the plain table
// format; markdown falls through to the reflectable renderer so a
// `-O markdown` consumer gets real markdown structure, and JSON / CSV
// consumers always get the machine-readable slice.
func shouldRenderCardView(env *azldev.Env, results []HistoryResult) bool {
	if len(results) != 1 {
		return false
	}

	switch env.DefaultReportFormat() {
	case azldev.ReportFormatTable:
		return true
	case azldev.ReportFormatCSV, azldev.ReportFormatJSON, azldev.ReportFormatMarkdown:
		return false
	default:
		return false
	}
}

// renderCardView prints a single-component card view: a vertical key/value
// header followed by an indented list of customization items (with their
// descriptions when present). This is what the user sees from
// `azldev component history <name>` and is intended to be the most useful
// view for hand-picking entries to document.
func renderCardView(writer io.Writer, result HistoryResult) {
	fmt.Fprintf(writer, "Component: %s\n", result.Name)

	if result.TomlPath != "" {
		fmt.Fprintf(writer, "  Source TOML:    %s\n", result.TomlPath)
	}

	sharedNote := ""
	if result.SharedToml {
		sharedNote = " (shared file -- count is coarse)"
	}

	latestNote := ""
	if !result.LatestCommit.IsZero() {
		// Render in UTC so the same commit shows the same date regardless of
		// the host's local timezone.
		latestNote = ", latest " + result.LatestCommit.UTC().Format(time.DateOnly)
	}

	fmt.Fprintf(writer, "  TOML commits:   %d%s%s\n", result.TomlCommits, sharedNote, latestNote)
	fmt.Fprintf(writer, "  Customizations: %d\n", result.Customizations)
	fmt.Fprintf(writer, "  FP changes:     %d\n", result.FingerprintChanges)

	// The per-commit FingerprintChangeDetails are populated for a single
	// surviving component but omitted from the card to keep it scannable;
	// point the user at -O json so the changelog records aren't a dead end.
	if result.FingerprintChanges > 0 {
		fmt.Fprintln(writer, "                  (run with -O json for per-commit details)")
	}

	if result.HasLock {
		fmt.Fprintf(writer,
			"  Lock state:     locked (manual-bump=%d, has-import=%t)\n",
			result.ManualBump, result.HasImport)
	} else {
		fmt.Fprintln(writer, "  Lock state:     no lock")
	}

	if len(result.Warnings) > 0 {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Warnings:")

		for _, warning := range result.Warnings {
			fmt.Fprintf(writer, "  - %s\n", warning)
		}
	}

	if len(result.CustomizationItems) == 0 {
		return
	}

	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Customizations:")

	for idx, item := range result.CustomizationItems {
		value := item.Value
		if value == "" {
			value = "(no value)"
		}

		fmt.Fprintf(writer, "  %d. [%s] %s\n", idx+1, item.Kind, value)

		if item.Description != "" {
			fmt.Fprintf(writer, "     %s\n", item.Description)
		}
	}
}
