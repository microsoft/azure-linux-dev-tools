# Component Identity & Change Detection

`azldev` computes a fingerprint for each component based on its config and sources. This enables:

- **Change detection**: identify which components have changed between two commits or branches, even if the change is a non-obvious config inheritance.
- **Build optimization**: only rebuild changed components and their dependents, skipping unchanged ones.
- **Automatic release bumping**: increment the release tag of changed packages automatically, and include the commits that caused the change in the changelog.

## Fingerprint Inputs

A component's fingerprint is a SHA256 combining:

1. **Config hash** — `hashstructure.Hash()` of the resolved `ComponentConfig` (after all merging). Fields tagged `fingerprint:"-"` are excluded.
2. **Source identity** — content hash for local specs (all files in the spec directory), commit hash for upstream.
3. **Overlay file hashes** — SHA256 of each file referenced by overlay `Source` fields.
4. **Distro name + version**
5. **Manual release bump counter** — increments with each manual release bump, ensuring a new fingerprint even if there are no config or source changes.

Global change propagation works automatically: the fingerprint operates on the fully-merged config, so a change to a distro or group default changes the resolved config of every inheriting component.

## `fingerprint:"-"` Tag System

The `hashstructure` library uses `TagName: "fingerprint"`. Untagged fields are **included by default** (safe default: false positive > false negative).

A guard test (`TestAllFingerprintedFieldsHaveDecision`) reflects over all fingerprinted structs and maintains a bi-directional allowlist of exclusions. It fails if a `fingerprint:"-"` tag is added without registering it, or if a registered exclusion's tag is removed.

### Adding a New Config Field

1. Add the field to the struct in `internal/projectconfig/`.
2. **If NOT a build input**: add `fingerprint:"-"` to the struct tag and register it in `expectedExclusions` in `internal/projectconfig/fingerprint_test.go`.
3. **If a build input**: do nothing — included by default.
4. Run `mage unit`.

### Adding a New Source Type

1. Implement `SourceIdentityProvider` on your provider (see `ResolveLocalSourceIdentity` in `localidentity.go` for a simple example).
2. Add a case to `sourceManager.CalculateSourceIdentity()` in `sourcemanager.go`.
3. Add tests in `identityprovider_test.go`.

## Known Limitations

- It is difficult to determine WHY a diff occurred (e.g., which specific field changed) since the fingerprint is a single opaque hash. The JSON output includes an `inputs` breakdown (`configHash`, `sourceIdentity`, `overlayFileHashes`, etc.) that can help narrow it down by comparing the two identity files manually.
