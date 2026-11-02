# Go vendor packaging: one-page design

**Status:** Design comparison, not approval to replace the current implementation.
“Current” describes this branch, including pending review fixes.

## Goal and requirements

Bundle application dependencies while preserving package-specific RPM behavior.
`prepare-sources` must produce all build inputs, including the vendor archive;
builds must consume those inputs without downloading Go modules. Configuration
should survive version upgrades without embedding versioned filenames, Source
numbers, import paths, or spec line numbers. Preserve unrelated dependencies,
patches, subpackages, and reviewed licensing. Generate in an isolated environment,
fail on ambiguous layouts, and record/verify archive hashes. Changes to source,
patches, native vendor settings, or toolchain must be accounted for when assessing
reproducibility; a generated checksum alone does not guarantee reproducibility.

## Use cases and current gaps

| Use case | Current behavior and gap |
|---|---|
| Fedora, already vendored: **containerd** | Regenerates a recognized vendor Source after patched `%prep`; preserves native configuration and verifies licensing. Containerd was exercised end-to-end. Recognition requires specific extraction/license macros; changing module layout or replacing a lookaside-backed config is rejected. Dependency overrides/hooks can regenerate patched content; directly patching the old vendor archive is not sufficient because regeneration replaces it. |
| Fedora, not vendored: **Go applications**; requested example **rust-uutils** | Converts supported Go application specs to vendor extraction, offline module builds, and license handling. Complex macros/subpackages require overlays. **rust-uutils is Rust:** it needs Cargo vendoring, Cargo build integration, and Rust license handling; neither this Go implementation nor a Go tarball provider covers it. |
| Downstream-only: **Telegraf** (assuming “telgraf”) | Local specs can use the same Go conversion pipeline when their layout is supported. A downstream-owned spec could instead consume a stable vendor Source directly. This particular package has not been validated; local-source, patch-order, and offline-build coverage remains necessary. |

## Current design and schema

```toml
[components.containerd.go-vendor]
enabled = true
config-file = "go-vendor-tools.toml" # optional; omission reuses upstream settings
```

Additional fields are `module-dir` and `prep-packages`. Custom native settings
live in a separate declaration-relative file, replace rather than merge upstream
settings, and participate in fingerprints.

The pipeline fetches sources, applies overlays, plans a narrowly scoped spec
adaptation, runs private `%prep`, and invokes `go-vendor-tools` for generation and
licensing. It publishes the archive, spec/config, and `sources` digest.
`internal/govendor` separates execution from spec planning, but still requires a
prepared RPM spec. Render/diff also regenerate, so they require downloads.
Generated files cannot also be declared in `source-files`.

## Alternative: automatic tarball through `source-files`

**Proposed syntax and defaults only; this origin is not implemented:**

```toml
[[components.containerd.source-files]]
hash-type = "SHA512"
# filename = "vendor.tar.bz2" # optional override of automatic discovery
# hash = "<expected digest>" # optional output verification
origin.type = "go-vendor"
origin.config-file = "go-vendor-tools.toml"
```

Filename discovery is default behavior, not a separate field: resolve the unique
vendor Source from the effective spec and expand RPM macros. An explicit
`filename` overrides discovery; require it when no unique target can be resolved.
New/downstream specs can reference a stable `vendor.tar.bz2`. Regeneration owns
the discovered vendor output; an explicit filename that replaces another existing
source remains subject to existing collision/replacement safeguards. Native
settings stay in the sidecar; origin options reuse `module-dir`/`prep-packages`.

For this generated origin, `hash` is optional: azldev computes the archive digest
and records it in `sources`. A supplied hash must match; omission records output
identity but does not detect unexpected changes between regenerations. This is an
intentional exception to current `source-files` preparation, which generally
requires hashes unless bootstrapping with `--allow-no-hashes`.

This needs more than a new provider switch: `FetchFiles` currently runs **before**
overlays. Defer vendor generation until patched inputs are prepared, excluding
its own output from fetching/prep to break the dependency cycle. Reuse the narrow
temporary-prep planner for recognized specs; reject unsupported extraction rather
than invent a general shell interpreter. Generate from patched module files,
apply vendor-specific changes through generation hooks, validate licensing and
regular-file outputs, then publish the final digest. Never silently refresh a
configured expected hash. Extend origin validation, schema,
fingerprints, deferred scheduling, and replacement/render handling together.

## Trade-offs and recommendation

| Current combined feature | Tarball-only alternative |
|---|---|
| Low-effort opt-in for supported Go specs. | Smaller spec-mutation surface, reusable source artifact. |
| azldev maintains conversion and licensing rules. | Maintainers own extraction, dependency removal, offline flags, bundled-license installation, and spec updates through overlays/local specs. |
| Handles generation and consumption together. | Tarball existence does **not** prove the RPM consumes it; offline-build and licensing checks remain required. |

**Recommendation:** retain the current bounded converter for now. If artifact-only
generation is prioritized, reuse its backend behind a deferred `go-vendor` origin,
with spec conversion remaining an optional adapter; reject simultaneous ownership
of one archive by both configurations. Validate containerd regeneration, a genuinely
unbundled Go application, and a downstream local package before migration. Treat
Cargo support as separate work. This reduces azldev's responsibilities only by
moving packaging work to maintainers, not by eliminating it.
