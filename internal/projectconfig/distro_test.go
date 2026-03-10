// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDistroReferenceStringer(t *testing.T) {
	t.Run("name and version", func(t *testing.T) {
		s := &projectconfig.DistroReference{
			Name:    "name",
			Version: "version",
		}

		assert.Equal(t, "name version", s.String())
	})

	t.Run("name only", func(t *testing.T) {
		s := &projectconfig.DistroReference{Name: "name"}

		assert.Equal(t, "name (default)", s.String())
	})

	t.Run("version only", func(t *testing.T) {
		s := &projectconfig.DistroReference{Version: "version"}

		assert.Equal(t, "(default) version", s.String())
	})

	t.Run("empty", func(t *testing.T) {
		s := &projectconfig.DistroReference{}

		assert.Equal(t, "(default) (default)", s.String())
	})
}

func TestDistroDefinition_MergeUpdatesFrom(t *testing.T) {
	t.Run("override description", func(t *testing.T) {
		base := &projectconfig.DistroDefinition{
			Description:    "original",
			DefaultVersion: "1.0",
		}

		other := &projectconfig.DistroDefinition{
			Description: "updated",
		}

		err := base.MergeUpdatesFrom(other)
		require.NoError(t, err)
		assert.Equal(t, "updated", base.Description)
		assert.Equal(t, "1.0", base.DefaultVersion)
	})

	t.Run("add new version", func(t *testing.T) {
		base := &projectconfig.DistroDefinition{
			Description: "distro",
			Versions: map[string]projectconfig.DistroVersionDefinition{
				"1.0": {Description: "v1", ReleaseVer: "1.0"},
			},
		}

		other := &projectconfig.DistroDefinition{
			Versions: map[string]projectconfig.DistroVersionDefinition{
				"2.0": {Description: "v2", ReleaseVer: "2.0"},
			},
		}

		err := base.MergeUpdatesFrom(other)
		require.NoError(t, err)
		assert.Equal(t, "distro", base.Description)
		assert.Len(t, base.Versions, 2)
		assert.Equal(t, "v1", base.Versions["1.0"].Description)
		assert.Equal(t, "v2", base.Versions["2.0"].Description)
	})

	t.Run("override existing version field", func(t *testing.T) {
		base := &projectconfig.DistroDefinition{
			Versions: map[string]projectconfig.DistroVersionDefinition{
				"1.0": {Description: "old desc", ReleaseVer: "1.0", DistGitBranch: "main"},
			},
		}

		other := &projectconfig.DistroDefinition{
			Versions: map[string]projectconfig.DistroVersionDefinition{
				"1.0": {Description: "new desc"},
			},
		}

		err := base.MergeUpdatesFrom(other)
		require.NoError(t, err)

		version := base.Versions["1.0"]
		assert.Equal(t, "new desc", version.Description)
		// mergo.WithOverride replaces the entire map value for the same key,
		// so non-specified fields will be zeroed out. This is intentional:
		// override configs are expected to fully redefine any version they touch.
		assert.Empty(t, version.ReleaseVer)
		assert.Empty(t, version.DistGitBranch)
	})

	t.Run("replace package repositories", func(t *testing.T) {
		base := &projectconfig.DistroDefinition{
			PackageRepositories: []projectconfig.PackageRepository{
				{BaseURI: "https://old-repo.example.com"},
				{BaseURI: "https://another-old-repo.example.com"},
			},
		}

		other := &projectconfig.DistroDefinition{
			PackageRepositories: []projectconfig.PackageRepository{
				{BaseURI: "https://new-repo.example.com"},
			},
		}

		err := base.MergeUpdatesFrom(other)
		require.NoError(t, err)
		require.Len(t, base.PackageRepositories, 1)
		assert.Equal(t, "https://new-repo.example.com", base.PackageRepositories[0].BaseURI)
	})
}
