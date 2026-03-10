// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package docs_test

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/docs"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestCommand creates a minimal cobra command for testing doc generation.
func newTestCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "A test command",
	}
}

func TestGenerateMarkdownDocs(t *testing.T) {
	// This integration test uses the host FS because cobra's doc.GenMarkdownTreeCustom
	// writes directly to the real filesystem.
	ctx := testctx.NewCtx(testctx.WithHostFS())

	// Select an output dir that won't yet exist. This lets us make sure it will get created.
	tempDir := t.TempDir()
	outputDir := filepath.Join(tempDir, "docs")

	err := docs.GenerateMarkdownDocs(ctx.FS(), newTestCommand(), &docs.GenerateMarkdownOptions{
		OutputDir: outputDir,
	})

	require.NoError(t, err)

	exists, err := fileutils.DirExists(ctx.FS(), outputDir)
	require.NoError(t, err)
	assert.True(t, exists, "Expected output directory to exist")

	matches, err := filepath.Glob(filepath.Join(outputDir, "*.md"))
	require.NoError(t, err)
	require.NotEmpty(t, matches, "Expected at least one markdown file to be generated")
}

func TestCheckOutputDir_NonExistentDir(t *testing.T) {
	testFS := afero.NewMemMapFs()

	err := docs.CheckOutputDir(testFS, &docs.GenerateMarkdownOptions{
		OutputDir: "/does/not/exist",
	})

	require.NoError(t, err)
}

func TestCheckOutputDir_EmptyDirWithoutForce(t *testing.T) {
	testFS := afero.NewMemMapFs()

	require.NoError(t, fileutils.MkdirAll(testFS, "/output"))

	err := docs.CheckOutputDir(testFS, &docs.GenerateMarkdownOptions{
		OutputDir: "/output",
		Force:     false,
	})

	require.NoError(t, err)
}

func TestCheckOutputDir_NonEmptyDirWithoutForce(t *testing.T) {
	testFS := afero.NewMemMapFs()

	require.NoError(t, fileutils.MkdirAll(testFS, "/output"))
	require.NoError(t, fileutils.WriteFile(testFS, "/output/existing.txt", []byte("hello"), fileperms.PrivateFile))

	err := docs.CheckOutputDir(testFS, &docs.GenerateMarkdownOptions{
		OutputDir: "/output",
		Force:     false,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force")
}

func TestCheckOutputDir_NonEmptyDirWithForce(t *testing.T) {
	testFS := afero.NewMemMapFs()

	require.NoError(t, fileutils.MkdirAll(testFS, "/output"))
	require.NoError(t, fileutils.WriteFile(testFS, "/output/existing.txt", []byte("hello"), fileperms.PrivateFile))

	err := docs.CheckOutputDir(testFS, &docs.GenerateMarkdownOptions{
		OutputDir: "/output",
		Force:     true,
	})

	require.NoError(t, err)

	// The directory should have been removed.
	exists, err := fileutils.DirExists(testFS, "/output")
	require.NoError(t, err)
	assert.False(t, exists, "Expected output directory to be removed by --force")
}
