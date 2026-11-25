// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec_test

import (
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func vendorSpec() string {
	return strings.Join([]string{
		"Name: app", "Version: 1", "Release: 1", "License: MIT",
		"Source0: app.tar.gz", "Source5: app-%{version}-vendor.tar.bz2", "Source8: go-vendor-tools.toml",
		"%description", "App", "%prep", "%autosetup -p1",
		"tar -xf %{S:5}", "sed -i s/old/new/ go.mod",
		"%build", "make build", "%install", "%go_vendor_license_install -c %{S:8}",
		"%files", "/usr/bin/app", "",
	}, "\n")
}

func TestPrepareVendorSource(t *testing.T) {
	input := vendorSpec()
	plan, err := spec.PrepareVendorSource([]byte(input), "app-1-vendor.tar.bz2")
	require.NoError(t, err)
	assert.Equal(t, "go-vendor-tools.toml", plan.ConfigName)
	assert.NotContains(t, string(plan.Spec), "Source5:")
	assert.NotContains(t, string(plan.Spec), "tar -xf %{S:5}")
	assert.Contains(t, string(plan.Spec), "Source0: app.tar.gz")
	assert.Contains(t, string(plan.Spec), "sed -i s/old/new/ go.mod")
	assert.Contains(t, string(plan.Spec), "%{shescape:app-%{version}-vendor.tar.bz2}")
}

func TestPrepareVendorSourceRejectsAmbiguity(t *testing.T) {
	for name, input := range map[string]string{
		"missing prep":      strings.ReplaceAll(vendorSpec(), "%prep", "%check"),
		"multiple":          strings.ReplaceAll(vendorSpec(), "tar -xf %{S:5}", "tar -xf %{S:5}\ntar -xf %{S:8}"),
		"secondary":         strings.ReplaceAll(vendorSpec(), "%autosetup -p1", "%autosetup -a 5"),
		"conditional":       strings.ReplaceAll(vendorSpec(), "tar -xf %{S:5}", "%if 1\ntar -xf %{S:5}\n%endif"),
		"flags":             strings.ReplaceAll(vendorSpec(), "tar -xf", "tar -xjf"),
		"opaque prep macro": strings.ReplaceAll(vendorSpec(), "%autosetup -p1", "%unpack_secondary"),
		"braced prep macro": strings.ReplaceAll(vendorSpec(), "%autosetup -p1", "%{unpack_secondary}"),
		"missing source":    strings.ReplaceAll(vendorSpec(), "%{S:5}", "%{S:6}"),
		"conditional source": strings.ReplaceAll(vendorSpec(), "Source5: app-%{version}-vendor.tar.bz2",
			"%if 1\nSource5: app-%{version}-vendor.tar.bz2\n%endif"),
		"duplicate source": strings.ReplaceAll(vendorSpec(), "Source5:", "Source0:"),
		"config macro":     strings.ReplaceAll(vendorSpec(), "go-vendor-tools.toml", "%{name}.toml"),
		"conflicting config": strings.ReplaceAll(vendorSpec(), "%files",
			"%go_vendor_license_check -c %{S:1}\n%files"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := spec.PrepareVendorSource([]byte(input), "vendor.tar.bz2")
			require.Error(t, err)
		})
	}

	for _, name := range []string{"", "../outside.tar.gz", "%{name}.tar.gz", "bad\nname"} {
		_, err := spec.PrepareVendorSource([]byte(vendorSpec()), name)
		require.Error(t, err)
	}

	plan, err := spec.PrepareVendorSource([]byte(vendorSpec()), "different.tar.bz2")
	require.NoError(t, err)
	assert.Contains(t, string(plan.Spec), "test %{shescape:app-%{version}-vendor.tar.bz2} = %{shescape:different.tar.bz2}")
}
