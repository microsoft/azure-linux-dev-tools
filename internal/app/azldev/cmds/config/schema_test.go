// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config_test

import (
	"encoding/json"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAzlDevTOMLJSONSchema(t *testing.T) {
	// Generate the schema.
	schemaText, err := config.GenerateAzlDevTOMLJSONSchema()
	require.NoError(t, err)
	require.NotEmpty(t, schemaText)

	deserialized := make(map[string]any)

	// Make sure the schema is valid JSON.
	err = json.Unmarshal([]byte(schemaText), &deserialized)
	require.NoError(t, err)
}

// TestSchema_SKUGroupIsImageLevelOnly guards that the generated schema keeps
// 'sku-group' an image-level concept: image test refs may set it (they $ref the
// full TestRef), while component test refs forbid it, matching the loader's
// runtime rejection. See ComponentTestsConfig.JSONSchemaExtend.
func TestSchema_SKUGroupIsImageLevelOnly(t *testing.T) {
	schemaText, err := config.GenerateAzlDevTOMLJSONSchema()
	require.NoError(t, err)

	var schema map[string]any
	require.NoError(t, json.Unmarshal([]byte(schemaText), &schema))

	defs := mustSchemaMap(t, schema["$defs"], "$defs")

	// Component test refs forbid sku-group.
	componentItems := testRefItems(t, defs, "ComponentTestsConfig")
	componentProps := mustSchemaMap(t, componentItems["properties"], "ComponentTestsConfig tests properties")
	require.NotContains(t, componentProps, "sku-group", "component tests must not expose sku-group")

	// Closed object (like TestRef): additionalProperties:false rejects sku-group
	// and any misspelled/unsupported field.
	assert.Equal(t, false, componentItems["additionalProperties"], "component tests items must be a closed object")

	// Image test refs allow sku-group via the shared TestRef definition.
	imageItems := testRefItems(t, defs, "ImageTestsConfig")
	assert.Equal(t, "#/$defs/TestRef", imageItems["$ref"], "image tests should reference the full TestRef")

	testRef := mustSchemaMap(t, defs["TestRef"], "TestRef")
	testRefProps := mustSchemaMap(t, testRef["properties"], "TestRef properties")
	assert.Contains(t, testRefProps, "sku-group", "TestRef (image refs) must expose sku-group")
}

// TestSchema_SKUGroupVMSizesConstraints guards that the generated schema matches
// validateSKUGroups: vm-sizes items must be non-empty/non-whitespace and unique.
func TestSchema_SKUGroupVMSizesConstraints(t *testing.T) {
	schemaText, err := config.GenerateAzlDevTOMLJSONSchema()
	require.NoError(t, err)

	var schema map[string]any
	require.NoError(t, json.Unmarshal([]byte(schemaText), &schema))

	defs := mustSchemaMap(t, schema["$defs"], "$defs")
	skuGroup := mustSchemaMap(t, defs["SKUGroup"], "SKUGroup")
	props := mustSchemaMap(t, skuGroup["properties"], "SKUGroup properties")
	vmSizes := mustSchemaMap(t, props["vm-sizes"], "SKUGroup.vm-sizes")

	assert.Equal(t, true, vmSizes["uniqueItems"], "vm-sizes must reject duplicates")

	items := mustSchemaMap(t, vmSizes["items"], "SKUGroup.vm-sizes items")
	assert.EqualValues(t, 1, items["minLength"], "vm-sizes items must be non-empty")
	assert.Equal(t, "^\\S(.*\\S)?$", items["pattern"], "vm-sizes items must forbid surrounding whitespace")
}

// TestSchema_TestGroupMembersAreClosed guards that [test-groups] members are a
// closed object: only 'name' is allowed, so 'group', 'sku-group', and misspelled
// fields are rejected, matching validateTestGroupMembers.
func TestSchema_TestGroupMembersAreClosed(t *testing.T) {
	schemaText, err := config.GenerateAzlDevTOMLJSONSchema()
	require.NoError(t, err)

	var schema map[string]any
	require.NoError(t, json.Unmarshal([]byte(schemaText), &schema))

	defs := mustSchemaMap(t, schema["$defs"], "$defs")
	items := testRefItems(t, defs, "TestGroup")

	assert.Equal(t, false, items["additionalProperties"], "test-group members must be a closed object")

	props := mustSchemaMap(t, items["properties"], "TestGroup tests item properties")
	assert.Contains(t, props, "name", "test-group members must allow name")
	assert.NotContains(t, props, "sku-group", "test-group members must not expose sku-group")
	assert.NotContains(t, props, "group", "test-group members must not expose group")
}

// mustSchemaMap asserts that value is a JSON object and returns it.
func mustSchemaMap(t *testing.T, value any, name string) map[string]any {
	t.Helper()

	result, isMap := value.(map[string]any)
	require.True(t, isMap, "%s must be a JSON object", name)

	return result
}

// testRefItems returns the resolved 'tests' items schema for a config def.
func testRefItems(t *testing.T, defs map[string]any, defName string) map[string]any {
	t.Helper()

	def := mustSchemaMap(t, defs[defName], defName)
	props := mustSchemaMap(t, def["properties"], defName+" properties")
	tests := mustSchemaMap(t, props["tests"], defName+".tests")

	return mustSchemaMap(t, tests["items"], defName+".tests.items")
}
