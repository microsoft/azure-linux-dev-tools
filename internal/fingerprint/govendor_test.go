// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/fingerprint"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoVendorConfigFileFingerprint(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/checkout-one/vendor.toml": "[archive]\ntidy = false\n",
		"/checkout-two/vendor.toml": "[archive]\ntidy = false\n",
		"/empty.toml":               "",
	})
	component := projectconfig.ComponentConfig{GoVendor: &projectconfig.GoVendorConfig{
		Enabled: lo.ToPtr(true), ConfigFile: "/checkout-one/vendor.toml",
	}}
	compute := func(config projectconfig.ComponentConfig) *fingerprint.ComponentIdentity {
		t.Helper()

		result, err := fingerprint.ComputeIdentity(ctx.FS(), config, testReleaseVer, fingerprint.IdentityOptions{})
		require.NoError(t, err)

		return result
	}
	initial := compute(component)
	require.NotEmpty(t, initial.Inputs.GoVendorConfigHash)
	assert.Equal(t, "/checkout-one/vendor.toml", component.GoVendor.ConfigFile)
	component.GoVendor.ConfigFile = "/checkout-two/vendor.toml"
	assert.Equal(t, initial.Fingerprint, compute(component).Fingerprint, "checkout location must not affect identity")

	require.NoError(t, fileutils.WriteFile(ctx.FS(), component.GoVendor.ConfigFile,
		[]byte("[archive]\ntidy = true\n"), fileperms.PublicFile))
	assert.NotEqual(t, initial.Fingerprint, compute(component).Fingerprint,
		"file-only edits must invalidate the fingerprint")
	component.GoVendor.ConfigFile = "/empty.toml"
	emptyFile := compute(component)
	component.GoVendor.ConfigFile = ""
	noFile := compute(component)
	assert.Empty(t, noFile.Inputs.GoVendorConfigHash)
	assert.NotEqual(t, emptyFile.Fingerprint, noFile.Fingerprint, "an empty override is not upstream fallback")
}

func TestGoVendorConfigFileFingerprintErrors(t *testing.T) {
	ctx := newTestFS(t, map[string]string{"/bad.toml": "["})
	for _, path := range []string{"/missing.toml", "/bad.toml"} {
		_, err := fingerprint.ComputeIdentity(ctx.FS(), projectconfig.ComponentConfig{
			GoVendor: &projectconfig.GoVendorConfig{Enabled: lo.ToPtr(true), ConfigFile: path},
		}, testReleaseVer, fingerprint.IdentityOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), path)
	}
}
