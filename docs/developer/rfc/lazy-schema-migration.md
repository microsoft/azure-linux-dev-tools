# RFC 002: Lazy Schema Migration for Lock-File Fingerprints

- **Status**: Draft
- **Author**: @damcilva
- **Created**: 2026-06-04
- **Related code**:
  - [`internal/fingerprint/fingerprint.go`](../../../internal/fingerprint/fingerprint.go) — `ComputeIdentity`, `combineInputs`
  - [`internal/lockfile/lockfile.go`](../../../internal/lockfile/lockfile.go) — `ComponentLock`, version gate
  - [`internal/projectconfig/fingerprint_test.go`](../../../internal/projectconfig/fingerprint_test.go) — field-inclusion audit
  - [`internal/app/azldev/core/components/resolver.go`](../../../internal/app/azldev/core/components/resolver.go) — `computeFreshnessStatus`

## Background

### Lock files and fingerprints

`azldev` tracks the resolved state of each component in a per-component lock file under `locks/<name>.lock`. A lock pins the upstream commit and records a content **fingerprint** of every input that affects the component's build output:

```go
// internal/lockfile/lockfile.go
type ComponentLock struct {
    Version             int    // lock file FORMAT version, currently 1
    ImportCommit        string // write-once fork point
    UpstreamCommit      string // resolved upstream commit
    ManualBump          int    // mass-rebuild counter
    InputFingerprint    string // sha256 of all render inputs
    ResolutionInputHash string // sha256 of upstream-resolution inputs
}
```

The fingerprint is computed by [`fingerprint.ComputeIdentity`](../../../internal/fingerprint/fingerprint.go). Its core is a single structural hash of the resolved component config:

```go
// hashstructure walks every exported field of ComponentConfig.
// Fields tagged `fingerprint:"-"` are excluded; everything else is included.
configHash, err := hashstructure.Hash(component, hashstructure.FormatV2,
    &hashstructure.HashOptions{TagName: "fingerprint"})
```

`configHash` is then folded together with the source identity, overlay file hashes, manual bump, and distro release version into a domain-separated SHA256 (`combineInputs`). Field inclusion is policed by [`TestAllFingerprintedFieldsHaveDecision`](../../../internal/projectconfig/fingerprint_test.go): every field of every fingerprinted struct must be consciously categorized as **included** (no tag) or **excluded** (`fingerprint:"-"`). The safe default is *included* — a new field contributes to the hash unless told otherwise.

Drift is detected in [`resolver.go`](../../../internal/app/azldev/core/components/resolver.go): `computeFreshnessStatus` → `checkFingerprintFreshness` recomputes the identity and compares it to `InputFingerprint`, yielding `FreshnessCurrent` or `FreshnessStale`. `component update` ([`update.go`](../../../internal/app/azldev/cmds/component/update.go)) re-stamps the lock and flips a user-visible `Changed` flag whenever the fingerprint moves.

### The three version axes

As the tool matures, three *independent* notions of "version" are emerging. Conflating them is the source of the problems in this RFC:

| Axis | Versions what | Lives where | Exists today? |
| ---- | ------------- | ----------- | ------------- |
| **Config schema version** | on-disk TOML field shape | load / migration layer | No |
| **Fingerprint algorithm version** | how inputs fold into the hash | `fingerprint` combiner | No (implicitly v1) |
| **Lock file format version** | lock file serialization | `lockfile` | Yes (`Version = 1`) |

### The problem

Because field inclusion defaults to *included*, **adding any new fingerprinted config field re-hashes every component**, even components that never set the field. `hashstructure` hashes a zero-value field identically to a present-but-empty field — but *differently* from a field that does not exist in the struct at all. So the moment the Go struct gains `Foo string`, every component's `configHash` changes, every `InputFingerprint` changes, and every `*.lock` shows drift on the next `component update`.

Concretely: we add field `foo` and set `foo = "baz"` on package `bar`. The desired outcome is that **only** `bar.lock` drifts. The actual outcome today is that **all** lock files drift.

**The root concern is git churn, not rebuilds.** The mass rebuild is a knock-on effect; the thing we actually want to protect is the **lock-file diff in a PR**. A change that touches one package should produce exactly one changed `*.lock` — ideally zero changed bytes in any other lock file, in any way. Lock files should change *only* when there is a real, per-component change. Clean diffs keep PRs reviewable, keep `git blame` meaningful, and make "this lock moved" a trustworthy signal that *that component's* inputs actually changed. The rebuild fan-out follows for free once the diffs are clean.

There is a harder variant lurking behind the additive case: **non-additive** schema changes — renaming a field, removing one, changing a baked-in default, or fixing a bug in the hashing logic itself. These legitimately change the *meaning* of the config without changing user intent, and we will eventually need to absorb them without forcing every consumer to rebuild.

### Goals

- **G1 (primary, non-functional): no spurious lock-file diffs.** Landing a config-schema or hashing change must not rewrite `*.lock` files for components whose effective inputs are unchanged — not even to bump a version field. Soft requirement (strongly preferred, not a hard gate), but it shapes which solutions are acceptable: it rules out any eager "migrate everything" pass.
- **G2: only real changes drift.** A lock changes iff that component's build-effective inputs changed.
- **G3: piecemeal, lazy migration.** Schema/algorithm evolution rolls out per-component, riding along with independent changes, never as a big-bang.
- **G4: additive fields are drift-neutral by construction.** Adding an unset field should be invisible to every existing lock with no author effort beyond declaring intent.
- **G5: correctness backstop preserved.** Never silently under-rebuild: a genuine input change must always drift its lock.

## Problem inventory

| # | Problem | Root cause | Severity |
| - | ------- | ---------- | -------- |
| 1 | Adding a config field drifts every lock, even unaffected components | Field inclusion defaults to *included*; zero-value ≠ absent in struct hash | Mass rebuild |
| 2 | No way to land a semantically no-op schema change (rename/move) without drift | Fingerprint hashes raw struct shape, not normalized intent | Mass rebuild |
| 3 | No way to evolve the hashing algorithm (bugfix, input reorder) without drift | `combineInputs` has no version; old and new outputs are incomparable | Mass rebuild + lock churn |
| 4 | No on-disk config schema version | `ConfigFile` has a `$schema` URL but no version field | Blocks managed migration |
| 5 | Migration is all-or-nothing | Freshness check is binary match/no-match against one stored hash | No piecemeal rollout |

Problems 1–3 share a shape: a change that *should* be invisible to most components is forced to be visible to all of them, because the fingerprint cannot distinguish "input changed" from "encoding changed." Problem 4 is the missing primitive for managed config evolution. Problem 5 is the property we actually want from any solution — **per-component, lazy** migration, where a lock upgrades only when something independently touches it.

## How fingerprinting works today (detail)

```text
ComponentConfig ──hashstructure(TagName:"fingerprint")──► configHash (uint64)
                                                              │
SourceIdentity ───────────────────────────────────────────┐ │
OverlayFileHashes ────────────────────────────────────────┤ │
ManualBump ───────────────────────────────────────────────┤ ▼
ReleaseVer ───────────────────────────────────────────► combineInputs ──► "sha256:…"  (InputFingerprint)
```

Two properties of `hashstructure` v2.0.2 are load-bearing for this RFC:

1. **No per-field `omitempty`.** The only field tags it recognizes are `-`/`ignore` (skip) and `set`/`string` (encoding). A zero-value field is hashed; it is not skipped.
2. **It honors the `Includable` interface.** If the value (or a pointer to it) implements `HashInclude(field string, v interface{}) (bool, error)`, the walker calls it per field and omits the field when it returns `false`. **An omitted field hashes identically to a field that was never declared.** There is also a global `IgnoreZeroValue` option that skips *all* zero-value fields.

The struct's type name *is* part of the hash (`hashstructure` mixes in `reflect.Type.Name()`), but that name does not change when fields are added, so it is irrelevant to drift.

One constraint: the top-level value passed to `hashstructure.Hash` is not addressable, so an `Includable` implementation must use a **value receiver** to be seen for the root struct.

## Change taxonomy

Not every config change should be treated the same way. The right mechanism depends on what kind of change it is. This taxonomy drives the design.

| Class | Example | Should unaffected locks drift? | Mechanism |
| ----- | ------- | ------------------------------ | --------- |
| **Additive field** | new `foo` field, unset on most components | No — only setters drift | Default omitempty (Layer 1); no version bump |
| **Additive with non-zero default** | new field defaulted to `"auto"` via defaults merge | No | Algorithm version + replay (Layer 2) |
| **Rename / move** | `foo` → `bar`, same semantics | No | Schema migration → canonical hash (Layer 3) + Layer 2 |
| **Semantic change** | meaning of `foo` changes; output differs | Yes — that's correct | None; drift is intended |
| **Hashing bugfix** | overlay ordering bug in `combineInputs` | No | Algorithm version + replay (Layer 2) |
| **Field removal** | drop deprecated `foo` | No, if nobody set it | Migration drops field; Layer 2 for setters |

The recurring requirement across the "No" rows is the same: **distinguish a change in user intent from a change in encoding, and only drift on the former.** Note the first row: with omitempty as the *default* (Layer 1), additive fields need no version bump and no replay at all — they are hash-neutral by construction. Layer 2 then carries only the genuinely hard cases (rows 2, 5).

## Research

### `hashstructure` options

- **`Includable` (per-field callback)** keeps existing hashes byte-identical: fields that don't opt into omission hash exactly as they do today. This is the only option that solves Problem 1 *without* itself triggering a mass rebuild.
- **`IgnoreZeroValue` (global)** is simpler to wire but flips the hash of *every* struct that has any zero-value field — i.e. it is itself a mass-rebuild event, and it removes our ability to say "this empty field is meaningful." Rejected for the default path.

### How other tools version lock state

- **Cargo (`Cargo.lock`)** carries an explicit `version = 4` at the top of the lock and teaches `cargo` to read older versions, upgrading in place on the next write. Migration is lazy — touching the lock upgrades it.
- **npm (`package-lock.json`)** uses `lockfileVersion` and supports reading v1/v2/v3, rewriting to the current version on install.
- **Terraform state** stores a `version` and a `terraform_version`; state is upgraded forward on use, never downgraded.
- **Go modules** avoid the problem entirely by hashing *content* (`h1:` dirhashes) rather than a struct shape, so adding metadata fields never perturbs existing sums.

The common pattern: an **integer version stamped into the persisted artifact**, plus the ability to **read and replay older versions**, plus **lazy forward-migration on write**. Our `ComponentLock.Version` already provides the slot; today we only ever reject mismatches instead of migrating.

### Where the hashing logic should live

A natural question (raised during design) is whether to move hashing onto the config types as a method. The hashing logic decomposes into two separable jobs:

1. **Pure config hash** — `hashstructure.Hash(component, …)` plus field-inclusion policy. This is genuinely *about the config type*; `HashInclude` is already a method on it.
2. **Combiner / orchestration** — reads overlay file contents (needs `opctx.FS`), folds in source identity / releasever / bump, applies domain separation, and (Layer 2) selects an algorithm version. None of these are config fields.

Moving (1) onto the type improves cohesion and version-locality. Moving (2) onto the type would drag I/O and cross-cutting algorithm versioning into `projectconfig` (a pure data package that `lockfile` imports), and would scatter the centralized field-inclusion audit. The combiner must own algorithm versioning because "I changed how overlays fold in" is not a per-type concern. **Recommendation: a hybrid seam** — expose `ComponentConfig.ConfigHash()` on the type; keep the combiner in `fingerprint`.

## Proposed approach

The design is **layered**, not a single switch. Each layer is independently shippable and addresses a distinct row of the taxonomy. Layers 1 and 2 cover the immediate need (Problems 1–3); Layer 3 is the forward-looking config-schema-version axis (Problem 4) and can follow later.

### Layer 1 — Omitempty as the default inclusion policy

Today the safe default is *include-always*: a new field contributes to the hash even at zero value. We **flip the default to omitempty** (include only when non-zero) and make the inclusion policy an explicit, exhaustive, CI-enforced choice per field.

Every fingerprinted field must carry one of three `fingerprint` tag values:

| Tag | Meaning | When to use |
| --- | ------- | ----------- |
| `fingerprint:"omitempty"` | included **only when non-zero** (the new default) | almost all fields |
| `fingerprint:"always"` | included even at zero value | fields whose **zero value is build-meaningful** (e.g. a `bool` that defaults true, where `false` must rebuild) |
| `fingerprint:"-"` | excluded from the hash entirely | paths, publish routing, runtime state |

There is no untagged state. `TestAllFingerprintedFieldsHaveDecision` is rewritten to assert that **every** field of every fingerprinted struct carries a valid tag value — failing CI on any bare field. This is *simpler* than today's audit: it no longer maintains an `expectedExclusions` registry, it just checks for tag presence and validity. The conscious decision moves to the point of field definition, where the author has the context to judge whether zero is meaningful.

Implement `Includable` on each fingerprinted struct, delegating to one shared helper:

```go
// includeFingerprintField reports whether a field participates in the hash.
// "-" fields never reach here (hashstructure skips them first). "always" fields
// are included unconditionally; "omitempty" (the default) is included only when
// the resolved value is non-zero.
func includeFingerprintField(t reflect.Type, field string, v reflect.Value) (bool, error) {
    sf, ok := t.FieldByName(field)
    if !ok {
        return true, nil
    }
    switch sf.Tag.Get("fingerprint") {
    case "always":
        return true, nil
    default: // "omitempty"
        return !v.IsZero(), nil
    }
}

// Value receiver: the root struct passed to hashstructure.Hash is not addressable.
func (c ComponentConfig) HashInclude(field string, v interface{}) (bool, error) {
    return includeFingerprintField(reflect.TypeOf(c), field, reflect.ValueOf(v))
}
```

**Why flipping the default is safe — fingerprints see the resolved config.** The usual objection to blanket omitempty is the false-negative footgun: a field whose zero is meaningful gets omitted and collides with "unset," so two semantically different configs hash the same and a rebuild is missed. That objection assumes we hash *raw user input*. We do not. `ComputeIdentity` runs on the **resolved, post-merge** config (`*result.config`, after defaults are applied). The omit predicate is therefore "the *resolved value* equals Go-zero," not "the user didn't type it." Consequences:

- Two configs that both resolve a field to zero build identically → hashing them the same is **correct**, not a collision.
- "Unset" never reaches the hasher — it has already been resolved to its default. If the default is non-zero, the field is non-zero and is included anyway. If the default *is* zero, then unset and explicit-zero resolve identically → same build → same hash → correct.

So the classic false-negative requires absence ≠ zero-default *at the point of hashing*, and post-merge resolution closes that gap. The load-bearing invariant is **G5's guarantee restated structurally: the fingerprint must see exactly the build-effective resolved config.** That invariant must already hold, or fingerprinting is broken independently of this change. The `fingerprint:"always"` escape hatch (plus the mandatory-tag audit) is cheap insurance against the invariant silently drifting later — e.g. if someone applies a default *after* fingerprinting.

**Result:** additive fields are drift-neutral **by construction** (G4) — an unset field omits identically to a field that never existed, with no version bump and no replay. Only setters drift (G2). The cost is one tag per field (verbose but mechanical) and two genuine edge cases (see below).

#### Edge cases under default omitempty

- **Meaningful zero with a non-zero default** (e.g. `int Jobs` defaulting to `4`, where `0` means serial). Post-merge: unset → `4` (included), explicit `0` → `0` (omitted-by-omitempty). These build differently *and* hash differently, so there is no collision — they are consistent. Such fields rarely trigger omission at all because the default keeps them non-zero. Tag them `always` only if a zero value must be distinguishable from a future change of default.
- **nil vs empty slice.** `reflect.Value.IsZero` on a slice is `IsNil`. A missing TOML key → nil → omitted; `key = []` → non-nil empty → included. Default omitempty thus makes nil-vs-empty a hash distinction that include-always collapses. Almost never observable, but it is a real behavioral edge; `always` forces both to hash.

**Adopting this flip is itself a fingerprint-algorithm change** (every config's hash moves), so it does not land for free — it is absorbed by Layer 2's versioned replay rather than by rewriting locks. See Layer 2.

### Layer 2 — Versioned fingerprint with lazy replay (algorithm and default changes)

Stamp the algorithm version into the lock and teach the freshness check to **replay** older versions:

1. Add `FingerprintVersion int` (`toml:"fingerprint-version,omitempty"`) to `ComponentLock`. Old locks read as `0` = baseline. The lock **format** `Version` stays `1`; this is a *content* version and is fully backward compatible.
2. Turn `ComputeIdentity` into a thin dispatcher over a small registry of historical compute functions, keyed by version. Keep the last *N* versions:

   ```go
   var fingerprinters = map[int]computeFn{
       1: computeV1, // current algorithm
       2: computeV2, // e.g. fixes overlay-ordering bug, or absorbs a new default
   }
   const currentFingerprintVersion = 2
   ```

3. In `checkFingerprintFreshness`, compute at the **current** version. On mismatch, if `lock.FingerprintVersion < current`, recompute at the lock's recorded version. If *that* matches the stored hash, the inputs are unchanged and only the algorithm evolved → treat as `FreshnessCurrent` and flag for silent re-stamp. Otherwise → `FreshnessStale`.
4. `component update` always stamps `FingerprintVersion = current` when it writes. Migration is therefore **lazy and per-component**: a lock upgrades only when something independently touches it.

This resolves Problems 2 (for default changes), 3 (hashing bugfixes), and 5 (piecemeal rollout). It is the same lazy-forward-migration pattern Cargo/npm use, specialized to a content hash.

#### Churn-avoidance policies (G1)

The version stamp is itself a potential source of spurious diffs — the exact thing G1 forbids. Two policies keep it invisible until a real change forces a write:

- **`fingerprint-version` is `omitempty` in TOML.** A baseline (`version 0/absent`) lock that is never otherwise touched never materializes the field, so its bytes stay identical. The field only appears in a lock that was *already* being rewritten for an independent reason. Existing checked-in locks therefore produce **zero diff** on the day this lands.
- **Re-stamp only on a real write; never write to advance the version.** The "silent re-stamp" in step 3 is *piggybacked* onto a write that is already happening — it must never be its own trigger. `component update` must keep its existing write-on-change guard: if nothing else changed, the version bump alone does **not** dirty the lock. (Concretely, the equivalent of `if !result.Changed && !resHashChanged { return false, nil }` stays in force; the re-stamp rides the `Changed` path, it does not create one.)

Together these make migration strictly opportunistic: a lock advances its version the next time its component changes for real, and not one commit sooner.

#### First concrete use: the Layer 1 switchover

Flipping the inclusion default to omitempty (Layer 1) moves every config's hash, so it cannot ship as a free additive change — it is **Layer 2's first real customer.** It registers as `computeV2` (omitempty default) alongside `computeV1` (include-always), bumps `currentFingerprintVersion`, and is absorbed by replay: every existing lock recomputes clean at v1, is recognized as unchanged-inputs, and re-stamps to v2 *only when next written* per the churn policy above. No mass regen, no flag day. And because omitempty makes all future additive changes hash-neutral by construction (G4), it permanently **shrinks** the set of changes that need a Layer 2 version event at all — Layer 1 is both the first user of Layer 2 and the thing that reduces Layer 2's future workload.

### Layer 3 — Config schema version and canonical migration (future)

This is the on-disk TOML axis. It is **independent** of the fingerprint axis and only needed once we make *non-additive* TOML changes (rename/move/remove fields in the file format itself).

1. Add an explicit `schema-version` to the config file (distinct from the existing `$schema` URL, which is for editor validation).
2. At **load time**, migrate older config shapes forward into the single latest canonical struct *before* anything hashes them. Fingerprinting stays blissfully unaware of file-format history.
3. Pair with the **hybrid seam**: expose `ComponentConfig.ConfigHash()` on the type (pure struct hash + inclusion policy); keep the combiner in `fingerprint`.

The critical invariant: **migrate old TOML → latest canonical struct, then hash once.** A semantically no-op migration (rename `foo`→`bar`) must produce the *same* canonical struct, hence the same hash, hence no drift — handled by Layer 2's replay only if the *encoding* changed, and by Layer 3's normalization for the *file shape*. Do **not** keep parallel `V1.Hash()`/`V2.Hash()` methods on versioned structs: that couples the lock to a Go type identity instead of a simple integer, and forces two independent code paths to agree on a hash forever.

### Layer interaction

```text
TOML on disk ──Layer 3: migrate to canonical struct──► ComponentConfig
                                                              │
                                          Layer 1: HashInclude omits zero fields (default omitempty)
                                                              ▼
                                   Layer 2: ComputeIdentity[version] ──► InputFingerprint
                                                              │
                                          lazy replay + re-stamp on update
                                                              ▼
                                                      locks/<name>.lock
```

## Design decisions

### D1 — `Includable` vs `IgnoreZeroValue`

Both omit zero values; the difference is **control granularity and escape hatches.**

| | `Includable` per-field (chosen) | `IgnoreZeroValue` global |
| --- | --- | --- |
| Meaningful empties | Preserved via `fingerprint:"always"` | Lost — no opt-out |
| Per-field intent | Explicit, CI-audited | Invisible |
| Wiring | One helper + value-receiver method per struct | One option flag |

`IgnoreZeroValue` is a blunt global switch with no way to keep a build-meaningful zero. `Includable` gives the same default behavior **plus** the `always` escape hatch and a point-of-definition audit. Both move every hash once on adoption — that cost is absorbed by Layer 2 either way (see the switchover note), so it is not a differentiator.

### D2 — Mandatory explicit tags, default omitempty

Every fingerprinted field must carry `fingerprint:"-"`, `"omitempty"`, or `"always"` — there is no untagged state. Rationale:

- The *unsafe* failure direction is the false-negative (a meaningful field omitted → missed rebuild). Defaulting to omitempty tilts toward that direction, so the safety check must be loud, not implicit.
- A mandatory tag forces the "is this field's zero value build-meaningful?" decision **at the point of definition**, where the author has the context — better locality than a far-away exclusions registry.
- It *simplifies* the audit: assert every field has a valid tag value; delete the `expectedExclusions` map entirely.

Fully implicit (omitempty default, no tags, no audit) was rejected — it removes the only guard against the unsafe direction. `fingerprint:"omitempty"` mirrors Go's own `json:",omitempty"`; `"always"` and `"-"` read unambiguously alongside it.

### D3 — Content version vs format version in the lock

Reusing `ComponentLock.Version` for the algorithm would force a format-version bump (and the strict `Parse` gate would reject old locks outright). A separate `FingerprintVersion` keeps the format stable and old locks readable, enabling lazy migration instead of hard rejection.

### D4 — Method-on-type hashing

Adopt the **hybrid seam**: pure `ConfigHash()` on the config type, combiner in `fingerprint`. A full move was rejected (layering regression: I/O + crypto + algorithm versioning do not belong on a data type). See [Research](#where-the-hashing-logic-should-live).

## Alternatives considered

- **Global `IgnoreZeroValue`** — see D1. Same default behavior but no per-field escape hatch for meaningful zeros and no point-of-definition audit. Rejected.
- **Implicit omitempty (no mandatory tags, no audit)** — see D2. Removes the only guard against the unsafe false-negative direction. Rejected in favor of mandatory 3-way tags.
- **Content-hash the rendered config** (Go-modules style) instead of struct-hashing — would sidestep field-shape sensitivity, but we deliberately exclude many fields (`paths`, `publish`, snapshots) from the fingerprint, so a blanket content hash over-captures. Rejected.
- **Parallel versioned structs with per-struct `Hash()`** — couples locks to Go type identity and duplicates hashing logic per version. Rejected in favor of Layer 2's integer-versioned combiner + Layer 3 canonical migration.
- **Bump lock format `Version` and migrate eagerly** — eager migration rewrites every lock at once, the exact mass-churn we are trying to avoid. Rejected in favor of lazy per-component re-stamp.

## Incremental delivery

1. **PR A (Layer 1)**: shared `includeFingerprintField` helper + `HashInclude` on `ComponentConfig` and `PackageConfig`; tag every fingerprinted field with one of `-`/`omitempty`/`always`; rewrite the field-decision audit to assert valid-tag presence and drop the `expectedExclusions` registry. **Note:** flipping the default moves every hash, so PR A must land *with or after* PR B's version machinery — it registers as `computeV2`, not as a standalone change. Unit test: an unset `omitempty` field is hash-invisible; setting it drifts; an `always` field drifts even at zero.
2. **PR B (Layer 2)**: `FingerprintVersion` on `ComponentLock`; version-dispatched `ComputeIdentity`; replay + re-stamp in `checkFingerprintFreshness` and `update.go`. Unit test: old-version lock with unchanged inputs → `Current`; changed inputs → `Stale`; re-stamp on update.
3. **PR C (validation)**: scenario test (in the style of `scenario/component_changed_test.go`) — set a new `omitempty` field on a single component and assert only that lock drifts.
4. **PR D (Layer 3, later)**: `schema-version` field, load-time canonical migration, `ComponentConfig.ConfigHash()` seam. Gated on the first real non-additive TOML change.

Each PR is independently revertible. Because the Layer 1 default flip is a hash-moving change, PRs A and B ship together (or B first); the `fingerprint-version` omitempty stamp and churn policies ensure existing locks see zero diff until independently touched. Layer 3 migrates lazily on next write.

## Open questions

1. How many historical fingerprint versions should the registry retain before dropping the oldest? (Trade-off: replay coverage vs. dead code.)
2. Should a lazy re-stamp during a *read-only* command (`render`, `build` freshness check) write the lock back, or defer all writes to `component update`? Writing on read is surprising; deferring means freshness checks stay slightly slower until the next update.
3. For Layer 3, does `schema-version` live per-config-file or per-component? Per-file is simpler; per-component allows mixed-version projects during migration.
4. Should `omitempty` semantics use `reflect.Value.IsZero()` (Go's notion) or a config-aware notion of "unset" (e.g. nil pointer vs empty string)? Pointers would make "set to empty" expressible but complicate the structs.
5. Do we want a `component update --rehash` escape hatch that force-advances `FingerprintVersion` across the whole project (for when a change *is* intended to be global)?
6. Can the audit go further than tag-presence and *statically* flag fields whose zero value is likely meaningful (e.g. a `bool` defaulting true) and nudge toward `always`? Or is the point-of-definition tag plus code review sufficient?
