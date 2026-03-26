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
| Package | `package` | The sub-package name for multi-package specs; omit to target the main package | All spec overlays (optional) |
| Regex | `regex` | Regular expression pattern to match | `spec-search-replace`, `file-search-replace` |
| Replacement | `replacement` | Literal replacement text; capture group references like `$1` are **not** expanded. Omit or leave empty to delete matched text. | `spec-search-replace`, `file-search-replace`, `file-rename` |
| Lines | `lines` | Array of text lines to insert | `spec-prepend-lines`, `spec-append-lines`, `file-prepend-lines` |
| File | `file` | The name of the non-spec file to modify or add | `file-prepend-lines`, `file-search-replace`, `file-add`, `file-remove`, `file-rename`, `patch-add` (optional), `patch-remove` |
| Source | `source` | Path to source file for `file-add` and `patch-add`; relative paths are relative to the config file | `file-add`, `patch-add` |

> **Note:** For `file-rename`, the `replacement` field is a **filename only** (not a path). The file is renamed within its current directory.

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
