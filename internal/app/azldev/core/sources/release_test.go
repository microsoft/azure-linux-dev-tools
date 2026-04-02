// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSpecPath = "/test.spec"

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
	// GetReleaseTagValue delegates to GetSpecTagValue; verify the delegation works.
	ctx := testctx.NewCtx()

	err := fileutils.WriteFile(ctx.FS(), testSpecPath,
		[]byte("Name: test-package\nVersion: 1.0.0\nRelease: 5%{?dist}\nSummary: Test\n"), 0o644)
	require.NoError(t, err)

	result, err := sources.GetReleaseTagValue(ctx.FS(), testSpecPath)
	require.NoError(t, err)
	assert.Equal(t, "5%{?dist}", result)
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

func TestGetSpecTagValue(t *testing.T) {
	specContent := "Name: test-package\nVersion: 2.5.1\nRelease: 3%{?dist}\nSummary: Test\n"

	for _, testCase := range []struct {
		name, specContent, tag, expected string
		wantErr                          bool
	}{
		{"version tag", specContent, "Version", "2.5.1", false},
		{"name tag", specContent, "Name", "test-package", false},
		{"release tag", specContent, "Release", "3%{?dist}", false},
		{"case insensitive", specContent, "version", "2.5.1", false},
		{"missing tag", specContent, "Epoch", "", true},
		{"file not found", "", "Version", "", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := testctx.NewCtx()

			if testCase.specContent != "" {
				err := fileutils.WriteFile(ctx.FS(), testSpecPath, []byte(testCase.specContent), 0o644)
				require.NoError(t, err)
			}

			result, err := sources.GetSpecTagValue(ctx.FS(), testSpecPath, testCase.tag)
			if testCase.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, result)
			}
		})
	}
}

func TestChangelogUsesAutochangelog(t *testing.T) {
	baseSpec := "Name: test-package\nVersion: 1.0.0\nRelease: 1%{?dist}\nSummary: Test\n\n"

	staticChangelog := baseSpec +
		"%changelog\n* Mon Jan 01 2024 Test User <test@example.com> - 1.0.0-1\n- Initial build\n"

	for _, testCase := range []struct {
		name        string
		specContent string
		expected    bool
		wantErr     bool
	}{
		{"bare autochangelog", baseSpec + "%changelog\n%autochangelog\n", true, false},
		{"braced autochangelog", baseSpec + "%changelog\n%{autochangelog}\n", true, false},
		{"static changelog", staticChangelog, false, false},
		{"no changelog section", baseSpec, false, false},
		{"empty changelog section", baseSpec + "%changelog\n", false, false},
		{"file not found", "", false, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := testctx.NewCtx()

			if testCase.specContent != "" {
				err := fileutils.WriteFile(ctx.FS(), testSpecPath, []byte(testCase.specContent), 0o644)
				require.NoError(t, err)
			}

			result, err := sources.ChangelogUsesAutochangelog(ctx.FS(), testSpecPath)
			if testCase.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, result)
			}
		})
	}
}
