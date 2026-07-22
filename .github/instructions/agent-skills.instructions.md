---
applyTo: "internal/app/azldev/agentskill/**"
description: "How to maintain azldev's emitted AI-agent skills and instruction files. Read before adding or editing a skill, an instruction wrapper, or the emit mechanism in the agentskill package."
---

# Maintaining the emitted agent skills and instruction files

The `agentskill` package is the **single source of truth** for the AI-agent skill and
instruction files that `azldev docs agent install` writes into distro repositories, and
that `azldev docs agent show` (a read-only MCP tool) serves. Everything is embedded in
the binary so the on-disk files, the CLI output, and the MCP response stay version-matched.

For the full architecture — the registries, the redirect-wrapper-vs-full-body rendering,
and how dynamic values are substituted — read the package doc in
[doc.go](../../internal/app/azldev/agentskill/doc.go).

## Model

- **Skills hold the content.** A `Skill` in the `skills` registry
  ([agentskill.go](../../internal/app/azldev/agentskill/agentskill.go)) pairs a name with a
  body template under [content/](../../internal/app/azldev/agentskill/content/). Skills are the
  cross-agent medium — served via `docs agent show` / the `docs-agent-show` MCP tool, or inlined
  on disk with `--full`.
- **Instruction files are lightweight wrappers.** An `Instruction` in the `instructions` registry
  is selected by its `applyTo` glob and only *points at* skills — `SkillPointer{Skill, Purpose}`
  renders as "You MUST read the `<skill>` skill `<purpose>`". Put substantive guidance in a
  **skill**, never in a wrapper. Do not make a wrapper reference the CLI or MCP tool — those are
  unavailable in `--full` installs; naming the skill works in every mode.

## Adding or editing a skill

1. Add a `Skill{Name, Description, bodyTemplate}` to the `skills` registry.
2. Add a `content/<topic>.md.tmpl` body, named for the topic (e.g. `mock.md.tmpl`,
   `azldev.md.tmpl`). The front-matter `name` must equal the skill's base name (and its on-disk
   directory), lowercase-hyphen, ≤ 64 chars. The `description` drives discovery, ≤ 1024 chars.
3. Write the description inline in the registry entry. The description is the **load gate**
   (an agent decides whether to open the skill from it), so **lead with a directive** —
   "Read this before <action>; do not <do it> from memory." — then what the skill covers,
   then a `Triggers include ...` keyword list.
4. Add a content test in
   [agentskill_test.go](../../internal/app/azldev/agentskill/agentskill_test.go) (assert the
   front-matter `name` and a distinctive, validated phrase). `TestSkillFrontmatterInvariants`
   auto-covers the spec limits.

## Adding or editing an instruction wrapper

1. Add an `Instruction{Name, ApplyTo, Description, Title, Intro, Skills}` to the `instructions`
   registry. `ApplyTo` may reference bindings (e.g. `{{ .RenderedSpecsDir }}/**/*`) — it is
   rendered against `Params` at emit time.
2. `Skills` is a list of `SkillPointer` — name each skill with a short purpose ("to add or change
   overlays").
3. A little hand-written prose here is fine, if it helps direct agents to the right skill. Keep it short, and avoid
   repeating the skill content.
4. Keep the count-based tests happy: `Files()` emits one file per skill plus one per instruction.

## Content accuracy is the hard part

Distilled content **drifts**. The azurelinux copies these are distilled from are frequently stale.
Before shipping any skill/instruction:

- **Validate every CLI, flag, and config claim against the current code** (command trees under
  `internal/app/azldev/cmds/`, config under `internal/projectconfig/`). Do not trust the source you
  distilled from. Confirm with `./out/bin/azldev <cmd> --help` and `azldev config generate-schema`.
- Prefer a **drift-guard test** whenever a code enum can back the content. Example:
  `TestOverlaysSkillCoversAllOverlayTypes` extracts the overlay-type enum from the jsonschema tag
  on `projectconfig.ComponentOverlay.Type` and fails if the skill omits a type.

## Config-resolved bindings

Repo-specific values (lock dir, rendered-specs dir, work dir) are resolved from the target `azldev.toml` in
[cmds/docs/agent.go](../../internal/app/azldev/cmds/docs/agent.go) and degrade to azldev's defaults
when no config is present. To add a binding, extend `Bindings`, resolve it in `resolveBindings`, and
reference it in a template as `{{ .FieldName }}`.

## Before you commit

- `mage unit` and `mage check all` pass.
- `mage docs` produces **no drift** (adding a registry skill/instruction should not change CLI docs,
  the JSON schema, or the MCP snapshot).
- Sanity-check emitted output: `./out/bin/azldev docs agent install -o "$(mktemp -d)"` and
   `./out/bin/azldev docs agent show --skill <name>`.
