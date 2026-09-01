# Changelog

<!-- markdownlint-disable-file MD013 MD024 -->

All notable changes to `azldev` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`--without-lockfile` preview mode.** Add a global `--without-lockfile`
  flag that opts in to a preview of tracking resolved upstream commits in
  generated component configuration instead of per-component lock files. The
  flag defaults to off; without it azldev's behavior, command set, and
  configuration handling are unchanged. The preview surface is not stable and
  may change.
- **Generated upstream commit configuration.** With `--without-lockfile`,
  record snapshot-selected upstream commits as normal layered TOML under
  `base/upstream-commits`. Generated pin files participate in standard
  configuration loading, merging, provenance tracking, and validation, and the
  project's `lock-dir` setting is accepted but ignored.
- **Upstream commit refresh command.** With `--without-lockfile`, `azldev
  component refresh-upstream-commit` resolves and records upstream commits. It
  supports check-only operation, removes obsolete pins for selected
  non-upstream components, and prunes orphaned generated files when all
  components are selected. Configuration is loaded permissively for this
  command so stale generated pins can be removed after a component is deleted
  or converted to another source type.

### Changed

- **Mode-specific component commands.** With `--without-lockfile`, `azldev
  component update`, `component history`, and `component query` are replaced by
  hidden no-op shims, and the lock-file-only `--skip-lock-validation` flag is
  not registered. All of them are unchanged in the default mode.
- **Configuration-based component change detection.** With
  `--without-lockfile`, `azldev component changed` loads each historical
  project configuration independently and compares normalized build inputs
  instead of stored fingerprints. It handles added and deleted components,
  resolves recursive includes and inherited defaults at each ref, compares
  local source and overlay content, and reports rendered `sources` changes
  separately.
- **Synthetic source history.** With `--without-lockfile`, synthetic dist-git
  history is built from configured upstream commit transitions and walks
  first-parent history to the repository root instead of relying on
  lock-recorded import commits.
- **Component workflow guidance.** Agent skills, instruction files, and MCP
  tools describe the workflow of the mode azldev runs in. The generated CLI
  reference continues to document the default mode; the preview mode is
  documented in the user guide.

## [0.3.0] - 2026-08-10

### Added

- **Agent skills and docs integration.** Add agent skill rendering core, add
  component/build/runtime skills, and expose skills through the docs agent.
  ([#287](https://github.com/microsoft/azure-linux-dev-tools/pull/287),
  [#295](https://github.com/microsoft/azure-linux-dev-tools/pull/295),
  [#305](https://github.com/microsoft/azure-linux-dev-tools/pull/305))
- **Archive overlay pipeline support.** Wire archive overlays into prep-sources
  with post-overlay hash tracking.
  ([#276](https://github.com/microsoft/azure-linux-dev-tools/pull/276))
- **Local image test execution.** Run LISA test suites locally via `azldev
  image test`.
  ([#292](https://github.com/microsoft/azure-linux-dev-tools/pull/292))
- **RPM provenance macros.** Add opt-in `%fedora_upstream_*` provenance
  macros. ([#290](https://github.com/microsoft/azure-linux-dev-tools/pull/290))

### Fixed

- **Dry-run defaults extraction.** Extract embedded defaults during dry runs.
  ([#278](https://github.com/microsoft/azure-linux-dev-tools/pull/278))
- **Deterministic archive directory copy.** Ensure archive dir is copied
  deterministically.
  ([#301](https://github.com/microsoft/azure-linux-dev-tools/pull/301))

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
