// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders_test

import (
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
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
