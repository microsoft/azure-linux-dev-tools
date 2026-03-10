// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"testing"

	rpmlib "github.com/cavaliergopher/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/buildtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/projecttest"
	"github.com/samber/lo"
	"github.com/shirou/gopsutil/host"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// We test running `azldev build component` to make sure that building a local component works, and
// that we get back reasonable results. This test generates the input project on the fly.
func TestBuildingLocalComponent(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	// Create a simple project with a simple noarch spec.
	// Include test default configs to get distro and mock configurations.
	spec := projecttest.NewSpec(projecttest.WithBuildArch(projecttest.NoArch))
	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec),
		projecttest.UseTestDefaultConfigs(),
	)

	// Run the build with test default configs copied into the container.
	results := buildtest.NewBuildTest(project, spec.GetName(), projecttest.WithTestDefaultConfigs()).Run(t)

	// Make sure we got 1 SRPM.
	srpms := results.GetSRPMs(t)
	require.Len(t, srpms, 1)

	// Make sure we got 1 RPM.
	rpms := results.GetRPMs(t)
	require.Len(t, rpms, 1)

	// Validate SRPM metadata
	srpm := srpms[0]
	assert.Equal(t, spec.GetName(), srpm.Name())
	assert.Equal(t, spec.GetVersion(), srpm.Version())
	assert.Equal(t, spec.GetRelease(), srpm.Release())

	// Validate RPM metadata
	rpm := rpms[0]
	assert.Equal(t, spec.GetName(), rpm.Name())
	assert.Equal(t, spec.GetVersion(), rpm.Version())
	assert.Equal(t, spec.GetRelease(), rpm.Release())
	assert.Equal(t, projecttest.NoArch, rpm.Architecture())
}

// We test running `azldev build component` to make sure that building a local component works, and
// that we get back reasonable results. This test uses checked-in files as the basis for the input
// project.
func TestBuildingLocalComponentFromCheckedInFiles(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	// Create a simple project with a simple noarch spec.
	// Include test default configs to get distro and mock configurations.
	project := projecttest.NewTemplatedTestProject(t, "testdata/simple",
		projecttest.TemplatedUseTestDefaultConfigs(),
	)

	// Run the build with test default configs copied into the container.
	results := buildtest.NewBuildTest(project, "a", projecttest.WithTestDefaultConfigs()).Run(t)

	// Make sure we got 1 SRPM.
	srpms := results.GetSRPMs(t)
	require.Len(t, srpms, 1)

	// Make sure we got 1 RPM.
	rpms := results.GetRPMs(t)
	require.Len(t, rpms, 1)

	// Validate SRPM metadata.
	srpm := srpms[0]
	assert.Equal(t, "a", srpm.Name())
	assert.Equal(t, "1.2.3", srpm.Version())
	assert.Equal(t, "4.azl3", srpm.Release())

	// Validate RPM metadata.
	rpm := rpms[0]
	assert.Equal(t, srpm.Name(), rpm.Name())
	assert.Equal(t, srpm.Version(), rpm.Version())
	assert.Equal(t, srpm.Release(), rpm.Release())
	assert.Equal(t, projecttest.NoArch, rpm.Architecture())
}

func TestBuildingUpstreamComponent(t *testing.T) {
	// We pick a relatively self-contained component that is available upstream in Fedora, and which builds
	// in our default distro.
	const testComponentName = "lolcat"

	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	// Create a project with a well-known component available in Fedora; we rely on the
	// default configuration defaulting to sourcing from upstream.
	// Include test default configs to get distro and mock configurations.
	project := projecttest.NewDynamicTestProject(
		projecttest.AddComponent(&projectconfig.ComponentConfig{Name: testComponentName}),
		projecttest.UseTestDefaultConfigs(),
	)

	// Run the build with test default configs copied into the container.
	results := buildtest.NewBuildTest(project, testComponentName, projecttest.WithTestDefaultConfigs()).Run(t)

	// Make sure we got 1 SRPM.
	srpms := results.GetSRPMs(t)
	require.Len(t, srpms, 1)

	// Make sure we got 2 RPMs: 1 prod, 1 debuginfo.
	rpms := results.GetRPMs(t)
	require.Len(t, rpms, 2)

	// Figure out our host architecture so we can validate the RPMs' architecture tags.
	hostInfo, err := host.Info()
	require.NoError(t, err, "failed to retrieve host info")

	//
	// Validate SRPM and RPM metadata. We don't actually know the version or release, since it's
	// whatever latest version is available upstream--but we can do some basic pattern checking
	// on them.
	//

	const releaseRegexStr = `^\d+\.azl3$`

	srpm := srpms[0]
	assert.Equal(t, testComponentName, srpm.Name())
	assert.Regexp(t, releaseRegexStr, srpm.Release())
	assert.Equal(t, hostInfo.KernelArch, srpm.Architecture())

	// Validate common properties of all RPMs.
	for _, rpm := range rpms {
		assert.Regexp(t, releaseRegexStr, rpm.Release())
		assert.Equal(t, hostInfo.KernelArch, rpm.Architecture())
	}

	// Validate RPM names.
	rpmsByName := lo.SliceToMap(rpms, func(rpm *rpmlib.Package) (string, *rpmlib.Package) {
		return rpm.Name(), rpm
	})

	require.Contains(t, rpmsByName, testComponentName)
	require.Contains(t, rpmsByName, testComponentName+"-debuginfo")
}
