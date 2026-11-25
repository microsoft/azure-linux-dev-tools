// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import "strings"

// renderBumpspecSubject is the default rpmdev-bumpspec changelog text for
// lockless renders.
const renderBumpspecSubject = "temporary commit message"

// locklessBumpspecComment returns the rpmdev-bumpspec changelog text: message
// (or the default subject when blank) followed by one '- ' line per upstream
// change.
func locklessBumpspecComment(message, changes string) string {
	if strings.TrimSpace(message) == "" {
		message = renderBumpspecSubject
	}

	changes = strings.TrimSpace(changes)
	if changes == "" {
		return message
	}

	return message + "\n- " + strings.ReplaceAll(changes, "\n", "\n- ")
}
