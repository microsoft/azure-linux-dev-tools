// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/projecttest"
	"github.com/stretchr/testify/require"
)

func TestCustomSourceRegenerationDetectsStaleHash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test")
	}

	const (
		componentName = "custom-source"
		scriptName    = "generate.sh"
		archiveName   = "generated.tar.gz"
	)

	spec := projecttest.NewSpec(projecttest.WithName(componentName))
	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec),
		projecttest.AddComponent(&projectconfig.ComponentConfig{
			Name: componentName,
			Spec: projectconfig.SpecSource{
				SourceType: projectconfig.SpecSourceTypeLocal,
				Path:       filepath.Join("specs", componentName, componentName+".spec"),
			},
			SourceFiles: []projectconfig.SourceFileReference{
				{
					Filename: archiveName,
					HashType: fileutils.HashTypeSHA512,
					Origin: projectconfig.Origin{
						Type:   projectconfig.OriginTypeCustom,
						Script: scriptName,
					},
				},
			},
		}),
		projecttest.AddFile(filepath.Join("specs", componentName, scriptName), `#!/bin/sh
set -eu
printf 'version-one\n' > /azldev-gen/output/payload.txt
`),
		projecttest.UseTestDefaultConfigs(),
	)

	projectStagingDir := t.TempDir()
	project.Serialize(t, projectStagingDir)

	testScript := `
set -eux

azldev -C project -v component prep-sources -p custom-source \
	-o prepared --without-git --allow-no-hashes

test "$(tar -xOf prepared/generated.tar.gz payload.txt)" = "version-one"

GENERATED_HASH="$(sha512sum prepared/generated.tar.gz | awk '{print $1}')"
sed -i "/hash-type =/a hash = \"$GENERATED_HASH\"" project/azldev.toml
sed -i 's/version-one/version-two/' project/specs/custom-source/generate.sh

if azldev -C project -v component prep-sources -p custom-source \
	-o prepared --without-git --force 2>second-run.stderr; then
    echo "expected regenerated custom source to fail hash validation" >&2
    exit 1
fi

grep -q "hash validation failed" second-run.stderr
test "$(tar -xOf prepared/generated.tar.gz payload.txt)" = "version-two"
`

	scenarioTest := cmdtest.NewScenarioTest().
		WithScript(strings.NewReader(testScript)).
		AddDirRecursive(t, "project", projectStagingDir).
		AddDirRecursive(t, projecttest.TestDefaultConfigsSubdir, projecttest.TestDefaultConfigsDir())

	results, err := scenarioTest.
		InContainer().
		WithPrivilege().
		WithNetwork().
		Run(t)
	require.NoError(t, err)

	t.Logf("Standard output:\n%s", results.Stdout)
	t.Logf("Standard error:\n%s", results.Stderr)
	results.AssertZeroExitCode(t)

	stderrPath := filepath.Join(results.Workdir, "second-run.stderr")
	require.FileExists(t, stderrPath)

	stderr, err := os.ReadFile(stderrPath)
	require.NoError(t, err)
	require.Contains(t, string(stderr), "hash validation failed")
}
