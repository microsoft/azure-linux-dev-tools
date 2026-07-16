# Changelog

<!-- markdownlint-disable-file MD013 MD024 -->

All notable changes to `azldev` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-07-15

### Added

- **Component updates and reproducibility.** Render and update components from
  the CLI, inspect their history, and identify which components changed.
  Deterministic fingerprints, validated lock files, upstream staleness checks,
  and freshness-based skipping make updates repeatable and efficient.
- **Source preparation and overlays.** Prepare sources through the `mock` batch
  pipeline, download sources independently with `download-sources`, and
  customize specs and archives with section, subpackage, file, metadata, and
  per-file overlays.
- **Dist-git and release handling.** Generate synthetic git history, construct
  dist-git repositories from lock-file history, and choose between automatic,
  `%autorelease`, and static release calculation.
- **Package and repository tools.** Inspect package configuration with `azldev
  package list`, query RPM repositories with `azldev repo query`, and organize
  built RPMs and SRPMs into publish-channel-aware output directories.
- **Image testing.** Configure image capabilities and test suites, including
  pytest support and booting live ISO images.
- **Configuration and command-line experience.** Load user configuration from
  the XDG config directory, validate configuration and lock files, and provide
  actionable error hints, progress reporting, check-only modes, and improved
  component list output.
- **Developer tooling.** Generate reference documentation explicitly, audit
  tests with mutation testing, and use source and batch-processing utilities
  through the MCP server.

### Fixed

- **Source and overlay reliability.** Keep `source-files` and `sources` in sync,
  handle renamed URL sources, expand overlay paths after configuration
  resolution, skip `.git` when applying file overlays, and detect source
  identity drift.
- **Spec and release parsing.** Discover patches throughout a spec, recognize
  more `%autorelease` and section-header forms, preserve balanced conditionals,
  and avoid invalid release bumps.
- **Synthetic history and dist-git.** Support shallow clones, worktrees,
  submodules, merge commits, and uncommitted configuration changes when
  generating history and dist-git repositories.
- **Command-line behavior.** Honor quiet mode consistently, return empty JSON
  arrays instead of `null`, improve image and shallow-clone errors, and make
  error suggestions safe during concurrent operations.
- **Build and runtime behavior.** Allow source downloads as root, prioritize
  disks over ISO media when booting images, reduce license-check noise, and
  improve MCP server reliability.

## [0.1.0] - 2026-03-18

First tagged preview release of `azldev`, the developer CLI for the
[Azure Linux](https://github.com/microsoft/azurelinux) distro.

### Added

- **Project and metadata management.** Scaffold a project with `azldev project
  init` or `project new`, then parse, resolve, and query the TOML metadata
  (`azldev.toml`) that defines Azure Linux. Configuration merges built-in
  defaults with project and user-level (XDG) files, is fully validated, and is
  published as a JSON Schema via `azldev config generate-schema`.
- **Component inspection and locking.** List and inspect components with `azldev
  component list` and `component query`, and import new ones with `component
  add`. Deterministic component fingerprints and per-component lock files keep
  builds reproducible; `component update` refreshes them with `--check-only`,
  `--bump`, freshness-based skipping, a progress bar, and upstream-staleness
  detection. `component changed` and `component diff-sources` report what moved.
- **Source preparation and spec rendering.** `component prepare-sources` and
  `component render` produce build-ready sources and specs through a
  `mock`-based batch pipeline, synthesizing the git history that `rpmautospec`
  needs and constructing dist-git from lock-file history. A rich overlay system
  (spec search/replace, prepend/append lines, remove section or subpackage, file
  and source replacement, per-file overlay files, and inline metadata)
  customizes specs, with explicit release-calculation modes (`autorelease`,
  `static`, and automatic). Source archives are fetched from lookaside caches.
- **Local package and image builds.** Build individual packages with `mock`
  using `component build`, emitting RPMs and SRPMs into structured,
  publish-channel-aware output directories. `azldev image` builds, customizes,
  injects files into, boots, and runs LISA tests against Azure Linux images on a
  local QEMU VM.
- **Package and repository queries.** Inspect binary package configuration with
  `azldev package list` (including `--rpm-file`, debug-package synthesis, and
  separate package/component group columns), and inspect or manage RPM
  repositories with `azldev repo query`, backed by repo resources and repo-set
  templates.
- **Command-line experience.** Shell completions for bash, zsh, fish, and
  PowerShell; actionable hints on errors; global `--quiet`, `--verbose`, and
  `--dry-run` flags with `table`, `json`, `csv`, and `markdown` output formats;
  an embedded MCP server (`azldev advanced mcp`); and auto-generated CLI
  reference documentation.
