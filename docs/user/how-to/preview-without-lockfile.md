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
validation for listed components. They use configured upstream commits, with
synthetic history derived from committed upstream-commit TOML changes.
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
| Synthetic dist-git history | Derived from lock-file fingerprint changes | Derived from generated upstream-commit TOML changes |
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

Commit the refreshed TOML together with the rendered output: synthetic dist-git
history — and therefore `%autorelease` and `%autochangelog` expansion — is derived
from committed changes to the generated file. Unlike the default mode, there is no
fingerprint to compare the working tree against, so uncommitted changes do not
produce a synthetic commit.

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
