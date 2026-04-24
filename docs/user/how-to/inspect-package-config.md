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
╭──────────────────┬────────────────┬───────────┬─────────────────╮
│ PACKAGE          │ GROUP          │ COMPONENT │ PUBLISH CHANNEL │
├──────────────────┼────────────────┼───────────┼─────────────────┤
│ curl-debugsource │ debug-packages │           │ rpm-debug       │
│ libcurl          │ base-packages  │           │ rpm-base        │
│ libcurl-devel    │ devel-packages │ curl      │ rpm-base        │
│ wget2-wget       │                │ wget2     │ rpm-base        │
╰──────────────────┴────────────────┴───────────┴─────────────────╯
```

### Column meanings

| Column | Meaning |
|--------|---------|
| **Package** | Binary package name (RPM `Name` tag) |
| **Group** | Package-group whose `packages` list contains this package, if any |
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

## Machine-Readable Output

Pass `-q -O json` to get JSON output suitable for scripting:

```bash
azldev package list -a -q -O json
```

```json
[
  {
    "packageName": "libcurl",
    "group": "base-packages",
    "component": "",
    "publishChannel": "rpm-base"
  },
  ...
]
```

## Alias

`pkg` is an alias for the `package` subcommand:

```bash
azldev pkg list -a
```
