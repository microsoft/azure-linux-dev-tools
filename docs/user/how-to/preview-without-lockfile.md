# How To: Preview the Lock-File-Free Mode

`--without-lockfile` is a **preview** global flag. It selects an alternative way of
tracking a component's resolved upstream commit: instead of per-component lock
files, azldev records the commit in generated component TOML that the project
includes like any other config file.

The flag is opt-in and defaults to off. Without it, azldev behaves exactly as it
always has — lock files, `component update`, `component history`, and
`component query` are unchanged. Nothing in the preview mode is stable yet; both
the command surface and the generated file layout may change.

```bash
# Default behavior: lock files.
azldev component render -p curl

# Preview behavior: generated upstream-commit config.
azldev --without-lockfile component render -p curl
```

Pass the flag on every invocation that should use the preview mode, before the
command name. `--without-lockfile=false` explicitly selects the default mode.

## What Changes

| Area | Default | `--without-lockfile` |
|------|---------|----------------------|
| Resolved commit storage | `locks/<name>.lock` | `base/upstream-commits/<name>.toml` |
| Refresh command | `azldev component update` | `azldev component refresh-upstream-commit` |
| Inspecting resolved state | `azldev component history`, `azldev component query` | read the generated TOML; no equivalent commands |
| Lock consistency checks | On, with `--skip-lock-validation` to opt out | Not applicable; the flag is not registered |
| `component changed` | Compares stored input fingerprints | Compares project configuration resolved at each ref |
| Synthetic dist-git history | Derived from lock-file fingerprint changes | Derived from generated upstream-commit TOML changes |
| Agent skills and MCP tools | Describe the lock-file workflow | Describe the upstream-commit workflow |

`component update`, `component history`, and `component query` remain registered
in preview mode as hidden no-ops so that existing scripts report clearly that the
commands do nothing, rather than failing with "unknown command".

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
partial component definition in this mode, component validation runs after all
config files have been merged.

An existing `[project] lock-dir` setting is accepted and ignored in preview mode,
so the same project config works in both modes.

## Refresh a Component

```bash
# Resolve and record the upstream commit for one component.
azldev --without-lockfile component refresh-upstream-commit -p curl

# Refresh everything and prune generated files for components that no longer exist.
azldev --without-lockfile component refresh-upstream-commit -a

# CI gate: exit 1 when any generated file is out of date.
azldev --without-lockfile component refresh-upstream-commit -a --check-only -q
```

Refresh after changing a commit pin, upstream distro or version, or snapshot.
Overlay, build-config, and metadata changes do not affect the resolved commit, so
they need only a re-render.

Commit the refreshed TOML together with the rendered output: synthetic dist-git
history — and therefore `%autorelease` and `%autochangelog` expansion — is derived
from committed changes to the generated file. Unlike the default mode, there is no
fingerprint to compare the working tree against, so uncommitted changes do not
produce a synthetic commit.

## Detect Changed Components

```bash
azldev --without-lockfile component changed --from main -a -q -O json
```

In preview mode this loads the project configuration independently at both refs
and compares the resolved component build inputs: normalized component
configuration, upstream commit or local spec-directory contents, overlay source
filenames and contents, and the effective distro release version. Documentation,
publishing, test-selection, scheduling-hint, snapshot-time, and checkout-path-only
fields do not mark a component as changed.

## Emit Agent Files for the Preview Mode

`azldev docs agent install` emits the content for the mode it runs in, so pass the
flag when the target repository uses the preview workflow:

```bash
azldev --without-lockfile docs agent install
```

## Reference Documentation

The generated CLI reference under [reference/cli/](../reference/cli/azldev.md)
documents azldev's default mode. Use `azldev --without-lockfile <command> --help`
to see the preview mode's command surface and help text.
