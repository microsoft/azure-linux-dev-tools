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
Lockless rendering, builds, queries, and source preparation do not generate
synthetic history; builds and queries use committed rendered history.
Unlisted components use lock files, lock validation, and fingerprint-derived
synthetic history. Both kinds can be processed together.
Locked rendering preserves existing non-symlink entries at percent-encoded alias
paths and warns about the collision; lockless rendering replaces those entries.
Locked rendered output is copied, so the work and output directories may reside
on different filesystems. Lockless output continues to use moves.
Only lockless `render --check-only` ignores root `.git` and
`.no-change-rebuild` entries. Locked rendering compares all existing output
entries after removing Git metadata from its staging directory.
Lockless Release edits preserve existing tag casing and alignment; locked
components retain the established `Release: value` normalization.

A missing or empty file means all components use lock files. Blank lines and
surrounding whitespace are ignored; names are not glob patterns. A file read
error stops configuration loading.

To add names and refresh their upstream commits in one command, first configure
the generated TOML include described below, then run:

```bash
azldev comp make-lockless curl rust-asyncband
```

`make-lockless` accepts the normal component selectors (`-p`, `-g`, and `-a`).
It merges selected exact names into the top-level list and writes the complete
list in lexical order, one name per line without blank lines or duplicates.
Existing names are retained and lock files are left untouched. It then runs the
`refresh-upstream-commit` pipeline for the selection.
Already-listed components are refreshed again without adding
duplicates. Non-upstream components are listed too; refresh removes any generated
pin for them without contacting an upstream provider.
If refreshing fails, the list stays updated and successful pins are saved.
Fix the failure and rerun the command. Dry runs change neither the list nor pins.

Both `component update` and `component refresh-upstream-commit` are available.
Update (including `--bump`) skips listed components and leaves
their locks untouched; refresh skips unlisted components and preserves their
generated TOML files. All-component scans still prune files for components that
no longer exist, not files belonging to components using the other mode.
This preservation includes generated pins for unlisted local components, both
when refreshing normally and when checking for stale files with `--check-only`.
`component history` skips listed components. `component query` extracts their
committed rendered dist-git history and processes it with rpmautospec before
querying; uncommitted rendered changes are ignored. `component changed` compares resolved project
inputs and committed rendered output for listed components, and stored fingerprints for unlisted components;
the current checkout's list determines which comparison to use.

## What Changes

| Area | Locked component | Lockless component |
|------|---------|----------------------|
| Resolved commit storage | `locks/<name>.lock` | `base/upstream-commits/<name>.toml` |
| Refresh command | `azldev component update` | `azldev component refresh-upstream-commit` |
| Inspecting resolved state | `azldev component history`, `azldev component query` | `component query` inspects committed rendered specs; `history` skips lockless components |
| Lock consistency checks | On, with `--skip-lock-validation` to opt out | Skipped |
| `component changed` | Compares stored input fingerprints | Compares resolved build inputs and committed rendered output at each ref |
| Synthetic dist-git history | Derived from lock-file fingerprint changes | Not generated; build and query extract committed rendered history |
| Agent skills and MCP tools | Both workflows are available | Both workflows are available |

### Preparing, querying, and building committed dist-git

Render and commit the component before querying or building it. Lockless
`component query` and `component build` use the same extraction as
`component dist-git-repo`: a temporary `--single-branch --no-local` clone of the
current project branch, filtered with `git filter-repo --subdirectory-filter`
using the component's repository-relative rendered directory. Install
`git-filter-repo` on the host. Uncommitted rendered changes are not included,
and extraction never modifies the original repository.

For a bulk query, the project branch is cloned once into a temporary bare
snapshot. Component repositories initially share that immutable object store
and are filtered with bounded CPU concurrency. Each filtered repository is then
repacked and detached from the shared store before entering Mock, so it contains
only its own history and does not depend on host-only object paths.

`component query` processes the temporary spec with rpmautospec in the project's
Mock chroot, then reports its source NEVR, required files, and binary subpackages.
The temporary query repositories are removed on success and failure.
Locked queries continue to read working-tree rendered files, including uncommitted edits.

`component build` fetches configured source files through the normal source manager,
including custom generation. Download-origin fallback is enabled unless
`disable-origins` is set. It then downloads any remaining payloads listed in the extracted
`sources` manifest from lookaside and builds the SRPM with Mock's `rpmautospec`
plugin enabled and the extracted repository passed as `--sources`. This requires Mock 5.3 or newer and
rpmautospec available from the build distro's package repositories.
Mock's rpmautospec plugin must be installed on the host as well; enabling an
unavailable plugin is an error, even if Mock itself would only warn.
The plugin resolves `%autorelease` and `%autochangelog` using the filtered
history; no synthetic history, overlay replay, or separate Release bump runs.
Build macros come from the committed spec, not duplicate
Mock `--define`, `--with`, or `--without` arguments. Render and commit macro changes
before building; live configuration cannot override committed macro settings.
Lockless rendering emits sorted `%define` entries at the top of the spec instead
of a separate `.azl.macros` file, `%{load:...}` directive, or generated `Source9999`.
These lazy definitions preserve references to macros defined later in the spec.
Locked components retain the separate macros file and its source entry.
Binary RPMs are built from that SRPM normally. `--no-check` is forwarded through
Mock's `--nocheck`; `--srpm-only` skips binary builds. Lockless builds reject
`--without-git` and `--rpmdev-bumpspec`. Locked build behavior is unchanged.

`component prepare-sources` uses lockless render preparation while writing
directly into `--output-dir`, allowing uncommitted and untracked project files.
Current TOML configuration (including upstream pins and distro settings), overlay
inputs, scripts, and local sources are read from the working tree. Modified and
newly added files are used without committing them first. Only the existing
component dist-git state used as the release/changelog baseline is read from
committed files; modified and untracked files in that directory are ignored.
Nonempty output requires `--force`, which removes existing contents before
preparation. Failures can leave partially prepared output. It fetches upstream
lookaside payloads with the spec/sidecars, reads render history and upstream
changes, then downloads/generates configured `source-files`. Like locked
preparation, it acquires payloads before emitting build macros and applying
overlays. Archive overlays run first, followed by inline macro definitions,
configured spec/loose-file overlays, and generated check-skip/header overlays. It
updates `sources` metadata with the configured post-overlay hashes. The
release/changelog are then prepared from the post-overlay spec, matching
`render`, including its temporary amend of overlay edits while host `rpmautospec`
queries the upstream checkout. The upstream HEAD is left at the fetched upstream
commit, and the overlay and release/changelog edits to tracked files are committed
on top of it as a new `azldev prepare-sources` commit (author
`azldev <azldev@localhost>`); no commit is made when nothing changed. Untracked
files, such as downloaded payloads, are not committed. It does not compare
rendered output, apply a final render release bump, or commit changes.
Render history is read from
`<project.rendered-specs-dir>/<lowercase-first-letter>/<component>` inside the
project repository, independently of `--output-dir`. The configured root defaults
to `specs/`. This path never generates synthetic history. Fetched upstream Git
metadata is retained through preparation; `--without-git` removes `.git` after
successful preparation. On failure, any remaining Git metadata is left for inspection.

The source-preparation flags also apply to lockless components:

| Flag | Behavior |
|------|----------|
| `--without-git` | Remove `.git` from prepared output after successful preparation. It remains available for upstream release/changelog preparation; no synthetic history is generated with or without the flag. |
| `--skip-overlays` | Skip configured and generated overlays, inline macro definitions, and `sources` metadata updates. Source acquisition still runs unless `--skip-sources` is also set. Upstream release/changelog preparation still runs, using the unmodified upstream spec. |
| `--skip-sources` | Skip upstream lookaside downloads, configured source downloads/generation, and archive overlays. Spec/sidecars are still fetched; other overlays and `sources` metadata updates still run unless `--skip-overlays` is set. Overlays requiring missing payloads can fail. |
| `--allow-no-hashes` | Compute missing hashes from acquired files when updating `sources`, defaulting to SHA-512 when `hash-type` is omitted. This does not edit TOML. With `--skip-sources`, missing hashes cannot be computed and cause an error; with `--skip-overlays`, metadata updates and hash computation are skipped. |

Use `azldev component dist-git-repo <name> -o <directory>` to obtain a committed
working dist-git repository. That extraction command does not download archives.

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

Explicit upstream pins may refer to commits on other branches. Source preparation
fetches all branches when an effective commit pin is present; floating lockless
sources retain the single-branch clone optimization.

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

Rendering lists upstream changes by comparing the upstream pin from the last
committed render with the current (working-tree) pin, so a refreshed pin does
not need to be committed before rendering. Lockless rendering neither generates synthetic
history nor expands `%autorelease` or `%autochangelog`.

## Render and Commit a Component

The following process applies to **all components selected as lockless**.
Local components skip upstream-history queries and release/changelog carryover;
their Release and changelog are never automatically modified, including with
`--rpmdev-bumpspec`. They copy local files except the TOML defining the local spec
path, retain other TOML sidecars, and participate in unchanged-render checks.
All lockless rendering runs on the host without mock;
components using lock files retain their existing mock-based rendering.

Rendering never checks for uncommitted or untracked files: working-tree inputs
anywhere in the repository (component TOML, overlays, local sources, and so on)
are used as-is. The previous render baseline — the committed rendered spec,
changelog, and `Version:`, and the upstream pin from committed TOML history at
the last render — is always read from committed files only, so uncommitted
rendered output never affects carryover. The current upstream pin comes from
the working-tree configuration.

For unpinned upstream components, the renderer clones only the branch selected by
the component's effective `spec.upstream-distro` (or the project default) into
temporary storage. The resolved distro version's `dist-git-branch` supplies the
branch name. The clone retains that branch's full history so snapshot selection
remains available without a shallow-history limit. Pinned components fetch all
branches so commits outside the selected branch remain usable. This branch
restriction is specific to unpinned lockless preparation; locked components
retain normal multi-branch cloning.

Immediately after checking out the pinned upstream commit, rendering captures
optional Fedora provenance values for `build.emit-upstream-provenance`, before
release/changelog carryover or bumping changes the spec. If the upstream release
uses `%autorelease` and `build.defines` does not already provide
`fedora_upstream_release`, it runs host `rpmautospec calculate-release` at this
point. The captured values are reused later when emitting inline macros, without
repeating the query against the modified checkout. No provenance query runs when
provenance is disabled or the component is not sourced from Fedora upstream.

The renderer then finds the existing committed output under
`<rendered-specs-dir>/<lowercase-first-letter>/<component-name>`, ignoring
uncommitted spec changes, and collects upstream changes. It then emits inline
macros, applies overlays, and updates `sources`. Finally, it prepares the
changelog, then Release, from the post-overlay spec. Overlay edits otherwise stay
uncommitted in the upstream checkout. Host `rpmautospec` queries the upstream
checkout itself only for an `%autorelease` spec on a first render or a version
change (`calculate-release`, plus `generate-changelog` on a first render). For
just those queries, overlay edits to tracked upstream files are temporarily
amended into the upstream HEAD commit, keeping its original committer name,
email, and date, so host `rpmautospec` sees a clean checkout and calculates the
same release and changelog dates. These queries run before the prepared
`changelog` or spec is written. Afterwards, even on failure, a mixed
`git reset` restores HEAD and the index to the original upstream commit, leaving
the overlay edits as uncommitted working-tree changes. Untracked files, such as
downloaded payloads, are never committed.

Changelog handling is independent of version changes. For non-`%autorelease`
specs, the committed inline `%changelog` section is preserved. For an initial
`%autorelease` render, host `rpmautospec generate-changelog` creates the complete
upstream `changelog`, replacing any existing file. For legacy-marker output, the
working `changelog` is replaced with the committed inline `%changelog` body,
excluding the section header and leading or trailing comment lines starting with
`##`; interior comments and whitespace are preserved. When the previous render
had a static `Release:` and upstream now uses `%autorelease`, its inline
`%changelog` body (the lines after the section header) is written as the working
`changelog`; if it had no `%changelog` section, the working file is left as-is.
For modern `%autorelease`
output, the committed `changelog` file replaces the working file byte-for-byte,
or is created if absent from the working checkout. A missing committed
`changelog` fails rendering.

Release preparation reads the post-overlay spec's `Release:` and `Version:`.
The spec must have exactly one `Release:` tag in its main package and at most
one in each subpackage (`%package foo` or `%package -n foo`). Tags inside `%if`
conditionals count; commented-out lines do not. A spec that breaks this rule,
for example one `Release:` per `%if`/`%else` branch, fails rendering with an
error naming the package and its tag count; use overlays to remove or comment
out the extra `Release:` lines.

The version is compared first: the post-overlay `Version:` is compared with the
committed spec's `Version:`, independently of upstream pin changes. When the
version **changes** (or there is no committed output), the release comes only
from the new upstream spec: no committed `Release:` value is read or validated,
static releases and the `baserelease` definition are left as supplied by
upstream and overlays, and `%autorelease` tags get the base calculated from the
upstream checkout. There is no later restoration of the original upstream release.

When the version is **unchanged**, the committed spec must follow the same
`Release:` tag rule, or rendering fails. For a static upstream main `Release:`,
the renderer then carries the committed main value. If the committed main
`Release:` references the `baserelease` macro, the renderer instead copies the
committed `%global baserelease` or `%define baserelease` line over the upstream
definition, leaving the upstream `Release:` value unchanged; a missing or
ambiguous macro definition fails rendering. Each upstream subpackage with its
own static `Release:` receives the committed value of the subpackage with the
same name; it keeps its upstream value when the committed render has no static
`Release:` for that subpackage. Committed subpackages that no longer exist
upstream are ignored. No temporary release-bump probe is performed.

For a `%autorelease` upstream main `Release:`, the base is derived from the
committed main `Release:` as described below and applied as `-b` to every
`%autorelease` `Release:` tag, including subpackages, preserving each tag's
other options. Static subpackage `Release:` values in such a spec are left as
supplied by upstream.

When upstream switches from `%autorelease` to a static release, the renderer
extracts the committed component history (this requires `git-filter-repo`). An
existing `%autochangelog` is materialized from that history before adding the
static changelog entry. For an unchanged version, it also calculates the
complete release with host rpmautospec and replaces each committed
`%autorelease` `Release:` (main and subpackages, each keeping its own `-n`
handling) with that concrete release, which becomes the baseline for the normal
static bump; the old `%autorelease` macro is never restored. A changed version
starts from the new upstream release without calculating the committed release.

For an initial `%autorelease` render, it calculates the numeric upstream release
and adds or replaces the macro's `-b` parameter. This rpmautospec operation runs
on the host; it does not preprocess or rewrite the spec. Any version change
uses this same calculated upstream base, whether or not the committed output
was processed by rpmautospec.

For an existing output whose spec has a line starting with `## RPMAUTOSPEC:`
and containing `autorelease` on that same line, a `%autorelease` upstream spec
with an unchanged version inherits its base from the committed rendered spec. The renderer expands the
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
If the old pin cannot be resolved in the single-branch checkout, rendering
fetches that specific commit hash from `origin` and checks again. If the commit
remains unavailable, including after a failed historical-branch fetch, rendering
continues with `update to upstream <component-name> commit <current-hash>` as the
upstream changes text. Other errors collecting the Git log remain fatal.
An error reading the current pin is fatal.

It then compares the prepared tree with existing output, ignoring the root `.git`
and any top-level `.no-change-rebuild` on both sides.
`--check-only` stops here, before the final release bump.
If normal rendering detects no changes, it returns an error without replacing
the existing output or writing a failure marker. Unchanged local non-autorelease
components must manually update their release and changelog; they fail even with
`--no-change-rebuild`. To explicitly rebuild unchanged upstream content or a local
autorelease component,
pass `--no-change-rebuild`, a boolean switch taking no argument:

```bash
azldev comp render -p curl --no-change-rebuild
```

For an unchanged autorelease component (upstream or local) with this flag, the
tool runs host `date -Is` and writes its exact output to `.no-change-rebuild` at
the working dist-git root. The marker is then published with the
rendered files. Unchanged upstream non-autorelease components instead proceed
to the normal release/changelog bump without writing a timestamp marker.
Local specs are never automatically bumped.
The flag does not create a marker when comparison detects real changes.
The root marker is ignored in both the working upstream checkout and existing
output, even if its contents differ. Nested files with the same name are compared.

Changed renders, or explicitly requested no-change rebuilds, then invoke host
`rpmdev-bumpspec -c` **only for upstream non-autorelease specs**. The changelog
text is the `--commit-message` value, followed by one line per upstream change
prefixed with `- `:

```text
Update curl configuration
- abc1234 Fix upstream build
```

Without `--commit-message`, an unchanged render allowed by
`--no-change-rebuild` uses `No-change rebuild.`. For changed output, the text is
detected from the project's git history:

- For a first render (no previous render commit), it is
  `Initial import from upstream.`
- Otherwise, azldev walks first-parent commits from `HEAD` back to, but not
  including, the component's last render commit. Each commit whose
  component comparison inputs (the inputs `comp changed` compares, such as the
  normalized TOML configuration and overlay source hashes) differ from the
  preceding commit's contributes its subject line. The walk stops early at a
  commit whose inputs match the last render commit's. Subjects are listed
  oldest first, one changelog line each.
- If no commit changed the component (for example, the output changed for
  reasons outside those inputs), the render fails; pass `--commit-message`.
- Uncommitted changes that alter the component's inputs fail the render;
  commit them or pass `--commit-message`.

These detection failures return a failing exit status even without
`--fail-on-error`, and leave existing output untouched.

Changes only under the rendered-specs directory are ignored. Upstream change
lines are always appended. You can also edit the generated changelog entry
before committing.
Rendering never prompts for a message and needs no editor or interactive terminal.

Local components and autorelease specs skip `rpmdev-bumpspec` entirely.
For upstream non-autorelease specs, a temporary process-local RPM wrapper sets
`_sourcedir` and `_specdir` to the spec's directory so sidecar macros resolve
correctly. Rendering preserves your `HOME`, packager settings, locale, and
timezone; it does not edit your `.rpmmacros`. The wrapper is removed after the
invocation, including when bumpspec fails. `Release:` lines inside `%changelog`
text (for example, quoted spec snippets) are temporarily masked so bumpspec
changes only real `Release` tags; they are restored unchanged afterward.

Macro-file generation, existing overlays, and `sources` manifest updates run
before changelog and Release preparation, comparison, and the final
`rpmdev-bumpspec` invocation. Changelog and Release preparation replace any
`Release:` or `%changelog` changes made by overlays when carryover or a committed
changelog applies. A `Version:` overlay is part of the version comparison, so it
can enable or disable carryover. For `%autorelease` specs, host `rpmautospec`
sees a `Version:` overlay as a version change in the temporarily amended upstream commit.
Release edits preserve the existing spaces and tabs between `Release:` and its
value, including during overlays and carryover, so aligned spec headers stay aligned
with either spec editor.
Rendered output retains all prepared dist-git files except `.git`, including
unreferenced sidecars, hidden files, and subdirectories. Local spec-defining TOML
files are excluded during copying.
Lockless rendering does not run `spectool` or filter files; `render.skip-file-filter`
has no effect on lockless components. Synthetic history and its legacy release
adjustment are skipped. The spec is **not** preprocessed with `rpmautospec process-distgit`:
`%autorelease`, `%autochangelog`, and other RPM macros remain unexpanded.
No mock configuration is needed for lockless rendering.
Comparison ignores the root `.git` entry without traversing or removing it;
the working checkout retains its Git metadata until staging cleanup.
After comparison and the final release bump, but immediately before removing
existing output, lockless rendering queries the existing and new source-package
NEVRs with host `rpmspec`. Queries expand each spec's macros using its own dist-git
directory. A spec whose `Release` uses `%autorelease` without having been processed
by rpmautospec (no `## RPMAUTOSPEC` header) is instead resolved as lockless builds
resolve it: against the component's filtered rendered-directory history, as
produced by `git filter-repo --subdirectory-filter`, using `rpmautospec
process-distgit` before the `rpmspec` query. The existing spec uses the committed
history; the new spec adds the prepared output as the next commit, unless its tree
is unchanged. For merge-free project history, the filtered history is replayed in a
temporary repository that reads project objects through Git alternates and never
writes to the project repository; histories with merges are cloned and filtered
with `git filter-repo`, which must then be installed. Plain `rpmspec` would expand
`%autorelease -b N` to `N`, ignoring the commits that raise the built release.
The package name must match. When the final new spec uses
`%autorelease`, its new epoch/version/release must be equal to or higher than the
existing value under RPM version ordering. Non-autorelease specs still require
a strictly higher value. Autorelease permits equality because unchanged output
creates no new commit. Lower NEVRs, and equal non-autorelease NEVRs, fail with both values
in the error message; query failures also abort. These errors return
a failing exit status even without `--fail-on-error`, and leave existing output
untouched. First renders with no existing directory skip the check.
`--check-only` and dry runs do not perform this replacement-time check.
`--no-change-rebuild` does not bypass it: a timestamp-only autorelease rebuild
may have an equal expanded NEVR, but must not decrease it.

After successful validation, lockless rendering removes the
existing output directory and moves all prepared entries except `.git` into
its replacement. Files found only in the old output are not retained.
Moves use filesystem renames, so the project work directory and rendered
output must be on the same filesystem.
Rendering never stages or commits anything; review and commit the rendered
output yourself. The next render's baseline reflects only committed output, so
uncommitted rendered changes are ignored (and replaced) by the next render.
Locked rendering retains its existing behavior.

```bash
azldev comp render -p curl
git add specs/c/curl
git commit -m "chore(curl): render"
```

Lockless component failures never create `RENDER_FAILED` or replace existing
output with a failure marker. Failures before output replacement leave the
existing directory unchanged. If replacement fails after
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
Lockless aliases are updated only after rendering succeeds.
`--check-only` does not replace aliases.

Install `rpmdevtools`, `rpm-build`, and `rpmautospec` on the host as needed for
the release/changelog queries. Output (including a custom `--output-dir`) must be inside the project
Git repository.
`--check-only` prepares and compares temporary output but never
performs the final `rpmdev-bumpspec` invocation or replaces rendered files.
`--check-only` also never creates a rebuild timestamp, even with `--no-change-rebuild`. The no-change refusal and rebuild flag apply only
to all lockless components; locked components retain their existing
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
It also compares the committed rendered component directories, including specs,
sidecars, and `.no-change-rebuild` markers. Render-only commits therefore select
the component for rebuilding even when its configuration and upstream pin are
unchanged. `sources-change` still reports only changes to the `sources` manifest.

## Emit Agent Files for the Preview Mode

`azldev docs agent install` includes both workflows, selected per component by
the project's list:

```bash
azldev docs agent install
```

## Reference Documentation

The generated CLI reference under [reference/cli/](../reference/cli/azldev.md)
documents both workflows. Use `azldev <command> --help` for command syntax.
