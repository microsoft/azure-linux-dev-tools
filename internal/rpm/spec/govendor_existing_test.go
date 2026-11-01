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

func existingVendorSpec() string {
	return strings.Join([]string{
		"Name: app", "Version: 1.2", "Release: %autorelease", "Summary: Application",
		"License: MIT AND Apache-2.0",
		"Source0: app-%{version}.tar.gz",
		"Source5: app-%{version}-vendor.tar.bz2",
		"Source8: go-vendor-tools.toml",
		"Source9: app.conf",
		"BuildRequires: go-vendor-tools", "Requires: runtime-helper",
		"%description", "Application", "%prep", "%goprep -p1",
		"tar -xf %{S:5}", "sed -i 's/old/new/' go.mod", "cp %{S:9} .",
		"%generate_buildrequires", "%go_vendor_license_buildrequires -c %{S:8}",
		"%build", "%global gomodulesmode GO111MODULE=on", "%gobuild -o app .",
		"%install", "%go_vendor_license_install -c %{S:8}",
		"install -D app %{buildroot}%{_bindir}/app",
		"%check", "%go_vendor_license_check -c %{S:8}", "%gocheck2 -t integration",
		"%files -f %{go_vendor_license_filelist}", "%{_bindir}/app", "%changelog", "%autochangelog", "",
	}, "\n")
}

func TestExistingGoVendoringPreservesPackaging(t *testing.T) {
	for _, version := range []string{"1.2", "2.4"} {
		input := strings.ReplaceAll(existingVendorSpec(), "Version: 1.2", "Version: "+version)
		result, err := spec.ConvertGoVendor([]byte(input), spec.GoVendorOptions{
			ArchiveName: "app-vendor.tar.bz2", ConfigName: "azldev-go-vendor.toml",
		})
		require.NoError(t, err)
		assert.True(t, result.AllowReplacement)
		assert.Equal(t, "go-vendor-tools.toml", result.ConfigName)
		assert.Equal(t, ".tar.bz2", result.ArchiveSuffix)
		assert.Equal(t, "MIT AND Apache-2.0", result.ExpectedLicense)

		for _, line := range strings.Split(input, "\n") {
			assert.Contains(t, string(result.Spec), line)
		}

		assert.NotContains(t, string(result.Spec), "azldev-go-vendor.toml")
		assert.NotContains(t, string(result.Spec), "azldev-go-module-root")
		assert.Contains(t, string(result.Spec), "GOPROXY=off")
		assert.NotContains(t, string(result.PrepSpec), "Source5:")
		assert.NotContains(t, string(result.PrepSpec), "tar -xf %{S:5}")
		assert.Contains(t, string(result.PrepSpec), "sed -i 's/old/new/' go.mod")
		assert.Contains(t, string(result.PrepSpec), "%{shescape:app-%{version}-vendor.tar.bz2}")
		assert.Contains(t, string(result.PrepSpec), `pwd -P > "%{_topdir}/azldev-go-module-root"`)
	}
}

func TestExistingGoVendoringRejectsAmbiguousInputs(t *testing.T) {
	for name, input := range map[string]string{
		"multiple extractions": strings.ReplaceAll(existingVendorSpec(), "%goprep -p1", "%goprep -p1\ntar -xf %{S:9}"),
		"no extraction":        strings.ReplaceAll(existingVendorSpec(), "tar -xf %{S:5}", ""),
		"mismatched config": strings.ReplaceAll(existingVendorSpec(),
			"%go_vendor_license_check -c %{S:8}", "%go_vendor_license_check -c %{S:9}"),
		"dynamic config":      strings.ReplaceAll(existingVendorSpec(), "go-vendor-tools.toml", "%{name}.toml"),
		"unknown compression": strings.ReplaceAll(existingVendorSpec(), "-vendor.tar.bz2", "-vendor.zip"),
		"macro license":       strings.ReplaceAll(existingVendorSpec(), "License: MIT AND Apache-2.0", "License: %{license}"),
		"missing source":      strings.ReplaceAll(existingVendorSpec(), "Source5:", "Source6:"),
		"conditional extraction": strings.ReplaceAll(existingVendorSpec(),
			"tar -xf %{S:5}", "%if 1\ntar -xf %{S:5}\n%endif"),
		"conditional source": strings.ReplaceAll(existingVendorSpec(),
			"Source5: app-%{version}-vendor.tar.bz2", "%if 1\nSource5: app-%{version}-vendor.tar.bz2\n%endif"),
		"conditional license": strings.ReplaceAll(existingVendorSpec(),
			"License: MIT AND Apache-2.0", "%if 1\nLicense: MIT AND Apache-2.0\n%endif"),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := spec.ConvertGoVendor([]byte(input), spec.GoVendorOptions{
				ArchiveName: "app-vendor.tar.bz2", ConfigName: "azldev-go-vendor.toml",
			})
			require.Error(t, err)
			assert.Nil(t, result)
		})
	}
}
