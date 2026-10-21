# How To: Build a Component

This guide walks through building a component with `azldev`.

## Prerequisites

- A configured Azure Linux project (see [Creating a Project](create-project.md))
- The `azldev` CLI installed and on your `PATH`

## Quick Start

Build a component by name:

```bash
azldev comp build -p <component-name>
```

Built packages are placed in structured subdirectories under the project's output directory
(configured by `output-dir` in [project config](../reference/config/project.md)):

| Directory | Contents |
|-----------|----------|
| `out/srpms/` | Source RPMs (SRPMs) |
| `out/rpms/` | Binary RPMs with no channel configured, or channel `none` |
| `out/rpms/<channel>/` | Binary RPMs assigned to a named publish channel |

## Common Options

| Flag | Description |
|------|-------------|
| `--no-check` | Skip the `%check` section (package test suite) |
| `--srpm-only` | Build only the SRPM, not the binary RPMs |
| `--preserve-buildenv on-failure` | Keep the mock chroot on build failure for debugging |
| `--continue-on-error` / `-k` | Continue building remaining components if one fails |
| `--unique-mockroot` | Use distinct mock roots instead of the shared default root |
| `--local-repo <path>` | Add a local RPM repository for build dependencies |
| `--local-repo-with-publish <path>` | Use a local repo and publish built RPMs into it |

## Build Chain

When building multiple components that depend on each other, use `--local-repo-with-publish` to make each component's output available to subsequent builds:

```bash
azldev comp build --local-repo-with-publish ./base/out -p dep-package -p main-package
```

## Debugging Build Failures

1. Re-run with `--preserve-buildenv on-failure` to keep the mock chroot
2. Enter the chroot with `azldev adv mock shell` to inspect the build environment
3. Check build logs in the project's log directory

## Isolate Mock Roots

Use the global `--unique-mockroot` flag to avoid sharing mock roots with other
azldev operations:

```bash
azldev --unique-mockroot comp build -p curl
azldev --unique-mockroot comp render -p curl -p zlib
```

Each mock runner passes `--uniqueext` with an extension of at most 24 ASCII
characters: an abbreviated action, a component label of up to 12 characters,
and six random characters, for example `bld-curl-a2b3c4`.
An extension such as `rndr-abcdefghijkl-a2b3c4` uses the full 24-character limit.
Action codes are `bld` for builds (including advanced mock builds), `rndr` for
rendering, `qry` for queries, and `shl` for mock shells with or without packages.
These codes are never shortened further. Other actions use an abbreviated
command name.

Batch roots label the first component and include the component count when it
fits; otherwise they use the abbreviated first component without the count.
Labels use letters, digits, `_`, and `-`, never spaces.
The extension stays the same for initialization, commands, and cleanup of that
root; separate roots receive separate random suffixes. Without the flag,
mock's existing root naming is unchanged.

The flag also applies to queries, custom source generation, and
`azldev advanced mock` commands. A unique `mock shell` session does not reuse a
previous session's root. To inspect a preserved unique build root, take its
`--uniqueext` value from the verbose command log and run
`mock -r <mock-config> --uniqueext <extension> --shell` directly; a new azldev
invocation generates a new extension.

## Related Resources

- [Components Reference](../reference/config/components.md) — component definition format
- [Overlays Reference](../reference/config/overlays.md) — modifying upstream specs

<!-- TODO: expand with detailed troubleshooting scenarios -->
