// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package dirdiff

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// fileDiffChangeJSON represents a single changed line in a structured diff.
type fileDiffChangeJSON struct {
	// Line is the 1-based line number. For "add" changes it refers to the new (modified)
	// file; for "remove" changes it refers to the original file.
	Line int `json:"line"`
	// Type is "add" or "remove".
	Type string `json:"type"`
	// Content is the text of the line without the leading +/- prefix.
	Content string `json:"content"`
}

// fileDiffJSON is the JSON-serialized form of [FileDiff]. The [UnifiedDiff] field is parsed
// into per-line [fileDiffChangeJSON] records; context (unchanged) lines are omitted.
type fileDiffJSON struct {
	Path     string               `json:"path"`
	Status   FileStatus           `json:"status"`
	IsBinary bool                 `json:"isBinary,omitempty"`
	Message  string               `json:"message,omitempty"`
	Changes  []fileDiffChangeJSON `json:"changes,omitempty"`
}

// MarshalJSON implements [json.Marshaler] for [FileDiff]. It serializes the file diff
// as a structured JSON object, parsing the [UnifiedDiff] text into per-line change records.
func (f FileDiff) MarshalJSON() ([]byte, error) {
	out := fileDiffJSON{
		Path:     f.Path,
		Status:   f.Status,
		IsBinary: f.IsBinary,
		Message:  f.Message,
	}

	if f.UnifiedDiff != "" {
		out.Changes = parseUnifiedDiffChanges(f.UnifiedDiff)
	}

	data, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshaling file diff %#q:\n%w", f.Path, err)
	}

	return data, nil
}

// parseUnifiedDiffChanges parses a unified diff string into a slice of [fileDiffChangeJSON].
// It tracks line numbers from @@ hunk headers and assigns each diff line its 1-based line
// number in the appropriate file (new file for add/context changes, old file for remove changes).
func parseUnifiedDiffChanges(unifiedDiff string) []fileDiffChangeJSON {
	var (
		changes []fileDiffChangeJSON
		oldLine int
		newLine int
		inHunk  bool
	)

	for _, line := range strings.Split(unifiedDiff, "\n") {
		switch {
		case line == "":
			// Trailing empty string after final newline; skip.

		case !inHunk && (strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ")):
			// File header lines before the first hunk — Path and Status already capture this.

		case strings.HasPrefix(line, "@@"):
			inHunk = true
			oldLine, newLine = parseHunkHeader(line)

		case strings.HasPrefix(line, `\ `):
			// Diff marker such as "\ No newline at end of file" — skip without advancing counters.

		case strings.HasPrefix(line, "+"):
			changes = append(changes, fileDiffChangeJSON{
				Line:    newLine,
				Type:    "add",
				Content: strings.TrimRight(line[1:], "\r"),
			})
			newLine++

		case strings.HasPrefix(line, "-"):
			changes = append(changes, fileDiffChangeJSON{
				Line:    oldLine,
				Type:    "remove",
				Content: strings.TrimRight(line[1:], "\r"),
			})
			oldLine++

		default: // context line (leading space) — advance counters but omit from output
			oldLine++
			newLine++
		}
	}

	return changes
}

// parseHunkHeader extracts the starting line numbers from a unified diff hunk header.
// The format is "@@ -oldStart[,oldLines] +newStart[,newLines] @@[ section heading]".
func parseHunkHeader(line string) (oldStart, newStart int) {
	// Tokenize: ["@@", "-n,m", "+p,q", "@@", ...]
	// A valid hunk header has at least 4 fields: "@@", "-n,m", "+p,q", "@@".
	const minHunkHeaderFields = 4

	parts := strings.Fields(line)
	if len(parts) < minHunkHeaderFields {
		return 1, 1
	}

	parse := func(s string) int {
		s = strings.TrimPrefix(s, "-")
		s = strings.TrimPrefix(s, "+")
		n, _, _ := strings.Cut(s, ",")
		v, _ := strconv.Atoi(n)

		return v
	}

	return parse(parts[1]), parse(parts[2])
}
