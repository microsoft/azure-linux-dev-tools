# Components

Components are the primary unit of packaging in azldev. Each component corresponds to one or more RPM packages and is defined under `[components.<name>]` in the TOML configuration.

A component definition tells azldev where to find the spec file, how to customize it with overlays, how to configure the build, and what additional source files to download.

## Component Config

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Spec source | `spec` | [SpecSource](#spec-source) | No | Where to find the spec file for this component. Inherited from distro defaults if not specified. |
| Release config | `release` | [ReleaseConfig](#release-configuration) | No | Controls how the Release tag is managed during rendering |
| Overlays | `overlays` | array of [Overlay](overlays.md) | No | Modifications to apply to the spec and/or source files |
| Build config | `build` | [BuildConfig](#build-configuration) | No | Build-time options (macros, conditionals, check config) |
| Render config | `render` | [RenderConfig](#render-configuration) | No | Options controlling spec rendering behavior |
| Source files | `source-files` | array of [SourceFileReference](#source-file-references) | No | Additional source files to download for this component |
| Default package config | `default-package-config` | [PackageConfig](package-groups.md#package-config) | No | Default configuration applied to all binary packages produced by this component; overrides project defaults and package-group defaults |
| Package overrides | `packages` | map of string → [PackageConfig](package-groups.md#package-config) | No | Exact per-package configuration overrides; highest priority in the resolution order |

### Bare Components

The simplest component definition is a bare entry with no fields. This inherits all configuration from the distro defaults:

```toml
[components.curl]
```

With a default distro config pointing to Fedora 43, this is equivalent to explicitly writing:

```toml
[components.curl]
spec = { type = "upstream", upstream-distro = { name = "fedora", version = "43" } }
```

Bare entries are the most common component definition — the majority of components in a typical project need no per-component customization.

### File Organization

- **Inline** definitions in a shared file (e.g., `components.toml`) are preferred for bare components with no customization.
- **Dedicated** files (e.g., `bash/bash.comp.toml`) are preferred when a component has overlays, build config, or other customization.

The `includes = ["**/*.comp.toml"]` pattern in `components.toml` automatically picks up all dedicated component files.

> **Note:** Each component name must be unique across all config files. Defining the same component in two files produces an error.

## Spec Source

The `spec` field identifies where azldev should find the RPM spec file for a component.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Source type | `type` | string | **Yes** | Spec source type: `"upstream"`, `"local"`, or `""` (empty) |
| Path | `path` | string | No | Path to the spec file (for `local` type). Relative to the config file. |
| Upstream distro | `upstream-distro` | [DistroReference](distros.md#distro-references) | No | Which distro to pull the spec from (for `upstream` type) |
| Upstream name | `upstream-name` | string | No | Package name in the upstream distro, if different from the component name |
| Upstream commit | `upstream-commit` | string | No | Git commit hash (7–40 hex chars) to pin the upstream spec to. Takes priority over the distro snapshot. |

### Upstream Specs (Default)

Most components use upstream specs pulled from a distro's dist-git repository. If no `spec` is defined, the component inherits the upstream distro from the `default-component-config` in the distro version config:

```toml
# Uses the default upstream distro (inherited from distro config)
[components.curl]

# Explicitly specifies the upstream distro and version
[components.bash]
spec = { type = "upstream", upstream-distro = { name = "fedora", version = "rawhide" } }
```

When the upstream package has a different name than the component, use `upstream-name`:

```toml
[components.azurelinux-rpm-config]
spec = { type = "upstream", upstream-name = "redhat-rpm-config" }
```

### Commit-Pinned Specs

When you need to pin to a specific git commit — for example, to pick up a fix that hasn't been included in a tagged release yet — use `upstream-commit`:

```toml
[components.bash]
spec = { type = "upstream", upstream-commit = "a1b2c3d4e5f6789" }
```

The value must be a hex string between 7 and 40 characters (a short or full git commit hash). When `upstream-commit` is set, it takes priority over the distro snapshot date — azldev checks out the exact commit instead of finding the latest commit before a snapshot timestamp.

> **Note:** Commit-pinning is intended for temporary use. Once the desired change lands in a tagged upstream release, switch back to version-based pinning or the default snapshot to keep the component aligned with the upstream distro.

### Local Specs

For components that originate from your project (not imported from upstream), use a local spec:

```toml
[components.azurelinux-release]
spec = { type = "local", path = "azurelinux-release.spec" }
```

The `path` is relative to the config file that defines the component. Local spec files and any associated source files should be placed alongside the component's `.comp.toml` file.

## Release Configuration

The `[components.<name>.release]` section controls how azldev manages the Release tag during rendering.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Calculation | `calculation` | string | No | `"auto"` (default) = auto-bump; `"manual"` = skip all automatic Release manipulation |

Most components use auto mode (the default) and need no release configuration. Set `calculation = "manual"` for components that manage their own release numbering, such as kernel:

```toml
[components.kernel.release]
calculation = "manual"
```

## Render Configuration

The `[components.<name>.render]` section controls rendering behavior for a component.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Skip file filter | `skip-file-filter` | boolean | No | Disable post-render file filtering (defaults to `false`) |

### Skip File Filter

During rendering, azldev uses `spectool` to determine which files are referenced by `Source` and `Patch` tags in the spec, then removes unreferenced files from the rendered output. Some specs use dynamic macros (e.g., `%{fontpkgname1}`) that `spectool` cannot expand, causing it to report incorrect filenames. This results in referenced files being incorrectly removed.

Set `skip-file-filter = true` to preserve all files from the dist-git checkout:

```toml
[components.dejavu-fonts.render]
skip-file-filter = true
```

> **Note:** This should only be used for specs with macros that `spectool` cannot resolve. For most components, the default filtering behavior is correct and keeps the rendered output clean.

## Build Configuration

The `[components.<name>.build]` section controls build-time options for a component.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| With options | `with` | string array | No | Build conditionals to enable (`--with <option>` passed to rpmbuild) |
| Without options | `without` | string array | No | Build conditionals to disable (`--without <option>` passed to rpmbuild) |
| Macro definitions | `defines` | map of string to string | No | RPM macro definitions (`--define '<name> <value>'` passed to rpmbuild) |
| Undefined macros | `undefines` | string array | No | RPM macro names to undefine (`--undefine '<name>'` passed to rpmbuild) |
| Check config | `check` | [CheckConfig](#check-configuration) | No | Configuration for the `%check` section of the spec |
| Failure config | `failure` | [FailureConfig](#failure-configuration) | No | Configuration and policy regarding build failures |
| Build hints | `hints` | [BuildHints](#build-hints) | No | Non-essential hints for how or when to build the component |

### With / Without Options

These correspond to rpmbuild's `--with` and `--without` flags, which toggle `%bcond` conditionals in spec files:

```toml
# Enable the "as_wget" build conditional
[components.wget2.build]
with = ["as_wget"]

# Disable the "debug" kernel variant to reduce build time
[components.kernel.build]
without = ["debug"]

# Disable the RedHat subscription manager plugin
[components.dnf5.build]
without = ["plugin_rhsm"]
```

### Macro Definitions

The `defines` field sets RPM macros that are available during the build. Each key-value pair becomes a `--define 'key value'` argument to rpmbuild:

```toml
[components.mypackage.build]
defines = { rhel = "11" }
```

The `undefines` field removes macros that would otherwise be defined:

```toml
[components.mypackage.build]
undefines = ["fedora"]
```

### Check Configuration

The `check` field controls the `%check` section of the spec (the package's test suite).

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Skip | `skip` | boolean | No | When `true`, disables the `%check` section by prepending `exit 0` (defaults to `false`) |
| Skip reason | `skip_reason` | string | Conditional | Justification for why tests are being skipped. **Required when `skip` is `true`.** |

```toml
[components.containerd.build]
check = { skip = true, skip_reason = "Tests require network access unavailable in build environment" }
```

> **Note:** The `skip_reason` field is mandatory when `skip` is `true`. This ensures that disabling tests is always documented and traceable. azldev will report a validation error if `skip_reason` is missing.

### Failure Configuration

The `failure` field configures how azldev handles build failures for a component.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Expected | `expected` | boolean | No | When `true`, indicates that this component is expected to fail building (defaults to `false`) |
| Expected reason | `expected-reason` | string | Conditional | Justification for why the component is expected to fail. **Required when `expected` is `true`.** |

This is intended as a temporary marker for components that are known to fail until they can be fixed — for example, when importing a batch of packages where some have unresolved dependency issues:

```toml
[components.broken-package.build]
failure = { expected = true, expected-reason = "Missing build dependency not yet available in Azure Linux" }
```

> **Note:** The `expected-reason` field is mandatory when `expected` is `true`. This ensures that expected failures are always documented and traceable.

### Build Hints

The `hints` field provides non-essential metadata about how or when to build a component. These hints do not affect build correctness but may be used by tools and CI systems for scheduling and resource allocation.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Expensive | `expensive` | boolean | No | When `true`, indicates that building this component is resource-intensive and should be carefully considered when scheduling (defaults to `false`) |

```toml
[components.kernel.build]
hints = { expensive = true }
```

## Package Configuration

Components can customize the configuration for the binary packages they produce. There are two fields for this, applied at different levels of specificity.

### Default Package Config

The `default-package-config` field provides a component-level default that applies to **all** binary packages produced by this component. It overrides any matching [package groups](package-groups.md) but is itself overridden by the `packages` map.

```toml
[components.curl.default-package-config.publish]
channel = "rpm-base"
```

### Per-Package Overrides

The `[components.<name>.packages.<pkgname>]` map lets you override config for a **specific** binary package by its exact name. This is the highest-priority layer and overrides all inherited defaults:

```toml
# Override just one subpackage
[components.curl.packages.curl-devel.publish]
channel = "rpm-devel"
```

### Resolution Order

For each binary package produced by a component, the effective config is assembled in this order (later layers win):

1. Project `default-package-config`
2. Package group containing this package name (if any)
3. Component `default-package-config`
4. Component `packages.<exact-name>` (highest priority)

See [Package Groups](package-groups.md) for the full field reference and a complete example.

### Example

```toml
[components.curl]

# Route all curl packages to "base" by default ...
[components.curl.default-package-config.publish]
channel = "rpm-base"

# ... but put curl-devel in the "devel" channel
[components.curl.packages.libcurl-devel.publish]
channel = "rpm-devel"

# Signal to downstream tooling that this package should not be published
[components.curl.packages.libcurl-minimal.publish]
channel = "none"
```

## Source File References

The `[[components.<name>.source-files]]` array defines additional source files that azldev should download before building. These are files not available in the dist-git repository or lookaside cache — typically binaries, pre-built artifacts, or files from custom hosting.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Filename | `filename` | string | **Yes** | Name of the file as it will appear in the sources directory |
| Hash | `hash` | string | Conditional | Expected hash of the downloaded file for integrity verification. Required for the `prep-sources` command unless `--allow-no-hashes` is used, in which case the hash is computed automatically from the downloaded file. |
| Hash type | `hash-type` | string | Conditional | Hash algorithm used (examples: `"SHA256"`, `"SHA512"`). Required when `hash` is specified. When omitted alongside `hash` for the `prep-sources` command and `--allow-no-hashes` is used, defaults to `"SHA512"`. |
| Origin | `origin` | [Origin](#origin) | **Yes** | Where to download the file from |

### Origin

The `origin` field specifies how to obtain the source file.

| Field | TOML Key | Type | Required | Description |
|-------|----------|------|----------|-------------|
| Type | `type` | string | **Yes** | Origin type. Currently only `"download"` is supported. |
| URI | `uri` | string | No | URI to download the file from (required when type is `"download"`) |

### Example

```toml
[[components.shim.source-files]]
filename = "shimx64.efi"
hash = "7741013d9a24ce554bf6a9df6b776a57b114055e..."
hash-type = "SHA512"
origin = { type = "download", uri = "https://example.com/repo/pkgs/shim/shimx64.efi/sha512/.../shimx64.efi" }

[[components.shim.source-files]]
filename = "shimaa64.efi"
hash = "57aa116d1c91a9ec36ab8b46c9164ae19af192b..."
hash-type = "SHA512"
origin = { type = "download", uri = "https://example.com/repo/pkgs/shim/shimaa64.efi/sha512/.../shimaa64.efi" }
```

## Complete Examples

### Bare upstream component (no customization)

```toml
[components.curl]
```

### Upstream component pinned to a specific distro version

```toml
[components.bash]
spec = { type = "upstream", upstream-distro = { name = "fedora", version = "rawhide" } }
```

### Upstream component pinned to a specific commit

```toml
[components.bash]
spec = { type = "upstream", upstream-commit = "a1b2c3d4e5f6789" }
```

### Upstream component with a different package name

```toml
[components.azurelinux-rpm-config]
spec = { type = "upstream", upstream-name = "redhat-rpm-config" }
```

### Local component

```toml
[components.azurelinux-release]
spec = { type = "local", path = "azurelinux-release.spec" }
```

### Component with build options

```toml
[components.kernel]

[components.kernel.build]
without = ["debug"]
```

### Component with overlays and build config

```toml
[components.mypackage]
spec = { type = "upstream" }

[components.mypackage.build]
with = ["feature_x"]
defines = { rhel = "11" }
check = { skip = true, skip_reason = "Tests require network access" }

[[components.mypackage.overlays]]
type = "spec-add-tag"
description = "Add missing build dependency"
tag = "BuildRequires"
value = "extra-devel"
```

### Component with source file downloads

```toml
[components.shim]

[[components.shim.source-files]]
filename = "shimx64.efi"
hash = "abc123..."
hash-type = "SHA512"
origin = { type = "download", uri = "https://example.com/shimx64.efi" }

[[components.shim.overlays]]
type = "spec-append-lines"
description = "Copy unsigned shim binaries into build tree"
section = "%prep"
lines = ["cp -vf %{shimdirx64}/$(basename %{shimefix64}) %{shimefix64} ||:"]
```

## Related Resources

- [Overlays](overlays.md) — detailed reference for all overlay types
- [Config File Structure](config-file.md) — top-level config file layout
- [Distros](distros.md) — distro definitions and `default-component-config` inheritance
- [Component Groups](component-groups.md) — grouping components with shared defaults
- [Package Groups](package-groups.md) — project-level package groups and full resolution order
- [Configuration System](../../explanation/config-system.md) — inheritance and merge behavior
- [JSON Schema](../../../../schemas/azldev.schema.json) — machine-readable schema
