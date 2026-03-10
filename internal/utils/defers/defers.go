// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package defers

import (
	"fmt"
)

// HandleDeferError calls a deferred function and updates the provided error
// pointer with any error that occurs during the call.
// Argument requirements:
//   - The deferred function MUST NOT be nil; if it is nil, an error is logged.
//   - The error pointer MUST NOT be nil; if it is nil, an error is logged.
//     The passed error will be preserved but may be wrapped in a new error
//     that includes the error from the deferred function.
func HandleDeferError(deferredFunc func() error, errPtr *error) {
	// Issue #237: ignoring nil input - allowing the code to panic.
	// We want to handle panics globally with a user-friendly message.
	fnErr := deferredFunc()

	if *errPtr == nil {
		*errPtr = fnErr

		return
	}

	if fnErr != nil {
		*errPtr = fmt.Errorf("error in deferred function:\n%w; original error:\n%w", fnErr, *errPtr)
	}
}
