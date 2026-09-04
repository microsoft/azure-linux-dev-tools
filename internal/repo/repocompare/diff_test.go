// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package repocompare_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/repo/repocompare"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPackage(name, version, arch string) repocompare.Package {
	return repocompare.Package{
		Name: name, Epoch: "0", Version: version, Release: "1.azl4", Arch: arch,
		Kind: projectconfig.SubrepoKindBinary,
	}
}

func TestCompareReportsDirectionalInventoryDifferences(t *testing.T) {
	t.Parallel()

	shared := testPackage("bash", "5.3", "x86_64")
	leftOnly := testPackage("curl", "8.0", "x86_64")
	rightOnly := testPackage("azurelinux-release", "4.0", "noarch")

	reports, err := repocompare.Compare(
		[]repocompare.Package{shared, leftOnly},
		[]repocompare.Package{shared, rightOnly},
	)
	require.NoError(t, err)

	assert.Equal(t, []repocompare.PackageReport{
		{
			Name: "azurelinux-release", Summary: "added-in-right",
			RightNEVRs: "azurelinux-release-4.0-1.azl4",
		},
		{
			Name: "curl", Summary: "missing-from-right",
			LeftNEVRs: "curl-8.0-1.azl4",
		},
	}, reports)
}

func TestCompareNormalizesEpochAndNoarchReplication(t *testing.T) {
	t.Parallel()

	left := testPackage("docs", "1", "noarch")
	left.Epoch = ""
	right := left
	right.Epoch = "0"

	reports, err := repocompare.Compare(
		[]repocompare.Package{left, left},
		[]repocompare.Package{right},
	)
	require.NoError(t, err)

	assert.Empty(t, reports)
}

func TestCompareKeepsArtifactKindsDistinct(t *testing.T) {
	t.Parallel()

	binary := testPackage("pkg", "1", "x86_64")
	source := binary
	source.Arch = "src"
	source.Kind = projectconfig.SubrepoKindSource

	reports, err := repocompare.Compare([]repocompare.Package{binary, source}, []repocompare.Package{binary})
	require.NoError(t, err)

	assert.Equal(t, []repocompare.PackageReport{{
		Name: "pkg", Summary: "missing-from-right", LeftNEVRs: "pkg-1-1.azl4", RightNEVRs: "pkg-1-1.azl4",
	}}, reports)
}

func TestCompareSummarizesVersionAndArchitectureDifferences(t *testing.T) {
	t.Parallel()

	leftX64 := testPackage("pkg", "2", "x86_64")
	leftArm := leftX64
	leftArm.Arch = "aarch64"
	rightX64 := testPackage("pkg", "2", "x86_64")
	rightOld := testPackage("pkg", "1", "x86_64")

	reports, err := repocompare.Compare(
		[]repocompare.Package{leftX64, leftArm},
		[]repocompare.Package{rightX64, rightOld},
	)
	require.NoError(t, err)

	assert.Equal(t, []repocompare.PackageReport{{
		Name:       "pkg",
		Summary:    "missing-from-right, architectures-differ",
		LeftNEVRs:  "pkg-2-1.azl4",
		RightNEVRs: "pkg-2-1.azl4, pkg-1-1.azl4",
	}}, reports)
}

func TestCompareDoesNotTreatHistoricalRightVersionAsAddedPackage(t *testing.T) {
	t.Parallel()

	left := testPackage("shared", "2", "x86_64")
	right := testPackage("shared", "1", "x86_64")

	reports, err := repocompare.Compare(
		[]repocompare.Package{left},
		[]repocompare.Package{right},
	)
	require.NoError(t, err)

	assert.Equal(t, []repocompare.PackageReport{{
		Name: "shared", Summary: "missing-from-right",
		LeftNEVRs: "shared-2-1.azl4", RightNEVRs: "shared-1-1.azl4",
	}}, reports)
}
