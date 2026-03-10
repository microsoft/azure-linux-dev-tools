// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/stretchr/testify/assert"
)

func TestReportFormatSet(t *testing.T) {
	var fmt azldev.ReportFormat

	if assert.NoError(t, fmt.Set("table")) {
		assert.Equal(t, azldev.ReportFormatTable, fmt)
	}

	if assert.NoError(t, fmt.Set("csv")) {
		assert.Equal(t, azldev.ReportFormatCSV, fmt)
	}

	if assert.NoError(t, fmt.Set("json")) {
		assert.Equal(t, azldev.ReportFormatJSON, fmt)
	}

	if assert.NoError(t, fmt.Set("markdown")) {
		assert.Equal(t, azldev.ReportFormatMarkdown, fmt)
	}

	assert.Error(t, fmt.Set("unsupported"))
}
