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

## Related Resources

- [Components Reference](../reference/config/components.md) — component definition format
- [Overlays Reference](../reference/config/overlays.md) — modifying upstream specs

<!-- TODO: expand with detailed troubleshooting scenarios -->
