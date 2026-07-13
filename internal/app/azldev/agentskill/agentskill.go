// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package agentskill

import (
	"bytes"
	"embed"
	"fmt"
	"path"
	"slices"
	"text/template"
)

const (
	// SkillName is the stable base identifier of the primary azldev skill. It is the
	// name passed to 'azldev docs agent show'.
	SkillName = "azldev"

	// ShowSkillToolName is the name of the read-only MCP tool that returns a skill
	// document. The emitted wrapper files point agents at this tool.
	ShowSkillToolName = "docs-agent-show"

	// ConfigGlob is the 'applyTo' glob for the azldev project-config instructions file;
	// it matches azldev project configuration files.
	ConfigGlob = "**/azldev.toml"

	// instructionFileSuffix is appended to an instruction's name to form its emitted file
	// name (e.g. "comp-toml" -> "comp-toml.instructions.md").
	instructionFileSuffix = ".instructions.md"
)

// The embedded templates rendered into the emitted files and the served skill
// documents.
//
//go:embed content/*.tmpl
var content embed.FS

// templates holds all parsed templates, keyed by their base file name.
//
//nolint:gochecknoglobals // parsed templates are effectively constant and safe for concurrent use.
var templates = template.Must(template.ParseFS(content, "content/*.tmpl"))

// Skill describes a single emitted Agent Skill.
type Skill struct {
	// Name is the stable base identifier (lowercase, hyphen-delimited). It is the
	// argument to 'azldev docs agent show' and the on-disk skill directory name.
	Name string

	// Description is the discovery text placed in the skill's front matter.
	Description string

	// bodyTemplate is the embedded template name rendered as the full skill body.
	bodyTemplate string
}

// skills is the registry of emitted skills, in emission order.
//
//nolint:gochecknoglobals // effectively a constant registry of the built-in skills.
var skills = []Skill{
	{
		Name: "azldev",
		Description: "Read this before running azldev or editing azldev config, and whenever working " +
			"in a repo that contains an azldev.toml file; do not guess azldev's commands or config. " +
			"Explains how to use the azldev CLI to build a distro from TOML config, including the core " +
			"concepts (components, overlays, distros, rendered specs, locks), running azldev (repo root or " +
			"-C, plus the -q and -O json flags), the common commands, and where to go for each workflow. " +
			"Triggers include azldev, comp build, comp render, comp update, build a component, add a " +
			"component, distro config.",
		bodyTemplate: "azldev.md.tmpl",
	},
	{
		Name: "azldev-update-component",
		Description: "Read this before finalizing a component change, changing source resolution, or " +
			"touching a lock file; lock edits are easy to get wrong. Explains how to refresh azldev " +
			"component lock files with 'azldev comp update', covering when to run update versus render, the " +
			"update/render/commit/re-render/amend workflow, and per-component versus -a refresh. Triggers " +
			"include comp update, refresh lock, bump pin, change snapshot, upstream distro, lock drift, " +
			"version bump, finalize component.",
		bodyTemplate: "update-component.md.tmpl",
	},
	{
		Name: "azldev-remove-component",
		Description: "Read this before deleting or dropping a component; there is no azldev remove " +
			"command, so doing it wrong leaves dangling state. Explains the manual removal workflow for " +
			"deleting component metadata, cleaning references, and validating any related output-affecting " +
			"changes. Triggers include remove component, delete package, drop " +
			"component, prune dependency.",
		bodyTemplate: "remove-component.md.tmpl",
	},
	{
		Name: "azldev-overlays",
		Description: "Read this before adding, changing, or diagnosing any overlay; never edit a spec or " +
			"rendered file from memory. Explains how to modify a component's RPM spec or loose source files " +
			"with azldev overlays (semantic patches applied at render time) instead of forking the spec, " +
			"covering overlay types, the render-and-inspect loop, common failures, pitfalls, and metadata. " +
			"Triggers include overlay, overlay failed, no match, spec-add-tag, spec-remove-tag, patch-add, " +
			"fix spec, backport, disable test, prune subpackage, edit spec.",
		bodyTemplate: "overlays.md.tmpl",
	},
	{
		Name: "azldev-comp-toml",
		Description: "Read this before authoring, editing, or reviewing a *.comp.toml file; do not work " +
			"from memory. Explains the azldev component definition format and review workflow, covering " +
			"component structure, spec sources, build config, release calculation, render options, file " +
			"organization, overlay hygiene, stale files, disabled tests, and testing verification. " +
			"Triggers include comp.toml, component config, review component, component hygiene, spec " +
			"source, upstream-distro, build defines, release calculation, includes.",
		bodyTemplate: "comp-toml.md.tmpl",
	},
	{
		Name: "azldev-add-component",
		Description: "Read this before adding or importing a component; follow the workflow instead of " +
			"guessing. Explains how to add a new component to an azldev distro, covering inspecting the " +
			"upstream spec, the inline-versus-dedicated-file decision, and validating with render, " +
			"diff-sources, and build. Triggers include add component, new package, import package, create " +
			"comp.toml, new component.",
		bodyTemplate: "add-component.md.tmpl",
	},
}

// Skills returns the registered skills in emission order.
func Skills() []Skill {
	return slices.Clone(skills)
}

// FindSkill returns the registered skill with the given name.
func FindSkill(name string) (Skill, error) {
	for _, skill := range skills {
		if skill.Name == name {
			return skill, nil
		}
	}

	return Skill{}, fmt.Errorf("unknown skill %#q", name)
}

// SkillPointer names a skill an instruction file points at, together with a short
// purpose describing when to read it ("read the `azldev-overlays` skill to add or change
// overlays").
type SkillPointer struct {
	// Skill is the name of the skill to read.
	Skill string

	// Purpose is a short phrase describing when to read the skill (e.g. "to add or
	// change overlays"). It follows the skill name in the rendered wrapper.
	Purpose string
}

// Instruction describes a single emitted path-specific instruction file. Instruction
// files are lightweight wrappers, selected automatically by their 'applyTo' glob, that
// point agents at the relevant skill(s); the substantive, always-current guidance lives
// in the skills, keeping a single source of truth. An instruction only names the skills
// to read — how a skill's content is delivered (a thin wrapper served by docs-agent-show,
// or the full body inlined by '--full') is the skill's concern, not the instruction's.
type Instruction struct {
	// Name is the file-name stem; the emitted file is "<Name>.instructions.md".
	Name string

	// ApplyTo is the front-matter glob selecting the files this instruction applies to.
	// It may reference binding fields (e.g. '{{ .RenderedSpecsDir }}') and is rendered
	// against [Params] at emission time.
	ApplyTo string

	// Description is the front-matter description.
	Description string

	// Title is the body heading.
	Title string

	// Intro is the body's opening sentence describing the file kind.
	Intro string

	// Skills lists the skills this wrapper points agents at, in order, each with a purpose.
	// The first skill is required for every matching file; remaining skills are loaded only when
	// their purpose matches the task.
	Skills []SkillPointer
}

// instructions is the registry of emitted instruction files, in emission order.
//
//nolint:gochecknoglobals // effectively a constant registry of the built-in instruction files.
var instructions = []Instruction{
	{
		Name:    SkillName,
		ApplyTo: ConfigGlob,
		Description: "This repo is an azldev distro project (azldev.toml present). Before running azldev " +
			"or editing its config, load the azldev skill; do not guess azldev's commands or config. " +
			"Triggers include azldev, comp build, comp render, comp update, build a component, add a " +
			"component, distro config.",
		Title: "Working with azldev projects",
		Intro: "This repository is an azldev distro project; its top-level configuration lives in `azldev.toml`.",
		Skills: []SkillPointer{
			{Skill: SkillName, Purpose: "for how to use the azldev CLI"},
		},
	},
	{
		Name:    "comp-toml",
		ApplyTo: "**/*.comp.toml,**/components.toml",
		Description: "These are azldev component definition files (*.comp.toml). Before editing " +
			"or reviewing one, load " +
			"the azldev-comp-toml skill (and azldev-overlays for spec changes); do not hand-write " +
			"component config from memory. Triggers include comp.toml, component config, spec source, " +
			"build defines, release calculation, overlays, review component.",
		Title: "Component definition files (`*.comp.toml`)",
		Intro: "These files define a distro's components — each one's spec source and how azldev customizes it.",
		Skills: []SkillPointer{
			{Skill: "azldev-comp-toml", Purpose: "for the component TOML format and review checklist"},
			{Skill: "azldev-add-component", Purpose: "to add a new component"},
			{Skill: "azldev-overlays", Purpose: "to add or change overlays"},
			{Skill: "azldev-update-component", Purpose: "to refresh a component's lock"},
			{Skill: "azldev-remove-component", Purpose: "to remove a component"},
		},
	},
	{
		Name:    "rendered-specs",
		ApplyTo: "{{ .RenderedSpecsDir }}/**/*",
		Description: "Rendered component files produced by 'azldev comp render'. They are build inputs and " +
			"must not be hand-edited. Before changing one, load the azldev-overlays or azldev-comp-toml " +
			"skill, edit the source, and re-render. Read this when viewing or tempted to edit generated " +
			"output.",
		Title: "Rendered component files",
		Intro: "These files are generated by `azldev comp render` and are build inputs; do not edit them " +
			"directly — change the component's `.comp.toml`, overlays, or source files and re-render.",
		Skills: []SkillPointer{
			{Skill: "azldev-comp-toml", Purpose: "for the component TOML format"},
			{Skill: "azldev-overlays", Purpose: "to change generated output via overlays"},
			{Skill: "azldev-update-component", Purpose: "to refresh and finalize a component"},
		},
	},
}

// Instructions returns the registered instruction files in emission order.
func Instructions() []Instruction {
	result := slices.Clone(instructions)
	for i := range result {
		result[i].Skills = slices.Clone(result[i].Skills)
	}

	return result
}

// Layout controls where emitted skill files are written in a target repository.
type Layout struct {
	// SkillsDir is the repo-relative parent directory that holds skill directories.
	SkillsDir string
}

// DefaultLayout returns the default emission layout: skills under the tool-neutral
// '.agents/skills' location from the Agent Skills open standard, and instructions
// under '.github/instructions'.
func DefaultLayout() Layout {
	return Layout{
		SkillsDir: ".agents/skills",
	}
}

// SkillDir returns the repo-relative directory for a skill under this layout.
func (l Layout) SkillDir(skill Skill) string {
	return path.Join(l.SkillsDir, skill.Name)
}

// SkillFile returns the repo-relative SKILL.md path for a skill under this layout.
func (l Layout) SkillFile(skill Skill) string {
	return path.Join(l.SkillDir(skill), "SKILL.md")
}

// InstructionFile returns the repo-relative file path for an instruction.
func InstructionFile(inst Instruction) string {
	return path.Join(".github/instructions", inst.Name+instructionFileSuffix)
}

// Command is a top-level azldev command with its one-line summary. The list is generated
// from the Cobra command tree so the overview skill's command list never goes stale.
type Command struct {
	Name  string
	Short string
}

// Bindings are the target-repo values resolved from the repo's azldev.toml and
// injected into skill content. The caller (the 'docs agent' command) is responsible
// for populating every field: from a loaded configuration when one is available, or
// from azldev's built-in defaults when it is not, so the emitted documentation stays
// accurate for a default project even with no configuration present.
type Bindings struct {
	// LockDir is the repo-relative directory holding per-component lock files.
	LockDir string

	// RenderedSpecsDir is the repo-relative directory holding rendered component specs.
	RenderedSpecsDir string

	// WorkDir is the repo-relative temporary working directory. Skills use it for
	// throwaway scratch output so agents stay within the project's configured layout
	// instead of writing to /tmp.
	WorkDir string
}

// Params carries the dynamic values injected into the emitted and served content.
type Params struct {
	// Version is the azldev version stamped into the generated content.
	Version string

	// TopLevelCommands is the sorted list of top-level azldev commands with summaries.
	TopLevelCommands []Command

	// Bindings are the target-repo values resolved from the repo's configuration
	// (or azldev defaults when none is available). Embedded so templates can reference
	// its fields directly (e.g. '{{ .LockDir }}').
	Bindings
}

// EmittedFile is a single file to be written into a target repository.
type EmittedFile struct {
	// RelPath is the repository-relative destination path, always forward-slash separated.
	RelPath string `json:"relPath"`

	// Content is the fully rendered file content.
	Content string `json:"-"`
}

func renderSkill(templateName string, skill Skill, params Params) (string, error) {
	var buf bytes.Buffer

	data := struct {
		Params
		Skill
		ShowSkillToolName string
	}{
		Params:            params,
		Skill:             skill,
		ShowSkillToolName: ShowSkillToolName,
	}

	err := templates.ExecuteTemplate(&buf, templateName, data)
	if err != nil {
		return "", fmt.Errorf("failed to render agent skill template %#q:\n%w", templateName, err)
	}

	return buf.String(), nil
}

func renderInstruction(inst Instruction, params Params) (string, error) {
	if err := validateInstruction(inst); err != nil {
		return "", err
	}

	applyTo, err := renderInline("applyTo", inst.ApplyTo, params)
	if err != nil {
		return "", fmt.Errorf("failed to render applyTo for instruction %#q:\n%w", inst.Name, err)
	}

	var buf bytes.Buffer

	inst.ApplyTo = applyTo
	data := struct {
		Params
		Instruction
	}{
		Params:      params,
		Instruction: inst,
	}

	err = templates.ExecuteTemplate(&buf, "instruction-wrapper.md.tmpl", data)
	if err != nil {
		return "", fmt.Errorf("failed to render instruction template for %#q:\n%w", inst.Name, err)
	}

	return buf.String(), nil
}

func validateInstruction(inst Instruction) error {
	if len(inst.Skills) == 0 {
		return fmt.Errorf("instruction %#q must reference at least one skill", inst.Name)
	}

	for _, pointer := range inst.Skills {
		if _, err := FindSkill(pointer.Skill); err != nil {
			return fmt.Errorf("instruction %#q references unknown skill %#q:\n%w",
				inst.Name, pointer.Skill, err)
		}
	}

	return nil
}

// renderInline renders a short, trusted template string (such as an instruction's applyTo
// glob, which may reference binding fields like '{{ .RenderedSpecsDir }}') against params.
func renderInline(name, text string, params Params) (string, error) {
	tmpl, err := template.New(name).Parse(text)
	if err != nil {
		return "", fmt.Errorf("failed to parse %s template %#q:\n%w", name, text, err)
	}

	var buf bytes.Buffer

	err = tmpl.Execute(&buf, params)
	if err != nil {
		return "", fmt.Errorf("failed to execute %s template %#q:\n%w", name, text, err)
	}

	return buf.String(), nil
}

// SkillDocument renders the full document for the named skill. It is served
// verbatim by the read-only MCP tool and by 'azldev docs agent show'. The default
// layout is used since a served document has no on-disk directory.
func SkillDocument(name string, params Params) (string, error) {
	skill, err := FindSkill(name)
	if err != nil {
		return "", err
	}

	return renderSkill(skill.bodyTemplate, skill, params)
}

// Files renders the set of agent files to write into a target repository using the
// given layout. When full is true, each on-disk SKILL.md contains the complete
// skill document instead of a light MCP wrapper (useful when the azldev MCP server
// is not available in the target environment). Instruction files are always light
// wrappers that point at the relevant skills.
func Files(layout Layout, params Params, full bool) ([]EmittedFile, error) {
	files := make([]EmittedFile, 0, len(skills)+len(instructions))

	for _, skill := range skills {
		templateName := "skill-wrapper.md.tmpl"
		if full {
			templateName = skill.bodyTemplate
		}

		rendered, err := renderSkill(templateName, skill, params)
		if err != nil {
			return nil, err
		}

		files = append(files, EmittedFile{RelPath: layout.SkillFile(skill), Content: rendered})
	}

	for _, inst := range instructions {
		rendered, err := renderInstruction(inst, params)
		if err != nil {
			return nil, err
		}

		files = append(files, EmittedFile{RelPath: InstructionFile(inst), Content: rendered})
	}

	return files, nil
}
