// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/projecttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// We test running `azldev component query` to make sure that batch rpmspec
// processing against the rendered specs tree works as expected.
func TestQueryingAComponent(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	// Create a simple spec with a known name and version. Add a subpackage
	// so we can also verify that 'query' reports the binary subpackages.
	spec := projecttest.NewSpec(
		projecttest.WithName("test-component"),
		projecttest.WithVersion("3.1.4.159"),
		projecttest.WithSubpackage("extra"),
	)

	// Create a simple project with the spec, using test default configs for
	// distro and mock configurations.
	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec),
		projecttest.UseTestDefaultConfigs(),
	)

	// 'component query' now reads from the rendered specs tree, so render
	// first as a pre-command and then query.
	results := projecttest.NewProjectTest(
		project,
		[]string{"component", "query", spec.GetName()},
		projecttest.WithTestDefaultConfigs(),
		projecttest.WithPreCommand("component", "render", "-a"),
	).RunInContainer(t)

	// Get the parsed JSON output.
	output := results.GetJSONResult()

	// There should just be one component in the output.
	require.Len(t, output, 1, "Expected one component in the output")
	componentOutput := output[0]

	// Check for name.
	require.Contains(t, componentOutput, "Name")
	assert.Equal(t, spec.GetName(), componentOutput["Name"], "Expected component name to match")

	// Check for EVR structure.
	require.Contains(t, componentOutput, "Version")
	version := componentOutput["Version"]

	// Check for version sub-field.
	versionMap, ok := version.(map[string]interface{})
	require.True(t, ok, "Version field is not a map")
	require.Contains(t, versionMap, "Version")
	assert.Equal(t, spec.GetVersion(), versionMap["Version"])

	// Check that subpackages were extracted.
	require.Contains(t, componentOutput, "Subpackages")
	subpackages, ok := componentOutput["Subpackages"].([]interface{})
	require.True(t, ok, "Subpackages should be a list")

	subpkgNames := make([]string, 0, len(subpackages))
	for _, sp := range subpackages {
		name, ok := sp.(string)
		require.True(t, ok, "Subpackage entry should be a string")

		subpkgNames = append(subpkgNames, name)
	}

	assert.Contains(t, subpkgNames, spec.GetName(),
		"Subpackages should include the main package")
	assert.Contains(t, subpkgNames, spec.GetName()+"-extra",
		"Subpackages should include the explicitly-added subpackage")
}

// TestQueryingComponentsWithArchFilter exercises the --arch path end-to-end:
// ExclusiveArch / ExcludeArch are evaluated inside the mock chroot by the
// embedded query_process.py helper, so this verifies the Python-side arch
// policy (and the Go-side ExcludedFromArch plumbing) honor the requested
// target arch. We render three specs (x86_64-only, aarch64-only, and one
// that ExcludeArch's aarch64) then query twice, once per arch, and check
// that the right subset comes back with full SpecInfo while the rest are
// emitted as arch-excluded (Name only, no Version/Subpackages).
func TestQueryingComponentsWithArchFilter(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	x86Only := projecttest.NewSpec(
		projecttest.WithName("x86only-component"),
		projecttest.WithVersion("1.0.0"),
		projecttest.WithExclusiveArch("x86_64"),
	)
	armOnly := projecttest.NewSpec(
		projecttest.WithName("armonly-component"),
		projecttest.WithVersion("2.0.0"),
		projecttest.WithExclusiveArch("aarch64"),
	)
	noArm := projecttest.NewSpec(
		projecttest.WithName("noarm-component"),
		projecttest.WithVersion("3.0.0"),
		projecttest.WithExcludeArch("aarch64"),
	)

	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(x86Only),
		projecttest.AddSpec(armOnly),
		projecttest.AddSpec(noArm),
		projecttest.UseTestDefaultConfigs(),
	)

	type expectation struct {
		arch              string
		fullyQueried      []string // expect populated Version/Subpackages
		archExcludedNames []string // expect Name only, empty Version/Subpackages
	}

	cases := []expectation{
		{
			arch:              "x86_64",
			fullyQueried:      []string{x86Only.GetName(), noArm.GetName()},
			archExcludedNames: []string{armOnly.GetName()},
		},
		{
			arch:              "aarch64",
			fullyQueried:      []string{armOnly.GetName()},
			archExcludedNames: []string{x86Only.GetName(), noArm.GetName()},
		},
	}

	for _, tc := range cases {
		t.Run(tc.arch, func(t *testing.T) {
			t.Parallel()

			results := projecttest.NewProjectTest(
				project,
				[]string{"component", "query", "-a", "--arch", tc.arch},
				projecttest.WithTestDefaultConfigs(),
				projecttest.WithPreCommand("component", "render", "-a"),
			).RunInContainer(t)

			output := results.GetJSONResult()

			byName := make(map[string]map[string]interface{}, len(output))

			for _, entry := range output {
				name, ok := entry["Name"].(string)
				require.True(t, ok, "Name should be a string")
				byName[name] = entry
			}

			require.Len(t, output, len(tc.fullyQueried)+len(tc.archExcludedNames),
				"expected one result per component (queried + excluded)")

			for _, name := range tc.fullyQueried {
				entry, ok := byName[name]
				require.True(t, ok, "missing entry for %q in --arch %s output", name, tc.arch)

				version, ok := entry["Version"].(map[string]interface{})
				require.True(t, ok, "Version should be a map for %q", name)
				assert.NotEmpty(t, version["Version"],
					"fully-queried entry %q should have a populated Version.Version", name)

				subpackages, ok := entry["Subpackages"].([]interface{})
				require.True(t, ok, "Subpackages should be a list for %q", name)
				assert.NotEmpty(t, subpackages,
					"fully-queried entry %q should have at least one subpackage", name)
			}

			for _, name := range tc.archExcludedNames {
				entry, ok := byName[name]
				require.True(t, ok,
					"arch-excluded entry %q should still appear in output for --arch %s",
					name, tc.arch)

				// Excluded entries surface as Name-only; Version is the zero
				// rpm.Version, Subpackages is the zero slice. JSON renders
				// the zero Version as a map with empty fields, so just
				// check it has no populated Version.Version string.
				version, ok := entry["Version"].(map[string]interface{})
				require.True(t, ok, "Version should still be a map for excluded %q", name)
				assert.Empty(t, version["Version"],
					"arch-excluded entry %q should not have a populated Version.Version", name)
				assert.Empty(t, entry["Subpackages"],
					"arch-excluded entry %q should have no subpackages", name)
			}
		})
	}
}
