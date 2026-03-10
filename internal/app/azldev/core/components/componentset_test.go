// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package components_test

import (
	"errors"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/specs"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testComponent struct {
	name     string
	specPath string
}

func (c *testComponent) GetName() string {
	return c.name
}

func (c *testComponent) GetConfig() *projectconfig.ComponentConfig {
	return &projectconfig.ComponentConfig{}
}

func (c *testComponent) GetSpec() specs.ComponentSpec {
	return &testComponentSpec{path: c.specPath}
}

func (c *testComponent) GetDetails() (*components.ComponentDetails, error) {
	return nil, errors.New("not implemented for test")
}

type testComponentSpec struct {
	path string
}

func (s *testComponentSpec) Parse() (*specs.ComponentSpecDetails, error) {
	return nil, errors.New("not implemented")
}

func (s *testComponentSpec) GetPath() (string, error) {
	return s.path, nil
}

func TestNewComponentSet(t *testing.T) {
	set := components.NewComponentSet()

	require.Zero(t, set.Len())
	require.Empty(t, set.Names())
	require.Empty(t, set.Components())
}

func TestComponentSetReadOperations(t *testing.T) {
	set := components.NewComponentSet()

	set.Add(&testComponent{name: "test1"})
	set.Add(&testComponent{name: "test2"})

	assert.Equal(t, 2, set.Len())
	assert.Equal(t, []string{"test1", "test2"}, set.Names())

	comp, found := set.TryGet("test1")
	require.True(t, found)
	assert.Equal(t, "test1", comp.GetName())

	comp, found = set.TryGet("test2")
	require.True(t, found)
	assert.Equal(t, "test2", comp.GetName())

	_, found = set.TryGet("test3")
	require.False(t, found)
}

func TestComponentSetAddExisting(t *testing.T) {
	set := components.NewComponentSet()

	set.Add(&testComponent{name: "test1", specPath: "dir/a.spec"})
	set.Add(&testComponent{name: "test1", specPath: "dir/b.spec"})

	assert.Equal(t, 1, set.Len())
	assert.Equal(t, []string{"test1"}, set.Names())

	comp, found := set.TryGet("test1")
	require.True(t, found)

	foundPath, err := comp.GetSpec().GetPath()
	require.NoError(t, err)

	assert.Equal(t, "dir/b.spec", foundPath)
}
