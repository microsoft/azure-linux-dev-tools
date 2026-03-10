// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package componentbuilder_test

import (
	"context"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/componentbuilder"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/specs"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/specs/specs_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/workdir"
	"github.com/microsoft/azure-linux-dev-tools/internal/buildenv"
	"github.com/microsoft/azure-linux-dev-tools/internal/buildenv/buildenv_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/sourceproviders_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type componentBuilderTestParams struct {
	ctrl    *gomock.Controller
	testEnv *testutils.TestEnv

	sourcePreparer sources.SourcePreparer
	buildEnv       *buildenv_testutils.MockRPMAwareBuildEnv
	workDirFactory *workdir.Factory
	builder        *componentbuilder.Builder
}

func setupBuilder(t *testing.T) *componentBuilderTestParams {
	t.Helper()

	ctrl := gomock.NewController(t)
	testEnv := testutils.NewTestEnv(t)

	workDirFactory, err := workdir.NewFactory(testEnv.Env.FS(), testEnv.Env.WorkDir(), testEnv.Env.ConstructionTime())
	require.NoError(t, err)

	sourceManager := sourceproviders_test.NewMockSourceManager(ctrl)

	// Configure the source manager to create a spec file when FetchComponent is called.
	sourceManager.EXPECT().FetchComponent(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(ctx context.Context, component components.Component, outputDir string) error {
			// Create the expected spec file.
			specPath := filepath.Join(outputDir, component.GetName()+".spec")

			return fileutils.WriteFile(testEnv.Env.FS(), specPath, []byte("# test spec"), fileperms.PublicFile)
		},
	)

	sourceManager.EXPECT().FetchFiles(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)

	preparer, err := sources.NewPreparer(sourceManager, testEnv.Env.FS(), testEnv.Env, testEnv.Env)

	require.NoError(t, err)

	params := &componentBuilderTestParams{
		ctrl:           ctrl,
		testEnv:        testEnv,
		sourcePreparer: preparer,
		workDirFactory: workDirFactory,
	}

	// Create a mocked-up RPM-aware build environment. Other method can be used to configure
	// it to simulate build behavior.
	params.buildEnv = buildenv_testutils.NewMockRPMAwareBuildEnv(ctrl)

	// Create a builder that we'll test.
	params.builder = componentbuilder.New(
		testEnv.Env,
		testEnv.Env.FS(),
		testEnv.Env,
		params.sourcePreparer,
		params.buildEnv,
		params.workDirFactory,
	)

	return params
}

func (p *componentBuilderTestParams) createTestComponent(t *testing.T) *components_testutils.MockComponent {
	t.Helper()

	const (
		testSpecPath      = "/test/spec/path"
		testComponentName = "test-component"
	)

	// Setup a test spec.
	testSpec := specs_testutils.NewMockComponentSpec(p.ctrl)
	testSpec.EXPECT().GetPath().AnyTimes().Return(testSpecPath, nil)
	testSpec.EXPECT().Parse().AnyTimes().Return(&specs.ComponentSpecDetails{}, nil)

	// Create the spec in the test filesystem.
	require.NoError(t, fileutils.WriteFile(p.testEnv.FS(), testSpecPath, []byte(""), fileperms.PublicFile))

	// Set up a test component backed by the test spec.
	testComponent := components_testutils.NewMockComponent(p.ctrl)
	testComponent.EXPECT().GetName().AnyTimes().Return(testComponentName)
	testComponent.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{})
	testComponent.EXPECT().GetSpec().AnyTimes().Return(testSpec)

	return testComponent
}

func (p *componentBuilderTestParams) setupToBuildSRPM(t *testing.T, component components.Component) {
	t.Helper()

	p.buildEnv.EXPECT().BuildSRPM(
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
	).AnyTimes().DoAndReturn(
		func(
			ctx context.Context, specPath, sourceDirPath, outputDirPath string,
			options buildenv.SRPMBuildOptions,
		) error {
			require.NoError(t, fileutils.WriteFile(p.testEnv.Env.FS(),
				path.Join(outputDirPath, component.GetName()+".src.rpm"),
				[]byte("test-srpm-content"),
				fileperms.PublicFile,
			))

			return nil
		})
}

func (p *componentBuilderTestParams) setupToBuildRPM(t *testing.T, component components.Component) {
	t.Helper()

	p.buildEnv.EXPECT().BuildRPM(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		DoAndReturn(func(ctx context.Context, srpmPath, outputDirPath string, options buildenv.RPMBuildOptions) error {
			require.NoError(t, fileutils.WriteFile(p.testEnv.Env.FS(),
				path.Join(outputDirPath, component.GetName()+".rpm"),
				[]byte("test-srpm-content"),
				fileperms.PublicFile,
			))

			return nil
		})
}

func TestBuilderNew(t *testing.T) {
	params := setupBuilder(t)

	require.NotNil(t, params)
}

func TestBuildSourcePackage(t *testing.T) {
	const testOutputDir = "/output"

	params := setupBuilder(t)
	testComponent := params.createTestComponent(t)

	params.setupToBuildSRPM(t, testComponent)

	path, err := params.builder.BuildSourcePackage(params.testEnv.Env, testComponent, []string{}, testOutputDir)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(testOutputDir, testComponent.GetName()+".src.rpm"), path)
	require.True(t, strings.HasPrefix(path, testOutputDir))
}

func TestBuildBinaryPackage(t *testing.T) {
	const (
		testOutputDir = "/output"
		testSRPMPath  = "/input/test-component.src.rpm"
	)

	params := setupBuilder(t)
	testComponent := params.createTestComponent(t)

	params.setupToBuildRPM(t, testComponent)

	paths, err := params.builder.BuildBinaryPackage(
		params.testEnv.Env,
		testComponent,
		testSRPMPath,
		[]string{},
		testOutputDir,
		false, /*noCheck?*/
	)

	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(testOutputDir, testComponent.GetName()+".rpm")}, paths)
}
