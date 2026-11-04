# How To: Preview the Lock-File-Free Mode

The **preview** lockless workflow tracks a component's resolved upstream commit
in generated component TOML instead of per-component lock files. Select it for
individual components using `lockless-components.txt`. To use it for the whole
project, list every component.

The file applies to every command, not just rendering. Nothing in the preview
workflow is stable yet; both the command surface and generated layout may change.

```bash
# Use curl's selected workflow.
azldev component render -p curl

# Refresh curl's generated commit if curl is listed in lockless-components.txt.
azldev component refresh-upstream-commit -p curl
```

## Select Lock Mode Per Component

Create `lockless-components.txt` beside the top-level `azldev.toml`. List one
exact, case-sensitive component name per line:

```text
curl
rust-asyncband
```

Rendering, builds, and source preparation ignore existing locks and lock
validation for listed components. They use configured upstream commits.
Lockless rendering does not generate synthetic history; builds and source
preparation still derive it from committed upstream-commit TOML changes.
Unlisted components use lock files, lock validation, and fingerprint-derived
synthetic history. Both kinds can be processed together.

A missing or empty file means all components use lock files. Blank lines and
surrounding whitespace are ignored; names are not glob patterns. A file read
error stops configuration loading.

Both `component update` and `component refresh-upstream-commit` are available.
Update (including `--bump`) skips listed components and leaves
their locks untouched; refresh skips unlisted components and preserves their
generated TOML files. All-component scans still prune files for components that
no longer exist, not files belonging to components using the other mode.
`component history` and `component query` skip listed components, matching the
existing lockless no-op behavior. `component changed` compares resolved project
inputs for listed components and stored fingerprints for unlisted components;
the current checkout's list determines which comparison to use.

## What Changes

| Area | Locked component | Lockless component |
|------|---------|----------------------|
| Resolved commit storage | `locks/<name>.lock` | `base/upstream-commits/<name>.toml` |
| Refresh command | `azldev component update` | `azldev component refresh-upstream-commit` |
| Inspecting resolved state | `azldev component history`, `azldev component query` | read the generated TOML; no equivalent commands |
| Lock consistency checks | On, with `--skip-lock-validation` to opt out | Skipped |
| `component changed` | Compares stored input fingerprints | Compares project configuration resolved at each ref |
| Synthetic dist-git history | Derived from lock-file fingerprint changes | Skipped during rendering; other workflows derive it from generated upstream-commit TOML changes |
| Agent skills and MCP tools | Both workflows are available | Both workflows are available |

## Configure the Project

Include the generated directory **before** the component-specific TOML, so that a
component definition can still override the generated pin:

```toml
includes = [
    "base/upstream-commits/*.toml",
    "base/components/*.toml",
]
```

Generated files hold only `spec.upstream-commit`; the component's own TOML
supplies the source type and everything else. Because a single file may hold a
partial component definition for a lockless component, its validation runs after
all config files have been merged. Unlisted components retain normal additive
merging and per-file validation.

`[project] lock-dir` still configures the directory used for locked components.
Lockless components do not read or write their lock files.

## Refresh a Component

```bash
# Resolve and record the upstream commit for one component.
azldev component refresh-upstream-commit -p curl

# Refresh everything and prune generated files for components that no longer exist.
azldev component refresh-upstream-commit -a

# CI gate: exit 1 when any generated file is out of date.
azldev component refresh-upstream-commit -a --check-only -q
```

Refresh after changing a commit pin, upstream distro or version, or snapshot.
Overlay, build-config, and metadata changes do not affect the resolved commit, so
they need only a re-render.

When refreshing multiple components, resolution failures are reported per
component without discarding successful work. TOML files for successfully
resolved components are created or updated before the command exits with an
error. Failed components remain unchanged, and orphan pruning is skipped for
that run.

Commit the refreshed TOML before rendering: rendering compares the upstream pins
from committed configuration. Lockless rendering neither generates synthetic
history nor expands `%autorelease` or `%autochangelog`.

## Render and Commit an Upstream Component

The following process applies only to **upstream components selected as
lockless**. Local components skip the upstream release/changelog carryover and
automatic commit steps. All lockless rendering runs on the host without mock;
components using lock files retain their existing mock-based rendering.

The renderer clones only the branch selected by the component's effective
`spec.upstream-distro` (or the project default) into temporary storage. The
resolved distro version's `dist-git-branch` supplies the branch name. The clone
retains that branch's full history, not every upstream branch, so pinned commits
and snapshot selection remain available without a shallow-history limit.

Immediately after checking out the pinned upstream commit, rendering captures
optional Fedora provenance values for `build.emit-upstream-provenance`, before
release/changelog carryover or bumping changes the spec. If the upstream release
uses `%autorelease` and `build.defines` does not already provide
`fedora_upstream_release`, it runs host `rpmautospec calculate-release` at this
point. The captured values are reused later when writing the macros file, without
repeating the query against the modified checkout. No provenance query runs when
provenance is disabled or the component is not sourced from Fedora upstream.

The renderer then finds the existing committed output under
`<rendered-specs-dir>/<lowercase-first-letter>/<component-name>`, ignoring
uncommitted spec changes. For an existing non-`%autorelease` upstream spec, it
checks the committed `Release:` value. If it references the `baserelease` macro,
the renderer copies the committed `%global baserelease` or `%define baserelease`
line over the upstream definition, leaving the upstream `Release:` value
unchanged. A missing or ambiguous macro definition fails rendering. Otherwise,
it copies the committed `Release:` value into the upstream spec's `Release:`.
No temporary release-bump probe is performed. The committed `%changelog`
section is also preserved.

For an initial `%autorelease` render, it generates the complete upstream
`changelog` file (replacing any existing file), calculates the numeric upstream
release, and adds or replaces the macro's `-b` parameter. These rpmautospec
operations run on the host; they do not preprocess or rewrite the spec.

For an existing output whose spec has a line starting with `## RPMAUTOSPEC:`
and containing `autorelease` on that same line, a `%autorelease` upstream spec
inherits its base from the committed rendered spec. The renderer expands the
committed `Release:` with host `rpmspec`, using a temporary copy of the
committed dist-git files, and uses its leading integer as `-b` (for example,
`9.fc43` becomes `-b 9`). It also replaces the working checkout's `changelog`
file with the committed `%changelog` body, excluding the section header and
leading or trailing comment lines starting with `##`. Interior comments and
changelog whitespace are preserved.
The later version-change release reset still applies.

The last commit touching the component output is the **last render commit**.
The renderer fully loads TOML, including includes and component defaults, at
that commit and at the current project commit to compare `spec.upstream-commit`.
If the last render has no configured pin, its committed lock file supplies
`upstream-commit` when that commit's top-level `lockless-components.txt` is absent
or does not list the component. The historical configuration's
`lock-dir` determines which lock file to read; current or uncommitted locks are
never used for this fallback.
When the last render used a different upstream branch and the pins differ, the
renderer fetches that branch into the working clone before comparing commits.
Its branch is resolved from the last render's committed TOML, including includes,
component overrides, and distro defaults. The current branch remains available;
fetching the historical branch does not change the working checkout.
Different pins, missing render history, or an error loading the old pin mean
upstream changed. It records `git log --oneline <old>..<current>` from the
upstream checkout; without an old pin it records a one-line version update.
An error reading the current pin is fatal.

When upstream changes exist and `Version:` differs from the committed rendered
version (or there is no previous output), it retains the original upstream
`Release:` and `baserelease` definition instead of carrying over previous
values, and restores the upstream `Release:` if an autorelease base was carried over.
It then always invokes host `rpmdev-bumpspec -c` with
`updated <component-name>` followed by upstream changes on separate lines.
The tool's no-op for `%autorelease` specs is accepted.

Existing overlays run after these steps. All prepared dist-git files are retained
except `.git`, including unreferenced sidecars, hidden files, and subdirectories.
Lockless rendering does not run `spectool` or filter files; `render.skip-file-filter`
has no effect on lockless components. Synthetic history and its legacy release
adjustment are skipped. The spec is **not** preprocessed with `rpmautospec process-distgit`:
`%autorelease`, `%autochangelog`, and other RPM macros remain unexpanded.
No mock configuration is needed for lockless rendering.
The renderer replaces the component output, excluding `.git`,
and commits only that directory. The subject is `updated <component-name>`; the
body lists upstream changes, if any. Unrelated staged files are not committed.
Unchanged Git content does not produce an empty commit. A commit failure leaves
the rendered files available for inspection.

Install `rpmdevtools`, `rpm-build`, and `rpmautospec` on the host as needed for
the release/changelog queries, and configure Git author identity before
rendering. Output (including a custom `--output-dir`) must be inside the project
Git repository. `--check-only` prepares and compares temporary output but never
replaces rendered files or creates project commits. Because ordinary release
bumping is part of rendering, a new render may differ even without new upstream
commits.

## Detect Changed Components

```bash
azldev component changed --from main -a -q -O json
```

For lockless components this loads the project configuration independently at both refs
and compares the resolved component build inputs: normalized component
configuration, upstream commit or local spec-directory contents, overlay source
filenames and contents, and the effective distro release version. Documentation,
publishing, test-selection, scheduling-hint, snapshot-time, and checkout-path-only
fields do not mark a component as changed.

## Emit Agent Files for the Preview Mode

`azldev docs agent install` includes both workflows, selected per component by
the project's list:

```bash
azldev docs agent install
```

## Reference Documentation

The generated CLI reference under [reference/cli/](../reference/cli/azldev.md)
documents both workflows. Use `azldev <command> --help` for command syntax.
