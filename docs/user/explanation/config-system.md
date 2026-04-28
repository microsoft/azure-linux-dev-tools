# Configuration System

The azldev configuration system uses TOML files organized in a hierarchical include structure. This page explains how config files are discovered, loaded, merged, and how configuration inheritance works.

For field-level reference documentation, see the [Reference](../reference/config/config-file.md) pages.

## Config File Discovery

azldev searches for its root config file (`azldev.toml`) by walking up the directory tree from the current working directory. The first `azldev.toml` found becomes the project root, and all relative paths in the configuration are resolved from that file's location.

## Load Order

Configuration is loaded in three phases, in this order:

1. **Embedded defaults** — azldev ships with built-in default values (e.g., tool container tags). These are loaded first and provide baseline configuration.
2. **Project config** — the `azldev.toml` file and all of its transitive includes.
3. **Extra config files** — any additional config files passed via `--config-file` CLI flags.

Later phases can override values from earlier phases according to the [merge rules](#merge-rules) described below.

## Include Resolution

The `includes` field specifies glob patterns for additional config files to load:

```toml
includes = ["distro/distro.toml", "base/project.toml"]
```

Key behaviors:

- **Relative to the including file.** Paths are resolved relative to the directory containing the file that declares the include — not relative to the project root. For example, if `base/project.toml` declares `includes = ["comps/components.toml"]`, azldev looks for `base/comps/components.toml`.
- **Recursive.** Included files can themselves declare further includes, which are resolved transitively.
- **Depth-first.** The including file is processed first, then each of its includes in depth-first order. This means included files' values are merged on top of the parent's values.
- **Glob patterns** that match no files are silently ignored. However, literal filenames (without wildcard characters `*`, `?`, or `[`) that do not exist produce an error.
- **TOML files only.** All included files must be valid TOML and conform to the [config file schema](../reference/config/config-file.md).

### Example Hierarchy

Here is a typical include hierarchy, showing the actual load order (numbered):

```
1. azldev.toml
   includes = ["distro/distro.toml", "base/project.toml"]
   │
   ├── 2. distro/distro.toml
   │   includes = ["*.distro.toml"]
   │   │
   │   ├── 3. distro/azurelinux.distro.toml
   │   └── 4. distro/fedora.distro.toml
   │
   └── 5. base/project.toml
       includes = ["comps/components.toml", "images/images.toml"]
       │
       ├── 6. base/comps/components.toml
       │   includes = ["**/*.comp.toml", "components-full.toml"]
       │   │
       │   ├── 7.  base/comps/bash/bash.comp.toml
       │   ├── 8.  base/comps/kernel/kernel.comp.toml
       │   ├── ... (other *.comp.toml files)
       │   └── N.  base/comps/components-full.toml
       │
       └── N+1. base/images/images.toml
```

Every file in this tree is a valid azldev config file. The include system stitches them into a single resolved configuration.

## Merge Rules

When multiple files define the same top-level section, azldev applies section-specific merge rules:

| Section | Merge behavior | Duplicates across files |
|---------|---------------|------------------------|
| `components` | Additive (union of all component definitions) | **Error** — each component name must be unique across all files |
| `component-groups` | Additive (union of all group definitions) | **Error** — each group name must be unique across all files |
| `images` | Additive (union of all image definitions) | **Error** — each image name must be unique across all files |
| `distros` | Additive with field-level merge | **Allowed** — fields from later files override matching fields in earlier files |
| `project` | Field-level override | N/A (single struct, not a map) |
| `tools` | Field-level override | N/A (single struct, not a map) |

### Components, Component Groups, and Images

These are strict-union maps: each name may appear in exactly one config file across the entire include tree. If two files both define `[components.curl]`, azldev reports an error. This prevents accidental shadowing and makes it clear where each definition lives.

### Distros

Distro definitions are merged additively. If the same distro name (e.g., `fedora`) appears in multiple files, later files' non-empty fields override earlier ones. This allows splitting a distro definition across files — for example, defining the distro's general properties in one file and version-specific details in another.

> **Note:** Slice fields (like `repos`) are **replaced**, not appended. If a later file defines `repos`, the earlier file's `repos` are discarded entirely.

### Project and Tools

These are simple structs (not maps). Later files' non-empty fields override earlier files' fields. Empty/zero values in later files do not clear values set by earlier files.

## Configuration Inheritance

Component configuration supports a layered inheritance model. When azldev resolves the effective configuration for a component, it assembles it from multiple sources in this order (later layers override earlier ones):

1. **Distro version defaults** — the `default-component-config` defined in the distro version (e.g., `[distros.azurelinux.versions.'4.0'.default-component-config]`)
2. **Project-level defaults** — the `default-component-config` defined at the project root
3. **Component group defaults** — the `default-component-config` from any component groups the component belongs to (applied in alphabetical order by group name)
4. **Component-specific config** — the component's own explicit configuration

This inheritance is applied lazily at resolution time, not at config load time.

### Example

Consider this configuration:

```toml
# In distro config
[distros.azurelinux.versions.'4.0'.default-component-config]
spec = { type = "upstream", upstream-distro = { name = "fedora", version = "43" } }

# In component config — only overrides what's needed
[components.bash]
spec = { type = "upstream", upstream-distro = { name = "fedora", version = "rawhide" } }
```

Here, `bash` inherits the distro-level default spec source (Fedora 43) but overrides it to use `rawhide` instead. All other components that don't specify their own `spec` will use the default Fedora 43 source.

For array fields (like `overlays`), the component's array is **appended** to the inherited array rather than replacing it. This allows distro-level or group-level overlays to apply to all components while individual components add their own.

## Schema Validation

azldev validates each config file against its schema as it is loaded. Unknown fields are rejected by default, catching typos and misconfigurations early. The authoritative schema is available at [`azldev.schema.json`](../../../schemas/azldev.schema.json) and can also be generated with:

```sh
azldev config generate-schema
```

Use `azldev config dump -q -O json` to inspect the fully resolved configuration after all includes are merged and inheritance is applied.

## Related Resources

- [Config File Reference](../reference/config/config-file.md) — root config file structure and field reference
- [JSON Schema](../../../schemas/azldev.schema.json) — machine-readable schema for editor integration
