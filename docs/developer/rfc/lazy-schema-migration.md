# RFC 002: Lazy Schema Migration for Lock-File Fingerprints

- **Status**: Draft
- **Author**: @damcilva
- **Created**: 2026-06-04
- **Related code**:
  - [`internal/fingerprint/fingerprint.go`](../../../internal/fingerprint/fingerprint.go) — `ComputeIdentity`, `combineInputs`
  - [`internal/lockfile/lockfile.go`](../../../internal/lockfile/lockfile.go) — `ComponentLock`, version gate
  - [`internal/projectconfig/fingerprint_test.go`](../../../internal/projectconfig/fingerprint_test.go) — field-inclusion audit
  - [`internal/app/azldev/core/components/resolver.go`](../../../internal/app/azldev/core/components/resolver.go) — `computeFreshnessStatus`, `BuildDirtyChange`
  - [`internal/app/azldev/cmds/component/update.go`](../../../internal/app/azldev/cmds/component/update.go) — `Changed` decision, re-stamp write
  - [`internal/app/azldev/core/sources/synthistory.go`](../../../internal/app/azldev/core/sources/synthistory.go) — `FindFingerprintChanges` (synthetic changelog/release)

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
| **Lock content-hash version** | how inputs fold into the lock's stored hashes (`InputFingerprint` *and* `ResolutionInputHash`) | `fingerprint` combiner | No (implicitly v1) |
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

**`Includable` is resolved per-struct — every fingerprinted struct needs the method.** `hashstructure` looks up `Includable` on each struct it walks (and the whole tree is non-addressable, since the root is passed by value), so a `HashInclude` on `ComponentConfig` alone governs only `ComponentConfig`'s own fields. On any nested struct that lacks its own value-receiver `HashInclude`, the `omitempty`/`always` tags are **decorative** — `hashstructure` natively understands only `-`/`ignore`/`set`/`string`, so the tag passes the CI audit while the field is still hashed at zero, and G4 silently holds only at the top level. The audit (`fingerprint_test.go` registers ~10 fingerprinted structs: `ComponentConfig`, `ComponentBuildConfig`, `CheckConfig`, `PackageConfig`, `ComponentOverlay`, `SpecSource`, `DistroReference`, `SourceFileReference`, `ReleaseConfig`, `ComponentRenderConfig`) must therefore **also assert that every registered struct implements `Includable`** — so a new fingerprinted struct cannot ship with inert tags. All registered structs get the one-line delegating method.

Implement `Includable` on each fingerprinted struct, delegating to one shared helper:

```go
// includeFingerprintField reports whether a field participates in the hash.
// "-" fields never reach here (hashstructure skips them first). "always" fields
// are included unconditionally; "omitempty" (the default) is included only when
// the resolved value is non-zero.
func includeFingerprintField(t reflect.Type, field string, val reflect.Value) (bool, error) {
    sf, ok := t.FieldByName(field)
    if !ok {
        return true, nil
    }
    switch sf.Tag.Get("fingerprint") {
    case "always":
        return true, nil
    default: // "omitempty"
        return !val.IsZero(), nil
    }
}

// Value receiver: the root struct passed to hashstructure.Hash is not addressable.
//
// CRITICAL: hashstructure calls HashInclude(field, innerV) where innerV is
// ALREADY a reflect.Value (the field's value), boxed into the interface{}.
// So we must TYPE-ASSERT it, not reflect.ValueOf it. reflect.ValueOf(v) would
// describe the reflect.Value struct itself (always non-zero) → !IsZero() always
// true → omitempty silently never fires and Layer 1 no-ops. Verified against
// hashstructure v2.0.2 hashstructure.go:346 (`include.HashInclude(name, innerV)`).
func (c ComponentConfig) HashInclude(field string, v interface{}) (bool, error) {
    return includeFingerprintField(reflect.TypeOf(c), field, v.(reflect.Value))
}
```

**Why flipping the default is safe — fingerprints see the resolved config.** The usual objection to blanket omitempty is the false-negative footgun: a field whose zero is meaningful gets omitted and collides with "unset," so two semantically different configs hash the same and a rebuild is missed. That objection assumes we hash *raw user input*. We do not. `ComputeIdentity` runs on the **resolved, post-merge** config (`*result.config`, after defaults are applied). The omit predicate is therefore "the *resolved value* equals Go-zero," not "the user didn't type it." Consequences:

- Two configs that both resolve a field to zero build identically → hashing them the same is **correct**, not a collision.
- "Unset" never reaches the hasher — it has already been resolved to its default. If the default is non-zero, the field is non-zero and is included anyway. If the default *is* zero, then unset and explicit-zero resolve identically → same build → same hash → correct.

So the classic false-negative requires absence ≠ zero-default *at the point of hashing*, and post-merge resolution closes that gap. The load-bearing invariant is **G5's guarantee restated structurally: the fingerprint must see exactly the build-effective resolved config.** That invariant must already hold, or fingerprinting is broken independently of this change. The `fingerprint:"always"` escape hatch (plus the mandatory-tag audit) is cheap insurance against the invariant silently drifting later — e.g. if someone applies a default *after* fingerprinting.

**Result:** additive fields are drift-neutral **by construction** (G4) — an unset field omits identically to a field that never existed, with no version bump and no replay. Only setters drift (G2). The cost is one tag per field (verbose but mechanical) and two genuine edge cases (see below).

#### Edge cases under default omitempty

- **Meaningful zero with a non-zero default** (e.g. `int Jobs` defaulting to `4`, where `0` means serial). Post-merge: unset → `4` (included), explicit `0` → `0` (omitted-by-omitempty). These build differently *and* hash differently, so there is no collision — they are consistent. Such fields rarely trigger omission at all because the default keeps them non-zero. Tag them `always` only if a zero value must be distinguishable from a future change of default.
- **nil vs empty slice.** `reflect.Value.IsZero` on a slice is `IsNil`. A missing TOML key → nil → omitted; `key = []` → non-nil empty → included. Default omitempty thus makes nil-vs-empty a hash distinction that include-always collapses. Almost never observable — but a TOML formatter that strips empty arrays (or any round-trip that maps `[]`→absent) would flip hashes. **Tag rule: for any slice/map field where an explicit-empty value is reachable and build-meaningful, prefer `fingerprint:"always"`** so nil and empty both hash and the distinction can't silently move a fingerprint.

**Adopting this flip is itself a fingerprint-algorithm change** (every config's hash moves), so it does not land for free — it is absorbed by Layer 2's versioned replay rather than by rewriting locks. See Layer 2.

### Layer 2 — Versioned lock content with lazy replay (algorithm and default changes)

Stamp one **lock content-hash version** into the lock and teach the freshness check to **replay** older versions. The version governs *both* stored hashes (`InputFingerprint` and `ResolutionInputHash`) — they live in one lock, share one write event, and a single integer is the natural fit (see [scope note](#both-hashes-share-one-version) for why one version, not two):

1. Add `LockContentVersion int` (`toml:"lock-content-version,omitempty"`) to `ComponentLock`. **An absent field reads as `1`** — the current, pre-RFC algorithms — *not* `0`. (`0` is the Go zero value but no `v0` exists; map the zero to the baseline at read time: `ver := lock.LockContentVersion; if ver == 0 { ver = 1 }`.) The lock **format** `Version` stays `1`; this is a *content* version and is fully backward compatible.
2. Turn the combiner into a thin dispatcher over a small registry of historical algorithms, keyed by version. Each entry pairs the two compute functions; when only one algorithm changes, the other slot **reuses** the prior function (no version-neutral hash moves for the untouched one). Keep versions back to a declared floor (see [Registry floor](#registry-floor-and-forced-migration)):

   ```go
   type lockAlgo struct {
       fingerprint computeFn // produces InputFingerprint
       resolution  resolveFn // produces ResolutionInputHash
   }
   var lockAlgos = map[int]lockAlgo{
       1: {computeFP1, computeRes1}, // current (pre-RFC) algorithms — the implicit baseline
       2: {computeFP2, computeRes1}, // omitempty default (Layer 1); resolution UNCHANGED → reuse v1 fn
   }
   const currentLockContentVersion = 2
   const minSupportedLockContentVersion = 1
   ```

3. In `checkFingerprintFreshness`, compute at the **current** version. On mismatch, if the lock's recorded version `< current`, recompute at the lock's recorded version. If *that* matches the stored hash, the inputs are unchanged and only the algorithm evolved → treat as `FreshnessCurrent` and flag for silent re-stamp. Otherwise → `FreshnessStale`. (Phase 1 wires this for the fingerprint hash; the resolution hash reuses `computeRes1` until its algorithm first changes — see scope note.)
4. `component update` stamps `LockContentVersion = current` **only when it is already writing for an independent reason** (see the churn policy below). Migration is therefore **lazy and per-component**: a lock upgrades only when something independently touches it.

This resolves Problems 2 (for default changes), 3 (hashing bugfixes), and 5 (piecemeal rollout). It is the same lazy-forward-migration pattern Cargo/npm use, specialized to a content hash.

#### Both hashes share one version

`ComponentLock` carries two persisted content hashes: `InputFingerprint` (render inputs, via `hashstructure` + `Includable`) and `ResolutionInputHash` (upstream-resolution inputs — a flat SHA256 over seven explicit fields in `ComputeResolutionHash`, *not* a struct walk, so the omitempty/`Includable` story does not apply to it). Both have the **same evolution problem**: appending an input or reordering the fold moves every lock's hash → G1 churn.

We version them with **one shared integer**, not two axes, because: they co-locate in a single lock, they are written in the same `update` pass, and a paired registry lets either evolve independently while the other reuses its prior function. Two separate version fields would double the floor/replay/`--rehash` machinery for an input set (`ResolutionInputHash`) that changes rarely — YAGNI.

**Phasing.** Naming the field `lock-content-version` *now* is the one expensive-to-reverse decision (it is baked into the on-disk TOML schema the moment Layer 2 ships; renaming a persisted key is itself a migration). The fingerprint replay is wired in the first Layer 2 PR. **Resolution-hash replay is reserved, not yet wired** — the registry slot exists and `computeRes1` is reused, so the day `ComputeResolutionHash` first changes we add `computeRes2` and extend replay to its one comparison site (`checkResolutionFreshness` + the `resHashChanged` silent-write guard in `update.go`), with no schema change. Critically, `ResolutionInputHash` does **not** feed the synthetic changelog path, so its churn is a one-line lock rewrite + a wasted re-resolution, never a phantom release (unlike `InputFingerprint`; see [Downstream consumers](#downstream-fingerprint-consumers-blast-radius)).

#### Churn-avoidance policies (G1)

The version stamp is itself a potential source of spurious diffs — the exact thing G1 forbids. Two policies keep it invisible until a real change forces a write:

- **`lock-content-version` is `omitempty` in TOML.** A baseline (absent / version `1`) lock that is never otherwise touched never materializes the field, so its bytes stay identical. The field only appears in a lock that was *already* being rewritten for an independent reason. Existing checked-in locks therefore produce **zero diff** on the day this lands.
- **The `Changed` decision must replay *before* it compares — this is the subtle seam.** The naive read of the existing guard `if !result.Changed && !resHashChanged { return false, nil }` suggests the re-stamp harmlessly "rides the `Changed` path." **It does not.** In [`update.go`](../../../internal/app/azldev/cmds/component/update.go), `result.Changed` is set to `true` the instant `lock.InputFingerprint != identity.Fingerprint` — and `identity` is computed at the *current* version. That comparison sits **upstream** of the write guard. So after the v1→v2 switchover, the current-version hash differs from every stored v1 hash, `Changed` flips for ~every component, and we get exactly the mass auto-release-bump + mass lock rewrite G1 forbids. The fix is mandatory, not incidental:

  ```go
  // Replay at the lock's recorded version BEFORE deciding Changed.
  lockVer := lock.LockContentVersion
  if lockVer == 0 {
      lockVer = 1
  }
  replayed, _ := fingerprint.ComputeIdentityAt(lockVer, *result.config, releaseVer, opts)
  if lock.InputFingerprint != replayed.Fingerprint {
      result.Changed = true // a REAL input change under the lock's own algorithm
  }
  // else: hashes match under the old algorithm → inputs unchanged, only the
  // algorithm moved → NOT Changed. Advance the version only if some other real
  // change is already dirtying this lock.
  lock.InputFingerprint = identity.Fingerprint // current-version hash
  if result.Changed { // re-stamp piggybacks a real write; never its own trigger
      lock.LockContentVersion = currentLockContentVersion
  }
  ```

  The principle: **"changed?" is judged under the lock's own algorithm version; the stored hash is only upgraded to the current version when the lock is already dirty for a real reason.** (When resolution replay is wired, the same replay-before-compare applies to the `resHashChanged` silent-write guard.)

Together these make migration strictly opportunistic: a lock advances its version the next time its component changes for real, and not one commit sooner.

#### Registry floor and forced migration

Lazy migration means an untouched lock can sit at an old version **indefinitely** (G3 by design). That makes "keep the last *N* versions" a **correctness cliff, not a tuning knob**: if pruning drops the compute function a lock still depends on, replay becomes impossible → forced `FreshnessStale` → the mass rebuild/rewrite (and, via the downstream-consumer analysis below, mass changelog churn) the whole design exists to avoid. So the floor must be explicit and paired with an escape hatch, decided now:

- **`minSupportedLockContentVersion`** is a hard floor. A lock below it cannot be replayed and is treated as `Stale`. Dropping a registry entry is therefore a deliberate, breaking, announced act — never incidental cleanup.
- **`component update --rehash`** (Open Q#5, promoted to a requirement) force-advances every lock to the current version in one deliberate pass. This is the *only* sanctioned way to retire an old version: rehash the fleet first (one intentional, reviewed, fleet-wide commit), then raise the floor. Note this pass is a deliberate G1 exception — it *is* the eager migration G1 normally forbids, made safe by being explicit and operator-driven rather than a silent side effect.

**Mixed-toolchain hazard.** `go-toml` silently drops unknown fields, so an *older* azldev binary that rewrites a lock a newer binary had stamped will strip `lock-content-version`, regressing it to the baseline. On the next new-binary run the stored (baseline-replayed) hash won't match the current algorithm → spurious `Changed` + bump. This is the classic down-migration trap. Mitigation is a documented invariant ("all writers of a given `locks/` tree must be ≥ the version that tree was last stamped at"), enforced in CI by pinning the azldev version; a hard guard (refuse to write a lock whose on-disk version exceeds the binary's `currentLockContentVersion`) is a possible belt-and-suspenders.

#### Replaying across a changed input set — `{a,b,c}` → `{a,b,d}`

A lock stores **one opaque hash string** plus its `LockContentVersion`; it does *not* store the individual inputs. So when the measured set changes — say the fingerprint stops measuring `c` and starts measuring `d` — an existing lock (whose stored hash was computed over `{a,b,c}` at v1) is reconciled the only way an opaque hash allows: **recompute and compare, at the lock's own version.**

Split the change into its two halves; they are handled independently:

- **Adding `d`** is the additive case — `d` is tagged `omitempty`, so for any component that doesn't set it the hash is byte-identical (G4). Free. No version bump.
- **Dropping `c`** is what forces the version bump, and it is reconciled by replay:
  1. `computeFP2` (measures `{a,b,d}`) ≠ stored hash → mismatch.
  2. lock version (1) < current (2) → **replay `computeFP1`** (still measures `{a,b,c}`).
  3. v1-replay == stored hash? **Yes** → `a,b,c` unchanged since the lock was written; only the *measurement* evolved → `FreshnessCurrent`, lazy re-stamp. **No** → a real input moved → `Stale`, rebuild. Both correct.

So the bump is **not breaking**: replay answers "were the *old* inputs unchanged?" without rebuilding.

**The load-bearing constraint the rest of Layer 2 assumes implicitly:** *a replay function reads the live config struct.* `computeFP1` is Go code in **today's** binary, reading fields off **today's** struct. That is fine when the struct shape is unchanged (the omitempty flip, a combiner bugfix, a changed default — all replay against the same fields). But **physically deleting field `c` from the struct breaks `computeFP1`** — it can no longer read `c`, cannot reproduce the `{a,b,c}` hash, and every lock that set `c` is forced `Stale`. Removal-from-the-struct is therefore the one edit that silently defeats replay.

The way around it is a **deprecate-then-delete** two-step, both non-breaking:

1. **Bump to v2 measuring `{a,b,d}` but keep field `c` in the struct**, tagged `fingerprint:"-"` so `computeFP2` ignores it while `computeFP1` can still read it for replay. Every old lock replays clean at v1, is recognized as unchanged, lazy re-stamps to v2. Zero forced rebuilds.
2. **Only after the floor passes v1** (`minSupportedLockContentVersion = 2`, ideally after a deliberate `--rehash`) physically delete field `c`. `computeFP1` is already retired, so nothing reads `c` anymore.

> **Invariant:** a field may be physically removed from the config struct only after *every* registry entry that measured it has been retired below `minSupportedLockContentVersion`. Equivalently: retained replay functions and the struct they read must stay in sync — you cannot delete a field a live version still needs.

This makes "drop an input" a lazy, per-component migration rather than a fleet-wide rebuild — at the cost of carrying a deprecated field on the struct until its replay function ages out.

#### First concrete use: the Layer 1 switchover

Flipping the inclusion default to omitempty (Layer 1) moves every config's hash, so it cannot ship as a free additive change — it is **Layer 2's first real customer.** It registers as the `computeFP2` algorithm (omitempty default) alongside `computeFP1` (include-always), bumps `currentLockContentVersion` to 2, and is absorbed by replay: every existing lock recomputes clean at v1, is recognized as unchanged-inputs, and re-stamps to v2 *only when next written* per the churn policy above. (The resolution slot is unchanged across this bump — v2 reuses `computeRes1`.) No mass regen, no flag day. And because omitempty makes all future additive changes hash-neutral by construction (G4), it permanently **shrinks** the set of changes that need a Layer 2 version event at all — Layer 1 is both the first user of Layer 2 and the thing that reduces Layer 2's future workload.

### Layer 3 — Config schema version and canonical migration (future)

This is the on-disk TOML axis. It is **independent** of the fingerprint axis and only needed once we make *non-additive* TOML changes (rename/move/remove fields in the file format itself).

1. Add an explicit `schema-version` to the config file (distinct from the existing `$schema` URL, which is for editor validation).
2. At **load time**, migrate older config shapes forward into the single latest canonical struct *before* anything hashes them. Fingerprinting stays blissfully unaware of file-format history.
3. Pair with the **hybrid seam**: expose `ComponentConfig.ConfigHash()` on the type (pure struct hash + inclusion policy); keep the combiner in `fingerprint`.

The critical invariant: **migrate old TOML → latest canonical struct, then hash once.** A semantically no-op migration (rename `foo`→`bar`) must produce the *same* canonical struct, hence the same hash, hence no drift — handled by Layer 2's replay only if the *encoding* changed, and by Layer 3's normalization for the *file shape*. Do **not** keep parallel `V1.Hash()`/`V2.Hash()` methods on versioned structs: that couples the lock to a Go type identity instead of a simple integer, and forces two independent code paths to agree on a hash forever.

**Caveat — `hashstructure` hashes the struct type name.** It mixes `reflect.Type.Name()` into the hash, so a Layer-3 migration that moves content into a *renamed* Go struct changes the fingerprint even when the content is byte-identical. "Rename is drift-neutral" therefore holds only if the canonical struct **keeps the original type name**, or the rename is shipped as a Layer-2 version bump that absorbs it. Prefer keeping the type name; reserve the version bump for when the type genuinely must be renamed.

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

## Downstream fingerprint consumers (blast radius)

The versioned-replay story in Layer 2 must hold for **every** reader of `InputFingerprint`, not just the two paths it grew up around. This is the migration blast-radius map; each consumer's behavior under a v1→v2 switchover is stated explicitly.

| Consumer | Reads | Compares | Migration behavior required |
| -------- | ----- | -------- | --------------------------- |
| `checkFingerprintFreshness` (resolver) | recomputed identity | vs stored hash | Replay at lock version (Layer 2 core) |
| `component update` `Changed` decision | recomputed identity | vs stored hash | **Replay before `Changed`** (see churn policy / M2 seam) |
| `synthistory.FindFingerprintChanges` | stored hash strings across git history | adjacent commits | **No change needed — if migration stays lazy** |
| `synthistory.BuildDirtyChange` | recomputed (current ver) | vs stored `headLock` hash | **Replay at headLock version** before declaring dirty |
| `ResolutionInputHash` staleness/write | recomputed resolution hash | vs stored | **Shares the version; replay reserved, not yet wired** |

### The synthetic changelog/release path is the real hazard

[`synthistory.go`](../../../internal/app/azldev/core/sources/synthistory.go) turns fingerprint movement into **user-visible, shipped** package state — `%autochangelog` entries and `%autorelease` increments. There are two distinct comparators, and the design resolves them asymmetrically.

- **`FindFingerprintChanges` (historical walker)** does a raw, version-blind string compare of `InputFingerprint` across the lock's git history and emits a synthetic changelog/release entry on every change. Making it genuinely version-aware is hard-to-infeasible — it only has committed *strings*, no inputs to replay. **It does not need to be**, *provided migration stays strictly lazy.* Under the churn policy, a version bump only ever rides a commit where a real input also changed, so there is never a version-only commit in history for the walker to misread. The migration folds honestly into that real change's entry. **This is a design decision, not a code fix:** the v1→v2 conversion is an *accepted, per-component, notable* changelog event that piggybacks a real change.
  - **Trap:** this only holds while migration is lazy. A fleet-wide `--rehash` (or the M2 bug where `Changed` flips for everyone) converts *phantom* → *honest-but-fleet-wide* — a truthful but fleet-wide release bump, i.e. **G1 is dead.** "Accept as notable" is therefore conditional on **migration never riding a version-only or fleet-wide write** (the `--rehash` floor pass excepted, because it is deliberate and operator-driven).
- **`BuildDirtyChange` (live dirty check)** compares a *recomputed* current-version (v2) hash against the *stored* (possibly v1) `headLock.InputFingerprint` and declares dirty on inequality. "Accept as notable" does **not** save this path: post-switchover an *unchanged* component would read **dirty on every `render`/`build`** until re-stamped — a persistent, recurring spurious signal, worse than a one-time entry. The fix is **free**: it is the *same replay Layer 2 already owes the freshness check* — replay at `headLock`'s recorded version before declaring dirty. One additional call site for logic already being written, no new mechanism.

**Net:** M1 is not "make the changelog walker version-aware" (hard, maybe infeasible). It is two things already on the books — (1) the strict lazy churn policy, so the walker never sees a version-only commit; and (2) extend the freshness replay to `BuildDirtyChange`, one extra call site.

### `ResolutionInputHash` — shares the version, replay deferred

`ComponentLock` carries a *second* persisted content hash, `ResolutionInputHash`, with its own staleness logic and its own silent-write path (it writes when only `resHashChanged`, never flipping `Changed`). It has the **identical** evolution problem as `InputFingerprint`: any future change to `ComputeResolutionHash`'s algorithm moves every lock's hash — exactly the mass-churn this RFC exists to prevent.

The single `lock-content-version` covers it (see [Both hashes share one version](#both-hashes-share-one-version)). What differs is **blast radius**, which is why we wire its replay later, not now:

- `ResolutionInputHash` does **not** feed `synthistory` — so an algorithm change can never mint a phantom changelog/release (the M1 hazard is fingerprint-only). Worst case is a one-line `resolution-input-hash` rewrite per lock plus a wasted re-resolution that usually yields the same commit. Churn, not corruption.
- It is a flat seven-field SHA256, not a struct walk, so the Layer 1 omitempty flip leaves it untouched — it has no pending v1→v2 event. Its registry slot stays `computeRes1` until its inputs genuinely change.

**Decision:** name the field for the general case now (`lock-content-version`); wire fingerprint replay in Layer 2's first PR; reserve resolution replay (slot present, prior fn reused) and wire it the day `ComputeResolutionHash` first changes — a localized follow-up with no schema change. This fixes the one irreversible thing (the persisted key name) without speculative code (KISS/YAGNI on the second replay).

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

Reusing `ComponentLock.Version` for the algorithm would force a format-version bump (and the strict `Parse` gate would reject old locks outright). A separate `LockContentVersion` keeps the format stable and old locks readable, enabling lazy migration instead of hard rejection. It is named for the *general* case — it versions every content hash the lock stores (`InputFingerprint` now, `ResolutionInputHash` when its replay is wired) — because the persisted TOML key is the one thing that is expensive to rename after ship.

### D4 — Method-on-type hashing

Adopt the **hybrid seam**: pure `ConfigHash()` on the config type, combiner in `fingerprint`. A full move was rejected (layering regression: I/O + crypto + algorithm versioning do not belong on a data type). See [Research](#where-the-hashing-logic-should-live).

Two constraints keep the seam from eroding back into the rejected methods-on-type design: **`ConfigHash()` must stay version-frozen** (it computes exactly one algorithm; it does *not* dispatch over versions — a single method "can't replay its own past"), and **the combiner is the sole version authority.** Version dispatch lives entirely in `fingerprint`'s registry; `ConfigHash()` is just the current pure-config step it calls. Keep `ConfigHash()` unexported-or-narrow if practical, so callers cannot route around the registry to get a raw, version-agnostic hash.

## Alternatives considered

- **Global `IgnoreZeroValue`** — see D1. Same default behavior but no per-field escape hatch for meaningful zeros and no point-of-definition audit. Rejected.
- **Implicit omitempty (no mandatory tags, no audit)** — see D2. Removes the only guard against the unsafe false-negative direction. Rejected in favor of mandatory 3-way tags.
- **Content-hash the rendered config** (Go-modules style) instead of struct-hashing. The naive version of this — "hash all the bytes" — over-captures, since we deliberately exclude many fields (`paths`, `publish`, snapshots) from the fingerprint. The *stronger* form is a **canonical-projection hash**: serialize only the included fields, keys sorted, and hash those bytes — immune to field-shape drift without per-field reflection tags. We still stay with `hashstructure` + `Includable` because our inclusion policy is **conditional** (omitempty = include-if-non-zero, evaluated on the resolved value), which a static byte serializer would have to re-implement anyway — so the projection hash buys field-shape immunity at the cost of reimplementing the very predicate `Includable` already gives us, plus a second serialization format to keep stable forever. Rejected on that basis, but recorded as the principled alternative; it is the one foundational choice that would be expensive to reverse post-adoption.
- **Parallel versioned structs with per-struct `Hash()`** — couples locks to Go type identity and duplicates hashing logic per version. Rejected in favor of Layer 2's integer-versioned combiner + Layer 3 canonical migration.
- **Bump lock format `Version` and migrate eagerly** — eager migration rewrites every lock at once, the exact mass-churn we are trying to avoid. Rejected in favor of lazy per-component re-stamp.

## Incremental delivery

1. **PR A (Layer 1)**: shared `includeFingerprintField` helper + a delegating value-receiver `HashInclude` on **every** fingerprinted struct (all ~10 registered in `fingerprint_test.go`, not just `ComponentConfig`/`PackageConfig` — see the per-struct resolution note in Layer 1); tag every fingerprinted field with one of `-`/`omitempty`/`always`; rewrite the field-decision audit to (a) assert valid-tag presence and (b) assert every registered struct implements `Includable`, then drop the `expectedExclusions` registry. **Note:** flipping the default moves every hash, so PR A must land *with or after* PR B's version machinery — it registers as the `computeFP2` algorithm, not a standalone change. Unit tests: a zeroed `omitempty` field hashes **equal to its absence-equivalent** (not merely "setting it drifts" — that positive-direction test passes even if `HashInclude` is a no-op, so it must be paired with the zero-equals-absent assertion that actually exercises omission); an `always` field drifts even at zero.
2. **PR B (Layer 2)**: `LockContentVersion` on `ComponentLock` (+ `ComponentLockData` and `populateFromLock`, so the replay site can read the version); a paired version registry (fingerprint + resolution compute fns) with a `minSupportedLockContentVersion` floor; fingerprint replay-before-`Changed` in `update.go`; fingerprint replay in `checkFingerprintFreshness` **and `BuildDirtyChange`** (same replay logic, two call sites). Resolution-hash replay is *reserved* — the registry slot reuses `computeRes1`; not wired until `ComputeResolutionHash` first changes. Unit tests: old-version lock with unchanged inputs → `Current` and **not** `Changed`; changed inputs → `Stale`; re-stamp only on an already-dirty write.
3. **PR C (validation)**: scenario test (in the style of `scenario/component_changed_test.go`) — set a new `omitempty` field on a single component and assert only that lock drifts.
4. **PR D (Layer 3, later)**: `schema-version` field, load-time canonical migration, `ComponentConfig.ConfigHash()` seam. Gated on the first real non-additive TOML change.

Each PR is independently revertible. Because the Layer 1 default flip is a hash-moving change, PRs A and B ship together (or B first); the `lock-content-version` omitempty stamp and churn policies ensure existing locks see zero diff until independently touched. Layer 3 migrates lazily on next write.

## Open questions

1. Should a lazy re-stamp during a *read-only* command (`render`, `build` freshness check) write the lock back, or defer all writes to `component update`? Writing on read is surprising; deferring means freshness checks stay slightly slower until the next update. (Leaning: defer all writes to `update`, keeping reads side-effect-free.)
2. For Layer 3, does `schema-version` live per-config-file or per-component? Per-file is simpler; per-component allows mixed-version projects during migration.
3. Should `omitempty` semantics use `reflect.Value.IsZero()` (Go's notion) or a config-aware notion of "unset" (e.g. nil pointer vs empty string)? Pointers would make "set to empty" expressible but complicate the structs.
4. Can the audit go further than tag-presence and *statically* flag fields whose zero value is likely meaningful (e.g. a `bool` defaulting true) and nudge toward `always`? Or is the point-of-definition tag plus code review sufficient?
5. Should the mixed-toolchain hazard get a hard write-time guard (refuse to write a lock whose on-disk version exceeds the binary's `currentLockContentVersion`), or is the CI version-pin invariant enough?

*Resolved in-text (recorded here so they aren't re-litigated):* registry retention is a **floor**, not "last N" (M8 / Registry floor); `--rehash` is the sanctioned forced-migration pass (promoted from a question to a requirement); absent `LockContentVersion` reads as `1`; one shared `lock-content-version` covers both stored hashes, with resolution-hash replay reserved (slot present, fn reused) until `ComputeResolutionHash` first changes.
