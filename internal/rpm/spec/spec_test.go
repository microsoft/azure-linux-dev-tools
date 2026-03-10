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
}
