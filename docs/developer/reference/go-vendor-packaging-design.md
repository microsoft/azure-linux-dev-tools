# Go vendor packaging: artifact acquisition

**Decision:** Regenerate the vendor archive from the prepared Go sources each
time. TOML describes the recipe; the prepared RPM `sources` manifest records the
digest of the resulting archive. azldev does not rewrite the published spec.

## Use cases and schema

* **Already vendored (Fedora containerd):** name the archive explicitly and
  declare replacement when reusing an upstream manifest entry.
* **Unbundled Fedora Go application:** use a packaging overlay to consume the
  vendor archive, remove obsolete build requirements, and install licenses.
  A Rust application such as `rust-uutils` needs Cargo tooling instead.
* **Azure Linux local spec (for example, Telegraf):** the maintained spec can
  reference a stable `filename = "vendor.tar.bz2"` directly.

```toml
[[components.containerd.source-files]]
filename = "containerd-2.3.5-vendor.tar.bz2"
replace-upstream = true
replace-reason = "Regenerate the existing vendor source"
origin.type = "go-vendor"
# origin.config-file = "go-vendor-tools.toml"
```

The configured basename is the output identity; the spec does not supply it.
Optional generation inputs include declaration-relative native settings, the
module directory, and extra prep packages.

## Acquisition contract

Source preparation, builds, and source diffs generate the vendor archive after
overlays in isolated mock. An existing archive or `sources` entry does not skip
generation. An optional TOML `hash` (with `hash-type`) verifies the newly generated
bytes before publication; without it, azldev records the computed digest.
Routine render requires that digest in TOML and writes it to `sources` without
creating an archive or starting a vendor chroot.
To revendor, change the generation inputs and prepare again. An explicit
filename does not authorize replacing an unrelated source.

## Trade-off

Compared with automatic spec conversion, archive-only generation keeps
upstream build scripts, subpackages, dependencies, and license policy under
maintainer control. The cost is that the maintainer must ensure the spec
extracts and actually uses the archive offline. Generation may adapt a *private*
prep copy to avoid consuming the archive it is about to produce; it does not
publish those edits. See [Generate a Go vendor source archive](../../user/how-to/vendor-go-application.md).
