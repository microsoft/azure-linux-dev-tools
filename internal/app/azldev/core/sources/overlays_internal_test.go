// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsGitInternalPath(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		// Root-level .git directory contents.
		{".git/HEAD", true},
		{".git/config", true},
		{".git/objects/pack/pack-abc.idx", true},

		// Pseudo-absolute root-level .git paths (/prefix from BasePathFs).
		{"/.git/HEAD", true},
		{"/.git/objects/pack/pack-abc.idx", true},

		// Nested .git directories (e.g., submodules).
		{"subdir/.git/HEAD", true},
		{"vendor/lib/.git/objects/pack-abc.idx", true},

		// Non-.git paths that should NOT match.
		{".gitignore", false},
		{".gitattributes", false},
		{"foo.spec", false},
		{"src/main.go", false},
		{"git/config", false},
		{"my.gitconfig", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.expected, isGitInternalPath(tt.path))
		})
	}
}
