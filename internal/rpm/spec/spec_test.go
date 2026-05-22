// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/stretchr/testify/assert"
)

func TestGetPackageNameFromSectionHeader(t *testing.T) {
	assert.Empty(t, spec.GetPackageNameFromSectionHeader([]string{"%package"}))
	assert.Empty(t, spec.GetPackageNameFromSectionHeader([]string{"%package", "-q"}))
	assert.Empty(t, spec.GetPackageNameFromSectionHeader([]string{"%package", "-p", "some-token"}))
	assert.Empty(t, spec.GetPackageNameFromSectionHeader([]string{"%package", "-x"}))
	assert.Empty(t, spec.GetPackageNameFromSectionHeader([]string{"%package", "-f", "some-token"}))

	assert.Equal(t, "foo", spec.GetPackageNameFromSectionHeader([]string{"%package", "foo"}))

	assert.Equal(t,
		"foo",
		spec.GetPackageNameFromSectionHeader([]string{"%package", "-n", "foo"}))

	// -l flag (localized descriptions) should be skipped, not treated as package name
	assert.Equal(t, "foo", spec.GetPackageNameFromSectionHeader([]string{"%description", "-l", "fr", "foo"}))
	assert.Empty(t, spec.GetPackageNameFromSectionHeader([]string{"%description", "-l", "fr"}))
	assert.Equal(t, "foo", spec.GetPackageNameFromSectionHeader([]string{"%description", "-l", "de", "-n", "foo"}))

	// -- trigger terminator: everything after -- is the trigger condition, not the package name
	assert.Empty(t, spec.GetPackageNameFromSectionHeader([]string{"%triggerin", "--", "devel"}))
	assert.Equal(t, "devel",
		spec.GetPackageNameFromSectionHeader([]string{"%triggerin", "devel", "--", "foo"}))
	assert.Equal(t, "test-devel",
		spec.GetPackageNameFromSectionHeader([]string{"%triggerin", "-n", "test-devel", "--", "foo"}))
	assert.Empty(t,
		spec.GetPackageNameFromSectionHeader([]string{"%triggerin", "-p", "/bin/sh", "--", "foo"}))
	assert.Equal(t, "devel",
		spec.GetPackageNameFromSectionHeader([]string{"%triggerin", "devel", "-p", "/bin/sh", "--", "foo"}))

	// -P flag (file trigger priority) should be skipped
	assert.Empty(t,
		spec.GetPackageNameFromSectionHeader([]string{"%filetriggerin", "-P", "100", "--", "/usr/lib"}))
	assert.Equal(t, "devel",
		spec.GetPackageNameFromSectionHeader([]string{"%filetriggerin", "devel", "-P", "100", "--", "/usr/lib"}))
}
