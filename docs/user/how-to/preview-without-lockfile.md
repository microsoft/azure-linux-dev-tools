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

To add names and refresh their upstream commits in one command, first configure
the generated TOML include described below, then run:

```bash
azldev comp make-lockless curl rust-asyncband
```

`make-lockless` accepts the normal component selectors (`-p`, `-g`, and `-a`).
It appends missing exact names to the top-level list without rewriting existing
entries or removing lock files, then runs the `refresh-upstream-commit` pipeline
for the selection. Already-listed components are refreshed again without adding
duplicates. Non-upstream components are listed too; refresh removes any generated
pin for them without contacting an upstream provider.
If refreshing fails, the list stays updated and successful pins are saved.
Fix the failure and rerun the command. Dry runs change neither the list nor pins.

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
supplies the source type and everything else. Newly written files use one table,
`[components.<name>.spec]`, without separate parent-table headers:

```toml
[components.curl.spec]
upstream-commit = 'abcdef1234567'
```

Existing files with explicit parent tables remain readable. Because a single file
may hold a partial component definition for a lockless component, its validation runs after
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

Before any upstream checkout or component preparation, rendering checks the
entire project Git repository for staged changes, unstaged tracked changes, and
untracked files. Git-ignored files do not block rendering. If any selected
component is lockless and upstream, a dirty repository stops the whole invocation.
Commit or remove those changes first, or explicitly bypass this check:

```sh
azldev comp render -p <name> --allow-uncommitted
```

`--check-only` and `--no-git-commit` still require a clean repository unless
`--allow-uncommitted` is specified. The override bypasses only the cleanliness
check: carryover and upstream-change comparison still use committed history,
and failure recovery can discard uncommitted files in the component output
directory. Locked and local components do not trigger this check.

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
uncommitted spec changes. It prepares the changelog first, then Release, before
writing the macros file, applying overlays, or updating `sources`.

Changelog handling is independent of version changes. For non-`%autorelease`
specs, the committed inline `%changelog` section is preserved. For an initial
`%autorelease` render, host `rpmautospec generate-changelog` creates the complete
upstream `changelog`, replacing any existing file. For legacy-marker output, the
working `changelog` is replaced with the committed inline `%changelog` body,
excluding the section header and leading or trailing comment lines starting with
`##`; interior comments and whitespace are preserved. For modern `%autorelease`
output, the committed `changelog` file replaces the working file byte-for-byte,
or is created if absent from the working checkout. A missing committed
`changelog` fails rendering.

Release preparation reads the upstream spec's `Release:` and `Version:` before
overlays, and compares that version with the committed spec's
`Version:`, independently of upstream pin changes. Static release carryover runs
**only when committed output exists and the version is unchanged**; otherwise the
working static release and `baserelease` definition are left as supplied by
upstream and overlays. Autorelease follows the case-specific rules below.
There is no later restoration of the original upstream release.

When release carryover runs for an existing non-`%autorelease` upstream spec, it
checks the committed `Release:` value. If it references the `baserelease` macro,
the renderer copies the committed `%global baserelease` or `%define baserelease`
line over the upstream definition, leaving the upstream `Release:` value
unchanged. A missing or ambiguous macro definition fails rendering. Otherwise,
it copies the committed `Release:` value into the upstream spec's `Release:`.
No temporary release-bump probe is performed.

For an initial `%autorelease` render, it calculates the numeric upstream release
and adds or replaces the macro's `-b` parameter. This rpmautospec operation runs
on the host; it does not preprocess or rewrite the spec. Initial and legacy
autorelease handling do not depend on version changes.

For an existing output whose spec has a line starting with `## RPMAUTOSPEC:`
and containing `autorelease` on that same line, a `%autorelease` upstream spec
inherits its base from the committed rendered spec. The renderer expands the
committed `Release:` with host `rpmspec`, using a temporary copy of the
committed dist-git files, and uses its leading integer as `-b` (for example,
`9.fc43` becomes `-b 9`).

For an existing `%autorelease` spec without the legacy marker, rendering carries
the committed `-b` value into the working spec **only when the version is
unchanged**, preserving the working macro's other options. If the committed spec
omits `-b`, the working spec also omits it, removing any working base option
rather than adding `-b 1`. An invalid numeric base fails release carryover.
No host release query is needed for unchanged-version carryover. When the version
changes, rendering calculates the upstream numeric base on the host and adds or
replaces `-b`, just as for an initial render. This changes only release preparation:
the committed changelog is still carried over as described below, not regenerated.

For calculated bases, including legacy expansion, a base of `1` omits `-b`
and removes any existing working base option, using the `%autorelease` default.
Other macro options are preserved. Unchanged-version modern output still carries
an explicitly committed `-b 1` as-is.

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

It then compares the prepared tree with existing output, ignoring the root `.git`
and any top-level `.no-change-rebuild` on both sides.
`--check-only` stops here, before the final release bump.
If normal rendering detects no changes, it returns an error indicating there
has been no change since the last render, without replacing the existing output
or writing a failure marker. To explicitly rebuild unchanged upstream content,
pass `--no-change-rebuild`, a boolean switch taking no argument:

```bash
azldev comp render -p curl --no-change-rebuild
```

For an unchanged render with this flag, the tool runs host `date -Is` and writes
its exact output to `.no-change-rebuild` at the working dist-git root before
bumping. The marker is then published and committed with the rendered files.
The flag does not create a marker when comparison detects real changes.
The root marker is ignored in both the working upstream checkout and existing
output, even if its contents differ. Nested files with the same name are compared.

Changed renders, or explicitly requested no-change rebuilds, then invoke host
`rpmdev-bumpspec -c` with the exact `--changelog-comment` value. If omitted, the
comment defaults to `update <component-name>` for each rendered component.
Upstream changes are not appended to this comment.
The tool's no-op for `%autorelease` specs is accepted.

```bash
azldev comp render -p curl --changelog-comment "Fix curl patches"
```

Macro-file generation, existing overlays, and `sources` manifest updates run
after changelog and Release preparation, but before comparison and the final
`rpmdev-bumpspec` invocation. Overlays can override the prepared Release and
changelog; carryover is not repeated afterwards. Version changes made by overlays
do not alter the earlier carryover decision.
Release edits preserve the existing spaces and tabs between `Release:` and its
value, including during overlays and carryover, so aligned spec headers stay aligned
with either spec editor.
Rendered output retains all prepared dist-git files except `.git`, including
unreferenced sidecars, hidden files, and subdirectories.
Lockless rendering does not run `spectool` or filter files; `render.skip-file-filter`
has no effect on lockless components. Synthetic history and its legacy release
adjustment are skipped. The spec is **not** preprocessed with `rpmautospec process-distgit`:
`%autorelease`, `%autochangelog`, and other RPM macros remain unexpanded.
No mock configuration is needed for lockless rendering.
Comparison ignores the root `.git` entry without traversing or removing it;
the working checkout retains its Git metadata until staging cleanup.
After comparison and the final release bump, lockless rendering removes the
existing output directory and moves all prepared entries except `.git` into
its replacement. Files found only in the old output are not retained.
Moves use filesystem renames, so the project work directory and rendered
output must be on the same filesystem.
The renderer then commits only the component output directory. The subject is
the same changelog comment, defaulting to `update <component-name>`; the body
lists upstream changes, if any. Unrelated staged files are not committed.
Unchanged Git content does not produce an empty commit.
Pass `--no-git-commit` to skip automatic staging and committing while still
rendering and bumping the spec. The rendered output remains available for review
and manual commit. Both flags affect only lockless upstream components;
locked and local rendering retain their existing behavior.

```bash
azldev comp render -p curl --changelog-comment "Fix curl patches" --no-git-commit
```

Lockless component failures never create `RENDER_FAILED` or replace existing
output with a failure marker. Failures before output replacement leave the
existing directory unchanged. If replacement or automatic committing fails after
removal begins, rendering clears the component output and resets only that
component's staged paths. If output existed, Git checks out its committed
contents from `HEAD`; otherwise, the directory remains absent.
This recovery does not preserve uncommitted changes or untracked files in the
replaced component directory, and uses no backup or index snapshot. Recovery
errors are reported with the original render failure.
`--check-only` failures leave output untouched and fail the check.
Locked rendering retains its failure marker behavior.

For component names requiring URL encoding, rendering also creates a sibling
alias such as `libxml%2B%2B -> libxml++` for downstream tooling. Any existing
entry at the encoded alias path is removed first, including regular files and
nonempty directories, and replaced with a relative symlink. Existing symlink
targets are not removed. Alias creation failures produce a warning.
Lockless aliases are updated only after rendering and any automatic commit succeed.
`--check-only` does not replace aliases.

Install `rpmdevtools`, `rpm-build`, and `rpmautospec` on the host as needed for
the release/changelog queries. Output (including a custom `--output-dir`) must be inside the project
Git repository. Git author identity is needed for automatic commits; it is not
required by the renderer with `--no-git-commit`.
`--check-only` prepares and compares temporary output but never
performs the final `rpmdev-bumpspec` invocation, replaces rendered files, or
creates project commits. `--check-only` also never creates a rebuild timestamp,
even with `--no-change-rebuild`. The no-change refusal and rebuild flag apply only
to lockless upstream components; local and locked components retain their existing
behavior. Change reporting includes a forced rebuild marker and changes made by
the final bump.

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
