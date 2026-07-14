// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders_test

import (
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSpecEVR_BaseTags(t *testing.T) {
	content := `Name: grub2
Epoch: 1
Version: 2.12
Release: 42%{?dist}
Summary: Bootloader

%description
Bootloader.
`

	evr, err := sourceproviders.ParseSpecEVR(strings.NewReader(content))
	require.NoError(t, err)
	assert.Equal(t, "grub2", evr.Name)
	assert.Equal(t, "1", evr.Epoch)
	assert.Equal(t, "2.12", evr.Version)
	assert.Equal(t, "42%{?dist}", evr.Release,
		"Release must be captured raw; %{?dist} preserved")
}

func TestParseSpecEVR_MissingEpochYieldsEmpty(t *testing.T) {
	content := `Name: curl
Version: 8.7.0
Release: 3%{?dist}

%description
curl.
`

	evr, err := sourceproviders.ParseSpecEVR(strings.NewReader(content))
	require.NoError(t, err)
	assert.Equal(t, "curl", evr.Name)
	assert.Empty(t, evr.Epoch,
		"missing Epoch: tag must produce empty string, not an error")
	assert.Equal(t, "8.7.0", evr.Version)
}

// TestParseSpecEVR_SubpackageTagsIgnored asserts we only pick up tags from the
// base (unnamed) package, not from any %package subsection. This keeps the
// captured values aligned with the top-level NEVR that would appear in the
// SRPM filename.
func TestParseSpecEVR_SubpackageTagsIgnored(t *testing.T) {
	content := `Name: base
Version: 1.0
Release: 1

%package devel
Summary: Devel bits
Version: 9.9.9

%description
base.

%description devel
devel.
`

	evr, err := sourceproviders.ParseSpecEVR(strings.NewReader(content))
	require.NoError(t, err)
	assert.Equal(t, "base", evr.Name)
	assert.Equal(t, "1.0", evr.Version,
		"only tags in the base package section should be captured")
}

func TestParseSpecEVRFromFile_OpensAndParses(t *testing.T) {
	filesystem := afero.NewMemMapFs()

	require.NoError(t, fileutils.WriteFile(filesystem, "/spec/foo.spec",
		[]byte("Name: foo\nVersion: 1.2.3\nRelease: 4\n"), fileperms.PublicFile))

	evr, err := sourceproviders.ParseSpecEVRFromFile(filesystem, "/spec/foo.spec")
	require.NoError(t, err)
	assert.Equal(t, "foo", evr.Name)
	assert.Equal(t, "1.2.3", evr.Version)
	assert.Equal(t, "4", evr.Release)
}

func TestParseSpecEVRFromFile_MissingFile(t *testing.T) {
	_, err := sourceproviders.ParseSpecEVRFromFile(afero.NewMemMapFs(), "/nope.spec")
	require.Error(t, err)
}
