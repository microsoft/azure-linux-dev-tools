// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package workdir_test

import (
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/workdir"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFactory_EmptyBaseDir(t *testing.T) {
	env := testutils.NewTestEnv(t)

	// Attempt to create a work dir factory; expect it to fail.
	_, err := workdir.NewFactory(env.FS(), "", time.Now())
	require.Error(t, err)
}

func Test_NewFactory_Success(t *testing.T) {
	const testBaseDir = "/workdir"

	env := testutils.NewTestEnv(t)

	// Create a work dir factory with a valid base directory.
	factory, err := workdir.NewFactory(env.FS(), testBaseDir, time.Now())
	require.NoError(t, err)
	require.NotNil(t, factory)
}

func Test_Create(t *testing.T) {
	const (
		testComponentName = "component"
		testLabelName     = "label"
	)

	env := testutils.NewTestEnv(t)
	component := projectconfig.ComponentConfig{Name: testComponentName}

	factory, err := workdir.NewFactory(env.FS(), env.Env.WorkDir(), env.Env.ConstructionTime())
	require.NoError(t, err)

	dirPath, err := factory.Create(component.Name, testLabelName)
	require.NoError(t, err)
	require.NotEmpty(t, dirPath)

	// Confirm that the component name and label ended up in the path somewhere
	// for human readability.
	assert.Contains(t, dirPath, testComponentName)
	assert.Contains(t, dirPath, testLabelName)

	// Confirm that the directory was created in the test FS.
	stat, statErr := env.TestFS.Stat(dirPath)
	require.NoError(t, statErr)
	assert.True(t, stat.IsDir())
}
