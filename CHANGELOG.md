# Changelog

All notable changes to `azldev` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
