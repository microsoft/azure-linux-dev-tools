// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage,nolintlint // Tests unexported release helpers.
package magerelease

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLatestChangelogRelease(t *testing.T) {
	version, releaseDate, err := parseLatestChangelogRelease(`# Changelog

## [0.5.0] - 2026-09-21

### Added

- Package azldev for Fedora.

## [0.4.0] - 2026-09-01
`)

	require.NoError(t, err)
	assert.Equal(t, "0.5.0", version)
	assert.Equal(t, time.Date(2026, time.September, 21, 0, 0, 0, 0, time.UTC), releaseDate)
}

func TestParseLatestChangelogReleaseRejectsUndatedLatestRelease(t *testing.T) {
	_, _, err := parseLatestChangelogRelease(`## [0.5.0]

## [0.4.0] - 2026-09-01
`)

	assert.ErrorContains(t, err, "YYYY-MM-DD")
}

func TestParseRPMSpecVersion(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
		wantErr  bool
	}{
		{name: "valid", content: "Name: azldev\nVersion: 0.5.0\n", expected: "0.5.0"},
		{name: "missing", content: "Name: azldev\n", wantErr: true},
		{name: "duplicate", content: "Version: 0.4.0\nVersion: 0.5.0\n", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := parseRPMSpecVersion(test.content)
			if test.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.expected, actual)
		})
	}
}

func TestRequireRPMSpecVersion(t *testing.T) {
	require.NoError(t, requireRPMSpecVersion("Version: 0.5.0\n", "0.5.0"))

	err := requireRPMSpecVersion("Version: 0.4.0\n", "0.5.0")
	require.ErrorContains(t, err, "RPM spec version `0.4.0` does not match changelog version `0.5.0`")
}

func TestUpdateRPMSpec(t *testing.T) {
	input := `Name:           azldev
Version:        0.4.0
Release:        3%{?dist}

%changelog
* Thu Sep 17 2026 Azure Linux Dev Tools <azldev@microsoft.com> - 0.4.0-1
- Add the initial RPM package
`
	expected := `Name:           azldev
Version:        0.5.0
Release:        1%{?dist}

%changelog
* Mon Sep 21 2026 Azure Linux Dev Tools <azldev@microsoft.com> - 0.5.0-1
- Release azldev 0.5.0.

* Thu Sep 17 2026 Azure Linux Dev Tools <azldev@microsoft.com> - 0.4.0-1
- Add the initial RPM package
`

	actual, err := updateRPMSpec(input, "0.5.0", time.Date(2026, time.September, 21, 0, 0, 0, 0, time.UTC))

	require.NoError(t, err)
	assert.Equal(t, expected, actual)

	actual, err = updateRPMSpec(actual, "0.5.0", time.Date(2026, time.September, 21, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, expected, actual, "updating the same release should be idempotent")
}

func TestUpdateRPMSpecRejectsMissingSections(t *testing.T) {
	_, err := updateRPMSpec("Name: azldev\nRelease: 1%{?dist}\n%changelog\n", "0.5.0", time.Time{})
	require.ErrorContains(t, err, "'Version:'")

	_, err = updateRPMSpec("Name: azldev\nVersion: 0.4.0\nRelease: 1%{?dist}\n", "0.5.0", time.Time{})
	require.ErrorContains(t, err, "'%changelog'")
}
