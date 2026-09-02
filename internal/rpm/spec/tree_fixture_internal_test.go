// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"bufio"
	"embed"
	"math/rand/v2"
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/specs/*.spec
var treeFixtureFS embed.FS

func TestStructuralFixtureTreesRoundTripByteForByte(t *testing.T) {
	entries, err := treeFixtureFS.ReadDir("testdata/specs")
	require.NoError(t, err)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		t.Run(entry.Name(), func(t *testing.T) {
			lines := fixtureLines(t, entry.Name())
			tree, err := parseTree(lines)
			require.NoError(t, err)
			assert.Equal(t, lines, serializeTree(tree))
		})
	}
}

func TestStructuralTreesReparseEditedFixtureAndSyntheticOutputs(t *testing.T) {
	inputs := map[string][]string{
		"fixture": fixtureLines(t, "straddling-wrapper.spec"),
	}

	for seed := uint64(1); seed <= 32; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed+1)) //nolint:gosec // Fixed test seeds.
		inputs["synthetic-"+strconv.FormatUint(seed, 10)] = syntheticTreeLines(rng.IntN(4) + 1)
	}

	for name, lines := range inputs {
		t.Run(name, func(t *testing.T) {
			tree, err := parseTree(lines)
			require.NoError(t, err)

			sections := (&specTree{root: tree}).Sections("%build", "")
			require.NotEmpty(t, sections)

			for _, section := range sections {
				section.AppendLines([]string{"/usr/share/structural-marker"})
			}

			edited := serializeTree(tree)
			reparsed, err := parseTree(edited)
			require.NoError(t, err)
			assert.Equal(t, edited, serializeTree(reparsed))
		})
	}
}

func fixtureLines(t *testing.T, name string) []string {
	t.Helper()

	contents, err := treeFixtureFS.ReadFile(path.Join("testdata/specs", name))
	require.NoError(t, err)

	var lines []string

	scanner := bufio.NewScanner(strings.NewReader(string(contents)))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	require.NoError(t, scanner.Err())

	return lines
}

func syntheticTreeLines(branches int) []string {
	lines := []string{"Name: synthetic"}
	for branch := range branches {
		lines = append(lines,
			"%if "+strconv.Itoa(branch%2),
			"%package package"+strconv.Itoa(branch),
			"%description package"+strconv.Itoa(branch),
			"synthetic",
			"%files",
			"/usr/bin/synthetic",
			"%endif",
		)
	}

	lines = append(lines, "%build", "make")

	return lines
}
