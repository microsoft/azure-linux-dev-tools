# How To: Inspect Resolved Package Configuration

This guide shows how to use `azldev package list` to audit the effective
binary-package configuration for your project without running a build.

## Background

Binary package configuration in azldev is assembled from up to four layers
(see [Package Groups](../reference/config/package-groups.md) and
[Config System](../explanation/config-system.md) for details):

1. Project `default-package-config` (lowest priority)
2. Component `publish` channel settings (`publish.rpm-channel`, `publish.debuginfo-channel`) —
   themselves resolved from distro defaults → project defaults → component-group defaults → component config
3. Package group `default-package-config`
4. Component `packages.<name>` override (highest priority)

`azldev package list` resolves all of these layers and prints the effective
configuration for each package you ask about.

## List All Explicitly-Configured Packages

Use `-a` to enumerate every package that appears in any package-group or
component `packages` map:

```bash
azldev package list -a
```

Example output:

```
╭──────────────────┬──────┬───────────────────┬─────────────────────┬───────────┬─────────────────╮
│ PACKAGE          │ TYPE │ PACKAGE GROUPS    │ COMPONENT GROUPS    │ COMPONENT │ PUBLISH CHANNEL │
├──────────────────┼──────┼───────────────────┼─────────────────────┼───────────┼─────────────────┤
│ curl-debugsource │ rpm  │ [debug-packages]  │ []                  │           │ rpm-debug       │
│ libcurl          │ rpm  │ [base-packages]   │ [base-published]    │           │ rpm-base        │
│ libcurl-devel    │ rpm  │ [devel-packages]  │ [base-published]    │ curl      │ rpm-base        │
│ wget2-wget       │ rpm  │ []                │ [base-published]    │ wget2     │ rpm-base        │
╰──────────────────┴──────┴───────────────────┴─────────────────────┴───────────┴─────────────────╯
```

### Column meanings

| Column | Meaning |
|--------|----------|
| **Package** | Binary or source package name (RPM `Name` tag) |
| **Type** | `rpm` for binary packages, `srpm` for source packages (set when using `--rpm-file`) |
| **Package Groups** | Sorted list of package-groups whose `packages` list contains this package. Always empty for SRPM rows. |
| **Component Groups** | Sorted list of component-groups the resolved component belongs to. |
| **Component** | Component that has an explicit `packages.<name>` override for this package, if any |
| **Publish Channel** | Effective publish channel after all config layers are applied |

> **Note:** A non-empty **Component** column means the component has an explicit
> per-package entry in its `packages` map — it does **not** mean "the component
> whose spec produces this package". Packages that get their configuration only
> from the project default or a package-group will show an empty Component.

## Look Up Specific Packages

Use `-p` to look up one or more packages by exact name. Packages that are not
in any explicit configuration are still shown — they resolve using only the
project default:

```bash
azldev package list -p libcurl -p libcurl-devel -p curl-debugsource
```

Positional arguments are equivalent to `-p`:

```bash
azldev package list libcurl libcurl-devel curl-debugsource
```

You can combine `-a` and `-p` — the results are the union of both selections.

## List Packages From an RPM Source Map File

Use `--rpm-file` to enumerate all source packages (SRPMs) and their binary RPMs from a
JSON RPM source map file. Each entry in the file maps a binary package name to the source
package (SRPM) name that produced it:

```bash
azldev package list --rpm-file rpm_source_map.json
```

The output includes a `type` column to distinguish SRPMs (`srpm`) from binary RPMs (`rpm`).
SRPM entries use the component's `srpm-channel`; binary RPM entries use the full
publish-channel resolution stack.

> **Note:** `--rpm-file` is mutually exclusive with `-a`, `-p`, and `--synthesize-debug-packages`.

## Machine-Readable Output

Pass `-q -O json` to get JSON output suitable for scripting:

```bash
azldev package list -a -q -O json
```

```json
[
  {
    "packageName": "libcurl",
    "type": "rpm",
    "packageGroups": ["base-packages"],
    "componentGroups": ["base-published"],
    "component": "",
    "publishChannel": "rpm-base"
  },
  ...
]
```

Both `packageGroups` and `componentGroups` are always emitted as JSON arrays — packages
with no membership receive an empty array `[]`, never `null` — so consumers can iterate
the fields without first null-checking them.

For an RPM source map file:

```bash
azldev package list --rpm-file rpm_source_map.json -q -O json
```

## Alias

`pkg` is an alias for the `package` subcommand:

```bash
azldev pkg list -a
```
