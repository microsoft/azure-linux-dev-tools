// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/require"
)

func TestSpecSourceValidation_SourceTypes(t *testing.T) {
	// An empty spec source should be valid. It indicates that inherited defaults should be used.
	require.NoError(t, validator.New().Struct(&projectconfig.SpecSource{}))

	// Valid source types should be okay.
	require.NoError(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType: projectconfig.SpecSourceTypeLocal,
		Path:       "/some/path",
	}))
	require.NoError(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType: projectconfig.SpecSourceTypeUpstream,
	}))

	// An invalid source type should get flagged.
	require.Error(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType: "invalid",
	}))
}

func TestSpecSourceValidation_SourceTargets(t *testing.T) {
	// A spec source can only specify a non-empty local path if the type is explicitly listed as local.
	require.NoError(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType: projectconfig.SpecSourceTypeLocal,
		Path:       "/some/path",
	}))
	require.Error(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType: projectconfig.SpecSourceTypeUpstream,
		Path:       "/some/path",
	}))
	require.Error(t, validator.New().Struct(&projectconfig.SpecSource{
		Path: "/some/path",
	}))

	// A spec source explicitly listed as local must be accompanied by a non-empty path.
	require.Error(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType: projectconfig.SpecSourceTypeLocal,
	}))
}

func TestSpecSourceValidation_UpstreamCommit(t *testing.T) {
	// UpstreamCommit is valid when type is upstream and value is a valid hex hash.
	require.NoError(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType:     projectconfig.SpecSourceTypeUpstream,
		UpstreamCommit: "abc1234",
	}))

	// Full 40-char SHA is valid.
	require.NoError(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType:     projectconfig.SpecSourceTypeUpstream,
		UpstreamCommit: "abc1234def5678abc1234def5678abc1234def56",
	}))

	// UpstreamCommit is optional (empty is fine with type=upstream).
	require.NoError(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType: projectconfig.SpecSourceTypeUpstream,
	}))

	// UpstreamCommit is rejected when type is local.
	require.Error(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType:     projectconfig.SpecSourceTypeLocal,
		Path:           "/some/path",
		UpstreamCommit: "abc1234",
	}))

	// UpstreamCommit is rejected when type is unspecified.
	require.Error(t, validator.New().Struct(&projectconfig.SpecSource{
		UpstreamCommit: "abc1234",
	}))

	// UpstreamCommit is rejected when non-hex.
	require.Error(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType:     projectconfig.SpecSourceTypeUpstream,
		UpstreamCommit: "not-a-hex-value",
	}))

	// UpstreamCommit is rejected when too short (< 7 chars).
	require.Error(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType:     projectconfig.SpecSourceTypeUpstream,
		UpstreamCommit: "abc12",
	}))

	// UpstreamCommit is rejected when too long (> 40 chars).
	require.Error(t, validator.New().Struct(&projectconfig.SpecSource{
		SourceType:     projectconfig.SpecSourceTypeUpstream,
		UpstreamCommit: "abc1234def5678abc1234def5678abc1234def5678a",
	}))
}
