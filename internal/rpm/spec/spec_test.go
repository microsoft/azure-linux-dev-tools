// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestOpenSpec_EmptyInput(t *testing.T) {
	sf, err := spec.OpenSpec(strings.NewReader(""))
	require.NoError(t, err)

	// Empty spec is parseable but has no tags.
	_, err = sf.GetTag("", "Name")
	require.ErrorIs(t, err, spec.ErrNoSuchTag)
}

func TestOpenSpec_BinaryContent(t *testing.T) {
	// Binary content should be parseable (lines are just raw strings).
	binaryData := "\x00\x01\x02\xFF\xFE\x89PNG\r\n"
	sf, err := spec.OpenSpec(strings.NewReader(binaryData))
	require.NoError(t, err)

	// Should round-trip without error.
	var buf bytes.Buffer
	require.NoError(t, sf.Serialize(&buf))
}

func TestOpenSpec_CRLFLineEndings(t *testing.T) {
	input := "Name: test\r\nVersion: 1.0\r\nRelease: 1\r\n"
	specFile, err := spec.OpenSpec(strings.NewReader(input))
	require.NoError(t, err)

	// UpdateExistingTag should still find tags despite \r in values.
	err = specFile.UpdateExistingTag("", "Name", "updated")
	require.NoError(t, err)
}

func TestOpenSpec_NoNameTag(t *testing.T) {
	// A spec with no Name tag is structurally valid for OpenSpec (it just stores lines).
	// Operations should handle it gracefully.
	sf, err := spec.OpenSpec(strings.NewReader("Version: 1.0\nRelease: 1\n"))
	require.NoError(t, err)

	// Attempting to update a non-existent Name tag should return ErrNoSuchTag.
	err = sf.UpdateExistingTag("", "Name", "test")
	require.ErrorIs(t, err, spec.ErrNoSuchTag)
}

func TestOpenSpec_DuplicateSectionHeaders(t *testing.T) {
	input := `Name: test
Version: 1.0

%description
First description.

%description
Second description.
`
	specFile, err := spec.OpenSpec(strings.NewReader(input))
	require.NoError(t, err)

	// Both sections should be removable.
	err = specFile.RemoveSection("%description", "")
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, specFile.Serialize(&buf))

	// Both %description sections should be gone.
	assert.NotContains(t, buf.String(), "%description")
}

func TestSetTag_NonExistentPackage(t *testing.T) {
	specFile, err := spec.OpenSpec(strings.NewReader("Name: test\nVersion: 1.0\n"))
	require.NoError(t, err)

	// Setting a tag on a non-existent package should fail with ErrSectionNotFound.
	err = specFile.SetTag("nonexistent-pkg", "Summary", "test")
	require.ErrorIs(t, err, spec.ErrSectionNotFound)
}

func TestAddPatchEntry_EmptySpec(t *testing.T) {
	specFile, err := spec.OpenSpec(strings.NewReader("Name: test\n"))
	require.NoError(t, err)

	// Adding a patch entry to a minimal spec should succeed (creates Patch0 tag).
	err = specFile.AddPatchEntry("", "fix-build.patch")
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, specFile.Serialize(&buf))
	assert.Contains(t, buf.String(), "Patch0: fix-build.patch")
}

func TestRemovePatchEntry_NoPatches(t *testing.T) {
	specFile, err := spec.OpenSpec(strings.NewReader("Name: test\nVersion: 1.0\n"))
	require.NoError(t, err)

	// Removing from a spec with no patches should fail.
	err = specFile.RemovePatchEntry("*.patch")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no patches matching")
}
