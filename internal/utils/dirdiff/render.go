// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package dirdiff

import (
	"strings"

	"github.com/fatih/color"
)

// String renders the complete diff result as a single unified diff string.
// Returns an empty string when there are no differences.
func (r *DiffResult) String() string {
	if len(r.Files) == 0 {
		return ""
	}

	var buf strings.Builder

	for i := range r.Files {
		writeFileDiff(&buf, &r.Files[i])
	}

	return buf.String()
}

// ColorString renders the complete diff result with ANSI color codes for terminal display.
// File headers (--- / +++) are bold, hunk headers (@@ ... @@) are cyan, added lines (+)
// are green, removed lines (-) are red, and context lines are uncolored. Returns an empty
// string when there are no differences.
func (r *DiffResult) ColorString() string {
	if len(r.Files) == 0 {
		return ""
	}

	var buf strings.Builder

	for i := range r.Files {
		colorizeFileDiff(&buf, &r.Files[i])
	}

	return buf.String()
}

// writeFileDiff writes the plain-text diff for a single file to buf.
func writeFileDiff(buf *strings.Builder, fileDiff *FileDiff) {
	if fileDiff.Message != "" {
		buf.WriteString(fileDiff.Message)
		buf.WriteByte('\n')

		return
	}

	buf.WriteString(fileDiff.UnifiedDiff)
}

// colorizeFileDiff writes the ANSI-colorized diff for a single file to buf.
// Lines are colorized by their unified-diff prefix character.
func colorizeFileDiff(buf *strings.Builder, fileDiff *FileDiff) {
	bold := color.New(color.Bold)
	bold.EnableColor()

	cyan := color.New(color.FgCyan)
	cyan.EnableColor()

	green := color.New(color.FgGreen)
	green.EnableColor()

	red := color.New(color.FgRed)
	red.EnableColor()

	if fileDiff.Message != "" {
		buf.WriteString(bold.Sprint(fileDiff.Message))
		buf.WriteByte('\n')

		return
	}

	for _, line := range strings.Split(fileDiff.UnifiedDiff, "\n") {
		if line == "" {
			continue // trailing empty string after the final newline; skip
		}

		switch {
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"):
			buf.WriteString(bold.Sprint(line))
		case strings.HasPrefix(line, "@@"):
			buf.WriteString(cyan.Sprint(line))
		case strings.HasPrefix(line, "+"):
			buf.WriteString(green.Sprint(line))
		case strings.HasPrefix(line, "-"):
			buf.WriteString(red.Sprint(line))
		default:
			buf.WriteString(line)
		}

		buf.WriteByte('\n')
	}
}
