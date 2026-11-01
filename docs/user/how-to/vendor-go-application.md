# Vendor a Go application

Use `go-vendor` to bundle a Go application's module dependencies instead of
building against separately packaged `golang(...)` libraries. This is a targeted
conversion of the selected upstream spec, not a `go2rpm` wrapper or a newly
generated replacement spec. The same setting regenerates sources for supported
specs that already use go-vendor-tools.

## Enable and prepare

Add this to the component definition:

```toml
[components.example.go-vendor]
enabled = true
```

Then prepare the complete build inputs:

```sh
azldev component prepare-sources -p example -o prepared-example --without-git
```

The project/build distro must have a working mock configuration and provide
`go-vendor-tools`, `askalono-cli`, `golang`, `go-rpm-macros`, `rpm-build`, and
`rpmdevtools`, `rpmautospec`, `git`, and `python3-click`. These are installed in a
fresh, network-enabled generation chroot;
neither `go2rpm` nor the Go vendor tools are required on the host. Configure
`prep-packages` for additional tools required by upstream prep scripts.

The pipeline fetches the selected upstream sources, applies overlays, and copies
them into isolated staging. It processes automatic release/changelog metadata
there and runs the existing `%prep` section with `rpmbuild -bp --nodeps`.
This retains upstream patches and edits to module files. It then calls
`go_vendor_archive create` on the prepared module tree, extracts the result for
license detection, and runs `go_vendor_license` without interactive prompts.
Unrecognized licenses fail preparation; add reviewed, hash-qualified exceptions
to `tool-config` rather than suppressing detection.

The output includes the converted spec, all its original sources and patches,
`<component>-vendor.tar.bz2`, and `azldev-go-vendor.toml`. The archive is recorded
with SHA-512 in `sources` and excluded from synthetic git commits with `.gitignore`.
The upstream release/changelog is retained; ordinary azldev release handling
still applies afterwards.

## What is converted

The converter removes standalone `BuildRequires: golang(import/path)` tags,
including version constraints, and bare `%go_generate_buildrequires` invocations.
It changes a bare `%gocheck` to `%gotest ./...`, adds vendor extraction at the end
of `%prep`, and enables module/vendor mode for subsequent build scripts.
It adds the Go compiler/macros and vendor tooling build requirements, dynamic
license backend requirements, license installation/file-list handling, and a
license check in `%build` so disabling `%check` does not bypass license validation.
The main `License:` tag is updated to the detected cumulative SPDX expression.

Unrelated runtime dependencies, C-library build dependencies, custom commands,
patches, metadata, and ordinary subpackages are retained. Go vendoring does not
eliminate dependencies on shared libraries used by cgo.

The initial supported scope is application specs with one unconditional
main-package `%prep`, `%build`, `%install`, and `%files` section. The prep section
must finish in the module root, or use `module-dir` for a relative subdirectory.
Custom build/install commands must already address that same module. Conditional
commands within those sections are retained.

Automatic Go development subpackages (`%gopkg`, `%goinstall`, Go file-list macros),
flagged `%gocheck` commands, compound/rich Go BuildRequires,
explicit conflicting module-mode settings, and ambiguous/conditional
target sections are rejected. Use narrowly scoped overlays to adapt these cases;
azldev does not silently drop subpackages or rewrite arbitrary shell scripts.

## Regenerate an existing vendor archive

For an already-vendored spec, azldev recognizes a single
`tar -xf %{S:N}` (or `%{SOURCEN}`) prep command and the configuration source
referenced by `%go_vendor_license_* -c %{S:M}` commands. The Source numbers need
not be 1 and 2. The configuration must name a plain sidecar file.

The original Source declarations, extraction command, license/file-list macros,
module-mode settings, and custom packaging are retained. The temporary prep spec
omits the vendor Source declaration and its extraction command, so generating
the replacement does not consume the old vendor payload. RPM expands the
upstream archive filename; the regenerated archive replaces that file and its
`sources` checksum rather than adding a second vendor archive.

When `tool-config` is omitted, the upstream go-vendor-tools configuration is used,
including its reviewed license exceptions. An explicit `tool-config` replaces
that configuration. The detected licenses are verified against the existing
main-package `License:` expression; mismatches fail instead of rewriting it.
The source-fetch phase currently still downloads the upstream vendor archive
alongside the other upstream sources, before regenerating its contents.

## Review and upgrade

Run `azldev component diff-sources -p example` to inspect the conversion, or
`azldev component render -p example` to refresh checked-in rendered output.
Rendering and source diffs also generate the archive: they need network access and
can be substantially slower for enabled components.
Rendered output retains the `sources` checksums but excludes payloads listed in
that manifest; archives remain preparation/build artifacts rather than checked-in files.

Review the cumulative license expression and the retained build/install/check
commands before building. Automatic detection is not approval of a dependency's
license. The converter sets `GO111MODULE=on`, `-mod=vendor`, `GOPROXY=off`,
`GOSUMDB=off`, and `GOTOOLCHAIN=local` for build scripts; custom commands must not
override these settings to download dependencies.

The same TOML applies after an upstream version update: no archive version,
import path, Source number, or spec line number is embedded in the declaration.
Generation reruns against the newly selected and patched inputs. Review changed
vendor hashes and licenses; an upstream packaging change may require updating
an overlay or resolving a newly unsupported construct.

See [Go vendoring configuration](../reference/config/components.md#go-vendoring)
for module layout, dependency overrides, and license exceptions.
