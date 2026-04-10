// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
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
		// Basic forms.
		{"%autorelease", true},
		{"%{autorelease}", true},

		// Braced form with arguments (e.g., 389-ds-base).
		{"%{autorelease -n %{?with_asan:-e asan}}%{?dist}", true},
		{"%{autorelease -e asan}", true},

		// Conditional forms (e.g., gnutls, keylime-agent-rust).
		{"%{?autorelease}%{!?autorelease:1%{?dist}}", true},
		{"%{?autorelease}", true},

		// Conditional forms with a fallback value are NOT autorelease — the fallback
		// means we cannot conclusively determine that autorelease is being used.
		{"%{!?autorelease:1%{?dist}}", false},
		{"%{?autorelease:1%{?dist}}", false},

		// False positives (e.g., python-pyodbc).
		{"%{autorelease_suffix}", false},
		{"%{?autorelease_extra}", false},

		// Static release values.
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

func TestHasUserReleaseOverlay(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		overlays []projectconfig.ComponentOverlay
		expected bool
	}{
		{"no overlays", nil, false},
		{"unrelated tag", []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Version", Value: "1.0"},
		}, false},
		{"unsupported overlay type", []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlayAddSpecTag, Tag: "Release", Value: "1%{?dist}"},
		}, false},
		{"spec-set-tag", []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Release", Value: "1%{?dist}"},
		}, true},
		{"spec-update-tag", []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlayUpdateSpecTag, Tag: "Release", Value: "2%{?dist}"},
		}, true},
		{"case insensitive", []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "release", Value: "1%{?dist}"},
		}, true},
		{"mixed overlays", []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "BuildRequires", Value: "gcc"},
			{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Release", Value: "5%{?dist}"},
		}, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.expected, sources.HasUserReleaseOverlay(testCase.overlays))
		})
	}
}
