// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectConfigFileValidation_EmptyFile(t *testing.T) {
	file := projectconfig.ConfigFile{}
	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_TestDefinitionMismatchedSubtable(t *testing.T) {
	file := projectconfig.ConfigFile{
		Tests: map[string]projectconfig.TestDefinition{
			"smoke": {
				Type:   "pytest",
				Pytest: map[string]any{"working-dir": "tests"},
				Lisa:   map[string]any{"suite": "vm"},
			},
		},
	}

	err := file.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, projectconfig.ErrMismatchedTestSubtable)
	assert.Contains(t, err.Error(), "invalid test")
	assert.Contains(t, err.Error(), "smoke")
	assert.Contains(t, err.Error(), "lisa")
}

func TestProjectConfigFileValidation_TestDefinitionMatchingSubtable(t *testing.T) {
	file := projectconfig.ConfigFile{
		Tests: map[string]projectconfig.TestDefinition{
			"smoke": {
				Type:   "pytest",
				Pytest: map[string]any{"working-dir": "tests"},
			},
		},
	}

	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_TestDefinitionRequiredSubtablePresentButEmpty(t *testing.T) {
	file := projectconfig.ConfigFile{
		Tests: map[string]projectconfig.TestDefinition{
			"smoke": {
				Type:   "pytest",
				Pytest: map[string]any{},
			},
		},
	}

	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_TestDefinitionDisallowedSubtablePresentButEmpty(t *testing.T) {
	file := projectconfig.ConfigFile{
		Tests: map[string]projectconfig.TestDefinition{
			"smoke": {
				Type:   "pytest",
				Pytest: map[string]any{"working-dir": "tests"},
				Lisa:   map[string]any{},
			},
		},
	}

	err := file.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, projectconfig.ErrMismatchedTestSubtable)
	assert.Contains(t, err.Error(), "smoke")
	assert.Contains(t, err.Error(), "lisa")
}

func TestProjectConfigFileValidation_TestDefinitionInvalidKind(t *testing.T) {
	file := projectconfig.ConfigFile{
		Tests: map[string]projectconfig.TestDefinition{
			"smoke": {
				Type:   "pytest",
				Kind:   projectconfig.TestKind("unknown-kind"),
				Pytest: map[string]any{"working-dir": "tests"},
			},
		},
	}

	err := file.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, projectconfig.ErrUnknownTestKind)
	assert.Contains(t, err.Error(), "unknown-kind")
}

func TestProjectConfigFileValidation_LisaSelectionMissing(t *testing.T) {
	file := projectconfig.ConfigFile{
		Tests: map[string]projectconfig.TestDefinition{
			"smoke": {
				Type: "lisa",
				Lisa: map[string]any{
					"source": map[string]any{"git-url": "https://example.com/lisa.git", "ref": "main"},
				},
			},
		},
	}

	err := file.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, projectconfig.ErrInvalidLisaSelection)
	assert.Contains(t, err.Error(), "must set at least one LISA selector")
}

func TestProjectConfigFileValidation_LisaSelectionCriteriaValid(t *testing.T) {
	file := projectconfig.ConfigFile{
		Tests: map[string]projectconfig.TestDefinition{
			"smoke": {
				Type: "lisa",
				Lisa: map[string]any{
					"criteria": map[string]any{"priority": []any{1, 2}, "tags": []any{"vm", "smoke"}},
				},
			},
			"perf": {
				Type: "lisa",
				Lisa: map[string]any{
					"criteria": []any{
						map[string]any{"area": "network", "category": "performance"},
						map[string]any{"testcaseNames": []any{"case_a", "case_b"}},
					},
				},
			},
		},
	}

	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_LisaSelectionUnsupportedCriteriaKey(t *testing.T) {
	file := projectconfig.ConfigFile{
		Tests: map[string]projectconfig.TestDefinition{
			"smoke": {
				Type: "lisa",
				Lisa: map[string]any{
					"criteria": map[string]any{"suite": "smoke"},
				},
			},
		},
	}

	err := file.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, projectconfig.ErrInvalidLisaSelection)
	assert.Contains(t, err.Error(), "unsupported selector")
}

func TestProjectConfigValidation_UndefinedTestReferenceInGroup(t *testing.T) {
	cfg := projectconfig.NewProjectConfig()
	cfg.TestGroups = map[string]projectconfig.TestGroup{
			"bvt": {
				Tests: []projectconfig.TestRef{{Name: "does-not-exist"}},
			},
	}

	err := cfg.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, projectconfig.ErrUndefinedTest)
	assert.Contains(t, err.Error(), "does-not-exist")
}

func TestProjectConfigValidation_UndefinedTestGroupReferenceInComponent(t *testing.T) {
	cfg := projectconfig.NewProjectConfig()
	cfg.Components = map[string]projectconfig.ComponentConfig{
			"openssl": {
				Tests: &projectconfig.ComponentTestsConfig{
					Tests: []projectconfig.TestRef{{Group: "missing-group"}},
				},
			},
	}

	err := cfg.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, projectconfig.ErrUndefinedTestGroup)
	assert.Contains(t, err.Error(), "missing-group")
}

func TestProjectConfigValidation_InvalidTestReferenceShapeInImage(t *testing.T) {
	cfg := projectconfig.NewProjectConfig()
	cfg.Images = map[string]projectconfig.ImageConfig{
			"base": {
				Tests: &projectconfig.ImageTestsConfig{
					Tests: []projectconfig.TestRef{{Name: "smoke", Group: "bvt"}},
				},
			},
	}
	cfg.Tests = map[string]projectconfig.TestDefinition{
			"smoke": {
				Type:   "pytest",
				Pytest: map[string]any{"working-dir": "tests"},
			},
	}
	cfg.TestGroups = map[string]projectconfig.TestGroup{
			"bvt": {Tests: []projectconfig.TestRef{{Name: "smoke"}}},
	}

	err := cfg.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, projectconfig.ErrInvalidTestRef)
	assert.Contains(t, err.Error(), "exactly one")
}

func TestProjectConfigValidation_DuplicateTestReferenceInGroup(t *testing.T) {
	cfg := projectconfig.NewProjectConfig()
	cfg.Tests = map[string]projectconfig.TestDefinition{
			"smoke": {
				Type:   "pytest",
				Pytest: map[string]any{"working-dir": "tests"},
			},
	}
	cfg.TestGroups = map[string]projectconfig.TestGroup{
			"bvt": {
				Tests: []projectconfig.TestRef{
					{Name: "smoke"},
					{Name: "smoke"},
				},
			},
	}

	err := cfg.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, projectconfig.ErrDuplicateTestRef)
	assert.Contains(t, err.Error(), "duplicates")
	assert.Contains(t, err.Error(), "smoke")
}

func TestProjectConfigValidation_DuplicateTestGroupReferenceInImage(t *testing.T) {
	cfg := projectconfig.NewProjectConfig()
	cfg.Tests = map[string]projectconfig.TestDefinition{
			"smoke": {
				Type:   "pytest",
				Pytest: map[string]any{"working-dir": "tests"},
			},
	}
	cfg.TestGroups = map[string]projectconfig.TestGroup{
			"bvt": {
				Tests: []projectconfig.TestRef{{Name: "smoke"}},
			},
	}
	cfg.Images = map[string]projectconfig.ImageConfig{
			"base": {
				Tests: &projectconfig.ImageTestsConfig{
					Tests: []projectconfig.TestRef{
						{Group: "bvt"},
						{Group: "bvt"},
					},
				},
			},
	}

	err := cfg.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, projectconfig.ErrDuplicateTestRef)
	assert.Contains(t, err.Error(), "duplicates")
	assert.Contains(t, err.Error(), "bvt")
}

func TestProjectConfigValidation_NestedTestGroupReferenceNotAllowed(t *testing.T) {
	cfg := projectconfig.NewProjectConfig()
	cfg.Tests = map[string]projectconfig.TestDefinition{
			"smoke": {
				Type:   "pytest",
				Pytest: map[string]any{"working-dir": "tests"},
			},
	}
	cfg.TestGroups = map[string]projectconfig.TestGroup{
			"a": {Tests: []projectconfig.TestRef{{Group: "b"}}},
			"b": {Tests: []projectconfig.TestRef{{Name: "smoke"}}},
	}

	err := cfg.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, projectconfig.ErrNestedTestGroupReference)
	assert.Contains(t, err.Error(), "is not allowed in [test-groups]")
}

func TestProjectConfigResolveImageTests_ExpandsGroups(t *testing.T) {
	cfg := projectconfig.NewProjectConfig()
	cfg.Tests = map[string]projectconfig.TestDefinition{
		"static-image-checks": {Type: "pytest", Pytest: map[string]any{"working-dir": "tests"}},
		"functional_core":    {Type: "lisa", Lisa: map[string]any{"criteria": map[string]any{"priority": []any{1}}}},
		"lisa_perf":          {Type: "lisa", Lisa: map[string]any{"criteria": map[string]any{"area": "network", "category": "performance"}}},
	}
	cfg.TestGroups = map[string]projectconfig.TestGroup{
		"vm-base-functional": {Tests: []projectconfig.TestRef{{Name: "functional_core"}}},
		"vm-base-performance": {Tests: []projectconfig.TestRef{{Name: "lisa_perf"}}},
	}

	imageCfg := &projectconfig.ImageConfig{
		Tests: &projectconfig.ImageTestsConfig{
			Tests: []projectconfig.TestRef{
				{Name: "static-image-checks"},
				{Group: "vm-base-functional"},
				{Group: "vm-base-performance"},
			},
		},
	}

	resolved, err := cfg.ResolveImageTests(imageCfg)
	require.NoError(t, err)
	require.Len(t, resolved, 3)
	assert.Equal(t, []string{"static-image-checks", "functional_core", "lisa_perf"}, []string{
		resolved[0].Name,
		resolved[1].Name,
		resolved[2].Name,
	})
}

func TestProjectConfigResolveComponentTests_ExpandsGroups(t *testing.T) {
	cfg := projectconfig.NewProjectConfig()
	cfg.Tests = map[string]projectconfig.TestDefinition{
		"bash-fedora-shell": {Type: "tmt", Tmt: map[string]any{"plan": "/plans/shell"}},
	}
	cfg.TestGroups = map[string]projectconfig.TestGroup{
		"shell-tests": {Tests: []projectconfig.TestRef{{Name: "bash-fedora-shell"}}},
	}

	componentCfg := &projectconfig.ComponentConfig{
		Tests: &projectconfig.ComponentTestsConfig{
			Tests: []projectconfig.TestRef{{Group: "shell-tests"}},
		},
	}

	resolved, err := cfg.ResolveComponentTests(componentCfg)
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	assert.Equal(t, "bash-fedora-shell", resolved[0].Name)
	assert.Equal(t, "tmt", resolved[0].Definition.Type)
}

func TestProjectConfigFileValidation_DefaultProjectInfo(t *testing.T) {
	file := projectconfig.ConfigFile{
		Project: &projectconfig.ProjectInfo{},
	}
	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_InvalidIncludePath(t *testing.T) {
	file := projectconfig.ConfigFile{
		Includes: []string{""},
	}
	assert.Error(t, file.Validate())
}

func TestProjectConfigFileValidation_ValidBuildCheckSkip(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				Build: projectconfig.ComponentBuildConfig{
					Check: projectconfig.CheckConfig{
						Skip:       true,
						SkipReason: "Tests require network access",
					},
				},
			},
		},
	}
	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_InvalidBuildCheckSkip(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				Build: projectconfig.ComponentBuildConfig{
					Check: projectconfig.CheckConfig{
						Skip: true,
						// Missing Reason
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reason")
	assert.Contains(t, err.Error(), "test-component")
}

func TestProjectConfigFileValidation_DuplicateSourceFileName(t *testing.T) {
	origin := projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/file"}

	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{Filename: "source.tar.gz", Hash: "abc", HashType: fileutils.HashTypeSHA256, Origin: origin},
					{Filename: "another.tar.gz", Hash: "def", HashType: fileutils.HashTypeSHA256, Origin: origin},
					{Filename: "source.tar.gz", Hash: "ghi", HashType: fileutils.HashTypeSHA256, Origin: origin}, // duplicate
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate filename")
	assert.Contains(t, err.Error(), "source.tar.gz")
	assert.Contains(t, err.Error(), "test-component")
}

func TestProjectConfigFileValidation_UniqueSourceFileNames(t *testing.T) {
	origin := projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/file"}

	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{Filename: "source.tar.gz", Hash: "abc", HashType: fileutils.HashTypeSHA256, Origin: origin},
					{Filename: "another.tar.gz", Hash: "def", HashType: fileutils.HashTypeSHA256, Origin: origin},
					{Filename: "patch.patch", Hash: "ghi", HashType: fileutils.HashTypeSHA256, Origin: origin},
				},
			},
		},
	}
	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_EmptySourceFiles(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{},
			},
		},
	}
	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_MD5HashTypeDisallowed(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "source.tar.gz",
						HashType: fileutils.HashTypeMD5,
						Hash:     "abc123",
						Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/source.tar.gz"},
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported hash type")
	assert.Contains(t, err.Error(), "source.tar.gz")
	assert.Contains(t, err.Error(), "test-component")
}

func TestProjectConfigFileValidation_SHA256HashTypeAllowed(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "source.tar.gz",
						HashType: fileutils.HashTypeSHA256,
						Hash:     "abc123",
						Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/source.tar.gz"},
					},
				},
			},
		},
	}
	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_UnsupportedHashType(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "source.tar.gz",
						HashType: "sha128",
						Hash:     "abc123",
						Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/source.tar.gz"},
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported hash type")
	assert.Contains(t, err.Error(), "sha128")
	assert.Contains(t, err.Error(), "source.tar.gz")
	assert.Contains(t, err.Error(), "test-component")
}

func TestProjectConfigFileValidation_HashWithoutHashType(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "source.tar.gz",
						Hash:     "abc123",
						Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/source.tar.gz"},
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hash value specified without hash type")
	assert.Contains(t, err.Error(), "source.tar.gz")
	assert.Contains(t, err.Error(), "test-component")
}

func TestProjectConfigFileValidation_ReplaceUpstreamRequiresReason(t *testing.T) {
	origin := projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/source.tar.gz"}

	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename:        "source.tar.gz",
						Hash:            "abc123",
						HashType:        fileutils.HashTypeSHA256,
						Origin:          origin,
						ReplaceUpstream: true,
						// ReplaceReason intentionally empty.
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "replace-upstream = true")
	assert.Contains(t, err.Error(), "no 'replace-reason'")
	assert.Contains(t, err.Error(), "source.tar.gz")
	assert.Contains(t, err.Error(), "test-component")
}

func TestProjectConfigFileValidation_ReplaceReasonRequiresReplaceUpstream(t *testing.T) {
	origin := projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/source.tar.gz"}

	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename:      "source.tar.gz",
						Hash:          "abc123",
						HashType:      fileutils.HashTypeSHA256,
						Origin:        origin,
						ReplaceReason: "stray reason without the flag",
						// ReplaceUpstream intentionally false.
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'replace-reason' is only valid when 'replace-upstream = true'")
	assert.Contains(t, err.Error(), "source.tar.gz")
	assert.Contains(t, err.Error(), "test-component")
}

func TestProjectConfigFileValidation_ReplaceUpstreamWithReasonAccepted(t *testing.T) {
	origin := projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/source.tar.gz"}

	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename:        "source.tar.gz",
						Hash:            "abc123",
						HashType:        fileutils.HashTypeSHA256,
						Origin:          origin,
						ReplaceUpstream: true,
						ReplaceReason:   "patched to fix upstream regression",
					},
				},
			},
		},
	}
	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_ReplaceReasonWhitespaceOnlyRejected(t *testing.T) {
	origin := projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/source.tar.gz"}

	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename:        "source.tar.gz",
						Hash:            "abc123",
						HashType:        fileutils.HashTypeSHA256,
						Origin:          origin,
						ReplaceUpstream: true,
						// Whitespace-only reason must be rejected just like an empty string;
						// it provides no auditable explanation for the override.
						ReplaceReason: "   \t  ",
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no 'replace-reason'")
	assert.Contains(t, err.Error(), "source.tar.gz")
}

// TestProjectConfigFileValidation_ReplaceReasonWhitespaceOnlyWithoutReplaceUpstreamRejected
// guards against silently accepting a whitespace-only 'replace-reason' when
// 'replace-upstream' is false: the user obviously meant to set the field, so
// surface the configuration mistake rather than letting it pass because the
// value happens to trim to empty.
func TestProjectConfigFileValidation_ReplaceReasonWhitespaceOnlyWithoutReplaceUpstreamRejected(t *testing.T) {
	origin := projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/source.tar.gz"}

	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "source.tar.gz",
						Hash:     "abc123",
						HashType: fileutils.HashTypeSHA256,
						Origin:   origin,
						// ReplaceUpstream intentionally false; a whitespace-only reason
						// must still trip the "reason set without replace-upstream" guard.
						ReplaceReason: "   \t  ",
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'replace-reason' is only valid when 'replace-upstream = true'")
	assert.Contains(t, err.Error(), "source.tar.gz")
	assert.Contains(t, err.Error(), "test-component")
}

func TestProjectConfigFileValidation_MissingOrigin(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "source.tar.gz",
						Hash:     "abc123",
						HashType: fileutils.HashTypeSHA256,
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing 'origin'")
	assert.Contains(t, err.Error(), "source.tar.gz")
	assert.Contains(t, err.Error(), "test-component")
}

func TestProjectConfigFileValidation_MissingHashWithOrigin(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "source.tar.gz",
						Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.com/source.tar.gz"},
					},
				},
			},
		},
	}
	assert.NoError(t, file.Validate())
}

func TestProjectConfigFileValidation_DownloadOriginMissingURI(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "source.tar.gz",
						Hash:     "abc123",
						HashType: fileutils.HashTypeSHA256,
						Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeURI},
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing 'uri'")
	assert.Contains(t, err.Error(), "source.tar.gz")
	assert.Contains(t, err.Error(), "test-component")
}

func TestProjectConfigFileValidation_DownloadOriginInvalidURI(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "source.tar.gz",
						Hash:     "abc123",
						HashType: fileutils.HashTypeSHA256,
						Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "not-a-uri"},
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid 'uri'")
	assert.Contains(t, err.Error(), "missing a scheme")
}

func TestProjectConfigFileValidation_UnsupportedOriginType(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "source.tar.gz",
						Hash:     "abc123",
						HashType: fileutils.HashTypeSHA256,
						Origin:   projectconfig.Origin{Type: "ftp"},
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported 'origin' type")
	assert.Contains(t, err.Error(), "ftp")
}

func TestProjectConfigFileValidation_PerComponentSnapshotDisallowed(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"test-component": {
				Spec: projectconfig.SpecSource{
					SourceType: projectconfig.SpecSourceTypeUpstream,
					UpstreamDistro: projectconfig.DistroReference{
						Snapshot: "2026-01-01T00:00:00Z",
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "snapshot")
	assert.Contains(t, err.Error(), "test-component")
}

// --- Custom origin source file validation ---

func TestValidateCustomSourceRef_ValidCustomOrigin(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"comp": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "gen.tar.gz",
						Origin: projectconfig.Origin{
							Type:   projectconfig.OriginTypeCustom,
							Script: "gen.sh",
						},
					},
				},
			},
		},
	}
	assert.NoError(t, file.Validate())
}

func TestValidateCustomSourceRef_MissingScript(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"comp": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "gen.tar.gz",
						Origin:   projectconfig.Origin{Type: projectconfig.OriginTypeCustom},
						// Script intentionally absent
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "script")
	assert.Contains(t, err.Error(), "gen.tar.gz")
	assert.Contains(t, err.Error(), "comp")
}

func TestValidateCustomSourceRef_ScriptOnDownloadOrigin(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"comp": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "src.tar.gz",
						Origin: projectconfig.Origin{
							Type:   projectconfig.OriginTypeURI,
							Uri:    "https://example.com/src.tar.gz",
							Script: "gen.sh",
						},
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'script'")
	assert.Contains(t, err.Error(), "custom")
}

func TestValidateCustomSourceRef_MockPackagesOnDownloadOrigin(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"comp": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "src.tar.gz",
						Origin: projectconfig.Origin{
							Type:         projectconfig.OriginTypeURI,
							Uri:          "https://example.com/src.tar.gz",
							MockPackages: []string{"curl"},
						},
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'mock-packages'")
	assert.Contains(t, err.Error(), "custom")
}

func TestValidateCustomSourceRef_UriOnCustomOrigin(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"comp": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "gen.tar.gz",
						Origin: projectconfig.Origin{
							Type:   projectconfig.OriginTypeCustom,
							Uri:    "https://example.com/should-not-be-here",
							Script: "gen.sh",
						},
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'uri'")
	assert.Contains(t, err.Error(), "custom")
}

func TestValidateCustomSourceRef_InvalidScriptFilename(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"comp": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "gen.tar.gz",
						Origin: projectconfig.Origin{
							Type:   projectconfig.OriginTypeCustom,
							Script: "../../escape.sh", // path traversal attempt
						},
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "script")
}

func TestValidateCustomSourceRef_ValidInputs(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"comp": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "gen.tar.gz",
						Origin: projectconfig.Origin{
							Type:   projectconfig.OriginTypeCustom,
							Script: "gen.sh",
							Inputs: []string{"upstream.tar.gz", "fix.patch"},
						},
					},
				},
			},
		},
	}
	assert.NoError(t, file.Validate())
}

func TestValidateCustomSourceRef_DuplicateInputs(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"comp": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "gen.tar.gz",
						Origin: projectconfig.Origin{
							Type:   projectconfig.OriginTypeCustom,
							Script: "gen.sh",
							Inputs: []string{"upstream.tar.gz", "upstream.tar.gz"},
						},
					},
				},
			},
		},
	}

	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate 'inputs' entry")
	assert.Contains(t, err.Error(), "upstream.tar.gz")
	assert.Contains(t, err.Error(), "gen.tar.gz")
	assert.Contains(t, err.Error(), "comp")
}

func TestValidateCustomSourceRef_InputsOnDownloadOrigin(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"comp": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "src.tar.gz",
						Origin: projectconfig.Origin{
							Type:   projectconfig.OriginTypeURI,
							Uri:    "https://example.com/src.tar.gz",
							Inputs: []string{"other.tar.gz"},
						},
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'inputs'")
	assert.Contains(t, err.Error(), "custom")
}

func TestValidateCustomSourceRef_InvalidInputFilename(t *testing.T) {
	file := projectconfig.ConfigFile{
		Components: map[string]projectconfig.ComponentConfig{
			"comp": {
				SourceFiles: []projectconfig.SourceFileReference{
					{
						Filename: "gen.tar.gz",
						Origin: projectconfig.Origin{
							Type:   projectconfig.OriginTypeCustom,
							Script: "gen.sh",
							Inputs: []string{"../escape.tar.gz"}, // path traversal attempt
						},
					},
				},
			},
		},
	}
	err := file.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'inputs'")
	assert.Contains(t, err.Error(), "escape.tar.gz")
}
