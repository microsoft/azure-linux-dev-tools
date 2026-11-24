# Go vendor packaging: artifact acquisition

**Decision:** Regenerate the vendor archive from the prepared Go sources each
time. TOML describes the recipe; the prepared RPM `sources` manifest records the
digest of the resulting archive. azldev does not rewrite the published spec.

## Use cases and schema

* **Already vendored (Fedora containerd):** reuse the archive name that the
  effective spec expects, rather than pinning a versioned filename in TOML.
* **Unbundled Fedora Go application:** use a packaging overlay to consume the
  vendor archive, remove obsolete build requirements, and install licenses.
  A Rust application such as `rust-uutils` needs Cargo tooling instead.
* **Azure Linux local spec (for example, Telegraf):** the maintained spec can
  reference a stable `filename = "vendor.tar.bz2"` directly.

```toml
[[components.containerd.source-files]]
origin.type = "go-vendor"
# origin.config-file = "go-vendor-tools.toml"
```

Discover the output name from a supported vendor Source or specify a basename.
Optional generation inputs include declaration-relative native settings, the
module directory, and extra prep packages.

## Acquisition contract

Source preparation, render, and source diffs generate the vendor archive after
overlays in isolated mock. An existing archive or `sources` entry does not skip
generation. An optional TOML `hash` (with `hash-type`) verifies the newly generated
bytes before publication; without it, azldev records the computed digest.
To revendor, change the generation inputs and prepare again. An explicit
filename does not authorize replacing an unrelated source.

## Trade-off

Compared with automatic spec conversion, archive-only generation keeps
upstream build scripts, subpackages, dependencies, and license policy under
maintainer control. The cost is that the maintainer must ensure the spec
extracts and actually uses the archive offline. Generation may adapt a *private*
prep copy to avoid consuming the archive it is about to produce; it does not
publish those edits. See [Generate a Go vendor source archive](../../user/how-to/vendor-go-application.md).
