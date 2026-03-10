// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"regexp"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
)

// infoPackageListRegex is compiled once at package initialization to extract
// package lists from log messages that contain "packages: [...]" patterns.
var infoPackageListRegex = regexp.MustCompile(`packages:\s*\[(.*?)\]`)

// We need a filtering mechanism because the Image Customizer 'info' log level
// is still more verbose than we want for normal azldev usage - where we want
// to display just the title of the main operations being performed.
//
// To achieve that, we define an 'allow list' of substrings that indicate
// that a log message is important enough to show to the user in normal mode.
// If the user runs azldev in verbose mode, we show all messages.
//
// The downside to this approach is that if new operations are added to the
// Image Customizer, we may need to update this list to ensure they are shown.
//
// We have https://dev.azure.com/mariner-org/polar/_workitems/edit/15516 to
// track toning down its output and then removing this filtering from azldev.
func getImageCustomizerInfoMarkers() []string {
	return []string{
		"Converting input image",
		"Regenerate initramfs file",
		"Creating full OS initrd",
		"Creating bootstrap initrd for",
		"Creating squashfs",
		"Installing files to empty image",
		"Creating UKIs",
		"Customizing partitions",
		"Setting file SELinux labels",
		"Provisioning verity",
		"Running script",
		"Removing packages",
		"Installing packages",
		"Updating packages",
		"Writing:",
	}
}

// extractMessage extracts the message content from a log line.
// A typical log line from the Image Customizer looks like this:
// time="2025-09-24T20:02:34Z" level=info msg="<message>"
// time="2025-09-24T20:02:34Z" level=debug msg="<message>".
func extractMessage(line string) string {
	const (
		prefix = " msg=\""
		suffix = `"`
	)

	start := strings.Index(line, prefix)
	if start == -1 {
		return ""
	}

	end := strings.LastIndex(line, suffix)
	if end == -1 || end <= start+len(prefix) {
		return ""
	}

	return line[start+len(prefix) : end]
}

// doesMessageContainPackages checks if the log message indicates that one or
// more packages are being installed, removed, or updated.
// A typical log message might look like:
// "Installing packages: [package1 package2 ...]"
// "Removing packages: []" (no packages)
// "Updating packages: [package1]".
func doesMessageContainPackages(message string) bool {
	const (
		expectedRegexMatches = 2
	)

	matches := infoPackageListRegex.FindStringSubmatch(message)
	if len(matches) < expectedRegexMatches {
		// expectedRegexMatches is 2: one for the full match, one for the capture group.
		return false // No match or no content group
	}

	return len(matches[1]) > 0
}

func filterImageCustomizerOutput(env *azldev.Env, line string, logMarkers []string) {
	message := extractMessage(line)
	if message == "" {
		return
	}

	switch {
	case env.Quiet():
	case env.Verbose():
		env.Event(message)
	default:
		// Default behavior: only show certain "info" messages.
		for _, logMarker := range logMarkers {
			if strings.Contains(message, logMarker) {
				// We don't display 'Installing packages:' if there are
				// no packages actually being installed.
				// Same for 'Removing packages:' and 'Updating packages:'.
				if strings.Contains(message, " packages:") {
					if !doesMessageContainPackages(message) {
						return
					}
				}

				env.Event(message)

				return
			}
		}
	}
}
