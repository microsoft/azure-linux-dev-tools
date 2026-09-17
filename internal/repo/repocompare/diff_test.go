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

func TestSummarizeCountsDirectionalDifferences(t *testing.T) {
	t.Parallel()

	stat := repocompare.Summarize([]repocompare.PackageReport{
		{Summary: "missing-from-right"},
		{Summary: "missing-from-right, added-in-right"},
		{Summary: "added-in-right, architectures-differ"},
	})

	assert.Equal(t, repocompare.DiffStat{
		MissingFromRight: 2, AddedInRight: 2, ArchitecturesDiffer: 1, Total: 3,
	}, stat)
}

func TestMissingFromRightIncludesMissingVersionsAndIgnoresArchitectureAndKind(t *testing.T) {
	t.Parallel()

	leftOnly := testPackage("left-only", "1", "x86_64")
	leftOnlyOtherArch := leftOnly
	leftOnlyOtherArch.Arch = "aarch64"

	sharedLeft := testPackage("shared", "1", "x86_64")
	sharedRight := sharedLeft
	sharedRight.Arch = "noarch"
	sharedRight.Kind = projectconfig.SubrepoKindSource
	newerLeft := sharedLeft
	newerLeft.Version = "2"

	missing := repocompare.MissingFromRight(
		[]repocompare.Package{sharedLeft, newerLeft, leftOnly, leftOnlyOtherArch},
		[]repocompare.Package{sharedRight},
	)

	assert.Equal(t, []repocompare.MissingPackage{
		{Name: "left-only", Version: "1-1.azl4"},
		{Name: "shared", Version: "2-1.azl4"},
	}, missing)
}

func TestMissingFromRightIncludesEpochInVersion(t *testing.T) {
	t.Parallel()

	left := testPackage("epoch-package", "1", "x86_64")
	left.Epoch = "2"

	missing := repocompare.MissingFromRight([]repocompare.Package{left}, nil)

	assert.Equal(t, []repocompare.MissingPackage{{
		Name: "epoch-package", Version: "2:1-1.azl4",
	}}, missing)
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
		Summary:    "missing-from-right, added-in-right, architectures-differ",
		LeftNEVRs:  "pkg-2-1.azl4",
		RightNEVRs: "pkg-2-1.azl4, pkg-1-1.azl4",
	}}, reports)
}

func TestCompareCanIgnoreOlderAddedInRight(t *testing.T) {
	t.Parallel()

	left := testPackage("pkg", "2", "x86_64")
	rightOlder := testPackage("pkg", "1", "x86_64")

	reports, err := repocompare.CompareWithOptions(
		[]repocompare.Package{left},
		[]repocompare.Package{rightOlder},
		repocompare.Options{IgnoreOlderAddedInRight: true},
	)
	require.NoError(t, err)

	assert.Equal(t, []repocompare.PackageReport{{
		Name: "pkg", Summary: "missing-from-right",
		LeftNEVRs: "pkg-2-1.azl4", RightNEVRs: "pkg-1-1.azl4",
	}}, reports)
}

func TestCompareDoesNotIgnoreNewerOrDifferentArchAddedInRight(t *testing.T) {
	t.Parallel()

	left := testPackage("pkg", "2", "x86_64")
	rightNewer := testPackage("pkg", "3", "x86_64")
	rightOlderArm := testPackage("pkg", "1", "aarch64")

	reports, err := repocompare.CompareWithOptions(
		[]repocompare.Package{left},
		[]repocompare.Package{rightNewer, rightOlderArm},
		repocompare.Options{IgnoreOlderAddedInRight: true},
	)
	require.NoError(t, err)

	assert.Equal(t, []repocompare.PackageReport{{
		Name:       "pkg",
		Summary:    "missing-from-right, added-in-right",
		LeftNEVRs:  "pkg-2-1.azl4",
		RightNEVRs: "pkg-3-1.azl4, pkg-1-1.azl4",
	}}, reports)
}

func TestCompareReportsContentDifference(t *testing.T) {
	t.Parallel()

	left := testPackage("pkg", "1", "x86_64")
	left.ChecksumType, left.Checksum, left.Size = "sha256", "left", 10
	right := left
	right.Checksum, right.Size = "right", 11

	reports, err := repocompare.CompareWithOptions(
		[]repocompare.Package{left},
		[]repocompare.Package{right},
		repocompare.Options{CompareChecksums: true},
	)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, "content-different", reports[0].Summary)
	assert.Equal(t, []repocompare.ContentDifference{{
		Package: "pkg-1-1.azl4.x86_64",
		Kind:    "binary",
		Left: []repocompare.PackageContent{{
			ChecksumType: "sha256", Checksum: "left", Size: 10,
		}},
		Right: []repocompare.PackageContent{{
			ChecksumType: "sha256", Checksum: "right", Size: 11,
		}},
	}}, reports[0].ContentDifferences)

	reports, err = repocompare.CompareWithOptions(
		[]repocompare.Package{left},
		[]repocompare.Package{right},
		repocompare.Options{},
	)
	require.NoError(t, err)
	assert.Empty(t, reports)
}

func TestCompareReportsSkippedMixedChecksumAlgorithms(t *testing.T) {
	t.Parallel()

	left := testPackage("pkg", "1", "x86_64")
	left.ChecksumType, left.Checksum = "sha256", "left"
	right := left
	right.ChecksumType, right.Checksum = "sha512", "right"

	reports, err := repocompare.CompareWithOptions(
		[]repocompare.Package{left},
		[]repocompare.Package{right},
		repocompare.Options{CompareChecksums: true},
	)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, "content-comparison-skipped", reports[0].Summary)
}
