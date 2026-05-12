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
	t.Setenv("AZLDEV_ENABLE_LOCK_VALIDATION", "1")

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
