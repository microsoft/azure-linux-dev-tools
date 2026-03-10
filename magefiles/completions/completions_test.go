//nolint:testpackage // Allow to test private functions
package completions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileDoesNotExist(t *testing.T) {
	// Removing lines from non-existing file should not fail.
	err := removeLinesFromFile("non_existent_file.txt", "#start", "#end")
	assert.NoError(t, err)
}

func TestNoMarkers(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "testfile.txt")
	initialContent := "line1\nline2\nline3"

	if err := os.WriteFile(filePath, []byte(initialContent), fileperms.PrivateFile); err != nil {
		t.Fatal(err)
	}

	err := removeLinesFromFile(filePath, "#start", "#end")
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	data, _ := os.ReadFile(filePath)
	if string(data) != initialContent {
		t.Errorf("Expected file content unchanged, got: %s", string(data))
	}
}

func TestMarkersPresent(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "testfile.txt")

	initialLines := []string{
		"first line",
		"#start",
		"line to remove 1",
		"line to remove 2",
		"#end",
		"last line",
	}

	err := os.WriteFile(filePath, []byte(strings.Join(initialLines, "\n")), fileperms.PrivateFile)
	require.NoError(t, err)

	err = removeLinesFromFile(filePath, "#start", "#end")
	require.NoError(t, err)

	data, _ := os.ReadFile(filePath)
	lines := strings.Split(string(data), "\n")
	expected := []string{"first line", "last line"}

	assert.Equal(t, expected, lines)
}

func TestMarkersMisaligned(t *testing.T) {
	inputs := []struct {
		name string
		data []string
	}{
		{
			name: "flip",
			data: []string{"l1", "#end", "l2", "#start", "l3"},
		},
		{
			name: "no start",
			data: []string{"l1", "l2", "#end", "l3"},
		},
		{
			name: "no end",
			data: []string{"#start", "l1", "l2", "l3"},
		},
	}

	for _, input := range inputs {
		t.Run(input.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			filePath := filepath.Join(tmpDir, "testfile.txt")

			err := os.WriteFile(filePath, []byte(strings.Join(input.data, "\n")), fileperms.PrivateFile)
			require.NoError(t, err)

			err = removeLinesFromFile(filePath, "#start", "#end")
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrCompletion)
		})
	}
}
