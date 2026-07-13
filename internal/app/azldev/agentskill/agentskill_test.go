// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package agentskill_test

import (
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/agentskill"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func testParams() agentskill.Params {
	return agentskill.Params{
		Version: "1.2.3-test",
		TopLevelCommands: []agentskill.Command{
			{Name: "component", Short: "Manage components"},
			{Name: "config", Short: "Manage configuration"},
			{Name: "docs", Short: "Generate documentation"},
		},
		Bindings: agentskill.Bindings{
			LockDir:          "locks",
			RenderedSpecsDir: "specs",
			WorkDir:          "build/work",
		},
	}
}

// parseFrontmatter extracts the leading YAML front matter of a Markdown document into a map of
// top-level "key: value" pairs. It is intentionally minimal (no nested structures) since the
// emitted files only use flat scalar fields.
func parseFrontmatter(t *testing.T, doc string) map[string]string {
	t.Helper()

	require.True(t, strings.HasPrefix(doc, "---\n"), "document must start with YAML front matter")

	rest := strings.TrimPrefix(doc, "---\n")
	frontmatter, _, found := strings.Cut(rest, "\n---")
	require.True(t, found, "front matter must be terminated by a '---' line")

	fields := map[string]string{}
	require.NoError(t, yaml.Unmarshal([]byte(frontmatter), &fields))

	return fields
}

// primarySkill returns the built-in azldev skill.
func primarySkill(t *testing.T) agentskill.Skill {
	t.Helper()

	skill, err := agentskill.FindSkill(agentskill.SkillName)
	require.NoError(t, err)

	return skill
}

func TestSkillDocument(t *testing.T) {
	doc, err := agentskill.SkillDocument(agentskill.SkillName, testParams())
	require.NoError(t, err)

	fields := parseFrontmatter(t, doc)

	assert.Equal(t, agentskill.SkillName, fields["name"])
	assert.NotEmpty(t, fields["description"])

	// The full document substitutes the dynamic version stamp and the generated command list.
	assert.Contains(t, doc, "1.2.3-test")
	assert.Contains(t, doc, "- `azldev component`")
	assert.Contains(t, doc, "- `azldev docs`")
}

func TestSkillDocumentUnknown(t *testing.T) {
	_, err := agentskill.SkillDocument("not-a-real-skill", testParams())
	require.Error(t, err)
}

func TestSkillDocumentUsesBindings(t *testing.T) {
	params := testParams()
	params.Bindings = agentskill.Bindings{
		LockDir:          "build/locks",
		RenderedSpecsDir: "build/specs",
	}

	doc, err := agentskill.SkillDocument("azldev-remove-component", params)
	require.NoError(t, err)

	// The resolved binding values, not azldev's defaults, appear in the rendered body.
	assert.Contains(t, doc, "build/locks/<name>.lock")
	assert.Contains(t, doc, "build/specs/")
}

func TestUpdateComponentSkillStagesRenderedOutputBeforeAmend(t *testing.T) {
	doc, err := agentskill.SkillDocument("azldev-update-component", testParams())
	require.NoError(t, err)

	assert.Equal(t, 2, strings.Count(doc, "git add specs/<first-char>/<name>/"),
		"both amend workflows must stage the post-commit render")
}

func TestSkillFrontmatterInvariants(t *testing.T) {
	layout := agentskill.DefaultLayout()

	// Every registered skill (current and future) must satisfy the Agent Skills spec.
	for _, skill := range agentskill.Skills() {
		t.Run(skill.Name, func(t *testing.T) {
			doc, err := agentskill.SkillDocument(skill.Name, testParams())
			require.NoError(t, err)
			assert.Contains(t, doc, `description: "`, "skill description must be quoted YAML")

			fields := parseFrontmatter(t, doc)

			assert.Equal(t, path.Base(layout.SkillDir(skill)), fields["name"],
				"skill name must match its parent directory name")
			assert.LessOrEqual(t, len(fields["name"]), 64, "skill name must be at most 64 characters")
			assert.Regexp(t, `^[a-z0-9-]+$`, fields["name"], "skill name must be lowercase, digits, and hyphens")
			assert.NotEmpty(t, fields["description"], "skill description must not be empty")
			assert.LessOrEqual(t, len(fields["description"]), 1024, "skill description must be at most 1024 characters")
		})
	}
}

// fileByPath returns the emitted file with the given repo-relative path.
func fileByPath(t *testing.T, files []agentskill.EmittedFile, relPath string) agentskill.EmittedFile {
	t.Helper()

	for _, file := range files {
		if file.RelPath == relPath {
			return file
		}
	}

	require.Failf(t, "missing emitted file", "no emitted file with path %q", relPath)

	return agentskill.EmittedFile{}
}

func mockSkill(t *testing.T) agentskill.Skill {
	t.Helper()

	skill, err := agentskill.FindSkill("azldev-mock")
	require.NoError(t, err)

	return skill
}

// instructionByName returns the registered instruction with the given name.
func instructionByName(t *testing.T, name string) agentskill.Instruction {
	t.Helper()

	for _, inst := range agentskill.Instructions() {
		if inst.Name == name {
			return inst
		}
	}

	require.Failf(t, "missing instruction", "no instruction named %q", name)

	return agentskill.Instruction{}
}

func TestInstructionsRegistry(t *testing.T) {
	names := make([]string, 0, len(agentskill.Instructions()))
	for _, inst := range agentskill.Instructions() {
		names = append(names, inst.Name)
	}

	assert.Contains(t, names, "azldev")
	assert.Contains(t, names, "comp-toml")
	assert.Contains(t, names, "rendered-specs")

	compToml := instructionByName(t, "comp-toml")
	assert.Equal(t, "**/*.comp.toml,**/components.toml", compToml.ApplyTo)

	skillNames := make([]string, 0, len(compToml.Skills))
	for _, pointer := range compToml.Skills {
		skillNames = append(skillNames, pointer.Skill)
	}

	assert.Contains(t, skillNames, "azldev-comp-toml")
	assert.Contains(t, skillNames, "azldev-overlays")
}

func TestSkillsRegistry(t *testing.T) {
	names := make([]string, 0, len(agentskill.Skills()))
	for _, skill := range agentskill.Skills() {
		names = append(names, skill.Name)
	}

	assert.Contains(t, names, agentskill.SkillName)
	assert.Contains(t, names, "azldev-mock")
	assert.Contains(t, names, "azldev-update-component")
	assert.Contains(t, names, "azldev-remove-component")
	assert.Contains(t, names, "azldev-overlays")
	assert.Contains(t, names, "azldev-comp-toml")
	assert.Contains(t, names, "azldev-add-component")
	assert.Contains(t, names, "azldev-build-component")
	assert.Contains(t, names, "azldev-image")
}

func TestRegistryAccessorsReturnCopies(t *testing.T) {
	const mutated = "mutated"

	skills := agentskill.Skills()
	require.NotEmpty(t, skills)
	originalSkillName := skills[0].Name
	skills[0].Name = mutated
	actualSkillName := agentskill.Skills()[0].Name
	skills[0].Name = originalSkillName
	assert.Equal(t, originalSkillName, actualSkillName)

	instructions := agentskill.Instructions()
	require.NotEmpty(t, instructions)
	require.NotEmpty(t, instructions[0].Skills)
	originalInstructionName := instructions[0].Name
	originalPointerSkill := instructions[0].Skills[0].Skill
	instructions[0].Name = mutated
	instructions[0].Skills[0].Skill = mutated
	actualInstructions := agentskill.Instructions()
	actualInstructionName := actualInstructions[0].Name
	actualPointerSkill := actualInstructions[0].Skills[0].Skill
	instructions[0].Name = originalInstructionName
	instructions[0].Skills[0].Skill = originalPointerSkill

	assert.Equal(t, originalInstructionName, actualInstructionName)
	assert.Equal(t, originalPointerSkill, actualPointerSkill)
}

func TestFilesWrapper(t *testing.T) {
	layout := agentskill.DefaultLayout()

	files, err := agentskill.Files(layout, testParams(), false)
	require.NoError(t, err)
	// One wrapper per skill, plus one instruction file per registered instruction.
	require.Len(t, files, len(agentskill.Skills())+len(agentskill.Instructions()))

	for _, file := range files {
		assert.Contains(t, file.Content, "Generated by `azldev docs agent`; do not hand-edit.", file.RelPath)
		assert.False(t, strings.HasSuffix(file.Content, "\n\n"), "%s has a trailing blank line", file.RelPath)
	}

	skill := fileByPath(t, files, layout.SkillFile(primarySkill(t))).Content
	assert.Contains(t, skill, "name: "+agentskill.SkillName)
	assert.Contains(t, skill, `description: "`)
	// The wrapper points at the read-only MCP tool and omits the full skill body.
	assert.Contains(t, skill, agentskill.ShowSkillToolName)
	assert.NotContains(t, skill, "Golden rules")

	// The mock wrapper also points at the tool but is not the full body.
	mockWrapper := fileByPath(t, files, layout.SkillFile(mockSkill(t))).Content
	assert.Contains(t, mockWrapper, agentskill.ShowSkillToolName)
	assert.NotContains(t, mockWrapper, "Never install built RPMs")

	// The azldev instruction wrapper applies to azldev.toml and points at the azldev skill by
	// name (never the CLI/MCP tool, which may be unavailable in --full installs).
	azldevInstruction := instructionByName(t, "azldev")
	instructions := fileByPath(t, files, agentskill.InstructionFile(azldevInstruction)).Content
	assert.Contains(t, instructions, `description: "`)
	assert.Contains(t, instructions, `applyTo: "`+agentskill.ConfigGlob+`"`)
	assert.Contains(t, instructions, "`"+agentskill.SkillName+"`")
	assert.NotContains(t, instructions, agentskill.ShowSkillToolName)
	assert.NotContains(t, instructions, "docs agent show")

	// The comp-toml instruction wrapper applies to *.comp.toml and points at its skills by
	// name and purpose.
	compTomlInstruction := instructionByName(t, "comp-toml")
	compTomlWrapper := fileByPath(t, files, agentskill.InstructionFile(compTomlInstruction)).Content
	assert.Contains(t, compTomlWrapper, `applyTo: "**/*.comp.toml,**/components.toml"`)
	assert.Contains(t, compTomlWrapper, "read the `azldev-comp-toml` skill")
	assert.Contains(t, compTomlWrapper, "Read the `azldev-overlays` skill to add or change overlays")
	// The file-format skill is required; mutually exclusive workflow skills are conditional.
	assert.Contains(t, compTomlWrapper, "You MUST read the `azldev-comp-toml` skill")
	assert.NotContains(t, compTomlWrapper, "You MUST read the `azldev-overlays` skill")
	assert.NotContains(t, compTomlWrapper, "docs agent show")

	// The rendered-specs wrapper carries the do-not-edit guardrail and a binding-resolved glob.
	renderedSpecsInstruction := instructionByName(t, "rendered-specs")
	renderedSpecsWrapper := fileByPath(t, files, agentskill.InstructionFile(renderedSpecsInstruction)).Content
	assert.Contains(t, renderedSpecsWrapper, `applyTo: "specs/**/*"`)
	assert.Contains(t, renderedSpecsWrapper, "do not edit them directly")
}

func TestRenderedSpecsInstructionApplyToTracksBindings(t *testing.T) {
	layout := agentskill.DefaultLayout()
	params := testParams()
	params.RenderedSpecsDir = "SPECS"

	files, err := agentskill.Files(layout, params, false)
	require.NoError(t, err)

	inst := instructionByName(t, "rendered-specs")
	wrapper := fileByPath(t, files, agentskill.InstructionFile(inst)).Content

	// The applyTo glob tracks the configured rendered-specs directory.
	assert.Contains(t, wrapper, `applyTo: "SPECS/**/*"`)
}

func TestFilesFull(t *testing.T) {
	layout := agentskill.DefaultLayout()

	files, err := agentskill.Files(layout, testParams(), true)
	require.NoError(t, err)
	require.Len(t, files, len(agentskill.Skills())+len(agentskill.Instructions()))

	for _, file := range files {
		assert.Contains(t, file.Content, "Generated by `azldev docs agent`; do not hand-edit.", file.RelPath)
	}

	// In full mode each on-disk SKILL.md inlines the complete skill document.
	assert.Contains(t, fileByPath(t, files, layout.SkillFile(primarySkill(t))).Content, "overlay system")
	assert.Contains(t, fileByPath(t, files, layout.SkillFile(mockSkill(t))).Content, "azldev adv mock shell")
}

func TestFilesGitHubLayout(t *testing.T) {
	layout := agentskill.Layout{
		SkillsDir: ".github/skills",
	}

	files, err := agentskill.Files(layout, testParams(), false)
	require.NoError(t, err)
	require.Len(t, files, len(agentskill.Skills())+len(agentskill.Instructions()))

	// The github layout places skills under .github/skills with their plain (namespaced) names.
	azldevSkill := fileByPath(t, files, ".github/skills/azldev/SKILL.md")
	assert.Contains(t, azldevSkill.Content, "name: azldev")

	mockFile := fileByPath(t, files, ".github/skills/azldev-mock/SKILL.md")
	assert.Contains(t, mockFile.Content, "name: azldev-mock")
}

// schemaEnum extracts values from an authoritative jsonschema enum tag.
func schemaEnum(t *testing.T, structType reflect.Type, fieldName string) []string {
	t.Helper()

	field, ok := structType.FieldByName(fieldName)
	require.Truef(t, ok, "%s must have a %s field", structType.Name(), fieldName)

	var values []string

	for _, part := range strings.Split(field.Tag.Get("jsonschema"), ",") {
		if value, found := strings.CutPrefix(part, "enum="); found {
			values = append(values, value)
		}
	}

	require.NotEmptyf(t, values, "expected enum values in %s.%s jsonschema tag", structType.Name(), fieldName)

	return values
}

// TestOverlaysSkillCoversSchemaEnums is a drift guard: the azldev-overlays skill must
// document every overlay type, metadata category, and upstream status defined in code.
func TestOverlaysSkillCoversSchemaEnums(t *testing.T) {
	doc, err := agentskill.SkillDocument("azldev-overlays", agentskill.Params{})
	require.NoError(t, err)

	schemaFields := []struct {
		structType reflect.Type
		fieldName  string
	}{
		{reflect.TypeOf(projectconfig.ComponentOverlay{}), "Type"},
		{reflect.TypeOf(projectconfig.OverlayMetadata{}), "Category"},
		{reflect.TypeOf(projectconfig.OverlayMetadata{}), "UpstreamStatus"},
	}

	for _, schemaField := range schemaFields {
		for _, value := range schemaEnum(t, schemaField.structType, schemaField.fieldName) {
			assert.Containsf(t, doc, "`"+value+"`",
				"azldev-overlays skill must document %s.%s value %q",
				schemaField.structType.Name(), schemaField.fieldName, value)
		}
	}
}
