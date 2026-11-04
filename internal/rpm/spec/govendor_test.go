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

func vendorInput() string {
	return strings.Join([]string{
		"# Upstream customization",
		"%global goipath example.com/app",
		"Name: app",
		"Version: 1.2",
		"Release: %autorelease",
		"Summary: App",
		"License: MIT",
		"Source0: app-%{version}.tar.gz",
		"Source1: app.service",
		"Source9999: app.macros",
		"Patch0: fix.patch",
		"BuildRequires: golang(example.com/dep) >= 1.0",
		"BuildRequires: gcc",
		"Requires: systemd",
		"",
		"%description",
		"App description",
		"%package tools",
		"Summary: Tools",
		"Requires: app = %{version}-%{release}",
		"%description tools",
		"Tool description",
		"%prep",
		"%goprep -p1",
		"sed -i 's/old/new/' go.mod",
		"%generate_buildrequires",
		"%go_generate_buildrequires",
		"echo custom-build-dependency",
		"%build",
		"%gobuild -o app %{goipath}",
		"%install",
		"install -D app %{buildroot}%{_bindir}/app",
		"%check",
		"%if %{with check}",
		"%gocheck",
		"./custom-test",
		"%endif",
		"%files",
		"%{_bindir}/app",
		"%files tools",
		"%{_bindir}/tool",
		"%changelog",
		"%autochangelog",
		"",
	}, "\n")
}

func TestConvertGoVendorPreservesUpstream(t *testing.T) {
	for _, version := range []string{"1.2", "2.7"} {
		t.Run(version, func(t *testing.T) {
			input := strings.ReplaceAll(vendorInput(), "Version: 1.2", "Version: "+version)
			result, err := spec.ConvertGoVendor([]byte(input), spec.GoVendorOptions{
				ArchiveName: "app-vendor.tar.bz2", ConfigName: "vendor.toml",
			})
			require.NoError(t, err)

			output := string(result.Spec)
			for _, line := range []string{
				"# Upstream customization", "Version: " + version, "Release: %autorelease",
				"Source0: app-%{version}.tar.gz", "Source1: app.service", "Source9999: app.macros",
				"Patch0: fix.patch", "BuildRequires: gcc", "Requires: systemd",
				"%package tools", "Requires: app = %{version}-%{release}",
				"%goprep -p1", "sed -i 's/old/new/' go.mod", "echo custom-build-dependency",
				"%gobuild -o app %{goipath}", "install -D app %{buildroot}%{_bindir}/app",
				"%if %{with check}\n" +
					`( cd "$(cat "%{_topdir}/azldev-go-module-root")"` + "\n%gotest ./...\n)\n./custom-test\n%endif",
				"%files tools\n%{_bindir}/tool", "%autochangelog",
				"Source2: app-vendor.tar.bz2", "Source3: vendor.toml", "tar -xf %{SOURCE2}",
				"%go_vendor_license_buildrequires -c %{SOURCE3}",
				"%go_vendor_license_check -c %{SOURCE3}",
				"%go_vendor_license_install -c %{SOURCE3}",
				"%files -f %{go_vendor_license_filelist}",
				"GOPROXY=off", "-mod=vendor", "%global gomodulesmode GO111MODULE=on",
			} {
				assert.Contains(t, output, line)
			}

			assert.NotContains(t, output, "BuildRequires: golang(")
			assert.NotContains(t, output, "%go_generate_buildrequires")
			assert.Contains(t, output, `pwd -P > "%{_topdir}/azldev-go-module-root"`)
			assert.Contains(t, output, `%global go_vendor_license_filelist() %{_topdir}/azldev-go-vendor-licenses.list`)
			assert.Contains(t, string(result.PrepSpec), "Release: %autorelease")
			assert.Contains(t, string(result.PrepSpec), `pwd -P > "%{_topdir}/azldev-go-module-root"`)
			assert.Contains(t, string(result.PrepSpec), "sed -i 's/old/new/' go.mod")
			assert.NotContains(t, string(result.PrepSpec), "Source2: app-vendor.tar.bz2")
			assert.Contains(t, string(result.PrepSpec), "%{shescape:app-vendor.tar.bz2}")
			assert.False(t, result.AllowReplacement)
			assert.Empty(t, result.ExpectedLicense)
			assert.Contains(t, string(result.PrepSpec), "%autochangelog")

			_, err = spec.OpenSpec(strings.NewReader(output), spec.WithEditor(spec.EditorStructural))
			require.NoError(t, err)
		})
	}
}

func TestConvertGoVendorRejectsAmbiguity(t *testing.T) {
	for name, input := range map[string]string{
		"development subpackages": strings.ReplaceAll(vendorInput(), "%package tools", "%gopkg\n%package tools"),
		"mixed requirements": strings.ReplaceAll(vendorInput(),
			"golang(example.com/dep) >= 1.0", "gcc, golang(example.com/dep)"),
		"check flags": strings.ReplaceAll(vendorInput(), "%gocheck", "%gocheck -z foo"),
		"generator shell": strings.ReplaceAll(vendorInput(),
			"%go_generate_buildrequires", "%go_generate_buildrequires | cat"),
		"conditional prep": strings.ReplaceAll(vendorInput(),
			"%prep\n%goprep -p1", "%if 1\n%prep\n%goprep -p1\n%endif"),
		"module mode conflict":   "%global gomodulesmode GO111MODULE=off\n" + vendorInput(),
		"module mode override":   "%global gomodulesmode GO111MODULE=on\n" + vendorInput(),
		"license check override": "%global go_vendor_license_check_disable 1\n" + vendorInput(),
		"existing vendor config": strings.ReplaceAll(vendorInput(), "app.service", "vendor.toml"),
		"missing install":        strings.ReplaceAll(vendorInput(), "%install", "# install"),
		"macro source index":     strings.ReplaceAll(vendorInput(), "Source1:", "Source%{number}:"),
		"already vendored":       strings.ReplaceAll(vendorInput(), "%gocheck", "%go_vendor_license_check"),
		"braced generator": strings.ReplaceAll(vendorInput(),
			"%go_generate_buildrequires", "%{go_generate_buildrequires}"),
		"braced devel macro":        strings.ReplaceAll(vendorInput(), "%package tools", "%{gopkg}\n%package tools"),
		"hidden generator":          "%global dependencies %{go_generate_buildrequires}\n" + vendorInput(),
		"go filelist":               strings.ReplaceAll(vendorInput(), "%files\n", "%files -f %{gofilelist}\n"),
		"unsupported after comment": "# unrelated comment\n%gopkg\n" + vendorInput(),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := spec.ConvertGoVendor([]byte(input), spec.GoVendorOptions{
				ArchiveName: "app-vendor.tar.bz2", ConfigName: "vendor.toml",
			})
			require.Error(t, err)
			assert.Nil(t, result)
		})
	}
}

func TestConvertGoVendorModuleDirAndStaticRequires(t *testing.T) {
	input := strings.ReplaceAll(vendorInput(),
		"%generate_buildrequires\n%go_generate_buildrequires\necho custom-build-dependency\n", "")
	input = strings.ReplaceAll(input, "%changelog\n%autochangelog\n", "")
	result, err := spec.ConvertGoVendor([]byte(input), spec.GoVendorOptions{
		ArchiveName: "app-vendor.tar.bz2", ConfigName: "vendor.toml", ModuleDir: "cmd/app",
	})
	require.NoError(t, err)
	assert.Contains(t, string(result.Spec), "tar -xf %{SOURCE2} -C cmd/app")
	assert.Contains(t, string(result.Spec), "cd cmd/app\n%go_vendor_license_install")
	assert.Contains(t, string(result.Spec),
		`( cd "$(cat "%{_topdir}/azldev-go-module-root")"`+"\ncd cmd/app\n%gotest ./...\n)")
	assert.Contains(t, string(result.Spec), `( cd "$(cat "%{_topdir}/azldev-go-module-root")"`)
	assert.Contains(t, string(result.Spec), "%generate_buildrequires\n")
}

func TestConvertGoVendorRejectsUnsafeOptions(t *testing.T) {
	for _, options := range []spec.GoVendorOptions{
		{ArchiveName: "../vendor.tar.bz2", ConfigName: "config"},
		{ArchiveName: "vendor", ConfigName: "%{name}"},
		{ArchiveName: "vendor", ConfigName: "config", ModuleDir: "../escape"},
		{ArchiveName: "vendor", ConfigName: "config", ModuleDir: "%{bad}"},
	} {
		_, err := spec.ConvertGoVendor([]byte(vendorInput()), options)
		require.Error(t, err)
	}
}

func TestConvertGoVendorPreservesStaticReleaseAndCustomDirectories(t *testing.T) {
	input := strings.ReplaceAll(vendorInput(), "Release: %autorelease", "Release: 42%{?dist}")
	input = strings.ReplaceAll(input, "%check", "cd assets\n%check")
	result, err := spec.ConvertGoVendor([]byte(input), spec.GoVendorOptions{
		ArchiveName: "app-vendor.tar.bz2", ConfigName: "vendor.toml",
	})
	require.NoError(t, err)
	assert.Contains(t, string(result.PrepSpec), "Release: 42%{?dist}")
	assert.Contains(t, string(result.Spec), "cd assets")
	assert.Contains(t, string(result.Spec),
		`( cd "$(cat "%{_topdir}/azldev-go-module-root")"`+"\n%go_vendor_license_install")
	assert.Contains(t, string(result.Spec),
		"%global go_vendor_license_filelist() %{_topdir}/azldev-go-vendor-licenses.list")
}

func TestConvertGoVendorPreservesNonTagsAndComments(t *testing.T) {
	script := "cat <<'EOF' > metadata\nBuildRequires: golang(not-a-dependency)\nSource2: not-a-source\nEOF\n"
	input := strings.ReplaceAll(vendorInput(), "%gobuild", script+"%gobuild")
	input = "# %gopkg is intentionally not invoked\n" + input
	input = strings.ReplaceAll(input, "%files\n", "%files\n# app files\n")
	result, err := spec.ConvertGoVendor([]byte(input), spec.GoVendorOptions{
		ArchiveName: "app-vendor.tar.bz2", ConfigName: "vendor.toml",
	})
	require.NoError(t, err)
	assert.Contains(t, string(result.Spec), script)
	assert.Contains(t, string(result.Spec), "Source2: app-vendor.tar.bz2")
	assert.Contains(t, string(result.Spec), "# %gopkg is intentionally not invoked")
	assert.Contains(t, string(result.Spec), "%files -f %{go_vendor_license_filelist}\n# app files")
}
