# Component Identity & Change Detection

`azldev` computes a fingerprint for each component based on its config and sources. This enables:

- **Change detection**: identify which components have changed between two commits or branches, even if the change is a non-obvious config inheritance.
- **Build optimization**: only rebuild changed components and their dependents, skipping unchanged ones.
- **Automatic release bumping**: increment the release tag of changed packages automatically, and include the commits that caused the change in the changelog.

> **Design & how-to.** The substrate design (version-set tags, the `v1:sha256:` content token, force-rehash reconciliation, and the future lazy schema migration) is specified in the [Lock-File Fingerprint Reset RFC](../rfc/lazy-schema-migration.md). The step-by-step rules for adding a fingerprinted field live in [`projectconfig-fingerprint.instructions.md`](../../../.github/instructions/projectconfig-fingerprint.instructions.md).

## Fingerprint Inputs

A component's fingerprint is a SHA256 combining:

1. **Config projection digest** - `sha256` of the canonical `projectV1` projection of the resolved `ComponentConfig` (after all merging). Only fields whose `fingerprint` tag measures them at v1 are emitted; `fingerprint:"-"` fields are excluded. A nil-or-empty scalar slice is treated as zero and omitted by the projection's omit predicate, so a merge-order nil-vs-`[]` difference never moves the digest.
2. **Source identity** - content hash for local specs (all files in the spec directory), commit hash for upstream.
3. **Overlay file hashes** - SHA256 of each file referenced by overlay `Source` fields.
4. **Release version** - the distro's formal `releasever` (e.g. `4.0`), which feeds RPM macros like `%{dist}`; different release versions produce different package NEVRAs even with identical specs.
5. **Manual release bump counter** - increments with each manual release bump, ensuring a new fingerprint even if there are no config or source changes.

The combined digest is stored as a single self-describing **content-version token**: `input-fingerprint = "v1:sha256:<hex>"`. The `v1` prefix is the *content* version (the projection/encoding generation), written together with the digest so the two can never desync. It is independent of the lock *format* `version` field, which stays `1`. A pre-reset (prefix-less) token reads as sub-floor and is force-rehashed to `v1` on the next `component update`.

Global change propagation works automatically: the fingerprint operates on the fully-merged config, so a change to a distro or group default changes the resolved config of every inheriting component.

## `fingerprint` Version-Set Tags

Every fingerprinted field carries an explicit `fingerprint` tag declaring **which content versions measure it**. There is no "included by default": an absent tag on a fingerprinted struct field fails the decision test (`TestAllFingerprintedFieldsHaveDecision`), so adding a field forces a conscious measure/exclude decision.

Grammar (`internal/fingerprint/versiontag.go`):

- `v1..*` - measured from v1 onward (the common case for a build input).
- `vN..vM`, `vN..*`, comma-separated sets (`v1..v1,v3..*`) - measured only in those ranges.
- `-` - never measured (excluded); register it in `expectedExclusions` in `internal/projectconfig/fingerprint_test.go`.
- `!` prefix on a range (`!v1..*`) - always emit even at the zero value (scalar leaves only; an always-emit composite is rejected at v1).
- `key=<name>` - override the emit-key (see below).

**Emit-key is the frozen `toml:` key, never the Go field name.** `projectV1` emits each measured field under its `toml:` key (or an explicit `key=`), sorted by that key. So a cosmetic Go rename is byte-neutral, and a TOML-key rename stays byte-neutral by pinning `key=<old>`. Renaming the emit-key itself is an output change, and therefore a new content version.

### Adding a New Config Field

1. Add the field to the struct in `internal/projectconfig/`.
2. Tag it: `fingerprint:"v1..*"` if it is a build input, or `fingerprint:"-"` (plus an `expectedExclusions` entry) if it is not.
3. A measured, omit-if-zero field is **drift-neutral**: components that do not set it emit identical bytes, so no existing lock moves - it needs no content-version bump.
4. Run `mage unit`.

### Adding a New Source Type

1. Implement `SourceIdentityProvider` on your provider (see `ResolveLocalSourceIdentity` in `localidentity.go` for a simple example).
2. Add a case to `sourceManager.ResolveSourceIdentity()` in `sourcemanager.go`.
3. Add tests in `identityprovider_test.go`.

## Known Limitations

- It is difficult to determine WHY a diff occurred (e.g., which specific field changed) since the fingerprint is a single opaque `v1:sha256:` token: `ComponentIdentity` emits only the combined `fingerprint`, not a per-input breakdown.
