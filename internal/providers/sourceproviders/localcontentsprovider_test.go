// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/specs"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/specs/specs_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const testOutputDir = "/output"

type localFetcherTestParams struct {
	ctrl      *gomock.Controller
	env       *testutils.TestEnv
	component *components_testutils.MockComponent
	spec      *specs_testutils.MockComponentSpec

	specContents string
	specDir      string
	specPath     string
	specDetails  *specs.ComponentSpecDetails
}

func setupLocalFetcherTest(t *testing.T) *localFetcherTestParams {
	t.Helper()

	const (
		testSpecDir       = "/fetched/spec"
		testSpecContents  = "Name: test"
		testComponentName = "test"
	)

	// Construct a test version.
	ver, err := rpm.NewVersion("1.0.0-1")
	require.NoError(t, err)

	// Create test environment, mock component and spec.
	ctrl := gomock.NewController(t)
	env := testutils.NewTestEnv(t)

	params := &localFetcherTestParams{
		ctrl:      ctrl,
		env:       env,
		component: components_testutils.NewMockComponent(ctrl),
		spec:      specs_testutils.NewMockComponentSpec(ctrl),

		specContents: testSpecContents,
		specDir:      testSpecDir,
		specPath:     filepath.Join(testSpecDir, testComponentName+".spec"),
		specDetails: &specs.ComponentSpecDetails{
			SpecInfo: rpm.SpecInfo{
				Name:          testComponentName,
				Version:       *ver,
				RequiredFiles: []string{},
			},
		},
	}

	localSpec := projectconfig.SpecSource{
		SourceType: projectconfig.SpecSourceTypeLocal,
	}
	componentConfig := &projectconfig.ComponentConfig{
		Spec: localSpec,
	}

	// Setup test component and spec.
	params.component.EXPECT().GetConfig().AnyTimes().Return(componentConfig)
	params.component.EXPECT().GetName().AnyTimes().Return(testComponentName)
	params.component.EXPECT().GetSpec().AnyTimes().Return(params.spec)

	// Have the mock's GetPath() check whether the spec file exists in the test filesystem.
	params.spec.EXPECT().GetPath().AnyTimes().DoAndReturn(func() (string, error) {
		_, err := params.env.FS().Stat(params.specPath)
		if err != nil {
			return "", err
		}

		return params.specPath, nil
	})

	// Setup Parse() to return spec details.
	params.spec.EXPECT().Parse().AnyTimes().Return(params.specDetails, nil)

	// Create the spec file.
	err = fileutils.WriteFile(params.env.FS(), params.specPath, []byte(testSpecContents), fileperms.PublicFile)
	require.NoError(t, err)

	return params
}

func TestFetchLocalComponent_NoRequiredFiles(t *testing.T) {
	testParams := setupLocalFetcherTest(t)

	err := sourceproviders.FetchLocalComponent(
		testParams.env.DryRunnable,
		testParams.env.EventListener,
		testParams.env.FS(),
		testParams.component, testOutputDir,
		true,
	)
	require.NoError(t, err)

	// Inspect the output directory; it should only contain the spec file.
	dirEntries, err := fileutils.ReadDir(testParams.env.FS(), testOutputDir)
	require.NoError(t, err)

	// Extract just the file names.
	fileNames := lo.Map(dirEntries, func(fileInfo os.FileInfo, _ int) string {
		return fileInfo.Name()
	})

	specFileName := filepath.Base(testParams.specPath)
	require.ElementsMatch(t, fileNames, []string{specFileName}, "Output directory should only contain the spec file")

	// Make sure the spec file was copied correctly.
	outputSpecPath := filepath.Join(testOutputDir, filepath.Base(testParams.specPath))
	contents, err := fileutils.ReadFile(testParams.env.FS(), outputSpecPath)
	require.NoError(t, err)
	require.Equal(t, testParams.specContents, string(contents))
}

func TestFetchLocalComponent_RequiredFileMissing(t *testing.T) {
	testParams := setupLocalFetcherTest(t)

	// Add a required file not present in the test filesystem.
	testParams.specDetails.RequiredFiles = []string{"required-file.txt"}

	err := sourceproviders.FetchLocalComponent(
		testParams.env.DryRunnable,
		testParams.env.EventListener,
		testParams.env.FS(),
		testParams.component,
		testOutputDir,
		true,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "required-file.txt")
}

func TestFetchLocalComponent_RequiredFilePresent(t *testing.T) {
	const (
		requiredFilename     = "required-file.txt"
		requiredFileContents = "This is a required file."
	)

	testParams := setupLocalFetcherTest(t)

	// Add a required file and create it in the same dir as the spec.
	testParams.specDetails.RequiredFiles = []string{requiredFilename}
	err := fileutils.WriteFile(testParams.env.FS(), filepath.Join(testParams.specDir, requiredFilename),
		[]byte(requiredFileContents), fileperms.PublicFile)
	require.NoError(t, err)

	err = sourceproviders.FetchLocalComponent(
		testParams.env.DryRunnable,
		testParams.env.EventListener,
		testParams.env.FS(),
		testParams.component,
		testOutputDir,
		true,
	)
	require.NoError(t, err)

	// Inspect the output directory; it should contain the spec file and the required file.
	dirEntries, err := fileutils.ReadDir(testParams.env.FS(), testOutputDir)
	require.NoError(t, err)

	// Extract just the file names.
	fileNames := lo.Map(dirEntries, func(fileInfo os.FileInfo, _ int) string {
		return fileInfo.Name()
	})

	require.ElementsMatch(t, fileNames, []string{filepath.Base(testParams.specPath), requiredFilename})

	// Make sure the required file was copied correctly.
	outputRequiredFilePath := filepath.Join(testOutputDir, requiredFilename)
	contents, err := fileutils.ReadFile(testParams.env.FS(), outputRequiredFilePath)
	require.NoError(t, err)
	require.Equal(t, requiredFileContents, string(contents))
}

func TestFetchLocalComponent_MissingSpec(t *testing.T) {
	testParams := setupLocalFetcherTest(t)

	// Delete the spec to simulate a missing spec file.
	err := testParams.env.FS().Remove(testParams.specPath)
	require.NoError(t, err)

	err = sourceproviders.FetchLocalComponent(
		testParams.env.DryRunnable,
		testParams.env.EventListener,
		testParams.env.FS(),
		testParams.component,
		testOutputDir,
		true,
	)
	require.Error(t, err)
}
