# Component Identity & Change Detection

The `component identity` and `component diff-identity` subcommands compute deterministic fingerprints of component build inputs. For example, CI can compute fingerprints for the base and head commits of a PR, then diff them to determine exactly which components have changed and need to be rebuilt/tested.

```bash
# Typical CI workflow
git checkout $BASE_REF && azldev component identity -a -O json > base.json
git checkout $HEAD_REF && azldev component identity -a -O json > head.json
azldev component diff-identity base.json head.json -O json -c
# → {"changed": ["curl"], "added": ["wget"], "removed": [], "unchanged": []}
```

## Fingerprint Inputs

A component's fingerprint is a SHA256 combining:

1. **Config hash** — `hashstructure.Hash()` of the resolved `ComponentConfig` (after all merging). Fields tagged `fingerprint:"-"` are excluded.
2. **Source identity** — content hash for local specs (all files in the spec directory), commit hash for upstream.
3. **Overlay file hashes** — SHA256 of each file referenced by overlay `Source` fields.
4. **Distro name + version**
5. **Affects commit count** — number of `Affects: <component>` commits in the project repo.

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
2. Add a case to `sourceManager.ResolveSourceIdentity()` in `sourcemanager.go`.
3. Add tests in `identityprovider_test.go`.

## CLI

### `azldev component identity`

Compute fingerprints. Uses standard component filter flags (`-a`, `-p`, `-g`, `-s`). Exposed as an MCP tool.

### `azldev component diff-identity`

Compare two identity JSON files. The `--changed-only` / `-c` flag filters to only changed and added components (the build queue). Applies to both table and JSON output.

## Known Limitations

- It is difficult to determine WHY a diff occurred (e.g., which specific field changed) since the fingerprint is a single opaque hash. The JSON output includes an `inputs` breakdown (`configHash`, `sourceIdentity`, `overlayFileHashes`, etc.) that can help narrow it down by comparing the two identity files manually.
