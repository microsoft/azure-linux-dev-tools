// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint_test

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

func newTestFS(t *testing.T, files map[string]string) *testctx.TestCtx {
	t.Helper()

	ctx := testctx.NewCtx()

	for path, content := range files {
		err := fileutils.WriteFile(ctx.FS(), path, []byte(content), fileperms.PublicFile)
		require.NoError(t, err)
	}

	return ctx
}

func baseDistroRef() projectconfig.DistroReference {
	return projectconfig.DistroReference{
		Name:    "azl",
		Version: "3.0",
	}
}

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
	distro projectconfig.DistroReference,
	affects int,
) string {
	t.Helper()

	identity, err := fingerprint.ComputeIdentity(ctx.FS(), comp, distro, fingerprint.IdentityOptions{
		AffectsCommitCount: affects,
	})
	require.NoError(t, err)

	return identity.Fingerprint
}

func TestComputeIdentity_Deterministic(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp := baseComponent()
	distro := baseDistroRef()

	fp1 := computeFingerprint(t, ctx, comp, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp, distro, 0)

	assert.Equal(t, fp1, fp2, "identical inputs must produce identical fingerprints")
	assert.Contains(t, fp1, "sha256:", "fingerprint should have sha256: prefix")
}

func TestComputeIdentity_SourceIdentityChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp := baseComponent()
	distro := baseDistroRef()

	identity1, err := fingerprint.ComputeIdentity(ctx.FS(), comp, distro, fingerprint.IdentityOptions{
		SourceIdentity: "abc123",
	})
	require.NoError(t, err)

	identity2, err := fingerprint.ComputeIdentity(ctx.FS(), comp, distro, fingerprint.IdentityOptions{
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

	distro := baseDistroRef()

	fp1 := computeFingerprint(t, ctx, comp1, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp2, distro, 0)

	assert.NotEqual(t, fp1, fp2, "adding build.with must change fingerprint")
}

func TestComputeIdentity_BuildWithoutChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp1 := baseComponent()
	comp2 := baseComponent()
	comp2.Build.Without = []string{"docs"}

	distro := baseDistroRef()

	fp1 := computeFingerprint(t, ctx, comp1, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp2, distro, 0)

	assert.NotEqual(t, fp1, fp2, "adding build.without must change fingerprint")
}

func TestComputeIdentity_BuildDefinesChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp1 := baseComponent()
	comp2 := baseComponent()
	comp2.Build.Defines = map[string]string{"debug": "1"}

	distro := baseDistroRef()

	fp1 := computeFingerprint(t, ctx, comp1, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp2, distro, 0)

	assert.NotEqual(t, fp1, fp2, "adding build.defines must change fingerprint")
}

func TestComputeIdentity_CheckSkipChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp1 := baseComponent()
	comp2 := baseComponent()
	comp2.Build.Check.Skip = true

	distro := baseDistroRef()

	fp1 := computeFingerprint(t, ctx, comp1, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp2, distro, 0)

	assert.NotEqual(t, fp1, fp2, "changing check.skip must change fingerprint")
}

func TestComputeIdentity_ExcludedFieldsDoNotChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})
	distro := baseDistroRef()

	// Base component.
	comp := baseComponent()
	fpBase := computeFingerprint(t, ctx, comp, distro, 0)

	// Changing Name (fingerprint:"-") should NOT change fingerprint.
	compName := baseComponent()
	compName.Name = "different-name"
	fpName := computeFingerprint(t, ctx, compName, distro, 0)
	assert.Equal(t, fpBase, fpName, "changing Name must NOT change fingerprint")

	// Changing Build.Failure.Expected (fingerprint:"-") should NOT change fingerprint.
	compFailure := baseComponent()
	compFailure.Build.Failure.Expected = true
	compFailure.Build.Failure.ExpectedReason = "known issue"
	fpFailure := computeFingerprint(t, ctx, compFailure, distro, 0)
	assert.Equal(t, fpBase, fpFailure, "changing failure.expected must NOT change fingerprint")

	// Changing Build.Hints.Expensive (fingerprint:"-") should NOT change fingerprint.
	compHints := baseComponent()
	compHints.Build.Hints.Expensive = true
	fpHints := computeFingerprint(t, ctx, compHints, distro, 0)
	assert.Equal(t, fpBase, fpHints, "changing hints.expensive must NOT change fingerprint")

	// Changing Build.Check.SkipReason (fingerprint:"-") should NOT change fingerprint.
	compReason := baseComponent()
	compReason.Build.Check.SkipReason = "tests require network"
	fpReason := computeFingerprint(t, ctx, compReason, distro, 0)
	assert.Equal(t, fpBase, fpReason, "changing check.skip_reason must NOT change fingerprint")

	// Changing RenderedSpecDir (fingerprint:"-") should NOT change fingerprint.
	// This is a derived output path that varies by checkout location.
	compRendered := baseComponent()
	compRendered.RenderedSpecDir = "/some/checkout/path/SPECS/t/testpkg"
	fpRendered := computeFingerprint(t, ctx, compRendered, distro, 0)
	assert.Equal(t, fpBase, fpRendered, "changing RenderedSpecDir must NOT change fingerprint")
}

func TestComputeIdentity_OverlayDescriptionExcluded(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})
	distro := baseDistroRef()

	comp1 := baseComponent()
	comp1.Overlays = []projectconfig.ComponentOverlay{
		{Type: "spec-set-tag", Tag: "Release", Value: "2%{?dist}"},
	}

	comp2 := baseComponent()
	comp2.Overlays = []projectconfig.ComponentOverlay{
		{Type: "spec-set-tag", Tag: "Release", Value: "2%{?dist}", Description: "bumped release"},
	}

	fp1 := computeFingerprint(t, ctx, comp1, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp2, distro, 0)

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
	distro := baseDistroRef()

	comp := baseComponent()
	comp.Overlays = []projectconfig.ComponentOverlay{
		{Type: "patch-add", Source: "/patches/fix.patch"},
	}

	fp1 := computeFingerprint(t, ctx1, comp, distro, 0)
	fp2 := computeFingerprint(t, ctx2, comp, distro, 0)

	assert.NotEqual(t, fp1, fp2, "different overlay source content must produce different fingerprints")
}

func TestComputeIdentity_DistroChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp := baseComponent()

	fp1 := computeFingerprint(t, ctx, comp, projectconfig.DistroReference{Name: "azl", Version: "3.0"}, 0)
	fp2 := computeFingerprint(t, ctx, comp, projectconfig.DistroReference{Name: "azl", Version: "4.0"}, 0)

	assert.NotEqual(t, fp1, fp2, "different distro version must produce different fingerprints")
}

func TestComputeIdentity_DistroNameChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp := baseComponent()

	fp1 := computeFingerprint(t, ctx, comp, projectconfig.DistroReference{Name: "azl", Version: "3.0"}, 0)
	fp2 := computeFingerprint(t, ctx, comp, projectconfig.DistroReference{Name: "fedora", Version: "3.0"}, 0)

	assert.NotEqual(t, fp1, fp2, "different distro name must produce different fingerprints")
}

func TestComputeIdentity_AffectsCountChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})

	comp := baseComponent()
	distro := baseDistroRef()

	fp1 := computeFingerprint(t, ctx, comp, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp, distro, 1)

	assert.NotEqual(t, fp1, fp2, "different affects commit count must produce different fingerprints")
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
	distro := baseDistroRef()

	fp1 := computeFingerprint(t, ctx, comp1, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp2, distro, 0)

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
	distro := baseDistroRef()

	fp1 := computeFingerprint(t, ctx, comp1, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp2, distro, 0)

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
	distro := baseDistroRef()

	fp1 := computeFingerprint(t, ctx, comp1, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp2, distro, 0)

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
	distro := baseDistroRef()

	_, err := fingerprint.ComputeIdentity(ctx.FS(), comp, distro, fingerprint.IdentityOptions{})
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
	distro := baseDistroRef()

	identity, err := fingerprint.ComputeIdentity(ctx.FS(), comp, distro, fingerprint.IdentityOptions{
		AffectsCommitCount: 3,
		SourceIdentity:     "test-source-identity-hash",
	})
	require.NoError(t, err)

	assert.NotEmpty(t, identity.Fingerprint)
	assert.NotZero(t, identity.Inputs.ConfigHash)
	assert.Equal(t, "test-source-identity-hash", identity.Inputs.SourceIdentity)
	assert.Equal(t, 3, identity.Inputs.AffectsCommitCount)
	assert.Equal(t, "azl", identity.Inputs.Distro)
	assert.Equal(t, "3.0", identity.Inputs.DistroVersion)
	assert.Contains(t, identity.Inputs.OverlayFileHashes, "/patches/fix.patch")
}

func TestComputeIdentity_NoSpecPath(t *testing.T) {
	ctx := newTestFS(t, nil)

	comp := projectconfig.ComponentConfig{
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
		},
	}
	distro := baseDistroRef()

	identity, err := fingerprint.ComputeIdentity(ctx.FS(), comp, distro, fingerprint.IdentityOptions{})
	require.NoError(t, err)

	assert.Empty(t, identity.Inputs.SourceIdentity)
}

func TestComputeIdentity_OverlayFunctionalFieldChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})
	distro := baseDistroRef()

	comp1 := baseComponent()
	comp1.Overlays = []projectconfig.ComponentOverlay{
		{Type: "spec-set-tag", Tag: "Release", Value: "2%{?dist}"},
	}

	comp2 := baseComponent()
	comp2.Overlays = []projectconfig.ComponentOverlay{
		{Type: "spec-set-tag", Tag: "Release", Value: "3%{?dist}"},
	}

	fp1 := computeFingerprint(t, ctx, comp1, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp2, distro, 0)

	assert.NotEqual(t, fp1, fp2, "changing overlay value must change fingerprint")
}

func TestComputeIdentity_AddingOverlay(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})
	distro := baseDistroRef()

	comp1 := baseComponent()

	comp2 := baseComponent()
	comp2.Overlays = []projectconfig.ComponentOverlay{
		{Type: "spec-set-tag", Tag: "Release", Value: "2%{?dist}"},
	}

	fp1 := computeFingerprint(t, ctx, comp1, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp2, distro, 0)

	assert.NotEqual(t, fp1, fp2, "adding an overlay must change fingerprint")
}

func TestComputeIdentity_BuildUndefinesChange(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})
	distro := baseDistroRef()

	comp1 := baseComponent()
	comp2 := baseComponent()
	comp2.Build.Undefines = []string{"_debuginfo"}

	fp1 := computeFingerprint(t, ctx, comp1, distro, 0)
	fp2 := computeFingerprint(t, ctx, comp2, distro, 0)

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
	distro := baseDistroRef()

	fpCurl1 := computeFingerprint(t, ctx, curl, distro, 0)
	fpOpenssl1 := computeFingerprint(t, ctx, openssl, distro, 0)

	// Now simulate a distro default adding build.with — after config merging,
	// both components would have this option in their resolved config.
	curl.Build.With = []string{"distro_feature"}
	openssl.Build.With = []string{"distro_feature"}

	fpCurl2 := computeFingerprint(t, ctx, curl, distro, 0)
	fpOpenssl2 := computeFingerprint(t, ctx, openssl, distro, 0)

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

	distro := baseDistroRef()

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

	fpA1 := computeFingerprint(t, ctx, compA, distro, 0)
	fpB1 := computeFingerprint(t, ctx, compB, distro, 0)
	fpC1 := computeFingerprint(t, ctx, compC, distro, 0)

	// Simulate a group default adding check.skip — after merging, only a and b have it.
	compA.Build.Check.Skip = true
	compB.Build.Check.Skip = true
	// compC is not in the group, remains unchanged.

	fpA2 := computeFingerprint(t, ctx, compA, distro, 0)
	fpB2 := computeFingerprint(t, ctx, compB, distro, 0)
	fpC2 := computeFingerprint(t, ctx, compC, distro, 0)

	assert.NotEqual(t, fpA1, fpA2, "group default must propagate to member A")
	assert.NotEqual(t, fpB1, fpB2, "group default must propagate to member B")
	assert.Equal(t, fpC1, fpC2, "non-group member C must NOT be affected")
}

func TestComputeIdentity_MergeUpdatesFromPropagation(t *testing.T) {
	ctx := newTestFS(t, map[string]string{
		"/specs/test.spec": "Name: testpkg\nVersion: 1.0",
	})
	distro := baseDistroRef()

	// Start with a base component.
	comp := baseComponent()
	fpBefore := computeFingerprint(t, ctx, comp, distro, 0)

	// Simulate applying a distro default via MergeUpdatesFrom.
	distroDefault := &projectconfig.ComponentConfig{
		Build: projectconfig.ComponentBuildConfig{
			Defines: map[string]string{"vendor": "azl"},
		},
	}

	err := comp.MergeUpdatesFrom(distroDefault)
	require.NoError(t, err)

	fpAfter := computeFingerprint(t, ctx, comp, distro, 0)

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
	distro := baseDistroRef()

	fp1 := computeFingerprint(t, ctx, comp, distro, 0)

	// Change only the snapshot timestamp.
	comp.Spec.UpstreamDistro.Snapshot = "2026-06-15T00:00:00Z"
	fp2 := computeFingerprint(t, ctx, comp, distro, 0)

	assert.Equal(t, fp1, fp2,
		"changing upstream distro snapshot must NOT change fingerprint "+
			"(snapshot is excluded; resolved commit hash is what matters)")
}
