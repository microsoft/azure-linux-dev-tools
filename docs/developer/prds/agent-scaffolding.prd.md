# PRD: Agent Scaffolding via `azldev docs agent`

Title: PRD: Agent Scaffolding via `azldev docs agent`
Author: Daniel McIlvaney
Created: 2026-07-02
Status: Draft

## Overview

`azldev docs agent` should be able to **initialize a new distro repository with the AI-agent
capabilities that the Azure Linux repo has today** — the skills, instruction files, schema, and MCP
wiring that let coding agents drive `azldev` effectively. `microsoft/azurelinux` is the reference
implementation we distill the generic, non-distro-specific content from.

A v1 exists: `azldev docs agent install` emits a single light-wrapper skill
([.agents/skills/azldev/SKILL.md](internal/app/azldev/agentskill/content/skill-wrapper.md.tmpl)) plus
a path-specific instructions file, and `azldev docs agent show` serves the full skill via a read-only
MCP tool. This PRD generalizes that into a **scaffolder for a distro repo's whole agent system**.

## Problem Statement

Azure Linux has a mature, PRD-governed agent system (`docs/prds/agent-instructions.prd.md` in
`microsoft/azurelinux`): repo-wide `copilot-instructions.md`, nested `AGENTS.md` guardrails,
`.github/instructions/*.instructions.md`, ~13 `.github/skills/skill-*`, prompts, and agents. The
azldev-facing subset of that content — the `build` / `add` / `fix-overlay` / `mock` / `update` skills,
the `comp-toml` / `distro-toml` instruction files, the CLI reference in `copilot-instructions.md`, and
the mirrored `external/schemas/azldev.schema.json` — is **almost entirely azldev-operation knowledge**:
command syntax, flags, semantics, overlay types, and the tool's recommended inner loop. That content:

- **Drifts** every time azldev changes (the azurelinux PRD flags this in "Areas to Revisit Regularly"
  and its Schema Reference note).
- Must be **hand-recreated** in every new distro repo that uses azldev.

There is no way to bootstrap a new distro repo with this capability, and no single source of truth for
the azldev-generic portion. The azurelinux PRD explicitly anticipates this feature:

> Open Question #4: "Should azldev have a command to install the resources into other repos?"
> Out of Scope (v1): "Automated regeneration of agent instructions via azldev."

## Goals

1. `azldev docs agent init` scaffolds a new distro repo with the azldev-generic agent substrate.
2. `azldev docs agent update` refreshes that substrate in place, without clobbering repo-authored
   editorial content.
3. The generic content is authored/embedded **in azldev** (single source of truth = the binary), so
   azurelinux and every new distro consume the same, version-matched material.
4. Content is bound to the **target repo's resolved config** (output dirs, upstream distro, dist tag)
   at generation time.

## Non-Goals

- Generating distro **editorial/policy** (e.g. "never install RPMs on the host", hygiene rules,
  canonical example components). Those stay human-authored in the target repo.
- Generating non-azldev skills (azurelinux's Koji/AKS ops skills, `azl-diagnose` agent, KQL/metrics).
- Owning the target repo's `azldev.toml` (project config). A repo must already have one — see Decisions.

## Locked Decisions

| # | Decision | Choice |
| --- | --- | --- |
| D1 | Source of truth for generic content | **Distill azurelinux's generic skills/instructions into azldev** (embedded); regenerate azurelinux + new repos from the binary. One source of truth = the tool. |
| D2 | Init scope | **azldev-mechanics substrate only** (skill set + config instructions + CLI reference + schema + MCP configs + version pin). Editorial is left as empty/stub slots for the repo to author. |
| D3 | Config binding | **Resolve bindings from the invoking project's `azldev.toml` when present; otherwise degrade gracefully to azldev's built-in defaults** (no hard requirement). Supersedes the earlier "require config" stance so that `show` / the MCP tool and greenfield repos still emit sensible, default-accurate content. |

Implied: default emit layout flips from `.agents/skills/` to **`.github/skills/`** (matches azurelinux
and Copilot CLI / coding-agent discovery), configurable either way. Skill naming/prefix configurable to
match host convention (azurelinux uses `skill-*`).

## Design

### The ownership boundary

| Emitted by azldev (generated, overwritten on `update`) | Human-owned (never touched) |
| --- | --- |
| Skill set: build / add / fix-overlay / mock / update / render / remove | Distro policy & hygiene rules |
| `comp-toml` / `distro-toml` / spec / rendered-specs `.instructions.md` | Canonical example components |
| `copilot-instructions` azldev section + CLI reference | Non-azldev skills (Koji/AKS), infra agents |
| `azldev.schema.json` (via `config generate-schema`) | Repo-specific prose, links |
| `.vscode/mcp.json` and `.mcp.json` (azldev MCP server registration), `.azldev-version` | — |

### Content sources (why generation beats hand-maintenance)

- **Command syntax / flags / semantics** → the Cobra command tree (already emitted by
  [docs markdown](internal/app/azldev/cmds/docs/markdown.go)).
- **Overlay types + required fields** → the JSON schema (`config generate-schema`).
- **Resolved bindings** (output-dir / log-dir / work-dir paths, upstream distro, dist tag) → azldev
  resolves these from the target repo's `azldev.toml`, so the "repo-specific" build-output table
  azurelinux hand-wrote becomes generated per repo.
- **Recommended inner loop / workflow narrative** → authored once in azldev's embedded templates
  (distilled from azurelinux's skills), rendered with the above.

### Composition / no-clobber

- Every generated file carries a "generated by azldev docs agent — do not hand-edit" marker (in-body
  for `SKILL.md`, since YAML frontmatter must be first).
- `update` overwrites only generated files. Editorial lives in **separate** files the tool never
  writes (e.g. a repo-authored `AGENTS.md` that links to the generated skills; a policy
  `.instructions.md`).
- Open question: whether to also support marker-delimited regions inside a shared file (e.g. an
  azldev section inside a repo-owned `copilot-instructions.md`) vs. keeping generated content in
  discrete files. Prefer discrete files first (simplest, safest).

### Command surface

- `azldev docs agent init [-o dir]` — scaffold the substrate into a repo (requires `azldev.toml`).
- `azldev docs agent update [-o dir]` — refresh generated files in place.
- `azldev docs agent show --skill <name>` — print a skill (read-only MCP tool). Parameterized by skill name
  once the set is multiple (per the earlier multi-skill decision).
- `azldev docs agent check [-o dir]` — (proposed) drift gate for CI: verify committed generated files
  match what the pinned azldev version would emit. Closes the azurelinux PRD's sync concern.
- Layout/config flags: `--skills-dir` / `--layout` (`.github` | `.agents`), `--skill-prefix`.

### MCP

- The read-only `docs-agent-show` tool (already implemented) is how agents load skill bodies on
  demand; the emitted wrappers reference it. `init` also writes `.vscode/mcp.json` for VS Code and
  `.mcp.json` for Copilot CLI, registering the `azldev advanced mcp` server so the tool is available.
  Read-only annotation enables auto-approval, which directly addresses azurelinux's "tool approvals
  are difficult" note in `DEVELOPING.md`.

## Relationship to v1 (already shipped)

v1 = single `azldev` skill wrapper + one instructions file + read-only `docs-agent-show` tool, emitted
to `.agents/skills/`. This PRD generalizes it: a skill **set**, `init`/`update`/`check`, config-resolved
bindings, `.github/` default layout, schema + `mcp.json` emission, and a composition model. The v1
shared package [internal/app/azldev/agentskill](internal/app/azldev/agentskill/agentskill.go) is the
natural home for the skill registry (the "small lift" refactor already scoped: singletons → `[]Skill`).

## Companion Workstream: rework azurelinux

Once azldev emits the substrate, azurelinux is reworked as a **two-way port**:

1. Lift azurelinux's generic skill/instruction content up into azldev's embedded templates (they are the
   distilled source).
2. Have azurelinux re-consume it via `azldev docs agent update`, keeping only editorial deltas in
   files azldev never touches.
3. Regenerate `external/schemas/azldev.schema.json`; add Phase 6 `.vscode/mcp.json` and `.mcp.json`.

## Implementation Plan (proposed)

- **Phase 1 — Skill registry**: refactor `agentskill` singletons → `[]Skill`; parameterize `show`;
  configurable layout + naming. (Foundational; independent of content.)
- **Phase 2 — Config-resolved bindings**: inject resolved paths (lock dir, rendered-specs
  dir) from the invoking project's `azldev.toml` into templates, degrading to azldev's built-in
  defaults when no configuration is available. (Upstream-distro / dist-tag bindings follow in
  Phase 3 alongside the skills that consume them.)
- **Phase 3 — Content distillation**: port azurelinux's build / add / fix-overlay / mock / update
  skill content + `comp-toml` / `distro-toml` instructions into embedded templates (real content
  replaces the pie placeholder).
- **Phase 4 — Substrate emission**: schema (`config generate-schema`), `.vscode/mcp.json`, `.mcp.json`,
  `.azldev-version`, `copilot-instructions`/`AGENTS` scaffold; `init` vs `update`.
- **Phase 5 — Drift gate**: `docs agent check` for CI.
- **Phase 6 — azurelinux rework**: the companion port above.

## Open Questions

1. **Composition mechanism**: discrete files only, or also marker-delimited regions inside repo-owned
   files? (Lean: discrete first.)
2. **Skill naming**: adopt azurelinux's `skill-*`, or an `azldev-*` namespace, as the emitted default?
3. **Prompts / agents**: azurelinux's prompts (`.prompt.md`, VS Code only) and agents (`.agent.md`) —
   in scope for generation, or left as repo-authored orchestration over the generated skills?
4. **Editorial stubs**: D2 says "empty/stub slots" — how much stub scaffolding (empty policy
   `AGENTS.md`? a TODO checklist?) vs. nothing at all?
5. **`check` in CI**: exact contract for "matches the pinned azldev version" given the version stamp is
   embedded in output.
