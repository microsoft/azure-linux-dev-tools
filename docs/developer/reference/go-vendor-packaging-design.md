# Go vendor packaging: artifact acquisition

**Decision:** TOML describes how to generate a vendor archive; the downstream
RPM `sources` manifest identifies the bytes to build with. A cached, verified
artifact does not have to be reproducible by today's Go toolchain to be usable.
azldev prepares the archive, not a replacement RPM spec.

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
Neither the recipe nor its filename override needs a duplicate manifest digest.
Optional generation inputs include declaration-relative native settings, the
module directory, and extra prep packages. These are not cache keys.

## Acquisition contract

| Downstream state | Required behavior |
|---|---|
| Manifest pin and valid cache hit | Use the verified archive. Do not run vendor generation. |
| Manifest pin, but no usable cached bytes | Generate from patched inputs in isolated mock and check the result against the same pin. A mismatch is an error, not permission to update the digest. |
| No downstream pin | Generate the archive and record its digest in prepared `sources` for review. Do not mistake a same-named upstream archive for a downstream pin. |

Changing a recipe alone does not supersede a valid pin. To revendor, either
set a known desired digest or remove the old downstream entry, prepare,
review the resulting dependencies and licenses, and record the new digest.
An explicit filename does not authorize replacing an unrelated source.

## Trade-off

Compared with automatic spec conversion, archive-only generation keeps
upstream build scripts, subpackages, dependencies, and license policy under
maintainer control. The cost is that the maintainer must ensure the spec
extracts and actually uses the archive offline. Generation may adapt a *private*
prep copy to avoid consuming the archive it is about to produce; it does not
publish those edits. See [Generate a Go vendor source archive](../../user/how-to/vendor-go-application.md).
