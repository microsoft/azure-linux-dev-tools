// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lisa

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLisaSource(t *testing.T) {
	t.Run("missing source", func(t *testing.T) {
		_, err := parseLisaSource(map[string]any{"criteria": map[string]any{"name": "x"}}, "my-test")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be run locally")
		assert.Contains(t, err.Error(), "my-test")
	})

	t.Run("valid source", func(t *testing.T) {
		sha := "1234567890123456789012345678901234567890"
		lisa := map[string]any{
			"source": map[string]any{
				"git-url": "https://example.com/lisa.git",
				"ref":     sha,
			},
		}

		source, err := parseLisaSource(lisa, "my-test")
		require.NoError(t, err)
		assert.Equal(t, "https://example.com/lisa.git", source.GitURL)
		assert.Equal(t, sha, source.Ref)
	})

	t.Run("rejects non-sha ref", func(t *testing.T) {
		lisa := map[string]any{
			"source": map[string]any{
				"git-url": "https://example.com/lisa.git",
				"ref":     "20260330.1",
			},
		}

		_, err := parseLisaSource(lisa, "my-test")
		require.Error(t, err)
	})
}

func TestParseLisaCriteriaFromDefinition(t *testing.T) {
	t.Run("single criteria table with testcase-names", func(t *testing.T) {
		lisa := map[string]any{
			"criteria": map[string]any{
				"testcase-names": []any{"verify_cpu_count", "verify_grub"},
			},
		}

		criteria, err := parseLisaCriteriaFromDefinition(lisa, "my-test")
		require.NoError(t, err)
		require.Len(t, criteria, 1)
		assert.Equal(t, "verify_cpu_count|verify_grub", criteria[0].Name)
	})

	t.Run("priority-only criteria", func(t *testing.T) {
		lisa := map[string]any{
			"criteria": map[string]any{
				"priority": []any{int64(1)},
			},
		}

		criteria, err := parseLisaCriteriaFromDefinition(lisa, "my-test")
		require.NoError(t, err)
		require.Len(t, criteria, 1)
		assert.Equal(t, []int{1}, criteria[0].Priority)
		assert.Empty(t, criteria[0].Name)
	})

	t.Run("list of area/category criteria", func(t *testing.T) {
		lisa := map[string]any{
			"criteria": []any{
				map[string]any{"area": "network", "category": "performance"},
				map[string]any{"area": "storage", "category": "performance"},
			},
		}

		criteria, err := parseLisaCriteriaFromDefinition(lisa, "my-test")
		require.NoError(t, err)
		require.Len(t, criteria, 2)
		assert.Equal(t, "network", criteria[0].Area)
		assert.Equal(t, "performance", criteria[0].Category)
		assert.Equal(t, "storage", criteria[1].Area)
	})

	t.Run("top-level testcase-name fallback", func(t *testing.T) {
		lisa := map[string]any{
			"testcase-name": "verify_grub",
		}

		criteria, err := parseLisaCriteriaFromDefinition(lisa, "my-test")
		require.NoError(t, err)
		require.Len(t, criteria, 1)
		assert.Equal(t, "verify_grub", criteria[0].Name)
	})

	t.Run("no selector", func(t *testing.T) {
		_, err := parseLisaCriteriaFromDefinition(map[string]any{}, "my-test")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no LISA selector")
	})

	t.Run("tags on criteria", func(t *testing.T) {
		lisa := map[string]any{
			"criteria": map[string]any{
				"name": "verify_x",
				"tags": []any{"smoke", "fast"},
			},
		}

		criteria, err := parseLisaCriteriaFromDefinition(lisa, "my-test")
		require.NoError(t, err)
		require.Len(t, criteria, 1)
		assert.Equal(t, []string{"smoke", "fast"}, criteria[0].Tags)
	})
}

func TestToOptionalStringSlice(t *testing.T) {
	t.Run("nil value", func(t *testing.T) {
		result, err := toOptionalStringSlice(nil, "extra-args")
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("valid list", func(t *testing.T) {
		result, err := toOptionalStringSlice([]any{"-v", "--foo"}, "extra-args")
		require.NoError(t, err)
		assert.Equal(t, []string{"-v", "--foo"}, result)
	})

	t.Run("invalid type", func(t *testing.T) {
		_, err := toOptionalStringSlice("not-a-list", "extra-args")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "extra-args")
	})
}
