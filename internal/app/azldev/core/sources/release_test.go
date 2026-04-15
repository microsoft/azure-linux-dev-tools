// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReleaseUsesAutorelease(t *testing.T) {
	for _, testCase := range []struct {
		value    string
		expected bool
	}{
		{"%autorelease", true},
		{"%{autorelease}", true},
		{"1", false},
		{"1%{?dist}", false},
		{"3%{?dist}.1", false},
		{"", false},
	} {
		t.Run(testCase.value, func(t *testing.T) {
			assert.Equal(t, testCase.expected, sources.ReleaseUsesAutorelease(testCase.value))
		})
	}
}

func TestBumpStaticRelease(t *testing.T) {
	for _, testCase := range []struct {
		name, value string
		commits     int
		expected    string
		wantErr     bool
	}{
		{"simple integer", "1", 3, "4", false},
		{"with dist tag", "1%{?dist}", 2, "3%{?dist}", false},
		{"larger base", "10%{?dist}", 5, "15%{?dist}", false},
		{"single commit", "1%{?dist}", 1, "2%{?dist}", false},
		{"no leading int", "%{?dist}", 1, "", true},
		{"empty string", "", 1, "", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := sources.BumpStaticRelease(testCase.value, testCase.commits)
			if testCase.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, result)
			}
		})
	}
}

func TestGetReleaseTagValue(t *testing.T) {
	makeSpec := func(release string) string {
		return "Name: test-package\nVersion: 1.0.0\nRelease: " + release + "\nSummary: Test\n"
	}

	for _, testCase := range []struct {
		name, specContent, expected string
		wantErr                     bool
	}{
		{"static with dist", makeSpec("1%{?dist}"), "1%{?dist}", false},
		{"autorelease", makeSpec("%autorelease"), "%autorelease", false},
		{"braced autorelease", makeSpec("%{autorelease}"), "%{autorelease}", false},
		{"no release tag", "Name: test-package\nVersion: 1.0.0\nSummary: Test\n", "", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := testctx.NewCtx()
			specPath := "/test.spec"

			err := fileutils.WriteFile(ctx.FS(), specPath, []byte(testCase.specContent), 0o644)
			require.NoError(t, err)

			result, err := sources.GetReleaseTagValue(ctx.FS(), specPath)
			if testCase.wantErr {
				require.ErrorIs(t, err, spec.ErrNoSuchTag)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, result)
			}
		})
	}
}

func TestGetReleaseTagValue_FileNotFound(t *testing.T) {
	ctx := testctx.NewCtx()
	_, err := sources.GetReleaseTagValue(ctx.FS(), "/nonexistent.spec")
	require.Error(t, err)
}
