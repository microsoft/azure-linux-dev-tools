---
applyTo: "internal/projectconfig/**/*.go,internal/fingerprint/**/*.go"
description: "Rules for interacting with project config structs and the fingerprint substrate. IMPORTANT: read before adding or changing any field on a config struct, or touching projectV1 / golden vectors / fingerprint tags."
---

# Config structs & fingerprinting

Editing config structs (`internal/projectconfig/`) and the fingerprint substrate
(`internal/fingerprint/`) has several **silent traps** - the compiler stays green
while a field quietly vanishes or a frozen byte moves. Follow these rules.

## `ComponentConfig.WithAbsolutePaths` hand-lists its fields

`(*ComponentConfig).WithAbsolutePaths` in `internal/projectconfig/component.go`
builds its result with an **explicit field-by-field struct literal**, not
`deep.MustCopy`. It is called for *every* component at load time (via
`mergeComponents` in `loader.go`), so any field missing from that literal is
**silently zeroed on load** - it parses from TOML fine, then disappears before
resolution, fingerprinting, or rendering. No error, no panic, just lost data.

> It is hand-written (unlike every sibling `WithAbsolutePaths`, which uses
> `deep.MustCopy`) for one reason: it must **alias** the cyclic, project-wide
> `SourceConfigFile` pointer rather than deep-copy it. Do not "simplify" it to
> a blanket `deep.MustCopy` - that reintroduces the cycle.

**Every new field on `ComponentConfig` MUST be added to this literal.**

## Adding a field to a config struct

1. **Add the field** with a `toml:` key and a **mandatory** `fingerprint:` tag.
   An absent tag fails `TestAllFingerprintedFieldsHaveDecision`
   (`internal/projectconfig/fingerprint_test.go`).
   - Build input → `fingerprint:"v1..*"` (measured).
   - Not a build input → `fingerprint:"-"` **and** register the field in
     `expectedExclusions` in that same test file. If the field's **parent struct is
     already excluded** (e.g. `ComponentBuildConfig.Failure`), do *not* tag the
     inner struct's fields - that subtree is pruned from both the decision walk and
     `projectV1`, so its fields are neither policed nor measured.
2. **If the field is on `ComponentConfig` directly → add it to
   `WithAbsolutePaths`.** Skip this and it never loads.
3. **If you added a new struct *type* to the fingerprinted graph**, add it to
   `FingerprintedStructTypes()` in `internal/fingerprint/project.go` - the single
   source of truth that **both** the mandatory-tag decision test
   (`internal/projectconfig/fingerprint_test.go`) and the emission probe
   (`measuredLeafEmitKeys` in `internal/fingerprint/project_internal_test.go`) walk.
   Add it there and nowhere else.
4. **If measured, emit it in `projectV1`** (`internal/fingerprint/project.go`,
   hand-written as of v1) inside the correct sub-projector, under its **frozen `toml`
   emit-key** (or an explicit `key=` in the tag - never the Go field name).
   - Removing a measured field won't compile (good); a Go rename is byte-neutral
     **only if** the `toml` key / `key=` is unchanged.
5. **Set the field non-zero in `emissionProbeConfig()`**
   (`internal/fingerprint/project_internal_test.go`). The emission probe FAILS
   until you do - that is the guard against "measured but forgot to emit".
6. **Append a `<toml-key>-set` golden vector** via
   `go test ./internal/fingerprint -run TestGoldenVectors -update-golden`.
7. **Regenerate the schema in all three places** if the field is user-facing (has
   a `toml` key / `jsonschema` tag): `mage docs` updates
   `schemas/azldev.schema.json` **and** the CLI reference docs, while
   **`mage scenarioUpdate`** updates the `config generate-schema` scenario
   snapshots (`TestSnapshots*config_generate-schema*`), which embed the schema
   again. Running only `mage docs` leaves those snapshots red. Also hand-update the
   relevant `docs/user/reference/config/` page (e.g. `components.md`).

**Build-meaningful zero value?** A field whose *zero value* is itself a deliberate
build setting (e.g. a `bool` where `false` carries meaning) is tagged
`fingerprint:"!v1..*"` (always-emit) and wired with `builder.emitAlways(...)`, **not**
`builder.emit(...)` - the omit-if-zero `emit` would drop it at its zero value and lose
that signal. It MUST get a golden vector **at its zero value**; the
differ-from-`minimal` guard in `golden_internal_test.go` then proves it actually emits.

## Golden vectors: append-only, and the frozen/growing split

- `maximalConfig()` is **FROZEN** - it is a golden-corpus config at the time of
  the initial implementation of that fingerprint algorithm, so its digest is
  pinned. **Never add a new field to `maximalConfig()`** (it would move the frozen
  `maximal` digest, a hard CI failure). Field growth goes in
  `emissionProbeConfig()` only.
- Existing golden digests are **append-only**: `-update-golden` may add a new
  `(name -> digest)` entry, but a CI check FATALs any commit that *modifies* an
  existing digest. A moved frozen byte is a hard failure, not something a reviewer
  must eyeball.
- A new **omit-if-zero measured field that no config sets is drift-neutral**: it
  emits no bytes, so no existing lock should move and no component version bump
  is needed. Only a component that actually sets the field drifts.
- **One isolation vector per new field is enough.** Add a single `<toml-key>-set`
  vector that sets *only* the new field (like `single-overlay` /
  `defines-empty-value`). Projection slots are length-prefixed and independent, so
  a field's encoding is fully pinned in isolation - do **not** add a "maximal + new
  field" or "previous + new field" combinatorial vector (and you cannot touch the
  frozen `maximal`). Add *extra* vectors only for genuine internal discrimination
  cases the field introduces (empty-vs-absent, map-key membership, etc.).

## Other config struct / fingerprinting pitfalls

- **Strict parsing.** The loader runs `DisallowUnknownFields()`
  (`internal/projectconfig/loader.go`). Renaming or removing a `toml` key breaks
  every existing on-disk config that still uses it. Load-time config migration is
  deferred (lazy schema migration RFC PR E); until it exists, a `toml`-key rename
  needs a coordinated, version-pinned rollout, not a bare rename.
  (`fingerprint:"...,key=<old>"` keeps the *fingerprint* byte-neutral, but does
  **not** keep old TOML parseable.)
- **`omitempty` + go-toml/v2 (`v2.3.1`).** Emptiness is decided by reflecting the
  **exported** fields *before* any `TextMarshaler` runs. A struct that marshals via
  `TextMarshaler` but holds its real value in an **unexported** field is treated as
  empty and **dropped even when set**. Keep round-tripped state in an exported field
  or a plain string.
- **`MergeUpdatesFrom` uses mergo with `WithOverride` + `WithAppendSlice`.** Slices
  *append* across merge layers, and the same intent can resolve to `nil` *or* `[]`
  depending on merge order. That is exactly why the projection's omit predicate
  treats a nil-or-empty scalar slice as a zero value (both collapse to no emitted
  bytes) at the hash boundary - do not rely on raw slice identity for a
  fingerprinted field.
- **A nested-struct field surfaces asymmetrically in output.** `encoding/json`
  `omitempty` does **not** drop an empty struct, so `component list -O json` shows
  `"foo":{}` on every component, while `config dump` (go-toml) *does* honor struct
  `omitempty` and omits it. Neither is fingerprint drift (the projector omits on
  projected emptiness) - don't mistake the JSON `{}` for churn.

## Standing invariants (do not break)

- **No reader recomputes a historical fingerprint.** Synthetic history and
  historic-overlay application read **stored** lock strings only
  (`synthistory.go`); only `computeCurrentFingerprint` (current tree) and the
  `update` re-stamp call `ComputeIdentity`.
- **Stored fingerprint is the atomic `v1:sha256:<digest>` token**; lock *format*
  `version` stays `1` (the content version lives in the token prefix). A pre-`v1`
  (prefix-less) token reconciles by **force-rehash** - the existing string
  inequality re-stamps it; do not make that compare version-aware before the replay
  registry (RFC PR C) exists.
- **`projectV1` output is frozen.** A new byte encoding is a new content version,
  never an edit to `projectV1`'s output. The golden vectors enforce this. The only
  allowed changes to `projectV1` are purely additive.
