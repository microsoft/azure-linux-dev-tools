// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package agentskill_test

import (
	"path"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/agentskill"
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
}

func TestSkillsRegistry(t *testing.T) {
	names := make([]string, 0, len(agentskill.Skills()))
	for _, skill := range agentskill.Skills() {
		names = append(names, skill.Name)
	}

	assert.Contains(t, names, agentskill.SkillName)
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

	// The azldev instruction wrapper applies to azldev.toml and points at the azldev skill by
	// name (never the CLI/MCP tool, which may be unavailable in --full installs).
	azldevInstruction := instructionByName(t, "azldev")
	instructions := fileByPath(t, files, agentskill.InstructionFile(azldevInstruction)).Content
	assert.Contains(t, instructions, `description: "`)
	assert.Contains(t, instructions, `applyTo: "`+agentskill.ConfigGlob+`"`)
	assert.Contains(t, instructions, "`"+agentskill.SkillName+"`")
	assert.NotContains(t, instructions, agentskill.ShowSkillToolName)
	assert.NotContains(t, instructions, "docs agent show")
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
}
