// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint_test

import (
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/fingerprint"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestFS(t *testing.T, files map[string]string) *testctx.TestCtx {
	t.Helper()

	ctx := testctx.NewCtx()

	for path, content := range files {
		err := fileutils.WriteFile(ctx.FS(), path, []byte(content), fileperms.PublicFile)
		require.NoError(t, err)
	}

	return ctx
}

const testReleaseVer = "4.0"

func baseComponent() projectconfig.ComponentConfig {
	return projectconfig.ComponentConfig{
		Name: "testpkg",
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/specs/test.spec",
		},
	}
}

func computeFingerprint(
	t *testing.T,
	ctx *testctx.TestCtx,
	comp projectconfig.ComponentConfig,
	releaseVer string,
	manualBump int,
) string {
	t.Helper()

	identity, err := fingerprint.ComputeIdentity(ctx.FS(), comp, releaseVer, fingerprint.IdentityOptions{
		ManualBump:     manualBump,
		SourceIdentity: "test-source-identity",
	})
	require.NoError(t, err)

	return identity.Fingerprint
}

func TestComputeIdentity_Deterministic(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp := baseComponent()
	releaseVer := testReleaseVer

	fp1 := computeFingerprint(t, ctx, comp, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp, releaseVer, 0)

	assert.Equal(t, fp1, fp2, "identical inputs must produce identical fingerprints")
	assert.Contains(t, fp1, "sha256:", "fingerprint should have sha256: prefix")
}

func TestComputeIdentity_SourceIdentityChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp := baseComponent()
	releaseVer := testReleaseVer

	identity1, err := fingerprint.ComputeIdentity(ctx.FS(), comp, releaseVer, fingerprint.IdentityOptions{
		SourceIdentity: "abc123",
	})
	require.NoError(t, err)

	identity2, err := fingerprint.ComputeIdentity(ctx.FS(), comp, releaseVer, fingerprint.IdentityOptions{
		SourceIdentity: "def456",
	})
	require.NoError(t, err)

	assert.NotEqual(t, identity1.Fingerprint, identity2.Fingerprint,
		"different source identity must produce different fingerprints")
}

func TestComputeIdentity_BuildWithChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp1 := baseComponent()
	comp2 := baseComponent()
	comp2.Build.With = []string{"feature_x"}

	releaseVer := testReleaseVer

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.NotEqual(t, fp1, fp2, "adding build.with must change fingerprint")
}

func TestComputeIdentity_BuildWithoutChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp1 := baseComponent()
	comp2 := baseComponent()
	comp2.Build.Without = []string{"docs"}

	releaseVer := testReleaseVer

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.NotEqual(t, fp1, fp2, "adding build.without must change fingerprint")
}

func TestComputeIdentity_BuildDefinesChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp1 := baseComponent()
	comp2 := baseComponent()
	comp2.Build.Defines = map[string]string{"debug": "1"}

	releaseVer := testReleaseVer

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.NotEqual(t, fp1, fp2, "adding build.defines must change fingerprint")
}

func TestComputeIdentity_CheckSkipChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp1 := baseComponent()
	comp2 := baseComponent()
	comp2.Build.Check.Skip = true

	releaseVer := testReleaseVer

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.NotEqual(t, fp1, fp2, "changing check.skip must change fingerprint")
}

func TestComputeIdentity_ExcludedFieldsDoNotChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})
	releaseVer := testReleaseVer

	// Base component.
	comp := baseComponent()
	fpBase := computeFingerprint(t, ctx, comp, releaseVer, 0)

	// Changing Name (fingerprint:"-") should NOT change fingerprint.
	compName := baseComponent()
	compName.Name = "different-name"
	fpName := computeFingerprint(t, ctx, compName, releaseVer, 0)
	assert.Equal(t, fpBase, fpName, "changing Name must NOT change fingerprint")

	// Changing Build.Failure.Expected (fingerprint:"-") should NOT change fingerprint.
	compFailure := baseComponent()
	compFailure.Build.Failure.Expected = true
	compFailure.Build.Failure.ExpectedReason = "known issue"
	fpFailure := computeFingerprint(t, ctx, compFailure, releaseVer, 0)
	assert.Equal(t, fpBase, fpFailure, "changing failure.expected must NOT change fingerprint")

	// Changing Build.Hints.Expensive (fingerprint:"-") should NOT change fingerprint.
	compHints := baseComponent()
	compHints.Build.Hints.Expensive = true
	fpHints := computeFingerprint(t, ctx, compHints, releaseVer, 0)
	assert.Equal(t, fpBase, fpHints, "changing hints.expensive must NOT change fingerprint")

	// Changing Build.Check.SkipReason (fingerprint:"-") should NOT change fingerprint.
	compReason := baseComponent()
	compReason.Build.Check.SkipReason = "tests require network"
	fpReason := computeFingerprint(t, ctx, compReason, releaseVer, 0)
	assert.Equal(t, fpBase, fpReason, "changing check.skip_reason must NOT change fingerprint")

	// Changing RenderedSpecDir (fingerprint:"-") should NOT change fingerprint.
	// This is a derived output path that varies by checkout location.
	compRendered := baseComponent()
	compRendered.RenderedSpecDir = "/some/checkout/path/SPECS/t/testpkg"
	fpRendered := computeFingerprint(t, ctx, compRendered, releaseVer, 0)
	assert.Equal(t, fpBase, fpRendered, "changing RenderedSpecDir must NOT change fingerprint")
}

func TestComputeIdentity_OverlayDescriptionExcluded(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})
	releaseVer := testReleaseVer

	comp1 := baseComponent()
	comp1.Overlays = []projectconfig.ComponentOverlay{
		{Type: "spec-set-tag", Tag: "Release", Value: "2%{?dist}"},
	}

	comp2 := baseComponent()
	comp2.Overlays = []projectconfig.ComponentOverlay{
		{Type: "spec-set-tag", Tag: "Release", Value: "2%{?dist}", Description: "bumped release"},
	}

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.Equal(t, fp1, fp2, "overlay description must NOT change fingerprint")
}

func TestComputeIdentity_OverlaySourceFileChange(t *testing.T) {
	ctx1 := newTestFS(t, map[string]string{
		"/specs/test.spec":   "Name: testpkg\nVersion: 1.0",
		"/patches/fix.patch": "--- a/file\n+++ b/file\n@@ original @@",
	})
	ctx2 := newTestFS(t, map[string]string{
		"/specs/test.spec":   "Name: testpkg\nVersion: 1.0",
		"/patches/fix.patch": "--- a/file\n+++ b/file\n@@ modified @@",
	})
	releaseVer := testReleaseVer

	comp := baseComponent()
	comp.Overlays = []projectconfig.ComponentOverlay{
		{Type: "patch-add", Source: "/patches/fix.patch"},
	}

	fp1 := computeFingerprint(t, ctx1, comp, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx2, comp, releaseVer, 0)

	assert.NotEqual(t, fp1, fp2, "different overlay source content must produce different fingerprints")
}

func TestComputeIdentity_PatchAddRenameChangesFP(t *testing.T) {
	// When patch-add omits 'file', the destination filename is derived from
	// filepath.Base(Source). Renaming the source file changes the rendered
	// spec output (PatchN: tag + copied file), so the fingerprint must change
	// even if the file content is identical.
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec":        "Name: testpkg\nVersion: 1.0",
		"/patches/fix.patch":      "identical patch content",
		"/patches/cve-2026.patch": "identical patch content",
	})
	releaseVer := testReleaseVer

	comp1 := baseComponent()
	comp1.Overlays = []projectconfig.ComponentOverlay{
		{Type: "patch-add", Source: "/patches/fix.patch"},
	}

	comp2 := baseComponent()
	comp2.Overlays = []projectconfig.ComponentOverlay{
		{Type: "patch-add", Source: "/patches/cve-2026.patch"},
	}

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.NotEqual(t, fp1, fp2,
		"renaming overlay source file must change fingerprint (same content, different basename)")
}

func TestComputeIdentity_DistroChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp := baseComponent()

	fp1 := computeFingerprint(t, ctx, comp, "3.0", 0)
	fp2 := computeFingerprint(t, ctx, comp, "4.0", 0)

	assert.NotEqual(t, fp1, fp2, "different release version must produce different fingerprints")
}

func TestComputeIdentity_ManualBumpChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp := baseComponent()
	releaseVer := testReleaseVer

	fp1 := computeFingerprint(t, ctx, comp, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp, releaseVer, 1)

	assert.NotEqual(t, fp1, fp2, "different manual bump count must produce different fingerprints")
}

func TestComputeIdentity_UpstreamCommitChange(t *testing.T) {
	ctx := newTestFS(t, nil)

	comp1 := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType:     projectconfig.SpecSourceTypeUpstream,
			UpstreamName:   "curl",
			UpstreamCommit: "abc1234",
			UpstreamDistro: projectconfig.DistroReference{Name: "fedora", Version: "41"},
		},
	}
	comp2 := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType:     projectconfig.SpecSourceTypeUpstream,
			UpstreamName:   "curl",
			UpstreamCommit: "def5678",
			UpstreamDistro: projectconfig.DistroReference{Name: "fedora", Version: "41"},
		},
	}
	releaseVer := testReleaseVer

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.NotEqual(t, fp1, fp2, "different upstream commit must produce different fingerprints")
}

func TestComputeIdentity_SourceFilesChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp1 := baseComponent()
	comp1.SourceFiles = []projectconfig.SourceFileReference{
		{Filename: "source.tar.gz", Hash: "aaa111", HashType: fileutils.HashTypeSHA256},
	}

	comp2 := baseComponent()
	comp2.SourceFiles = []projectconfig.SourceFileReference{
		{Filename: "source.tar.gz", Hash: "bbb222", HashType: fileutils.HashTypeSHA256},
	}
	releaseVer := testReleaseVer

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.NotEqual(t, fp1, fp2, "different source file hash must produce different fingerprints")
}

func TestComputeIdentity_SourceFileOriginExcluded(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp1 := baseComponent()
	comp1.SourceFiles = []projectconfig.SourceFileReference{
		{
			Filename: "source.tar.gz",
			Hash:     "aaa111",
			HashType: fileutils.HashTypeSHA256,
			Origin:   projectconfig.Origin{Type: "download", Uri: "https://old-cdn.example.com/source.tar.gz"},
		},
	}

	comp2 := baseComponent()
	comp2.SourceFiles = []projectconfig.SourceFileReference{
		{
			Filename: "source.tar.gz",
			Hash:     "aaa111",
			HashType: fileutils.HashTypeSHA256,
			Origin:   projectconfig.Origin{Type: "download", Uri: "https://new-cdn.example.com/source.tar.gz"},
		},
	}
	releaseVer := testReleaseVer

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.Equal(t, fp1, fp2, "changing source file origin URL must NOT change fingerprint")
}

func TestComputeIdentity_SourceFileNoHash_Error(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp := baseComponent()
	comp.SourceFiles = []projectconfig.SourceFileReference{
		{
			Filename: "source.tar.gz",
			Origin:   projectconfig.Origin{Type: "download", Uri: "https://example.com/source.tar.gz"},
		},
	}
	releaseVer := testReleaseVer

	_, err := fingerprint.ComputeIdentity(ctx.FS(), comp, releaseVer, fingerprint.IdentityOptions{
		SourceIdentity: "test-source-identity",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source.tar.gz")
	assert.Contains(t, err.Error(), "no hash")
}

func TestComputeIdentity_InputsBreakdown(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec":   "Name: testpkg\nVersion: 1.0",
		"/patches/fix.patch": "patch content here",
	})

	comp := baseComponent()
	comp.Overlays = []projectconfig.ComponentOverlay{
		{Type: "patch-add", Source: "/patches/fix.patch"},
	}
	releaseVer := testReleaseVer

	identity, err := fingerprint.ComputeIdentity(ctx.FS(), comp, releaseVer, fingerprint.IdentityOptions{
		ManualBump:     3,
		SourceIdentity: "test-source-identity-hash",
	})
	require.NoError(t, err)

	assert.NotEmpty(t, identity.Fingerprint)
	assert.NotZero(t, identity.Inputs.ConfigHash)
	assert.Equal(t, "test-source-identity-hash", identity.Inputs.SourceIdentity)
	assert.Equal(t, 3, identity.Inputs.ManualBump)
	assert.Equal(t, testReleaseVer, identity.Inputs.ReleaseVer)
	assert.Contains(t, identity.Inputs.OverlayFileHashes, "0")
}

func TestComputeIdentity_MissingSourceIdentity_Error(t *testing.T) {
	ctx := newTestFS(t, nil)

	comp := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
		},
	}
	releaseVer := testReleaseVer

	_, err := fingerprint.ComputeIdentity(ctx.FS(), comp, releaseVer, fingerprint.IdentityOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source identity is required")
}

func TestComputeIdentity_OverlayFunctionalFieldChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})
	releaseVer := testReleaseVer

	comp1 := baseComponent()
	comp1.Overlays = []projectconfig.ComponentOverlay{
		{Type: "spec-set-tag", Tag: "Release", Value: "2%{?dist}"},
	}

	comp2 := baseComponent()
	comp2.Overlays = []projectconfig.ComponentOverlay{
		{Type: "spec-set-tag", Tag: "Release", Value: "3%{?dist}"},
	}

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.NotEqual(t, fp1, fp2, "changing overlay value must change fingerprint")
}

func TestComputeIdentity_AddingOverlay(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})
	releaseVer := testReleaseVer

	comp1 := baseComponent()

	comp2 := baseComponent()
	comp2.Overlays = []projectconfig.ComponentOverlay{
		{Type: "spec-set-tag", Tag: "Release", Value: "2%{?dist}"},
	}

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.NotEqual(t, fp1, fp2, "adding an overlay must change fingerprint")
}

func TestComputeIdentity_BuildUndefinesChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})
	releaseVer := testReleaseVer

	comp1 := baseComponent()
	comp2 := baseComponent()
	comp2.Build.Undefines = []string{"_debuginfo"}

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.NotEqual(t, fp1, fp2, "adding build.undefines must change fingerprint")
}

// Tests below verify global change propagation: changes to shared config
// (distro defaults, group defaults) must fan out to all inheriting components.

func TestComputeIdentity_DistroDefaultPropagation(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/curl.spec":    "Name: curl\nVersion: 1.0",
		"/specs/openssl.spec": "Name: openssl\nVersion: 3.0",
	})

	// Simulate two components that both inherit from a distro default.
	// First, compute fingerprints with no distro-level build options.
	curl := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal, Path: "/specs/curl.spec"},
	}
	openssl := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal, Path: "/specs/openssl.spec"},
	}
	releaseVer := testReleaseVer

	fpCurl1 := computeFingerprint(t, ctx, curl, releaseVer, 0)
	fpOpenssl1 := computeFingerprint(t, ctx, openssl, releaseVer, 0)

	// Now simulate a distro default adding build.with — after config merging,
	// both components would have this option in their resolved config.
	curl.Build.With = []string{"distro_feature"}
	openssl.Build.With = []string{"distro_feature"}

	fpCurl2 := computeFingerprint(t, ctx, curl, releaseVer, 0)
	fpOpenssl2 := computeFingerprint(t, ctx, openssl, releaseVer, 0)

	assert.NotEqual(t, fpCurl1, fpCurl2,
		"distro default change must propagate to curl's fingerprint")
	assert.NotEqual(t, fpOpenssl1, fpOpenssl2,
		"distro default change must propagate to openssl's fingerprint")
}

func TestComputeIdentity_GroupDefaultPropagation(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/a.spec": "Name: a\nVersion: 1.0",
		"/specs/b.spec": "Name: b\nVersion: 1.0",
		"/specs/c.spec": "Name: c\nVersion: 1.0",
	})

	releaseVer := testReleaseVer

	// Three components: a and b are in a group, c is not.
	compA := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal, Path: "/specs/a.spec"},
	}
	compB := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal, Path: "/specs/b.spec"},
	}
	compC := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal, Path: "/specs/c.spec"},
	}

	fpA1 := computeFingerprint(t, ctx, compA, releaseVer, 0)
	fpB1 := computeFingerprint(t, ctx, compB, releaseVer, 0)
	fpC1 := computeFingerprint(t, ctx, compC, releaseVer, 0)

	// Simulate a group default adding check.skip — after merging, only a and b have it.
	compA.Build.Check.Skip = true
	compB.Build.Check.Skip = true
	// compC is not in the group, remains unchanged.

	fpA2 := computeFingerprint(t, ctx, compA, releaseVer, 0)
	fpB2 := computeFingerprint(t, ctx, compB, releaseVer, 0)
	fpC2 := computeFingerprint(t, ctx, compC, releaseVer, 0)

	assert.NotEqual(t, fpA1, fpA2, "group default must propagate to member A")
	assert.NotEqual(t, fpB1, fpB2, "group default must propagate to member B")
	assert.Equal(t, fpC1, fpC2, "non-group member C must NOT be affected")
}

func TestComputeIdentity_MergeUpdatesFromPropagation(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})
	releaseVer := testReleaseVer

	// Start with a base component.
	comp := baseComponent()
	fpBefore := computeFingerprint(t, ctx, comp, releaseVer, 0)

	// Simulate applying a distro default via MergeUpdatesFrom.
	distroDefault := &projectconfig.ComponentConfig{
		Build: projectconfig.ComponentBuildConfig{
			Defines: map[string]string{"vendor": "azl"},
		},
	}

	err := comp.MergeUpdatesFrom(distroDefault)
	require.NoError(t, err)

	fpAfter := computeFingerprint(t, ctx, comp, releaseVer, 0)

	assert.NotEqual(t, fpBefore, fpAfter,
		"merged distro default must change the fingerprint")
}

func TestComputeIdentity_SnapshotChangeDoesNotAffectFingerprint(t *testing.T) {
	ctx := newTestFS(t, nil)

	comp := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType:     projectconfig.SpecSourceTypeUpstream,
			UpstreamName:   "curl",
			UpstreamCommit: "abc1234",
			UpstreamDistro: projectconfig.DistroReference{
				Name:     "fedora",
				Version:  "41",
				Snapshot: "2025-01-01T00:00:00Z",
			},
		},
	}
	releaseVer := testReleaseVer

	fp1 := computeFingerprint(t, ctx, comp, releaseVer, 0)

	// Change only the snapshot timestamp.
	comp.Spec.UpstreamDistro.Snapshot = "2026-06-15T00:00:00Z"
	fp2 := computeFingerprint(t, ctx, comp, releaseVer, 0)

	assert.Equal(t, fp1, fp2,
		"changing upstream distro snapshot must NOT change fingerprint "+
			"(snapshot is excluded; resolved commit hash is what matters)")
}

func TestComputeIdentity_DifferentCheckoutPaths(t *testing.T) {
	// Simulate the same component checked out in two different directories.
	// After WithAbsolutePaths, Spec.Path and overlay Source paths will differ.
	// The fingerprint must be identical — it should not depend on checkout location.
	ctx := newTestFS(t, map[string]string{
		"/home/user1/repo/specs/test.spec":   "Name: testpkg\nVersion: 1.0",
		"/home/user2/repo/specs/test.spec":   "Name: testpkg\nVersion: 1.0",
		"/home/user1/repo/patches/fix.patch": "patch content",
		"/home/user2/repo/patches/fix.patch": "patch content",
	})
	releaseVer := testReleaseVer

	comp1 := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/home/user1/repo/specs/test.spec",
		},
		Overlays: []projectconfig.ComponentOverlay{
			{Type: "patch-add", Source: "/home/user1/repo/patches/fix.patch"},
		},
	}

	comp2 := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/home/user2/repo/specs/test.spec",
		},
		Overlays: []projectconfig.ComponentOverlay{
			{Type: "patch-add", Source: "/home/user2/repo/patches/fix.patch"},
		},
	}

	fp1 := computeFingerprint(t, ctx, comp1, releaseVer, 0)
	fp2 := computeFingerprint(t, ctx, comp2, releaseVer, 0)

	assert.Equal(t, fp1, fp2,
		"same component in different checkout directories must produce identical fingerprints")
}

func testResolutionInputs() fingerprint.ResolutionInputs {
	return fingerprint.ResolutionInputs{
		Snapshot:       "2025-01-01T00:00:00Z",
		DistroName:     "fedora",
		DistroVersion:  "41",
		DistGitBranch:  "f41",
		DistGitBaseURI: "https://src.fedoraproject.org/rpms/$pkg.git",
		UpstreamName:   "curl",
	}
}

func TestComputeResolutionHash_SnapshotChangeAffectsHash(t *testing.T) {
	inputs := testResolutionInputs()
	hashBefore := fingerprint.ComputeResolutionHash(inputs)

	inputs.Snapshot = "2026-06-15T00:00:00Z"
	hashAfter := fingerprint.ComputeResolutionHash(inputs)

	assert.NotEqual(t, hashBefore, hashAfter,
		"snapshot change must change resolution hash")
}

func TestComputeResolutionHash_BuildOptionDoesNotAffectHash(t *testing.T) {
	// Build options are not part of ResolutionInputs — they only affect
	// InputFingerprint. Verify the hash is stable regardless.
	inputs := testResolutionInputs()
	hashBefore := fingerprint.ComputeResolutionHash(inputs)

	// Re-compute with identical inputs (build options are external).
	hashAfter := fingerprint.ComputeResolutionHash(inputs)

	assert.Equal(t, hashBefore, hashAfter,
		"resolution hash must not be affected by build options")
}

func TestComputeResolutionHash_Deterministic(t *testing.T) {
	inputs := testResolutionInputs()

	hashFirst := fingerprint.ComputeResolutionHash(inputs)
	hashSecond := fingerprint.ComputeResolutionHash(inputs)

	assert.Equal(t, hashFirst, hashSecond, "same inputs must produce same hash")
	assert.True(t, strings.HasPrefix(hashFirst, "sha256:"), "hash must have sha256 prefix")
}

func TestComputeResolutionHash_PinChangeAffectsHash(t *testing.T) {
	inputs := testResolutionInputs()
	hashNoPin := fingerprint.ComputeResolutionHash(inputs)

	inputs.UpstreamCommitPin = "abc123def456"
	hashWithPin := fingerprint.ComputeResolutionHash(inputs)

	assert.NotEqual(t, hashNoPin, hashWithPin,
		"adding an upstream commit pin must change resolution hash")
}

func TestComputeResolutionHash_DistroVersionChangeAffectsHash(t *testing.T) {
	inputs := testResolutionInputs()
	hashV41 := fingerprint.ComputeResolutionHash(inputs)

	inputs.DistroVersion = "42"
	inputs.DistGitBranch = "f42"
	hashV42 := fingerprint.ComputeResolutionHash(inputs)

	assert.NotEqual(t, hashV41, hashV42,
		"distro version change must change resolution hash")
}

func TestComputeResolutionHash_BranchChangeAffectsHash(t *testing.T) {
	inputs := testResolutionInputs()
	hashBefore := fingerprint.ComputeResolutionHash(inputs)

	inputs.DistGitBranch = "f41-stabilization"
	hashAfter := fingerprint.ComputeResolutionHash(inputs)

	assert.NotEqual(t, hashBefore, hashAfter,
		"dist-git branch change must change resolution hash")
}

func TestComputeResolutionHash_BaseURIChangeAffectsHash(t *testing.T) {
	inputs := testResolutionInputs()
	hashBefore := fingerprint.ComputeResolutionHash(inputs)

	inputs.DistGitBaseURI = "https://internal-mirror.example.com/rpms/$pkg.git"
	hashAfter := fingerprint.ComputeResolutionHash(inputs)

	assert.NotEqual(t, hashBefore, hashAfter,
		"dist-git base URI change must change resolution hash")
}
