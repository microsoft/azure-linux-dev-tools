# Changelog

<!-- markdownlint-disable-file MD013 MD024 -->

All notable changes to `azldev` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-07-15

### Added

- Generate Synthetic Git Repo (#17)
- Add package publish channel annotations to TOML schema (#38)
- Add `spec-remove-section` (#40)
- Add GetCurrentCommit function and WithMetadataOnly option (#44)
- Add `azldev package list` command (#53)
- Add ResolveSourceIdentity() to the source provider interface (#45)
- Add WithSkipLookaside preparer option (#61)
- Static release bumped during dist-git generation (#54)
- Add mock batch processor and source utilities (#80)
- Place built RPMs/SRPMs into structured output dirs and validate channel names (#68)
- Add azldev component render command (#81)
- Added download-sources command (#67)
- Include rendered spec location in component list output (#85)
- Add fingerprint ignore tags to some config fields (#46)
- Render files into subdirectories (#88)
- Add lock file foundations (#90)
- Add update CLI command (#92)
- Add user hints to help fix errors (#95)
- Rename publish channel field to publish-channel and PublishChannel (#96)
- Add image capabilities, test suites, and publish config (#101)
- Add advanced command hint to help output (#102)
- Require components to opt out of auto release calculation (#100)
- Add --synthesize-debug-packages to package list (#108)
- Add ability to boot livecd-style ISO (#103)
- Add lock file validation and orphan detection primitives
- Skip file filter for specs with unexpandable macros
- Add component-level publish channel configuration (#107)
- Add deterministic component fingerprints (#47)
- Add structural validation and feature-gated lock checks (#111)
- Add pytest test suite support (#122)
- Compute and store input fingerprints during component update (#123)
- Populate resolved lock data onto components during resolution (#129)
- Add --bump flag for manual rebuild counter (#130)
- Extend lock files to cover local components (#133)
- Construct dist-git with lock file history (#121)
- Add spec-remove-subpackage overlay (#132)
- Add ReadAllAtCommit for batch lock file reading (#150)
- Add --rpm-file flag to package list command (#136)
- Load user-level config from XDG config home (#148)
- Add explicit release calculation modes (autorelease, static) (#155)
- Add facility for upstream commit staleness detection (#154)
- Add freshness-based skip optimization to component update (#158)
- Add explicit document generation mage target (#153)
- Add component changed command (#149)
- Switch update cmd to use a progress bar (#159)
- Add root user check escape hatch with env var (#163)
- Add check-only option to update (#168)
- Add check-only option to render (#170)
- Add dirty commit for uncommitted config changes in dist-git (#166)
- Enable full validation for configs and locks (#177)
- Allow file replacement in `source-files` (#171)
- Validate component group membership (#191)
- Split list 'group' column into 'packageGroups' and 'componentGroups' (#165)
- Add RPM repo resources and repo-set templates (#202)
- Add flag to source prep to skip downloading source tars (#207)
- Add 'azldev repo query' for inspecting and managing RPM repositories (#213)
- Add deterministic archive extract and repack utility (#223)
- Add gremlins mutation testing via mage and MCP (#244)
- Add inline [metadata] block to component overlays
- Add per-file overlay format via overlay-files
- Add history command to show changes made to components (#212)
- Handle empty sections explicitly (#208)
- In verbose mode have mock print all output (#231)
- Add custom origin type for mock-based source generation (#261)
- Add optional metadata to component groups
- Generative source dependencies (#271)
- Add archive overlay engine and config model (#275)

### Fixed

- Make patch discovery work across whole spec (#18)
- Replace raw `%s` with `%#q` (#56)
- Skip .git directory when applying file-targeting overlays (#64)
- Remove exact azldev version from spec headers (#66)
- Improve error when no image specified (#70)
- Make `sources-files` update `sources` (#69)
- Allow download-sources to run as root (#89)
- Remove truncated (e.g.) example from hash-type schema description (#87)
- Suppress wait messages and progress bars in quiet mode (#41)
- Handle additional %autorelease macro forms in Release tag detection (#91)
- Bump testcontainers-go to v0.42.0 and fix moby/moby import breaking change (#115)
- Parse URL sources that use the rename '#' tag (#118)
- Improve shallow clone guidance for synthetic history (#105)
- Support git worktrees in synthetic history generation (#125)
- URL-escape placeholder values in BuildLookasideURL and dist-git URL construction (#74)
- Swap boot order so disk has priority over ISO (#135)
- Disallow release bumping on decimal releases (#138)
- Fix render staging, import-commit seeding, and commit parsing (#139)
- Scrub submodules from distgits (#137)
- Restore git worktree support in synthetic history generation (#145)
- Consume merge-commits in dist-git (#146)
- Count unnumbered Patch: tags in GetHighestPatchTagNumber (#160)
- Don't return null json when getting empty slices (#167)
- Error on source vs. identity drift (#169)
- Allow autobump for Non Conditional %{dist} Releases (#179)
- Handle -l flag in spec section header package name extraction (#180)
- Handle -- trigger terminator and -P flag in spec section header parsing (#189)
- Use first-parent for snapshot-time commit resolution (#192)
- Balance conditional nesting when removing spec sections (#190)
- Make AddFixSuggestion thread-safe (#183)
- Disable validation checks when passing --permissive-config (#216)
- Filter noise from license checks (#230)
- Surface close errors and drop unused writable handle in completions (#233)
- Expand overlay files after config resolution
- Repair main after component-groups metadata + overlay-metadata API drift
- Fix issues with the MCP server mode (#272)

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
