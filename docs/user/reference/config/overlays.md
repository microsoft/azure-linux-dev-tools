# Overlays

Overlays are semantic patches that modify RPM spec files and other source files during component processing. They allow you to make targeted changes to upstream specs without maintaining full forks.

Overlays are defined within a component's configuration in your TOML config file and are applied in the order they appear. Each overlay specifies a type and the parameters needed to perform its modification.

> **Note:** Overlays are applied in sequence and modifications are non-atomic. If an overlay fails mid-way, previously applied changes remain. Work on copies if atomicity is required.

## Overlay Types

### Spec Overlays

These overlays modify `.spec` files using the structured spec parser, allowing precise targeting of tags and sections.

| Type | Description | Required Fields |
|------|-------------|-----------------|
| `spec-add-tag` | Adds a tag to the spec; **fails if the tag already exists** | `tag`, `value` |
| `spec-insert-tag` | Inserts a tag after the last tag of the same family (e.g., `Source9999` after the last `Source*`); falls back to after the last tag of any kind, then appends to the section end. **Fails if targeting a sub-package that doesn't exist.** | `tag`, `value` |
| `spec-set-tag` | Sets a tag value; replaces if exists, adds if not | `tag`, `value` |
| `spec-update-tag` | Updates an existing tag; **fails if the tag doesn't exist** | `tag`, `value` |
| `spec-remove-tag` | Removes a tag from the spec; **fails if the tag doesn't exist** | `tag` |
| `spec-prepend-lines` | Prepends lines to the start of a section; **fails if section doesn't exist** | `lines` |
| `spec-append-lines` | Appends lines to the end of a section; **fails if section doesn't exist** | `lines` |
| `spec-search-replace` | Regex-based search and replace on spec content | `regex` |
| `spec-remove-section` | Removes an entire section from the spec; **fails if section doesn't exist** | `section` |
| `spec-remove-subpackage` | Removes every section associated with a sub-package (e.g. its `%package`, `%description`, `%files`, `%post`, `%postun`, ...); **fails if no such sections exist** | `package` |
| `patch-add` | Adds a patch file and registers it in the spec (PatchN tag or %patchlist) | `source` |
| `patch-remove` | Removes patch files and their spec references matching a glob pattern | `file` |

### File Overlays

These overlays modify non-spec source files directly. They cannot be used on `.spec` files. These
overlays are typically only used to modify loose files next to specs when standard patching mechanisms
can't easily be used.

For overlays that use the `file` field and may apply to multiple files, this field is
interpreted as a glob pattern for files to match; the table below details this.
Glob patterns support doublestar (`**`) for recursive matching (e.g., `**/*.conf` matches all `.conf` files in any subdirectory).
For `file-search-replace`, the overlay is considered to have been correctly applied if it
successfully makes a replacement to at least one matching file.

| Type | Description | Required Fields | Interpretation of `file` field |
|------|-------------|-----------------|--------------------------------|
| `file-prepend-lines` | Prepends lines to a file | `file`, `lines` | Glob pattern for files to transform |
| `file-search-replace` | Regex-based search and replace on a file | `file`, `regex` | Glob pattern for files to transform |
| `file-add` | Copies a new file from a source location; **fails if destination already exists** | `file`, `source` | Name of destination file |
| `file-remove` | Removes a file | `file` | Glob pattern for files to remove |
| `file-rename` | Renames a file within the same directory | `file`, `replacement` | Name of file to rename |

## Field Reference

| Field | TOML Key | Description | Used By |
|-------|----------|-------------|---------|
| Type | `type` | **Required.** The overlay type to apply | All overlays |
| Description | `description` | Human-readable explanation documenting the need for the change; helps identify overlays in error messages | All (optional) |
| Tag | `tag` | The spec tag name (e.g., `BuildRequires`, `Requires`, `Version`) | `spec-add-tag`, `spec-insert-tag`, `spec-set-tag`, `spec-update-tag`, `spec-remove-tag` |
| Value | `value` | The tag value to set, or value to match for removal | `spec-add-tag`, `spec-insert-tag`, `spec-set-tag`, `spec-update-tag`, `spec-remove-tag` (optional for matching) |
| Section | `section` | The spec section to target (e.g., `%build`, `%install`, `%files`, `%description`) | `spec-prepend-lines`, `spec-append-lines`, `spec-search-replace` (optional), `spec-remove-section` |
| Package | `package` | The sub-package name for multi-package specs; omit to target the main package | All spec overlays (optional, except `spec-remove-subpackage` which **requires** it) |
| Regex | `regex` | Regular expression pattern to match | `spec-search-replace`, `file-search-replace` |
| Replacement | `replacement` | Literal replacement text; capture group references like `$1` are **not** expanded. Omit or leave empty to delete matched text. | `spec-search-replace`, `file-search-replace`, `file-rename` |
| Lines | `lines` | Array of text lines to insert | `spec-prepend-lines`, `spec-append-lines`, `file-prepend-lines` |
| File | `file` | The name of the non-spec file to modify or add | `file-prepend-lines`, `file-search-replace`, `file-add`, `file-remove`, `file-rename`, `patch-add` (optional), `patch-remove` |
| Source | `source` | Path to source file for `file-add` and `patch-add`; relative paths are relative to the config file that defines the overlay (the overlay file if loaded via [`overlay-files`](#per-file-overlay-format), otherwise the component config) | `file-add`, `patch-add` |
| Metadata | `metadata` | Documentation table describing intent and provenance — see [Overlay Metadata](#overlay-metadata). Not allowed inside an overlay file loaded via `overlay-files` (the file-level `[metadata]` block applies to every overlay in the file). | All (optional) |

> **Note:** For `file-rename`, the `replacement` field is a **filename only** (not a path). The file is renamed within its current directory.

### Component-level fields for overlays

In addition to per-overlay fields, the following fields are set directly on the component:

| Field | TOML Key | Description |
|-------|----------|-------------|
| Overlay files | `overlay-files` | List of glob patterns (relative to the component's config file) matched against the filesystem at load time to locate per-file overlay documents. Patterns support `**` (globstar). Matches are concatenated in declaration order; within a single pattern, matches are applied in filename (lexicographic) order, with full path as a tie-breaker for duplicate filenames. Each pattern must match at least one file. Duplicate matches across patterns are de-duplicated. See [Per-file overlay format](#per-file-overlay-format). |

## Overlay Metadata

Overlays can carry an optional `metadata` table that documents *why* the overlay exists and *when* it can be removed. Metadata is reviewed by humans and surfaced in tooling; it does **not** affect how the overlay is applied and is excluded from component fingerprints (so editing metadata never invalidates build caches).

### `metadata` fields

| Field | TOML Key | Description |
|-------|----------|-------------|
| Category | `category` | **Required.** Classification of the overlay's intent. See the table below. |
| Commits | `commits` | List of upstream commit URLs (Fedora dist-git or upstream project) that this overlay backports or implements. Each entry must be an absolute http(s) URL. |
| Bugs | `bugs` | List of bug-tracker references. Each entry is a table with a required `url`. See [Bug references](#bug-references). |
| Upstreamable | `upstreamable` | Boolean indicating whether this change can be upstreamed: `true` or `false`. Omit the field when upstreamability has not yet been assessed. |

### Categories

| Category | When to use |
|----------|-------------|
| `backport-dist-git` | Fix backported from (or being upstreamed to) a dist-git or upstream project. Self-resolves when AZL bumps past it. Requires at least one entry in `commits`. |
| `azl-pruning` | Removing content from a component for AZL: dependencies that are not shipped, unneeded features, subpackages, or files. |
| `azl-compatibility` | Making a component work in the AZL build/runtime environment: toolchain and mock adjustments, and similar compatibility fixes that are not themselves backports. |
| `azl-dep-missing-workaround` | Working around a runtime or build dependency that has not yet been imported into AZL (or is unavailable on a given target). Drop the overlay once the dependency lands. |
| `azl-branding-policy` | Fedora→Azure Linux name/path changes; RHEL/enterprise convention alignment. |
| `azl-disable-flaky-tests` | Skipping tests that fail intermittently or due to environmental flakiness rather than a real problem with the component. |
| `azl-disable-unsupported-tests` | Skipping tests that cannot meaningfully run in AZL's build/runtime environment (e.g. tests that require network access, root, or hardware that is unavailable in mock). |
| `azl-security-compliance` | FIPS or crypto-policy changes. |
| `azl-release-management` | Release-tag and changelog mechanics. |
| `azl-platform-adaptation` | Architecture-specific adjustments. |

### Bug references

The `bugs` field is a list of references to issue-tracker entries. Each entry is a table with a single required field:

| Field | TOML Key | Description |
|-------|----------|-------------|
| URL | `url` | **Required.** HTTP(S) link to the bug entry. |

Example:

```toml
[[components.mypackage.overlays.metadata.bugs]]
url = "https://bugzilla.redhat.com/show_bug.cgi?id=2234567"

[[components.mypackage.overlays.metadata.bugs]]
url = "https://github.com/example/repo/issues/42"
```

The inline-table form is more compact for short lists:

```toml
bugs = [
  { url = "https://bugzilla.redhat.com/show_bug.cgi?id=2234567" },
  { url = "https://github.com/example/repo/issues/42" },
]
```

### Inline metadata example

TOML inline tables (`metadata = { ... }`) must fit on a single line. When the metadata has more than one or two fields, use a sub-table (`[components.<name>.overlays.metadata]`) so each field gets its own line:

```toml
[[components.xclock.overlays]]
type = "spec-search-replace"
description = "Pass --force to autoreconf"
regex = "autoreconf -i"
replacement = "autoreconf -fi"

  [components.xclock.overlays.metadata]
  category = "backport-dist-git"
  commits = ["https://src.fedoraproject.org/rpms/xclock/c/1e407488"]
```

For short metadata, the single-line inline form is also valid:

```toml
[[components.xclock.overlays]]
type = "spec-set-tag"
tag = "Vendor"
value = "Microsoft"
metadata = { category = "azl-branding-policy" }
```

## Per-file overlay format

When a single logical change (a CVE backport, a feature disablement, a Fedora cherry-pick…) needs **several overlays** that all share the same provenance, declaring them inline in the component config gets noisy and makes the boundary between unrelated changes hard to see. Use the per-file format instead.

### Layout

Set `overlay-files` on the component to one or more globs (relative to the component config) and drop one overlay file per logical change into a directory of your choosing. The conventional layout uses a sibling `overlays/` directory and a `*.overlay.toml` filename suffix, but neither is required — `overlay-files` is just a glob, so any layout you can describe with `**`/`*` patterns works.

```
base/comps/mypackage/
├── mypackage.comp.toml
└── overlays/
    ├── 0001-cve-2024-1234.overlay.toml
    ├── 0002-disable-broken-tests.overlay.toml
    └── 0003-azl-branding.overlay.toml
```

```toml
# mypackage.comp.toml
[components.mypackage]
overlay-files = ["overlays/*.overlay.toml"]
```

Files are loaded in **filename (lexicographic) order** within each glob, using the full path as a tie-breaker when multiple matches have the same filename. Globs are concatenated in declaration order, so prefix each file with a numeric ordinal (`0001-`, `0002-`, …) to make the apply order obvious and stable. Files that don't match any of your globs are ignored, so you can keep `README.md` or other notes alongside without naming them out explicitly. Each declared glob must match at least one file; an empty match is treated as a misconfiguration and surfaced as an error.

Overlays loaded via `overlay-files` are **appended after** any inline overlays declared directly on the component.

### File structure

Each overlay file represents one logical change. It has:

* exactly one top-level `[metadata]` table (uses the same fields documented in [Overlay Metadata](#overlay-metadata)); and
* one or more `[[overlays]]` entries, applied in declaration order.

Per-overlay `metadata` is **not allowed** inside an overlay file — the file-level `[metadata]` is the single source of truth and is stamped onto every overlay in the file. Relative `source` paths are resolved against the directory of the overlay file (not the component config).

```toml
# overlays/0001-cve-2024-1234.overlay.toml
[metadata]
category = "backport-dist-git"
commits = ["https://src.fedoraproject.org/rpms/mypackage/c/abc123def456"]
bugs = [{ url = "https://bugzilla.redhat.com/show_bug.cgi?id=12345" }]

[[overlays]]
type = "patch-add"
source = "patches/CVE-2024-1234.patch"

[[overlays]]
type = "spec-append-lines"
section = "%changelog"
lines = ["- Fix CVE-2024-1234"]
```

## Examples

### Adding a Build Dependency

```toml
[[components.mypackage.overlays]]
type = "spec-add-tag"
description = "Add missing build dependency"
tag = "BuildRequires"
value = "some-devel-package"
```

### Inserting a Tag by Family

Use `spec-insert-tag` to place a tag after the last existing tag of the same family rather than appending it to the end of the section. The "family" is determined by stripping trailing digits from the tag name (case-insensitive), so `Source0`, `Source1`, and `Source` all belong to the `Source` family.

This is useful when tag placement matters — for example, ensuring a new `Source` tag doesn't end up after macros like `%fontpkg` or inside `%if` conditionals:

```toml
[[components.mypackage.overlays]]
type = "spec-insert-tag"
description = "Add macros file as a source"
tag = "Source9999"
value = "macros.azl.macros"
```

If no tags from the same family exist, the tag is placed after the last tag of any kind. If there are no tags at all, it falls back to `spec-add-tag` behavior (appending to the section end).

### Setting a Version

Use `spec-set-tag` when you want to set a value regardless of whether the tag exists:

```toml
[[components.mypackage.overlays]]
type = "spec-set-tag"
tag = "Version"
value = "2.0.0"
```

### Removing a Dependency

Remove a specific tag by matching both the tag name and value:

```toml
[[components.mypackage.overlays]]
type = "spec-remove-tag"
description = "Remove problematic dependency"
tag = "BuildRequires"
value = "unwanted-package"
```

### Appending Lines to a Section

```toml
[[components.mypackage.overlays]]
type = "spec-append-lines"
section = "%install"
lines = [
    "mkdir -p %{buildroot}%{_datadir}/mypackage",
    "install -m 644 extra.conf %{buildroot}%{_datadir}/mypackage/"
]
```

### Prepending Lines to a Section

```toml
[[components.mypackage.overlays]]
type = "spec-prepend-lines"
section = "%build"
lines = ["export CFLAGS=\"$CFLAGS -DEXTRA_FLAG\""]
```

### Search and Replace in Spec

> **Tip:** The regex must match at least once or an error is raised. This prevents silent no-ops from typos or upstream changes.

```toml
[[components.mypackage.overlays]]
type = "spec-search-replace"
description = "Remove unwanted configure flag"
section = "%build"
regex = "--enable-deprecated-feature\\s*"
replacement = ""
```

### Targeting a Sub-Package

For multi-package specs, use the `package` field to target a specific sub-package:

```toml
[[components.mypackage.overlays]]
type = "spec-append-lines"
section = "%files"
package = "devel"
lines = ["%{_includedir}/mypackage/*.h"]
```

```toml
[[components.mypackage.overlays]]
type = "spec-set-tag"
package = "libs"
tag = "Summary"
value = "Shared libraries for mypackage"
```

### Prepending Lines to a Non-Spec File

```toml
[[components.mypackage.overlays]]
type = "file-prepend-lines"
file = "Makefile"
lines = ["# Modified by azldev overlay", "EXTRA_FLAGS := -O2"]
```

### Search and Replace in a File

```toml
[[components.mypackage.overlays]]
type = "file-search-replace"
file = "configure.ac"
regex = "AC_INIT\\(\\[mypackage\\],\\s*\\[\\d+\\.\\d+\\]"
replacement = "AC_INIT([mypackage], [2.0]"
description = "Update version in configure.ac"
```

### Adding a New File

The `source` path is relative to the config file that defines the overlay:

```toml
[[components.mypackage.overlays]]
type = "file-add"
file = "extra-config.conf"
source = "files/mypackage/extra-config.conf"
description = "Add custom configuration file"
```

### Removing a File

```toml
[[components.mypackage.overlays]]
type = "file-remove"
file = "undesired.conf"
```

### Renaming a File

```toml
[[components.mypackage.overlays]]
type = "file-rename"
file = "oldname.conf"
replacement = "newname.conf"
```

### Adding a Patch

The `patch-add` overlay copies a patch file into the component's sources and registers it
in the spec. If the spec has a `%patchlist` section, the filename is appended there; otherwise,
a `PatchN` tag is added with the next available number.

```toml
[[components.mypackage.overlays]]
type = "patch-add"
source = "patches/fix-build-flags.patch"
description = "Fix compiler flags for our toolchain"
```

By default, the destination filename is the basename of `source`. Use `file` to override:

```toml
[[components.mypackage.overlays]]
type = "patch-add"
source = "patches/0001-upstream-fix.patch"
file = "fix-upstream-bug.patch"
description = "Rename upstream patch for clarity"
```

### Removing a Patch

The `patch-remove` overlay removes patch references from the spec (`PatchN` tags and/or
`%patchlist` entries) and deletes the matching patch files from the component's sources.
The `file` field is a glob pattern matched against patch filenames.

```toml
[[components.mypackage.overlays]]
type = "patch-remove"
file = "fix-old-bug.patch"
description = "Remove patch that is no longer needed"
```

Glob patterns can remove multiple patches at once:

```toml
[[components.mypackage.overlays]]
type = "patch-remove"
file = "CVE-*.patch"
description = "Remove CVE patches that are now upstream"
```

> **Note:** `patch-remove` does not remove `%patchN` application lines from `%prep`.
> If the spec uses individual `%patch` directives rather than `%autosetup`, you may need
> a `spec-search-replace` overlay to remove those lines as well. Similarly, `%autopatch`
> has `-m` and `-M` options referencing specific patch numbers and will need targeted
> adjustments.

> **Limitation:** `patch-add` auto-assigns PatchN numbers by scanning existing numeric
> `PatchN` tags. Macro-based tag numbering (e.g., `Patch%{n}`) is not expanded and may
> conflict with auto-assigned numbers.

### Removing a Section

The `spec-remove-section` overlay removes an entire section from the spec, including its
header and all body lines. The section is identified by `section` name and optionally
scoped to a specific sub-package with `package`.

```toml
[[components.mypackage.overlays]]
type = "spec-remove-section"
section = "%generate_buildrequires"
description = "Remove dynamic build requirements generation"
```

To remove a section from a specific sub-package:

```toml
[[components.mypackage.overlays]]
type = "spec-remove-section"
section = "%files"
package = "devel"
description = "Remove devel sub-package files section"
```

> **Conditionals (`%if`/`%endif`):** The same conditional handling described below for
> `spec-remove-subpackage` applies here as well — boundary conditionals are preserved,
> and an error is returned if a conditional block is interleaved with section content
> in a way that cannot be cleanly separated.

### Removing an Entire Sub-package

The `spec-remove-subpackage` overlay removes **every** section associated with a given
sub-package — its `%package` preamble as well as any per-section directives that target
it (e.g. `%description`, `%files`, `%post`, `%postun`, `%pre`, `%trigger*`, etc.).
Only the `package` field is needed; you do **not** need to enumerate each section.

This is the preferred way to drop an unwanted sub-package: it avoids having to author
multiple `spec-remove-section` overlays (and remember to keep them in sync if upstream
later adds new sub-package scriptlets).

```toml
[[components.mypackage.overlays]]
type = "spec-remove-subpackage"
package = "devel"
description = "Drop the devel sub-package; not shipped in Azure Linux"
```

The overlay fails if the spec contains no sections matching the indicated sub-package.
Specifying a `section` field on this overlay is rejected at config-load time, since
the overlay always removes every section associated with the sub-package.

> **Note:** `spec-remove-subpackage` only edits the spec. If other parts of the project
> reference the removed sub-package (for example, dependency lists in other components),
> those references must be cleaned up separately.

> **Note:** RPM permits two forms for declaring sub-package sections — a suffix form
> (e.g. `%package devel`, which declares a sub-package named `<base>-devel`) and an
> absolute form (e.g. `%package -n my-other-pkg`). The `package` value here must match
> whichever form the spec uses on the section headers: `package = "devel"` matches
> sections written as `%files devel`, while `package = "my-other-pkg"` matches sections
> written as `%files -n my-other-pkg`. Specs that mix both forms for the same sub-package
> (uncommon but legal) require a separate overlay per form.

> **Conditionals (`%if`/`%endif`):** The overlay only removes section content — it does
> not remove `%if`/`%endif` lines that sit at section boundaries. Conditional directives
> that are entirely within a section (e.g. `%ifarch` … `%endif` guarding a `Requires`
> tag) are removed along with the section. Conditional directives that straddle a
> section boundary are left in place so the spec remains valid. For example, if a
> sub-package is wrapped in `%if 0%{?with_devel}` … `%endif`, removing the sub-package
> leaves an empty `%if` … `%endif` block behind (which is harmless). If a conditional
> block is interleaved with section content in a way that cannot be cleanly separated,
> an error is returned; use a `spec-search-replace` overlay to adjust the conditionals
> before removing the sub-package.

## Known Limitations

### Section-scoped operations and straddling conditionals

Spec overlays that target a specific section and package (e.g., `spec-remove-tag` with `package = "headless"`) rely on the structural tree parser to identify which lines belong to that section. In rare cases, a section header may live inside a `%if` wrapper while its content continues past the `%endif`:

```spec
%if 0%{!?scl:1}
%package headless
Requires: binutils
%endif
# ← content below is still part of %package headless in RPM's view,
#   but the tree parser cannot associate it with the headless section
#   because the section header is structurally inside the wrapper.
Recommends: default-yama-scope
```

In this pattern, section-scoped overlays (`spec-remove-tag`, `spec-add-tag`, `spec-update-tag`, `spec-insert-tag`) cannot find or modify tags that fall outside the `%endif`. Use `spec-search-replace` with a precise anchored regex instead:

```toml
# Instead of:
#   type = "spec-remove-tag"
#   package = "headless"
#   tag = "Recommends"
#   value = "default-yama-scope"

# Use:
type = "spec-search-replace"
regex = "^Recommends: default-yama-scope$"
replacement = "# Recommends: default-yama-scope (disabled)"
```

### Macro-generated sections

Specs that use macros like `%ghc_lib_subpackage`, `%pyproject_extras_subpkg`, or `%fontpkg` generate sections at build time that are invisible to the static parser. Section-scoped overlays cannot target these generated sections. Use `spec-search-replace` for modifications that need to reach macro-generated content.

## Validation

Overlay configurations are validated when the config file is loaded. Validation checks:

- Required fields are present for each overlay type
- Regex patterns compile successfully
- Error messages include the `description` field (if provided) to help identify which overlay failed

> **Tip:** Always provide a `description` for overlays to make debugging easier when validation or application fails.

## Related Resources

- [Components](components.md) — overlays are defined within component configuration
- [Config File Structure](config-file.md) — top-level config file layout
- [JSON Schema](../../../../schemas/azldev.schema.json) — use with editors that support JSON Schema for TOML to get validation and auto-completion
