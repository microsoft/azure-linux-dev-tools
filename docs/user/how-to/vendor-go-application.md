# Generate a Go vendor source archive

Use the `go-vendor` source origin to regenerate dependencies for an upstream or
local Go package. azldev generates a tarball; **it does not convert the published
spec**, remove dependency requirements, set offline build flags, change licensing,
or install bundled license files. Maintain that packaging in the spec or overlays.
Rust packages need Cargo vendoring, not this Go provider.

## Already-vendored packaging

```toml
[[components.containerd.source-files]]
origin.type = "go-vendor"
# For render, copy these values from the prepared archive's `sources` entry:
# hash-type = "SHA512"
# hash = "<reviewed digest>"
# Optional native settings, relative to this TOML:
origin.config-file = "go-vendor-tools.toml"
```

By default, azldev finds the single unconditional
`tar -xf %{S:N}` or `tar -xf %{SOURCEN}` in `%prep` referencing a secondary Source,
then asks RPM to expand its filename. Source numbers and package versions are not
embedded in the config. Ambiguous/conditional extraction, secondary setup
extraction (`-a`/`-b`), and release-dependent archive filenames fail rather than
guessing. Use a narrowly scoped prep/Source overlay to make these layouts explicit.
The discovered vendor archive's existing `sources` entry is replaced automatically.
Upstream lookaside fetching may still download the old vendor payload before
regeneration; private prep never consumes it.

## New or downstream-owned packaging

```toml
[[components.example.source-files]]
filename = "vendor.tar.bz2"
origin.type = "go-vendor"
origin.module-dir = "cmd/example" # optional; relative to the final prep directory
origin.prep-packages = ["patch"] # optional tools needed by prep
```

`filename` overrides discovery and is required when no unique target is found.
Use a stable name in the maintained spec. An explicit filename colliding with an
upstream `sources` entry follows the existing `replace-upstream`/`replace-reason`
rules; no implicit replacement of arbitrary explicit targets is allowed.
Only one generated Go vendor archive per component is supported.

The spec must extract the archive into the same module directory, build with
`GOFLAGS=-mod=vendor`, and prevent downloads (`GOPROXY=off`, `GOSUMDB=off`,
`GOTOOLCHAIN=local`). Retain necessary cgo/native dependencies. Review/install
bundled licenses and maintain bundled dependency Provides as required by policy.
Generation alone does not establish an offline or correctly licensed RPM.

## Settings, hashes, and workflow

Native settings stay in a separate file:

```toml
# go-vendor-tools.toml
[archive]
use_module_proxy = true
```

An explicit file replaces native settings, including when empty; missing/invalid
files fail. Without one, azldev reuses the sidecar referenced by
`%go_vendor_license_* -c %{S:N}`, then `go-vendor-tools.toml` when present, otherwise
tool defaults. The selected file's contents affect fingerprints independently of
checkout paths. Native `general.go_mod_dir` is honored; a conflicting component
origin `module-dir` fails. Hooks execute inside the generation chroot with
`GVT_CWD` pointing at staged dist-git. Treat settings and hooks as trusted build code.

For initial preparation, omit `hash`: azldev computes and records the generated
digest in `sources` (SHA-512 by default, or configured `hash-type`).
A configured hash is an expected result and must match **before** publication.
Without one, checksums identify each output but do not detect unexpected changes
between regenerations. Generation is not cached by archive name or configured hash.

```sh
azldev component prepare-sources -p example -o prepared --without-git
# Copy the vendor digest and algorithm from prepared/sources into the
# component's source-files entry as hash and hash-type before rendering.
azldev component diff-sources -p example
azldev component render -p example
```

The project distro needs a working mock configuration with `go-vendor-tools`,
Go, RPM macros/build tools, rpmautospec, and git. Generation fetches the sources,
applies overlays, and runs a **private** `%prep` with the selected vendor Source
and its extraction removed. This avoids consuming the artifact being regenerated.
Upstream history is retained privately for rpmautospec, even with `--without-git`.
The original published spec and native config remain untouched by the generator.

Generation occurs after overlays, even with `--skip-sources` or `--skip-overlays`;
the latter still skips overlays themselves. Module/source patches therefore feed
generation. Do not patch the old vendor archive using archive overlays: use native
dependency overrides/hooks instead. Ensure prep does not apply the same module
patch twice when rebuilding with the regenerated module files.

Source diffs regenerate the archive. Render instead requires the TOML pin and
writes it to the manifest without downloading or generating vendor inputs. For
automatic filenames it reuses the normal batched RPM metadata expansion. Render
does not verify archive bytes; preparation and builds do. Validate upgrades with source diffs,
license review, and an offline RPM build; supported syntax does not guarantee
arbitrary upstream packaging will continue to work.
