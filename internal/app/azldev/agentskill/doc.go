// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package agentskill is the single source of truth for the AI-agent skill and
// instruction files that describe how to use the azldev CLI. Everything is embedded
// in the binary, so the files written into a repo, the CLI output, and the MCP tool
// response are always in lock-step with the azldev version that produced them.
//
// # Mental model
//
// At heart this is "a few text files with find/replace": the text lives in
// content/*.md.tmpl, and the "replace" is Go text/template substitution of a small set
// of dynamic values (see Params). Two Go registries name the files and carry the bits
// that can't live in a single shared template:
//
//   - skills     — each Skill is {Name, Description, bodyTemplate}. The body template
//     under content/ holds the real, substantive guidance for one topic.
//   - instructions — each Instruction is {Name, ApplyTo, Description, Title, Intro,
//     Skills}. An instruction is a lightweight, path-scoped pointer: its applyTo glob
//     decides which files it covers, and it only tells the agent which skill(s) to read.
//
// Put substantive content in a skill; keep instructions thin.
//
// # Two renderings of every skill
//
// A skill can be emitted two ways from the same registry entry:
//
//   - full body — the skill's own bodyTemplate (e.g. azldev.md.tmpl), the complete
//     content. Used by 'docs agent show', the docs-agent-show MCP tool, and
//     'docs agent install --full'.
//   - redirect wrapper — one generic skill-wrapper.md.tmpl for all skills, a thin
//     SKILL.md that points the agent at the docs-agent-show MCP tool for always-fresh
//     content. This is the default on-disk file 'docs agent install' writes.
//
// Because the redirect wrapper is generic, it cannot hard-code each skill's name and
// description in its front matter — they are handed to it as data. That is the only
// reason Skill.Description exists as a field rather than living in the template front
// matter: holding it once in the registry lets both the full body and
// the shared wrapper render the same name/description without duplicating the text.
// Instruction files work the same way: one shared instruction-wrapper.md.tmpl renders
// every instruction, so each instruction's Description/ApplyTo/etc. are data too.
//
// # Where a Description ends up
//
// renderSkill copies Skill.Description into the template data; the template (full body
// or shared wrapper) emits it as the YAML front-matter "description:" line, which agent
// runtimes read to decide whether to load the skill. renderInstruction does the same via
// the instruction wrapper. That front-matter line is the description's only destination.
//
// # Substituted values (Params)
//
// The {{ .Field }} placeholders in the templates are filled from Params, resolved by the
// 'docs agent' command:
//
//   - Version           — the azldev version stamped into every file.
//   - TopLevelCommands  — generated from the Cobra command tree, so the overview skill's
//     command list never goes stale.
//   - Bindings          — repo-specific paths (LockDir, RenderedSpecsDir, WorkDir) read from
//     the target azldev.toml, degrading to azldev's defaults when no config is present.
//
// # Outputs (three sinks, one registry)
//
//	skills[] / instructions[]  --render(Params)-->  install --> write files into a repo
//	   + content/*.md.tmpl                           show    --> print to stdout
//	                                                 MCP     --> docs-agent-show returns text
//
// All three enumerate the same registries, so the on-disk files, the CLI, and the MCP
// tool cannot drift from one another.
//
// # Maintenance
//
// The rules for adding or editing a skill/instruction — front-matter limits, drift-guard
// tests, and the mandatory "validate every claim against the current code" step — live in
// .github/instructions/agent-skills.instructions.md.
package agentskill
