// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/fingerprint"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoVendorOriginValidation(t *testing.T) {
	for name, ref := range map[string]projectconfig.SourceFileReference{
		"discovered": {Origin: projectconfig.Origin{Type: projectconfig.OriginTypeGoVendor}},
		"explicit": {Filename: "vendor.tar.bz2", Origin: projectconfig.Origin{
			Type:       projectconfig.OriginTypeGoVendor,
			ConfigFile: "vendor.toml", ModuleDir: "cmd/app", PrepPackages: []string{"patch"},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			file := projectconfig.ConfigFile{Components: map[string]projectconfig.ComponentConfig{
				"app": {SourceFiles: []projectconfig.SourceFileReference{ref}},
			}}
			require.NoError(t, file.Validate())
		})
	}

	for name, origin := range map[string]projectconfig.Origin{
		"traversal":    {Type: projectconfig.OriginTypeGoVendor, ModuleDir: "../bad"},
		"package":      {Type: projectconfig.OriginTypeGoVendor, PrepPackages: []string{"--bad"}},
		"uri":          {Type: projectconfig.OriginTypeGoVendor, Uri: "https://example.com"},
		"wrong origin": {Type: projectconfig.OriginTypeURI, Uri: "https://example.com", ConfigFile: "vendor.toml"},
	} {
		t.Run(name, func(t *testing.T) {
			file := projectconfig.ConfigFile{Components: map[string]projectconfig.ComponentConfig{
				"app": {SourceFiles: []projectconfig.SourceFileReference{{Filename: "vendor.tar.gz", Origin: origin}}},
			}}
			require.Error(t, file.Validate())
		})
	}
}

func TestGoVendorSettingsFingerprint(t *testing.T) {
	ctx := testctx.NewCtx()
	config := projectconfig.ComponentConfig{SourceFiles: []projectconfig.SourceFileReference{{
		Origin: projectconfig.Origin{Type: projectconfig.OriginTypeGoVendor, ConfigFile: "vendor.toml"},
	}}}
	resolved := config.WithAbsolutePaths("/project")
	assert.Equal(t, "/project/vendor.toml", resolved.SourceFiles[0].Origin.ConfigFile)
	assert.Equal(t, "vendor.toml", config.SourceFiles[0].Origin.ConfigFile)

	for _, path := range []string{"/project/vendor.toml", "/elsewhere/vendor.toml"} {
		require.NoError(t, fileutils.WriteFile(ctx.FS(), path, []byte("[archive]\ntidy = false\n"), fileperms.PublicFile))
	}

	first, err := fingerprint.ComputeIdentity(ctx.FS(), *resolved, "", fingerprint.IdentityOptions{})
	require.NoError(t, err)

	relocated := config.WithAbsolutePaths("/elsewhere")
	second, err := fingerprint.ComputeIdentity(ctx.FS(), *relocated, "", fingerprint.IdentityOptions{})
	require.NoError(t, err)
	assert.Equal(t, first.Fingerprint, second.Fingerprint)
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/project/vendor.toml", []byte(""), fileperms.PublicFile))
	third, err := fingerprint.ComputeIdentity(ctx.FS(), *resolved, "", fingerprint.IdentityOptions{})
	require.NoError(t, err)
	assert.NotEqual(t, first.Fingerprint, third.Fingerprint)
}
