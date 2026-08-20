# RPM Repo Resources

The `[resources]` section defines reusable, named resource definitions referenced from elsewhere in the configuration. Today this is RPM repositories — both individual repositories and **repo sets** that instantiate a layout template (e.g., a `base`/`sdk` × `binary`/`debug`/`source` matrix) for a specific deployment.

For an end-to-end walkthrough that ties this together with distro versions and build inputs, see [RPM repos & repo sets overview](../../explanation/repos.md).

## Top-Level Layout

| TOML Key | Type | Description |
|---|---|---|
| `resources.rpm-repos` | map of [RpmRepoResource](#rpm-repo-resource) | Individually-defined RPM repositories, keyed by name |
| `resources.rpm-repo-set-templates` | map of [RpmRepoSetTemplate](#rpm-repo-set-template) | Named layout templates that describe a fixed matrix of sub-repos |
| `resources.rpm-repo-sets` | map of [RpmRepoSet](#rpm-repo-set) | Template instantiations that expand into a group of related repos |

Repos defined under `rpm-repos` and repos synthesized by expanding `rpm-repo-sets` share a single namespace. The combined list is what distro version `inputs` (see [Distros — Inputs](distros.md#inputs)) refer to.

## RPM Repo Resource

Defined under `[resources.rpm-repos.<name>]`. The `<name>` is projected verbatim into dnf section headers and kiwi `--add-repo` arguments, so it must match `^[A-Za-z0-9][A-Za-z0-9_.:-]*$`.

| Field | TOML Key | Type | Description |
|---|---|---|---|
| Description | `description` | string | Human-readable description (diagnostic only) |
| Type | `type` | string | Access protocol; defaults to `rpm-md`. Currently the only supported value. |
| Base URI | `base-uri` | string (http/https URL) | Repository base URL. **Mutually exclusive** with `metalink`. |
| Metalink | `metalink` | string (http/https URL) | Repository metalink URL. **Mutually exclusive** with `base-uri`. |
| Disable GPG check | `disable-gpg-check` | bool | Opt out of GPG verification. Default (`false`) means **GPG checking is enabled** — the safe default. |
| GPG key | `gpg-key` | string | Path or URI to the GPG key. Accepted shapes: an `https` URI, an `http` URI, a `file` URI with absolute path, or a bare path resolved relative to the defining TOML file. Required unless `disable-gpg-check = true`. |
| Arches | `arches` | list of string | Restrict to specific target architectures (e.g. `["x86_64"]`). Empty = all. |

### URI placeholders

`$basearch` and `$releasever` are passed through verbatim to the consumer (mock/dnf or kiwi), which expands them at use time.

### `gpg-key` portability

For repos referenced from `inputs.rpm-build`, `gpg-key` **must not** be a bare path or `file://` URI: mock evaluates the URI inside the chroot, where the host path is invisible. Image builds (kiwi, on the host) accept all forms but http(s) is the most portable.

### Example

```toml
[resources.rpm-repos.fedora-43-everything]
description = "Fedora 43 Everything (binary)"
base-uri    = "https://example.com/releases/43/Everything/$basearch/os/"
gpg-key     = "https://example.com/keys/RPM-GPG-KEY-fedora-43-primary"
```

## RPM Repo Set Template

A template describes a fixed layout — the set of sub-repos hosted under a common URL prefix. Templates are reusable across deployments; pair a template with one or more [`rpm-repo-sets`](#rpm-repo-set) entries to produce concrete bundles of repos.

Two templates ship out of the box:

- **`azl-standard`** — a "channelized" layout: `base`/`sdk` channels × `binary`/`debug`/`source` kinds.
- **`koji-dist-repo`** — Koji dist-repo layout: per-arch binary tree at `<arch>/`, parallel debuginfo tree at `<arch>/debug/`, and a single `src/` tree for SRPMs.

Defined under `[resources.rpm-repo-set-templates.<name>]`.

| Field | TOML Key | Type | Description |
|---|---|---|---|
| Description | `description` | string | Human-readable description |
| Sub-repos | `subrepos` | list of [SubrepoSpec](#subrepospec) | Ordered list of sub-repo entries that make up the layout |

### SubrepoSpec

| Field | TOML Key | Type | Description |
|---|---|---|---|
| Name | `name` | string | Stable short identifier; combined with the set's `name-prefix` to form the resulting repo ID. Same grammar as a top-level repo name. |
| Sub-path | `subpath` | string | Path relative to the set's `base-uri`. May contain `$basearch` (passed through verbatim). |
| Kind | `kind` | string (`binary` \| `debug` \| `source`) | Classification of what the sub-repo carries: signed binary RPMs (`binary`), debuginfo/debugsource RPMs (`debug`), or SRPMs (`source`). Defaults to `binary`. Diagnostic only today; reserved for future filtering features. |

### Example

```toml
[resources.rpm-repo-set-templates.azl-standard]
description = "Standard base/sdk x binary/debug/source layout"
subrepos = [
    { name = "base",       kind = "binary", subpath = "base/$basearch" },
    { name = "base-debug", kind = "debug",  subpath = "base/debuginfo/$basearch" },
    { name = "base-src",   kind = "source", subpath = "base/srpms" },
    { name = "sdk",        kind = "binary", subpath = "sdk/$basearch" },
    { name = "sdk-debug",  kind = "debug",  subpath = "sdk/debuginfo/$basearch" },
    { name = "sdk-src",    kind = "source", subpath = "sdk/srpms" },
]
```

## RPM Repo Set

A repo set instantiates a [template](#rpm-repo-set-template) for a specific deployment by supplying a base URL, name prefix, and shared GPG configuration. At validation time each set expands into one synthesized [`RpmRepoResource`](#rpm-repo-resource) per included sub-repo, keyed by `<name-prefix><subrepo.name>`.

Defined under `[resources.rpm-repo-sets.<name>]`.

| Field | TOML Key | Type | Description |
|---|---|---|---|
| Description | `description` | string | Human-readable description |
| Template | `template` | string | **Required.** Name of the [`rpm-repo-set-templates`](#rpm-repo-set-template) entry to instantiate |
| Base URI | `base-uri` | string (http/https URL) | **Required.** URL prefix under which the sub-repos live |
| Name prefix | `name-prefix` | string | Prepended to each sub-repo's `name` to form the synthesized repo ID. May be empty. |
| GPG key | `gpg-key` | string | Shared GPG key for sub-repos in this set; same shape rules as the per-repo `gpg-key` |
| Disable GPG check | `disable-gpg-check` | bool | Opt out of GPG verification for sub-repos in this set |
| Arches | `arches` | list of string | Restrict every synthesized repo in this set to specific architectures |
| Sub-repos | `subrepos` | list of string | Allowlist of sub-repo names to include from the template. Empty/unset = include all sub-repos. Names must match entries in the referenced template. |

### Sub-repo selection

By default (`subrepos` unset or empty), every sub-repo in the referenced template is instantiated. Provide `subrepos = [...]` to take only a strict allowlist:

```toml
subrepos = ["base", "base-src", "sdk", "sdk-src"]   # skip debug for slim build inputs
```

Unknown names (and duplicates within the list) are rejected at load time.

### Collisions

Synthesized repo IDs (`<name-prefix><subrepo.name>`) must not collide with explicit `[resources.rpm-repos.…]` entries or with another set's expansion. The validator surfaces collisions with a clear error.

### Example

```toml
[resources.rpm-repo-sets.azl4-prod]
description = "Production build inputs"
template    = "azl-standard"
base-uri    = "https://example.com/azl4"
name-prefix = "azl4-"
gpg-key     = "https://example.com/keys/RPM-GPG-KEY"
subrepos    = ["base", "base-src", "sdk", "sdk-src"]   # binary + sources, no debug
```

This expands into four `RpmRepoResource` entries: `azl4-base`, `azl4-base-src`, `azl4-sdk`, `azl4-sdk-src`.

## Merging across files

Like other top-level maps, `rpm-repos`, `rpm-repo-set-templates`, and `rpm-repo-sets` are merged across config files **by key with wholesale entry replacement**. A duplicate name in a later-loaded file fully replaces the earlier definition (including any zero-value fields). This makes `--config-file` overrides predictable: setting `disable-gpg-check = false` (the zero value) will override an earlier `true`.

## Related Resources

- [Distros — Inputs](distros.md#inputs) — wiring repos and repo-sets into per-version build inputs
- [RPM repos & repo sets overview](../../explanation/repos.md) — end-to-end design rationale and full TOML example
- [Configuration System](../../explanation/config-system.md) — load order and merge rules
