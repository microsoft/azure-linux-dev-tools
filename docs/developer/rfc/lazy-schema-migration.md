# RFC 002: Lock-File Fingerprint Reset and Lazy Schema Migration

- **Status**: Draft
- **Author**: @damcilva
- **Created**: 2026-06-04
- **Related code**:
  - [`internal/fingerprint/fingerprint.go`](../../../internal/fingerprint/fingerprint.go) - `ComputeIdentity`, `ComputeResolutionHash`, `combineInputs`
  - [`internal/lockfile/lockfile.go`](../../../internal/lockfile/lockfile.go) - `ComponentLock`, `Parse` format-version gate
  - [`internal/projectconfig/fingerprint_test.go`](../../../internal/projectconfig/fingerprint_test.go) - field-inclusion audit
  - [`internal/app/azldev/core/components/resolver.go`](../../../internal/app/azldev/core/components/resolver.go) - `computeFreshnessStatus`, `checkFingerprintFreshness`
  - [`internal/app/azldev/cmds/component/update.go`](../../../internal/app/azldev/cmds/component/update.go) - `Changed` decision, re-stamp write
  - [`internal/app/azldev/cmds/component/changed.go`](../../../internal/app/azldev/cmds/component/changed.go) - `classifyComponent`, `haveMatchingFingerprints` (CI classification)
  - [`internal/app/azldev/core/sources/synthistory.go`](../../../internal/app/azldev/core/sources/synthistory.go) - `FindFingerprintChanges`, `BuildDirtyChange` (synthetic changelog/release)
  - [`internal/app/azldev/core/sources/sourceprep.go`](../../../internal/app/azldev/core/sources/sourceprep.go) - `computeCurrentFingerprint`

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

`configHash` is then folded together with the source identity, overlay file hashes, manual bump, and distro release version into a domain-separated SHA256 (`combineInputs`). Field inclusion is policed by [`TestAllFingerprintedFieldsHaveDecision`](../../../internal/projectconfig/fingerprint_test.go): every field of every fingerprinted struct must be consciously categorized as **included** (no tag) or **excluded** (`fingerprint:"-"`). The safe default is *included* - a new field contributes to the hash unless told otherwise.

Drift is detected in [`resolver.go`](../../../internal/app/azldev/core/components/resolver.go): `computeFreshnessStatus` → `checkFingerprintFreshness` recomputes the identity and compares it to `InputFingerprint`, yielding `FreshnessCurrent` or `FreshnessStale`. `component update` ([`update.go`](../../../internal/app/azldev/cmds/component/update.go)) re-stamps the lock and flips a user-visible `Changed` flag whenever the fingerprint moves.

### The three version axes

As the tool matures, three *independent* notions of "version" are emerging. Conflating them is the source of the problems in this RFC:

| Axis | Versions what | Lives where | Exists today? | Forced-migration verb |
| ---- | ------------- | ----------- | ------------- | --------------------- |
| **Config schema version** | on-disk TOML field shape | load / migration layer | No | `config migrate` (future) |
| **Lock content-hash version** | how inputs fold into the lock's stored hashes (`InputFingerprint` *and* `ResolutionInputHash`) | `fingerprint` combiner | No (implicitly v1) | `component migrate` |
| **Lock file format version** | lock file serialization | `lockfile` | Yes (`Version = 1`) | - (frozen at `1`) |

### The problem

Because field inclusion defaults to *included*, **adding any new fingerprinted config field re-hashes every component**, even components that never set the field. `hashstructure` hashes a zero-value field identically to a present-but-empty field - but *differently* from a field that does not exist in the struct at all. So the moment the Go struct gains `Foo string`, every component's `configHash` changes, every `InputFingerprint` changes, and every `*.lock` shows drift on the next `component update`.

Concretely: we add field `foo` and set `foo = "baz"` on package `bar`. The desired outcome is that **only** `bar.lock` drifts. The actual outcome today is that **all** lock files drift.

**The root concern is git churn, not rebuilds.** The mass rebuild is a knock-on effect; the thing we actually want to protect is the **lock-file diff in a PR**. A change that touches one package should produce exactly one changed `*.lock` - ideally zero changed bytes in any other lock file, in any way. Lock files should change *only* when there is a real, per-component change. Clean diffs keep PRs reviewable, keep `git blame` meaningful, and make "this lock moved" a trustworthy signal that *that component's* inputs actually changed. The rebuild fan-out follows for free once the diffs are clean.

There is a harder variant lurking behind the additive case: **non-additive** schema changes - renaming a field, removing one, changing a baked-in default, or fixing a bug in the hashing logic itself. These legitimately change the *meaning* of the config without changing user intent, and we will eventually need to absorb them without forcing every consumer to rebuild.

### The substrate problem: replay only works if old algorithms stay frozen

The natural fix for non-additive change is **versioned replay**: stamp an algorithm version into the lock, keep the old algorithm around, and when a lock is behind, recompute with *its* algorithm to ask "were the inputs actually unchanged, or did only the encoding move?" If unchanged, accept the lock without a rebuild.

Replay only works if an old algorithm function can faithfully reproduce the hash it produced when the lock was written. **On the current `hashstructure` substrate, it cannot** - a "frozen" algorithm function is not actually frozen:

- Its body is `hashstructure.Hash(component, …)`, which **reflects over the live Go struct**. Add a field later and the old function now sees that field (at zero value, included) → its output moves → it can no longer reproduce the historical hash. So *adding* a field breaks *replay of older versions*, which is exactly the additive case we are trying to make free.
- It also resolves the live **method set**: once `ComponentConfig` implements `Includable`, the same `hashstructure.Hash` call silently switches inclusion behavior, with no per-call opt-out (the interface is resolved automatically).

An incremental "flip the default to omitempty, lazily migrate" plan therefore **cannot keep its central promise.** "Additive fields are drift-neutral by construction" holds only for locks already at the new version; for the older locks that lazy migration deliberately leaves alone, the next field addition forces a hash change anyway. You do not avoid the mass rebuild - you defer it to the first field addition, and you build the whole replay apparatus on a substrate that makes replay unsound.

### The opportunity: a coordinated cutover is already scheduled

The project has a **dev→prod environment cutover** coming that forces a full rebuild regardless. This is a *coordinated cutover* - a one-time, distro-wide switch with no mixed-version window, the sanctioned moment to make changes that cannot be made lazily. That changes the calculus completely. The entire "lazy" framing exists to *avoid* a mass update; if exactly one sanctioned mass update is already on the calendar, the strategy inverts:

> **Lazy migration is for the cheap and additive. The one free rebuild is a budget - spend it exclusively on the one-way doors that are cheap now and a coordinated-cutover-only change later.**

This RFC therefore has two parts: **(1)** a one-time **reset** at the dev→prod cutover that replaces the hashing substrate with one whose old algorithms are *genuinely* frozen, and **(2)** a **post-reset lazy migration** mechanism (versioned registry + replay) that rides that clean substrate for the rare genuine algorithm change thereafter. Part 2 is what the original "lazy" design was reaching for; part 1 is what makes it sound.

### Goals

- **G1 (primary, non-functional): no spurious lock-file diffs *after the reset*.** Once prod locks exist, landing a config-schema or hashing change must not rewrite `*.lock` files for components whose effective inputs are unchanged. The reset itself is the *one* sanctioned exception, absorbed by the already-scheduled rebuild.
- **G2: only real changes drift.** Post-reset, a lock changes iff that component's build-effective inputs changed.
- **G3: piecemeal, lazy migration post-reset.** Genuine algorithm evolution after the reset rolls out per-component, riding independent changes, never as a big-bang.
- **G4: additive fields are drift-neutral by construction - *truly*, not just for new locks.** On the projection substrate (below) an unset additive field is invisible to *every* lock including old ones, because old versions emit only the fields their tags include - a field added later is not in any shipped version's tag set, so it cannot move an existing hash.
- **G5: correctness backstop preserved - relative to the lock's own content version.** Never silently under-rebuild: a genuine input change must drift any lock *whose version measures that input*. An input a lock's version does not measure (a field introduced later, a not-yet-adopted newly-measured input) is correctly invisible until the lock migrates - lazy non-adoption is by contract, not a miss. Replay may accept encoding/over-capture changes; it must never mask a behavior-changing one within the lock's own measured set.
- **G6 (new, hard): back-compatible reads for synthetic history.** The new binary must still **read** pre-reset locks across git history (synthetic changelog/release walks them), even though it **writes** only the new format. Reading never recomputes a historical hash - it compares stored strings only.

## Problem inventory

| # | Problem | Root cause | Severity |
| - | ------- | ---------- | -------- |
| 1 | Adding a config field drifts every lock, even unaffected components | Field inclusion defaults to *included*; zero-value ≠ absent in struct hash | Mass rebuild |
| 2 | No way to land a semantically no-op schema change (rename/move) without drift | Fingerprint hashes raw struct shape, not normalized intent | Mass rebuild |
| 3 | No way to evolve the hashing algorithm (bugfix, input reorder) without drift | `combineInputs` has no version; old and new outputs are incomparable | Mass rebuild + lock churn |
| 4 | No on-disk config schema version | `ConfigFile` has a `$schema` URL but no version field | Blocks managed migration |
| 5 | Migration is all-or-nothing | Freshness check is binary match/no-match against one stored hash | No piecemeal rollout |
| 6 | Versioned replay is unsound on the current substrate | "Frozen" algorithm = `hashstructure.Hash` over the **live** struct/method-set; adding a field moves the old function's output | Replay cannot reproduce historical hashes |

Problems 1-5 share a shape: a change that *should* be invisible to most components is forced to be visible to all of them, because the fingerprint cannot distinguish "input changed" from "encoding changed." Problem 4 is the missing primitive for managed config evolution. Problem 5 is the property we want from any post-reset solution - **per-component, lazy** migration. Problem 6 is the one that kills the *incremental* path outright: the very mechanism that would make problems 1-3 free (versioned replay) is unsound while the substrate reflects the live struct. Fixing 6 is what the reset buys.

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

The struct's type name *is* part of the hash (`hashstructure` mixes in `reflect.Type.Name()`), so a rename of the Go type moves every hash even when content is byte-identical.

**Why this substrate cannot host frozen replay.** Every property above is resolved *at hash time against the live program*, not against a pinned description of the v1 encoding:

- The set of fields walked is whatever the struct has *now* - add a field, and last year's `computeFP1` (whose body is still just `hashstructure.Hash(component)`) now includes it.
- Whether `Includable` is consulted depends on whether the type implements it *now* - not on what was true when v1 locks were written.
- A `value` vs `pointer` receiver subtlety even decides whether the root struct's `HashInclude` is seen at all (the top-level value is not addressable).

A function meant to be "the v1 algorithm, forever" therefore changes meaning every time the struct or its method set changes. That is the disqualifier for the incremental plan (Problem 6) and the motivation for the projection substrate below, whose v1 projection emits only its version-tagged fields and reads neither the method set nor the type name - immune to all three.

## Change taxonomy

Not every config change should be treated the same way. The right mechanism depends on what kind of change it is. This taxonomy drives the design.

| Class | Example | Should unaffected locks drift? | Mechanism |
| ----- | ------- | ------------------------------ | --------- |
| **Additive field** | new `foo` field, unset on most components | No | **Free, no bump.** Tag the new field `vN..*` (current version, omit-if-zero); a component that leaves it unset emits identical bytes, so no shipped hash moves - adding an omit-if-zero field to the live version is the one output-preserving no-bump edit. A setter whose lock is *already at* version N drifts; a setter on an older, un-migrated lock is left unchanged (false-fresh) until that lock next re-stamps or is migrated - the same lazy contract as a newly-measured input. To force the field onto the whole fleet now, do an explicit `component migrate`. **Tagging a build-meaningful-zero field `!vN..*` (always-emit) on the live version is *not* this case** - see the note below. |
| **Additive with non-zero default** | new field defaulted to `"auto"` via defaults merge | No | **Bump + replay.** The default resolves non-zero on *every* component, so it is emitted everywhere and would move every hash - omit-if-zero can't save it. Bump and tag the field `v(N+1)..*`; old locks **replay at their version** (whose set excludes it), match their stored digest → recognized unchanged → lazy re-stamp, no rebuild. |
| **Default change on an *existing* field** | bump `jobs` default `4`→`8` | Yes - every component's effective input moved | **Not lazy-maskable.** Replay recomputes the *current* config (now resolving to `8`) under the old algorithm → `jobs=8` ≠ stored `jobs=4` → genuine fleet-wide drift; replay cannot suppress it because the resolved value genuinely changed for everyone. Escape hatch: `config migrate` writes the *old* resolved value explicitly (`jobs=4`) into each config **before** moving the default - existing components then pin the old value (no drift) and only new components pick up `8`. Without that pre-pass it is a legitimate (if large) fleet rebuild, not a bug. |
| **Rename / move** | `foo` → `bar`, same semantics | No | **Schema migration + bump + replay.** Migrate old TOML → canonical struct (the rename lands in the struct), then tag the renamed field `v(N+1)..*`. Old locks replay at their version and are recognized unchanged → lazy re-stamp, no rebuild. |
| **Semantic change** | meaning of `foo` changes; output differs | Yes - that's correct | **None.** The build output genuinely differs, so the lock *should* drift. Replay at the old version would (correctly) mismatch → `Stale` → rebuild. Nothing to suppress. |
| **Hashing bugfix** | overlay ordering bug in the combiner | No | **Bump + replay.** Ship the fixed combiner as the version-`N+1` half of `computeFP(N+1)`; old locks replay at the old (buggy) version. If their inputs are unchanged the buggy digest still matches → recognized unchanged → lazy re-stamp to the fixed version, no rebuild. |
| **Newly measured input** | start folding in a new overlay source or identity element | No | **Bump + replay.** A non-config input is added in the combiner half of `computeFP(N+1)` (a config field would be tagged `v(N+1)..*` instead). Old locks replay at their version, which didn't fold it in, match their stored digest → recognized unchanged → lazy re-stamp, no rebuild. **Caveat:** until a lock migrates, replay is *blind* to the new input, so a change to it reads as fresh (false-fresh) - if it is build-critical, force a `component migrate` pass instead of riding lazy adoption (see [churn-avoidance](#churn-avoidance-policies-g1)). |
| **Field removal** | drop deprecated `foo` | No, if nobody set it | **Deprecate-then-delete (+ bump for setters).** Close the field's range at the prior version (`vK..*` → `vK..vN`, so v(N+1) stops measuring it) but **keep the field on the struct** so older versions can still read it for replay. Only after the floor passes vN (ideally after a `component migrate`) physically delete the field. Setters drift on the bump; non-setters replay clean. |
| **Resurrected field** | re-measure a previously-dropped `foo` | Depends - only if its value moved | **Tag edit (+ bump).** Append a new range to the field's set (`v1..v3,v8..*`) so v8+ measures it again while v1-v7 stay byte-identical (golden-vector-enforced). If the field was already physically deleted, bring it back as a fresh additive field tagged `v8..*`. The earlier life and the revival never collide because each version's output is pinned independently. |

The recurring requirement across the "No" rows is the same: **distinguish a change in user intent from a change in encoding, and only drift on the former.** Note the first row: on the projection substrate, a new field is added to `projectVN` as *omit-if-zero*, so a component that does not set it emits identical bytes and stays hash-neutral - *for every lock, old or new*, because old configs never set the brand-new field. Adding it does not move any existing hash (no shipped lock set it), so it needs no version bump. Part 2 then carries only the genuinely hard cases (rows 2, 5, and post-reset renames/removals). The shared move in every "Bump + replay" row is the same primitive - **increment the content version, keep the old `projectVN` as a frozen replay projection, and let unchanged locks re-stamp lazily** - detailed in [Part 2](#part-2-post-reset-lazy-migration).

**Adding a field as `!` (always-emit) to a *live* version is a version-bump event, not a free additive.** A zero-valued `!` field emits bytes for *every* component, including those that never set it, so it moves every lock the instant it lands on the current version - the opposite of "leave old locks alone." Build-meaningful-zero fields must therefore be introduced at a *new* version (`!v(N+1)..*`) and absorbed by replay, exactly like any other non-additive change. Only omit-if-zero additions (`vN..*`) are free on the live version.

> **`projectVN`** is shorthand used throughout this RFC for the canonical *projection at content-version N* introduced by this design (defined in [Substrate options](#substrate-options) and [The projection substrate](#the-projection-substrate)). It is a per-version function `projectVN(cfg) []byte` - **generated** from declarative version-set tags on the struct fields (see [Version-tagged field selection](#version-tagged-field-selection)), not hand-written. `projectV1` measures the fields whose tag set includes v1; `projectV2` the next version, and so on. Each generated `projectVN` freezes once *superseded* (the next version is registered): its source tags no longer move, its generated code is checked in, and golden vectors backstop it. The live version stays editable for output-preserving additions.

## Research

### Substrate options

Two substrates can produce a content fingerprint of the resolved config. The difference that matters here is **whether an old algorithm function can be frozen.**

- **`hashstructure` + `Includable` (rejected as the substrate).** Keeps existing hashes byte-identical and gives per-field omission via `HashInclude`. But, as established above (Problem 6), a function built on `hashstructure.Hash` reflects over the live struct and method set, so it cannot be a frozen historical algorithm. It also requires a value-receiver `HashInclude` on *every* nested fingerprinted struct and a subtle `v.(reflect.Value)` type-assert to work at all - brittle plumbing in service of a substrate that still can't host sound replay.
- **Canonical projection + stdlib hash (chosen).** Split the two jobs `hashstructure` fuses - *field selection* and *hashing* - into explicit steps. Field selection is **declared per field** as a version-set in the `fingerprint` tag (`fingerprint:"v1..*"`); a `go generate` step emits a per-version `projectVN(cfg) []byte` function that serializes the fields whose set includes version N in a canonical, sorted, self-delimiting byte form, and an stdlib `sha256` hashes those bytes. Because a shipped `projectVN` is frozen checked-in code, it does not see fields added later, does not depend on the type's method set, and does not depend on receiver subtleties. It is a genuinely frozen pure function of `(cfg)` per version - the property replay requires. The cost is owning the generator and **golden hash vectors** per version (a checked-in `(config, version) → hash` table) so the generator itself is CI-backstopped.

The projection substrate is what makes G4 true for old locks and what makes Part 2's replay sound. It is adopted at the reset (below), not incrementally.

### How other tools version lock state

- **Cargo (`Cargo.lock`)** carries an explicit `version = 4` at the top of the lock and teaches `cargo` to read older versions, upgrading in place on the next write. Migration is lazy - touching the lock upgrades it.
- **npm (`package-lock.json`)** uses `lockfileVersion` and supports reading v1/v2/v3, rewriting to the current version on install.
- **Terraform state** stores a `version` and a `terraform_version`; state is upgraded forward on use, never downgraded.
- **Go modules** avoid the problem entirely by hashing *content* (`h1:` dirhashes) rather than a struct shape, so adding metadata fields never perturbs existing sums.

The common pattern: an **integer version stamped into the persisted artifact**, plus the ability to **read and replay older versions**, plus **lazy forward-migration on write**. We keep `ComponentLock.Version` (the lock *format* slot) fixed at `1` and carry the *content* version **inside the `InputFingerprint` token** (`v<N>:sha256:…`) rather than in a separate struct field - one atomic value, no version/digest desync, no new TOML field for an old binary to mishandle. The Go-modules lesson is the deepest one: hashing *content* rather than struct shape is what makes additive metadata free - the canonical-projection substrate is our version of that lesson.

**Where this design goes beyond the precedent.** All four tools above keep exactly **one** active algorithm: Cargo/npm/Terraform rewrite the *whole* artifact to the current version on next touch (eager-on-write), and Go modules sidestep replay entirely by never re-migrating semantics. **None of them keeps N historical hashing algorithms alive simultaneously across an indefinitely-unmigrated fleet** - which is exactly Part 2's behavior. The citations support "version stamp + lazy forward-migrate on write"; they do *not* cover "frozen algorithms coexisting forever." That coexistence is justified here on its own terms (it is what avoids a fleet rebuild on every algorithm change), and its one real cost - append-only registry growth - is bounded by the [floor-advance cadence](#registry-floor-and-forced-migration), not by precedent.

### Where the hashing logic should live

With the projection substrate the fingerprint algorithm decomposes into two steps. **Both are versioned together** by the single lock content version - the version pins the *entire* fingerprint computation, not just the field list:

1. **Projection** - `projectVN(config)` names and serializes the config fields version N measures. This is *about the config type*, but it is data extraction, not hashing: it returns canonical **bytes**, not a hash.
2. **Combiner / orchestration** - reads overlay file contents (needs `opctx.FS`), folds in source identity / releasever / bump, applies domain separation, and runs `sha256` over the projection bytes plus those non-config inputs. None of these are config fields, but the combiner equally decides *what is measured*: starting to fold in a new overlay source, adding an identity input, or reordering the fold all change the digest exactly as a projection change does.

So the per-version compute function in the registry is the **whole algorithm** - `computeFPN` = `projectVN` + the combiner step frozen at version N. "Watching another field" splits cleanly: if it is a *config* field, it goes in `projectV(N+1)`; if it is a *non-config* input (a new overlay source, a new identity element), it goes in the combiner half of `computeFP(N+1)`. Either way it is a content-version bump absorbed by replay, never a silent hash move. The combiner is the **sole version authority**: it owns the registry and the dispatch, and `projectVN` is just the frozen config-extraction step it calls.

Expose the projection on (or beside) the config type and keep the combiner in `fingerprint`. **Do not** expose a `ConfigHash()` method on the type: a method that returns a finished hash both drags a hashing concern onto a data type *and* tempts callers to route around the version registry to get a raw, version-agnostic hash. Returning bytes from `projectVN` keeps the type ignorant of versioning and crypto.

## Proposed approach

The design has **two parts** with very different cost profiles:

1. **Part 1 - the reset (one coordinated cutover).** At the dev→prod cutover, swap the hashing substrate to canonical projection, declare the post-cutover projection as content-version **v1**, and spend the already-scheduled rebuild on every change that is *cheap now and a one-way door later* (the irreversible changes). Pre-reset locks already committed to **git history** stay readable and are never recomputed (the back-compat invariant below); a pre-reset lock in the **working tree** is force-rehashed to the `v1:` token on its first post-reset `update`.
2. **Part 2 - post-reset lazy migration (below).** A versioned registry + replay, now riding the *frozen* projection functions, absorbs the rare genuine algorithm change after the cutover, lazily and per-component, with no second coordinated cutover.

Part 1 cannot be made lazy: there is no way to make a substrate swap or a batch of one-way-door normalizations free, so they ride the one rebuild we are already paying for. Everything that *can* be lazy (additive fields) is pushed into Part 2 and costs nothing.

## Part 1: The reset

### The projection substrate

Replace `hashstructure.Hash(component, …)` with an explicit two-step pipeline:

```text
ComponentConfig ──projectV1(cfg)──► canonical bytes ──sha256──► configHash
                  (generated from v1 tags,             (stdlib)
                   sorted keys, emit-if-nonzero)
```

`projectV1` is the projection at version 1. Field membership is declared **on each struct field** as a version-set in the `fingerprint` tag (`fingerprint:"v1..*"`); a `go generate` step reads those tags and emits a frozen `projectV1(cfg) []byte` function that emits, in stable key order, every field whose set includes v1, length-prefixing key+value so distinct field sets cannot collide. It omits a field when its **resolved value is zero** (omit-if-zero); a range prefixed with `!` (e.g. `!v1..*`) always-emits, for fields whose zero is build-meaningful. The generated functions are checked in and the registry dispatches to them. (Grammar, generation, and recovery semantics: [Version-tagged field selection](#version-tagged-field-selection) below.)

Three things this buys that `hashstructure` could not:

- **Frozen by construction.** A *superseded* `projectVN`'s body is fixed checked-in code (CI fails on any diff to it), so adding `Foo` to the struct later cannot change a historical `projectV1`'s output for an old config. (The *live* version's projector stays mutable for output-preserving additions - see [enforcement](#version-tagged-field-selection); "frozen" means `version < current`.) This is what makes Part 2's replay sound (Problem 6) and G4 true for *old* locks, not just new ones.
- **No method-set / receiver magic.** No `Includable`, no per-nested-struct method, no `v.(reflect.Value)` type-assert footgun. Selection is a declarative tag the generator reads.
- **Removal is a compile error; rename is byte-neutral.** A generated `projectVN` references each measured field by its literal Go path and emits a literal key, so deleting a field a retained version still measures won't compile, and renaming the Go field changes nothing. Golden vectors backstop the generator itself.

The cost is owning the projection encoder and the golden vectors. That cost is paid once, at the reset, against a rebuild we are already doing.

### Version-tagged field selection

Field membership in each version's projection is declared **on the struct field**, as a version-set in the existing `fingerprint` tag. A `go generate` step reads those tags and **emits** a per-version `projectVN(cfg) []byte` function - the tags are the source of truth, the generated functions are the artifact. This is the chosen mechanism; a runtime reflective walker and hand-written functions are the [alternatives](#alternatives-considered).

**Grammar** (deliberately small):

```ebnf
tag     = "-" | member, { ",", member } ;
member  = [ "!" ], range ;            (* leading "!" ⇒ always-emit for this range *)
range   = version, [ "..", ( version | "*" ) ] ;
version = "v", digit, { digit } ;
```

| Tag | Meaning |
| --- | --- |
| *(absent)* | **build failure** - every fingerprinted field must carry an explicit decision |
| `-` | never measured (unchanged from today) |
| `v1..*` | measured from v1 onward, omit-if-zero - the common "active field" case |
| `v1..v4` | measured v1-v4, then dropped |
| `v3..*` | introduced at v3 |
| `v1..v4,v6..*` | measured v1-v4, **dropped at v5, brought back at v6** |
| `!v1..*` | measured v1 onward, **always-emit** (zero value still hashes) |
| `v1..v4,!v5..*` | omit-if-zero v1-v4, then **always-emit from v5** (the temporal toggle) |

`*` resolves to "this version and every later one," so an *active* field never needs a tag edit across a version bump - only a field that is *dropped* at the bump gets its range closed (`v1..*` → `v1..vN`).

**The emit-key is the field's TOML key, frozen - never the Go identifier.** The generated function emits each field under a stable string key and sorts by it; that key is the field's **`toml:` name**, or - for a field with no usable TOML key - an explicit `key=<name>` member in the `fingerprint` tag (grammar below). The generator emits it as a literal, so it is pinned as part of the frozen output. It is deliberately *not* the Go field name, so a cosmetic Go rename (`Foo`→`Bar`, same TOML key, same tag) is byte-neutral - making the [struct-rename drift-neutral claim](#config-schema-version-and-canonical-migration-future) true at the *field* level too, not just the type level. Renaming the *emit-key* is an output-changing edit and therefore a version bump like any other. **Duplicate emit-keys within one retained version fail generation** (two fields resolving to the same key would collide and alias - a silent G5 hazard), so the generator checks key uniqueness at every retained version.

**The grammar is frozen at three range-operators** (`..` range, `!` always-emit, `*` open-end), plus the orthogonal `key=` override:

```ebnf
tag     = "-" | [ keyopt, "," ], set ;
keyopt  = "key=", identifier ;       (* emit-key override; default is the toml: name *)
set     = member, { ",", member } ;
member  = [ "!" ], range ;            (* leading "!" ⇒ always-emit for this range *)
range   = version, [ "..", ( version | "*" ) ] ;
version = "v", digit, { digit } ;
```

This mirrors protobuf's `reserved` field-range discipline. Adding a fourth *range*-operator is an RFC-grade change, not a tag edit - cheap insurance against a bespoke mini-language accreting edge cases.

**Recovery is the property that justifies the range syntax.** The hard requirement: if we drop a field, then versions later realize we need it again, we must be able to bring it back *without* disturbing any frozen historical hash. The rule that guarantees it: **you only ever *add* a range for the *new* version; you never edit a shipped version's membership.** Walk it:

- `Foo` tagged `v1..*`. At the v2 bump we drop it → edit to `v1..v1`. v1 still emits `Foo`; v2+ does not.
- At v5 we need it back → edit to `v1..v1,v5..*`. **v1's membership is unchanged (still in the set), v2-v4 unchanged (still out), only v5+ is added.**

Every frozen output is byte-preserved, and the **golden vectors prove it**: the edit `v1..v1` → `v1..v1,v5..*` must leave the v1-v4 vectors identical or CI fails. The grammar lets you *express* the non-contiguous set; the golden vectors *forbid* rewriting history while doing so. Two recovery flavors, both covered: (a) field still on the struct (lingering for replay) → reopen its range; (b) field already physically deleted (floor passed it) → bring-back is just a fresh additive field tagged `vN..*`. Same outcome, no special case.

**Always-emit is per-range, for the same reason.** Whether a field's *zero value emits* can change over time just as its membership can - so `!` flags an individual range, not the whole field. `v1..v4,!v5..*` means omit-if-zero through v4, then always-emit from v5. Toggling it is an *output-changing* edit (a zero-valued field starts or stops emitting), so it lands as a new range at a new version exactly like a drop/re-add - same output-preservation rule, same golden-vector backstop. The generator simply emits, for each version, whether that field's range is `!`. A whole-field always-flag could not express this temporal toggle.

**What tags version, and what they don't.** Tags version *membership* - which fields a version measures. They do **not** version *encoding* - how a field's bytes are formed, or how the combiner folds non-field inputs. So a pure tag edit (additive / removal / bring-back) regenerates with no hand-written code, while a genuine encoding or combiner change still ships as versioned code in `computeFP(N+1)` (the projection output + the combiner step frozen at N). The taxonomy's non-additive rows are exactly that small set.

**Escape hatch - the registry already is one; a per-field one is deferred.** The whole-function hatch is free: the registry is `map[int]computeFn` and does not care whether an entry was generated or hand-written, so a version the generator cannot express is simply *not generated* - you drop a hand-written `computeFPN` into the map instead. No new mechanism. A *per-field* hatch (massaging one field's encoding inside an otherwise-generated function - e.g. `fingerprint:"v1..*,enc=sortedSlice"`) is **deliberately not built now**: custom encoding is an *encoding* concern, which the rule above already routes through a versioned-code bump, and adding an `enc=` operator is an RFC-grade grammar change (the grammar is frozen at three range-operators + `key=`). Note the cost either way: a hand-written or hand-edited version drops back to **golden-vectors-only** - it loses regeneration-idempotence and the generator's completeness/coverage guards - so the hatch is for rare, deliberate cases, not routine use.

> **What codegen freezes structurally, and what golden vectors backstop.** Generation reflects the live struct - but at *build time*, and its output is **frozen checked-in code**, so the runtime projection never reflects the live struct (the way the rejected `hashstructure` substrate did at hash time, Problem 6). Three things the tag alone does not pin are pinned by the generated code instead: the **emit-key** (a literal string), **field membership** (a literal field list per version), and **field removal** (a retained `projectVN` references the field by Go path, so deleting it won't compile). Two things the compiler cannot independently judge - the **per-field encoding/type** (whether the emitted bytes are *right*) and the **zero-predicate** - are caught by regeneration-idempotence (CI runs `go generate`; any diff to a retained `projectVN` fails) and ultimately by **golden vectors**, which catch a generator bug that would move a shipped version's bytes. Golden-vector coverage is therefore a *backstop* behind compiler + generator, not the sole load-bearing guarantee - the design keeps its structural guarantees structural.

**Enforcement**, in order of strength:

1. **Compiler.** A generated `projectVN` references each measured field by literal Go path and emits a literal key: deleting a field a retained version measures won't compile, and the key cannot silently drift to the Go identifier.
2. **Generator (generate-time).** The generator **enumerates every field reachable from a fingerprinted root, recursing by field type** - this walk is the completeness guard, and it auto-discovers nested structs, replacing today's hand-maintained `fingerprintedStructs` list (which can silently go stale when a new nested type is added). It then refuses to emit on: a reached field with **no tag** (the include/exclude decision is mandatory - the generator must *fail* on an unrecognized field, never silently skip it, or a forgotten field drops out of the hash, a G5 hazard); a malformed, future-referencing, overlapping, or key-colliding tag set; or a `-`-tagged field absent from the **exclusion ledger** (an enumerated list - the surviving half of today's `expectedExclusions` - naming every `-` field with a justification, so an accidental exclusion fails generation). It also enforces the [coverage oracle](#golden-vector-coverage-the-backstop).
3. **Regeneration-idempotence.** CI runs `go generate` and fails on any diff to a **strictly-historical** `projectVN` (`version < currentLockContentVersion`) - a *superseded* version's emitted code cannot change without an intentional, diff-surfaced regeneration. The **live** version's `projectVN` is deliberately *mutable*: an output-preserving omit-if-zero addition regenerates it (a new `b.emit` line) and that diff is expected, not a violation. "Frozen" throughout this RFC means **superseded** (`version < current`), not "shipped" - a version freezes when the next one is registered, not the moment it first ships.
4. **Golden vectors** - the semantic backstop (next).

#### Golden-vector coverage: the backstop

Compiler + generator + regeneration-idempotence carry the structural load; golden vectors are the semantic backstop that catches a *generator* bug moving a shipped version's bytes. Two properties make the backstop structural rather than discipline:

- **Expected digests are hand-authored and never generator-emitted.** If the `(config, version) → digest` table were regenerated in lockstep with the projector code, a "delete-everything-and-regenerate" commit would move *both* the code and its own expected values, and the backstop would silently agree with itself. The expected digests are therefore hand-committed; `go generate` may scaffold *cases* but must never write the expected values. A mutation to any retained vector is a hard CI failure, not a moved line a reviewer must notice.
- **Retained-version manifest.** The generator validates each retained version against a checked-in manifest (the field set + emit-keys that version measures); generation fails if a retained version's entry lacks a compatible live path, *unless* it is below `minSupportedLockContentVersion`. This is what keeps field-removal structural under the normal *delete + regenerate + commit* workflow (where the compile guard alone would be bypassed) - so the negative test is **delete + regenerate + build**, not just delete.

The backstop is only as good as its corpus, so the corpus must exercise every field of every retained version:

> **Coverage invariant:** every field whose tag set includes a retained version MUST appear, **non-zero**, in at least one retained golden vector, with a discrimination check that varies it and asserts the version's hash *moves*. A field never exercised non-zero is invisible to the backstop.

**The coverage oracle must be independent of the tag** (enforced by the generator). If the discrimination check derived *which versions to test* from the same range tag it polices, a wrong-narrow tag would silence its own check: a field build-effective today but mistagged `v1..v1` (while `currentLockContentVersion` is `v2`) would tell a tag-derived check "only check v1," so the promised v2 check never runs and the field silently drops out of the current hash (G5). The fix: **every fingerprinted field is expected to be measured-and-discriminating at the *current* version unless it appears in a reviewed *dropped-fields ledger*** (sibling to the `-` exclusion ledger, naming each field intentionally closed at version N). The oracle reads that ledger by struct-reflection, *not* the range tag - so a range that excludes the current version without a matching ledger entry fails generation. (PR-A acceptance criterion, with a unit test that the oracle catches the narrow-tag hole.)

Non-zero coverage alone is necessary but not sufficient; four more obligations close holes a single `"foo"`-valued vector leaves open, each a generator responsibility:

- **`!`-zero behavior.** Dropping `!` silently stops emitting a build-meaningful zero (G5). Every retained `!` range needs a **zero-valued** discrimination vector.
- **Encoding across the value space.** A `"foo"` vector misses an encoder change affecting only delimiter bytes, multibyte runes, or multi-entry slices/maps. Add per-encoder **property/fuzz** vectors. (Fails toward G1 over-drift except under a collision the length-prefixed form makes unlikely.)
- **nil-vs-empty is a resolver invariant - slices *and* maps.** Under `IsZero()` a nil slice/map omits and a non-nil empty one (`[]`, `{}`) emits, and the resolver's `mergo.Merge(…, WithOverride, WithAppendSlice)` ([`component.go`](../../../internal/projectconfig/component.go) `MergeUpdatesFrom`, `ResolveComponentConfig`) can yield *either* for the same intent depending on merge order, with **no post-merge normalization today**. This is the one correctness assumption the design *preserves rather than proves*. PR A closes it structurally: (a) a single named chokepoint - `canonicalizeForFingerprint(cfg)` at the **end of `ResolveComponentConfig`** - owns nil-vs-empty normalization, so it is one enforced place, not a convention scattered across merge sites; (b) it carries an **inventory** of every fingerprint-sensitive slice/map field; and (c) its canonical-form test is written **first - before any golden vector is authored**, or a vector bakes in a non-deterministic encoding. (This and the scalar-slice row of the encoding table are the same question - settle them together.) This is the single most load-bearing PR-A gate.
- **`!` on an all-zero nested struct emits.** `IsZero()` on a struct is true iff every sub-field is zero, so a `!`-tagged nested struct whose fields all resolve to zero would otherwise be omitted; the generated sub-projector treats a `!` range as "emit the (recursively projected) value even when the struct `IsZero`," so a build-meaningful all-zero struct still hashes. Covered by a zero-valued discrimination vector like any other `!` range.
- **Enumerator completeness.** The coverage corpus is checked against the **generator's own field enumeration** (above), so it cannot drift from what is measured - a newly-added field or nested struct that the generator reaches but the corpus does not cover fails the backstop, not just the generator.

### Baseline v1: omit-if-zero, no include-always legacy

Because the reset rebuilds everything, there is **no pre-existing population to stay byte-compatible with.** That removes the single biggest constraint of the incremental plan: we do **not** need an `include-always` compatibility mode to preserve today's hashes. `projectV1` is the omit-if-zero projection from day one. There is no `computeFP1 = legacy include-always` entry to carry forever - the registry's floor *starts* at the clean projection.

```go
// You write tags; a `go generate` step emits the per-version projection.
type ComponentConfig struct {
    Upstream   string            `toml:"upstream"    fingerprint:"v1..*"`   // key "upstream", omit-if-zero
    Patches    []string          `toml:"patches"     fingerprint:"v1..*"`   // omit-if-zero
    Defines    map[string]string `toml:"defines"     fingerprint:"v1..*"`   // map: emitted in sorted-key order
    StripDebug bool              `toml:"strip-debug" fingerprint:"!v1..*"`  // always-emit: zero (false) is build-meaningful
    Internal   string            `toml:"-"           fingerprint:"-"`       // never measured
    // … every fingerprinted field carries an explicit tag; absent ⇒ generation fails …
}

// GENERATED - do not edit. The body is a literal field list, so deleting a
// field it names won't compile, and the emit-key is a literal string.
func projectV1(c *ComponentConfig) []byte {
    var b canonicalBuf
    b.emitMap("defines", c.Defines)           // entries emitted in sorted-key order (deterministic)
    b.emit("patches", c.Patches)              // omit-if-zero
    b.emitAlways("strip-debug", c.StripDebug) // '!' → emit even when zero
    b.emit("upstream", c.Upstream)            // omit-if-zero
    return b.Bytes()                          // field order fixed at generation time
}
```

**The generated encoding contract - frozen per version, fully specified before PR A.** The golden vectors bake the byte encoding in irreversibly at the reset, so every value type's serialization is a one-way door and must be pinned now, not discovered later. The contract:

- **Composite omission is by *projected* emptiness, not raw `IsZero()`.** A nested struct or a map/slice-of-struct entry is `reflect.Value.IsZero()` only when **every** sub-field is zero - *including excluded (`fingerprint:"-"`) children* - so a global `IsZero()` predicate would leak: a measured composite whose only non-zero content is an excluded child is not `IsZero`, so it would emit, and the digest would move on a change that touched no *measured* input. **This is a hazard the generator must design out, not a current bug:** today `ComponentConfig.Build` holds only the already-excluded `Failure`/`Hints` (both `fingerprint:"-"`), and today's `hashstructure` drops them *in its own walk*, so setting `build.failure.expected` moves no hash today - the leak appears only if the new projector naively reflects the parent through one global `IsZero()`. The rule that forecloses it: a composite is omitted when its **frozen sub-projector emits no measured bytes** (unless tagged `!`), *not* when the raw value `IsZero`. So the predicate **splits by kind: scalar leaves (including scalar slices like `[]string`) keep plain `IsZero()`; composites (nested struct, map, slice-of-struct) use projected emptiness** - see the **v1 encoding table** below for the per-type rule, including where slices and maps fall. The coverage backstop gains the **inverse** check it was missing: a **negative discrimination vector** per `-` field (and per all-`-`-value map entry) that varies that field alone and asserts the digest does **not** move.
- **Maps emit in sorted-key order; an entry whose value projects empty still emits its key.** A naive `range` over a Go map is **non-deterministic** (randomized iteration), so a generated `b.emitMap` must sort entries by key and emit each as `<len>:<map-key>=<len>:<value>` under the field key - the one guarantee `hashstructure` gave for free that the projection must re-establish, else an unchanged config hashes differently across runs (intermittent spurious drift). **Map-key membership is itself measured:** an entry whose *value* projects to empty still emits its key, so `{"baz":{}}` ≠ `{}` (matching today's `hashstructure`, which hashes map keys). Tests: a fuzz vector (≥2 keys, varying insert order → identical digest) **and** a key-varying vector (add an empty-value entry → digest moves). The natural "set a non-zero value" vector would *not* exercise this, so it must be written explicitly.
- **Value-slot encoding is defined per type, not left to `%v`.** `bool` → `"true"`/`"false"`; integers → base-10; `[]T` → each element as its own length-prefixed sub-value in slice order (not a JSON blob); `map` → as above. A **named scalar type** (e.g. `fileutils.HashType`, `SpecSourceType`, `ComponentOverlayType`, `ReleaseCalculation`) encodes by its **underlying `reflect.Kind`** (named string/int/bool → the underlying kind) - these are measured fields, so they must *not* fail generation. Only genuinely un-encodable shapes (interfaces, generics, pointers to external types, `time.Time`/`[]byte`-style special-cases not present in today's measured graph) **fail generation** rather than fall back to a `fmt`-style encoding a dependency could change underneath us. The v1 encoding test enumerates every named scalar in the measured graph.
- **Nested struct values are emitted by a frozen per-version sub-projector, never by runtime reflection.** If a generated `projectV1` emitted a `[]ComponentOverlay` by delegating to a *live* reflective encoder, adding a field to `ComponentOverlay` later would change `projectV1`'s output at hash time - Problem 6 reborn one layer down. The generator therefore emits a literal per-version projector for each nested struct type too; element/value projectors are frozen exactly like top-level ones.
- **Recursion prunes at `fingerprint:"-"`, per edge.** The completeness walk descends only through *measured* fields and only into struct kinds (through slice-element, map-value, and pointer-element types), treating defined scalar types as leaves. A `-` tag stops the walk at that **edge** (the field isn't measured, so its subtree isn't either) - not at the type (the same type reached through an *included* field elsewhere is still enumerated there). This breaks the real `ComponentConfig → SourceConfigFile → … → ComponentConfig` cycle (both back-edges are already `-`), and a **visited-type memo** on the included graph guards against any future included-path cycle. An untagged field reached on an *included* edge fails generation (the mandatory-decision guard); fields under a `-` edge are never reached, so they need no tag.

**v1 encoding table (normative - every measured kind, pinned irreversibly at the reset).** This is the single source of truth the prose above points at; the golden vectors bake it in.

| Go kind (v1 examples) | Encoded as | Omitted when | Notes |
| --- | --- | --- | --- |
| `string` (`upstream`) | raw bytes | `IsZero` (`""`) | scalar leaf |
| `bool` (`strip-debug`) | `true` / `false` | `IsZero` (`false`), **unless `!`** | `!` emits the build-meaningful `false` |
| `int` (`manual-bump`) | base-10 | `IsZero` (`0`) | scalar leaf |
| named scalar (`SpecSourceType`, `fileutils.HashType`, `ComponentOverlayType`, `ReleaseCalculation`) | by **underlying `reflect.Kind`** | underlying `IsZero` | measured - must **not** fail generation |
| `[]string` (`patches`) | length-prefixed elements, slice order | nil **or** empty | **scalar slice**: membership measured; nil≡`[]` collapsed by the resolver chokepoint (below), then pinned by a golden vector |
| `[]Struct` (`[]ComponentOverlay`) | each element via its frozen sub-projector | no element projects bytes | **composite slice**: each element kept/dropped by *its own* projected emptiness |
| `map[string]string` (`defines`) | sorted-key `<len>:k=<len>:v` | no entries | key membership measured (`{"k":""}` ≠ `{}`) |
| `map[string]Struct` (`map[string]PackageConfig`) | sorted-key; value via frozen sub-projector | no entries | **excluded `-` in v1**; if ever included, this row governs |
| nested struct (`build`, `spec`) | frozen per-version sub-projector | sub-projector emits no measured bytes, **unless `!`** | **projected** emptiness, not raw `IsZero` |
| `*Struct` | follow if non-nil; element via sub-projector | nil, or points to a projected-empty value | |
| interface / type param / `func` / `chan` / pointer-to-external / `time.Time`·`[]byte`-style | - | - | **fails generation** (no silent `fmt` fallback); none present in the v1 graph |

**Cost of pruning at `-`, and its tripwire (G5 guard).** Excluding a composite also removes its subtree from the completeness walk, so a *future* build-effective field added under an excluded type would be **silently unmeasured**. `Packages map[string]PackageConfig` is excluded today because `PackageConfig` holds only the publish-only, `-`-tagged `Publish` - correct now, but `PackageConfig` is documented as growable. You cannot both kill the key-churn (needs excluding the map) *and* keep the per-leaf guard alive (needs an included edge), so the exclusion carries an **external tripwire**: a CI test asserting the excluded type's field set stays within its known-inert set (a new `PackageConfig` field fails CI → forces re-evaluation of the parent exclusion), recorded against its exclusion-ledger entry.

**Why omit-if-zero is safe - fingerprints see the resolved config.** The usual objection to blanket omit-if-zero is the false-negative footgun: a field whose zero is meaningful gets omitted and collides with "unset," so two semantically different configs hash the same and a rebuild is missed. That objection assumes we hash *raw user input*. We do not. `ComputeIdentity` runs on the **resolved, post-merge** config (`*result.config`, after defaults are applied). The omit predicate is therefore "the *resolved value* equals Go-zero," not "the user didn't type it." Consequences:

- Two configs that both resolve a field to zero build identically → hashing them the same is **correct**, not a collision.
- "Unset" never reaches the hasher - it has already been resolved to its default. If the default is non-zero, the field is non-zero and is emitted anyway. If the default *is* zero, then unset and explicit-zero resolve identically → same build → same hash → correct.

So the classic false-negative requires absence ≠ zero-default *at the point of hashing*, and post-merge resolution closes that gap. The load-bearing invariant is **G5's guarantee restated structurally: the fingerprint must see exactly the build-effective resolved config.** That invariant must already hold, or fingerprinting is broken independently of this change. A `!`-prefixed range is the escape hatch for the rare field whose zero value is build-meaningful.

**Result:** additive fields are drift-neutral **by construction** (G4) - a newly added field, listed omit-if-zero in `projectVN`, emits nothing for any component that does not set it, so it is invisible to every lock that leaves it unset, old or new. Adding it moves no existing hash (no shipped lock could have set a field that did not yet exist), so it needs no version bump. Only setters drift (G2).

#### Edge cases under omit-if-zero

The omit predicate **splits by kind** - scalar leaves use `reflect.Value.IsZero()`, composites (nested struct, map, slice-of-struct) use projected emptiness (the encoding contract above is the single source of truth); `!` is the only per-field override. A few `IsZero` consequences on the **scalar leaves** still need stating, because `IsZero` is type-specific:

- **Meaningful zero with a non-zero default** (e.g. `int Jobs` defaulting to `4`, where `0` means serial). Post-merge: unset → `4` (emitted), explicit `0` → omitted. These build differently *and* hash differently, so there is no collision - they are consistent. Use a `!` range only if a zero value must be distinguishable from a future change of default.
- **nil vs empty slice - they hash *differently* under `IsZero`.** A nil slice is zero → omitted; a non-nil empty slice (`[]`) is **not** zero → emitted. If post-merge resolution can produce *either* nil or `[]` for the same intent, that ambiguity would move a hash - so the rule is: **resolution must normalize to one canonical form**, and where an explicit-empty value is build-meaningful and reachable, tag the field `!` so nil and empty both emit and stay distinguishable. This is a constraint on the resolver, pinned by a golden vector, not a free-for-all.

### The reset load-out: what to spend the free rebuild on

The reset rebuild is a budget. Spend it on the irreversible / cutover-only changes; **do not** spend it on anything Part 2 can do lazily for free. Priority order:

1. **Switch the substrate to canonical projection.** Foundational, one-way, enables everything else. (Above.)
2. **Establish `projectV1` as omit-if-zero with no include-always legacy.** The compatibility mode never enters the registry, so it never has to age out.
3. **Keep the lock *format* `Version` at `1` - the content-version token carries the reset.** The reset adds **no new TOML field** (the atomic token in item 4 reuses `InputFingerprint`) and touches **no** pinning field (`upstream-commit`, `import-commit`, `manual-bump`), so an old binary still parses a reset lock and reads everything it needs to *queue a build*. The substrate swap rides entirely on the content-version machinery (Part 2): pre-reset locks carry a legacy (prefix-less) token below the registry floor, and the reset is simply the **first forced upgrade** of the fleet to the `v1:` token. This also makes the one real mixed-toolchain risk self-correcting: if an old binary ever rewrites a reset lock with its legacy-substrate hash, the next new-binary run sees a sub-floor token and **force-rehashes** it back to `v1` - a clean forced upgrade, never silent corruption (next subsection).
4. **Adopt an atomic, self-describing `v1:sha256:…` token** for the stored hash, so the version and the digest can never desync (closes the re-stamp/desync class of bug where the version field and the hash field are written independently).
5. **Unify on `sha256` everywhere**, retiring the `uint64`→decimal-string wart from the `hashstructure` era. One hash format, one encoding.
6. **Do every pending rename / default-normalization now.** Renaming a field, moving content between structs, or changing a baked-in default is a one-way door under Part 2 (it needs a version bump + replay); at the reset it is free because everything rebuilds anyway. This is where the schema-axis "hardest cases" get absorbed cheaply.
7. **Resolve each field's mandatory tag - and bank the free corrections.** The "absent ⇒ generation fails" rule forces a conscious decision on every fingerprinted field at the reset, which is the moment to *fix* existing mistakes for free. Concretely, tag `ComponentConfig.Packages` `fingerprint:"-"`: every `PackageConfig` field is publish-only (`Publish`, itself `-`), so the map measures nothing build-effective - yet today `hashstructure` hashes its *keys*, so adding a publish-only package name already triggers a spurious rebuild. Excluding it at the reset retires that existing G1 churn at zero cost. Audit the whole struct for the same pattern (a measured composite whose every leaf is `-`).

**Anti-goal:** do *not* burn reset budget on additive fields - Part 2 handles those for free, forever. The success criterion for the load-out is that **no *routine* change ever forces a second coordinated cutover**: after the reset, every ordinary change must be expressible as either a free additive field or a lazy Part 2 version bump. Retiring an *old* content version is the one sanctioned exception - a fleet-wide `component migrate` is itself a deliberate, planned, reset-grade event (see [Registry floor](#registry-floor-and-forced-migration)); the goal is that nothing *unplanned* ever forces one.

### The lock changes at the reset: atomic token + forced upgrade

The stored hash becomes a single self-describing token:

```text
input-fingerprint = "v1:sha256:9f86d0…"   # <content-version>:<algo>:<digest>
```

One field carries both the content version and the digest, so they cannot be written out of step (the desync bug a split version/digest field invites). Parsing splits on `:`; an absent prefix on a pre-reset lock reads as the legacy format.

The lock **format** `Version` stays at `1`. The on-disk *schema* is unchanged - same fields, same TOML shape - so an old binary still parses a reset lock and reads its pins (`upstream-commit`, `import-commit`, `manual-bump`), which is all it needs to queue a build. What changes is the *value* of `InputFingerprint`: the substrate swap is expressed purely as a content-version step, and the reset is the **first forced upgrade** to the `v1:` token. The existing singleton `Parse` gate (`Version == 1`) is left untouched; all substrate/version reconciliation routes through the content-version registry instead of a format gate.

Recovery from a sub-`v1` token is the **same mechanism** as the reset itself: a token with no `v<N>:` prefix (or a version below `minSupportedLockContentVersion`) cannot be replayed, so it is treated as `Stale` and **force-rehashed** to the current version on the next `update`. One code path unifies three cases:

- **Pre-reset locks** carry a legacy `sha256:<hex>` hash with no `v<N>:` prefix → force-rehashed to `v1` at the reset.
- **An old binary that rewrites a reset lock** stamps its legacy-substrate hash (no prefix) → the next new-binary run force-rehashes it back to `v1`. The mischief is self-correcting, never silent corruption.
- **A future floor raise** (after a deliberate `component migrate`) retires an old `v<N>` the same way.

This is the one place back-compatibility is load-bearing, and it is satisfied without a format bump: old binaries read pins and build; the fingerprint value reconciles by version. See the next section for why reading *historical* locks never needs to recompute their hash at all.

### Back-compat invariant: synthetic history reads stored strings, never recomputes

The reset is only safe because of a property of the codebase verified against the source: **nothing that reads a *historical* lock ever recomputes a fingerprint for it.** Every historical reader compares the *stored* hash strings; the only code that recomputes a fingerprint does so for the **current working tree against HEAD**, never against an arbitrary past commit. Concretely:

| Reader | What it does with a historical lock | Recomputes? |
| ------ | ----------------------------------- | ----------- |
| `synthistory.FindFingerprintChanges` | walks `lockfile.ShowAtCommit`→`Parse`, compares `InputFingerprint` *strings* between adjacent commits | No |
| `synthistory.BuildDirtyChange` | compares the precomputed current fingerprint to HEAD's stored string | No (HEAD only) |
| `sourceprep.computeCurrentFingerprint` | the *only* `ComputeIdentity` call on this surface - computes for the **current tree**, compares to HEAD's stored hash | Current tree only |

The consequence: **swapping the substrate is invisible to synthetic history.** A pre-reset (legacy-token) lock and a post-reset `v1:` lock are just two different opaque strings at two different commits; the walker reports "changed" across the reset commit (correct - it *is* a notable, deliberate, fleet-wide event, the coordinated cutover) and never tries to recompute either side. Applying historic overlays likewise reads stored lock fields and needs no hash recomputation.

> **Invariant (must hold forever):** synthetic history and historic-overlay application operate on **stored lock fields only.** No reader recomputes a fingerprint for a historical commit. This is precisely what lets a frozen `projectVN` be *forward-only*: it never has to reproduce a hash from a different substrate generation, only hashes the lock that the *current* binary writes. A future change that recomputes a historical fingerprint would break this and must be rejected in review.

This invariant - no reader recomputes a historical fingerprint - is the complete back-compatibility story: **new-reads-old by string, never-recompute-old by algorithm.** The lock *format* never bumps, so old and new binaries parse every lock identically; only the *interpretation* of the fingerprint value evolves, and that rides the content-version registry.

## Part 2: Post-reset lazy migration

The reset gives us a clean, frozen substrate. Part 2 is the machinery that rides it for the rare genuine algorithm change *after* the cutover - lazily, per-component, with no second coordinated cutover. This is the original "lazy" design, now sound because `projectVN` is genuinely frozen.

### Versioned lock content with lazy replay (algorithm changes)

Stamp one **lock content-hash version** into the lock (the `v1:` prefix of the atomic token) and teach the freshness check to **replay** older versions. The version governs *both* stored hashes (`InputFingerprint` and `ResolutionInputHash`) - they live in one lock, share one write event, and a single integer is the natural fit (see [scope note](#both-hashes-share-one-version) for why one version, not two):

1. The content version lives in the atomic `v<N>:sha256:…` token (it is **not** the lock *format* `Version`, which stays at `1`). The registry floor *starts* at `1` = the projection baseline; there is no legacy pre-projection algorithm in the registry, because pre-reset locks are never replayed (they are read-only history, per the invariant above). A pre-reset lock's prefix-less token is therefore *below* the floor and reconciled by force-rehash, not replay.
2. Turn the combiner into a thin dispatcher over a small registry of historical algorithms, keyed by version. Each entry pairs the two compute functions; when only one algorithm changes, the other slot **reuses** the prior function (no version-neutral hash moves for the untouched one). Keep versions back to a declared floor (see [Registry floor](#registry-floor-and-forced-migration)):

   ```go
   type lockAlgo struct {
       fingerprint computeFn // produces the InputFingerprint digest
       resolution  resolveFn // produces the ResolutionInputHash digest
   }
   var lockAlgos = map[int]lockAlgo{
       1: {computeFP1, computeRes1}, // projection + combiner baseline, established at the reset
       // a future GENUINE algorithm change appends: 2: {computeFP2, computeRes1}
   }
   const currentLockContentVersion = 1       // == the reset baseline; bumps only on a real algo change
   const minSupportedLockContentVersion = 1  // floor; raise only after a deliberate `component migrate`

   // init enforces the registry/floor contract: every version in
   // [minSupported, current] MUST have an entry, or replay panics at
   // runtime instead of failing the build. The map and the two consts are
   // edited independently, so this assertion is load-bearing, not decorative.
   func init() {
       for v := minSupportedLockContentVersion; v <= currentLockContentVersion; v++ {
           if _, ok := lockAlgos[v]; !ok {
               panic(fmt.Sprintf("lockAlgos missing version %d in [%d,%d]",
                   v, minSupportedLockContentVersion, currentLockContentVersion))
           }
       }
   }
   ```

3. In `checkFingerprintFreshness`, compute at the **current** version. On mismatch, if the lock's token version `< current`, recompute at the lock's token version. If *that* matches the stored digest, the inputs are unchanged and only the algorithm evolved → treat as `FreshnessCurrent` and flag for silent re-stamp. Otherwise → `FreshnessStale`. (The resolution hash reuses `computeRes1` until its algorithm first changes - see scope note.)
4. `component update` re-stamps the token to the **current** version **only when it is already writing for an independent reason** (see the churn policy below). Migration is therefore **lazy and per-component**: a lock upgrades only when something independently touches it.

This resolves Problems 2 (for default changes), 3 (hashing bugfixes), and 5 (piecemeal rollout). It is the same lazy-forward-migration pattern Cargo/npm use, specialized to a content hash.

#### Both hashes share one version

`ComponentLock` carries two persisted content hashes: `InputFingerprint` (render inputs, via `projectVN` + `sha256`) and `ResolutionInputHash` (upstream-resolution inputs - a flat SHA256 over seven explicit fields in `ComputeResolutionHash`). Both have the **same evolution problem**: appending an input or reordering the fold moves every lock's hash → G1 churn.

We version them with **one shared integer**, not two axes, because: they co-locate in a single lock, they are written in the same `update` pass, and a paired registry lets either evolve independently while the other reuses its prior function. Two separate version axes would double the floor/replay/migrate machinery for an input set (`ResolutionInputHash`) that changes rarely - YAGNI.

**`InputFingerprint` is the sole prefix authority; `ResolutionInputHash` stays bare.** The shared version is physically stored **only** in `InputFingerprint`'s `v<N>:` prefix. `ResolutionInputHash` carries **no prefix** - it remains a bare `sha256:<hex>` digest. This is the decisive choice that prevents the *fingerprint-bump* desync: the **first fingerprint-only `v2`** already advances the shared prefix, and if `ResolutionInputHash` *also* carried it, `resolver.go`'s raw string compare of the whole field would see a prefix-only move (`v1:…X` → `v2:…X`, `computeRes1` unchanged) and mark resolution stale → fleet-wide re-resolution for nothing. With the prefix living only in `InputFingerprint`, the resolver compares a bare digest that does not move on a fingerprint bump. (This closes the fingerprint-bump direction only; the *symmetric* resolution-only-write desync stays dormant until `computeRes2` and is held by the structural tripwire in [`ResolutionInputHash`](#resolutioninputhash-bare-digest-replay-deferred).) The "shared version" therefore means: the integer in `InputFingerprint`'s prefix selects which `computeResN` produced `ResolutionInputHash` during replay (read from the one prefix), not that resolution stores its own copy. This also keeps `InputFingerprint` the only release-bearing field, so the historical changelog/classifier comparators - which compare the **digest**, stripping the `v<N>:` prefix - never see a phantom move on a version-only re-stamp. (See [the synthetic-history path](#the-synthetic-changelogrelease-path-is-the-real-hazard).)

**Phasing.** The atomic token format (`v<N>:sha256:…`) is fixed at the reset. Fingerprint replay is wired in Part 2's first PR; **resolution-hash replay is reserved, not yet wired** - the slot exists and `computeRes1` is reused, so the day `ComputeResolutionHash` first changes we add `computeRes2` and extend replay to its one comparison site (`checkResolutionFreshness` + the `resHashChanged` silent-write guard in `update.go`). Because `ResolutionInputHash` is bare and prefix-free, a fingerprint-only bump before that day is a no-op for the resolver - the deferral is genuinely safe, not merely small-blast-radius. See [`ResolutionInputHash`](#resolutioninputhash-bare-digest-replay-deferred).

#### Churn-avoidance policies (G1)

The version stamp is itself a potential source of spurious diffs - the exact thing G1 forbids. The rule that prevents it is one idea: **judge "changed?" by replaying the lock's *own* version, not the current one.** Everything below follows from that.

**Why the obvious approach is wrong.** Today `update.go` sets `result.Changed = true` the instant `lock.InputFingerprint != identity.Fingerprint`, where `identity` is computed at the **current** version. That comparison sits *upstream* of the write guard `if !result.Changed && !resHashChanged { return false, nil }`. So the moment you ship a v1→v2 *algorithm* change, the current-version hash differs from every stored v1 token, `Changed` flips for **~every component at once**, and you get the mass auto-release-bump + mass lock rewrite G1 exists to prevent. The version stamp cannot "harmlessly ride the `Changed` path" - it *triggers* it.

**The fix: replay before you compare.** Recompute at the lock's recorded version first, and only call it changed if *that* disagrees:

```go
// Replay at the lock token's recorded version BEFORE deciding Changed.
lockVer := parseTokenVersion(lock.InputFingerprint) // "v1:sha256:…" → 1
replayed := fingerprint.ComputeIdentityAt(lockVer, *result.config, releaseVer, opts)
if lock.InputFingerprint != replayed.Token() {
    result.Changed = true // a REAL input change under the lock's own algorithm
}
// else: tokens match under the old algorithm → inputs unchanged, only the
// algorithm moved → NOT Changed.

// Re-stamp to the current version ONLY when the lock is already dirty for a
// real reason - the version upgrade piggybacks a real write, never triggers one.
if result.Changed {
    lock.InputFingerprint = identity.Token() // current version + digest, written together
}
```

This makes migration strictly **opportunistic**: a lock advances its version the next time its component changes for real, and not one commit sooner. Because the version lives *inside* the atomic token, a lock at `v1` with unchanged inputs keeps its exact `v1:sha256:…` bytes - there is no separate version field to materialize and no zero-diff bookkeeping. (When resolution replay is wired, the same replay-before-compare guards the `resHashChanged` write.)

**The unavoidable flip side - false-fresh on a newly-measured input.** "Replay at the lock's own version" is what buys churn-avoidance, but it is the *same* property that creates a blind spot, because replaying `computeFP(old)` is **blind to any input that version did not measure.** Concretely, when v2 starts folding in an input v1 never touched (the [*Newly measured input*](#change-taxonomy) row):

- A change to that **new** input on a still-`v1` lock replays at v1, which ignores it → digest still matches → **`Changed = false`** → the change is silently treated as fresh.
- The new input only takes effect on that lock when the lock migrates to v2 - i.e. the next time it is dirtied for an *independent* reason, or via `component migrate`.

This is correct *by contract* (a v1 lock promises freshness under the v1 input set, which excludes the new input), and harmless for a cosmetic input. But for a **build-critical** new input it is a latent-stale hazard: artifacts can lag the new input by an unbounded number of commits. **Decision rule:** if a newly-measured input must take effect fleet-wide immediately, do **not** rely on lazy adoption - pair the version bump with a deliberate `component migrate` (see [Registry floor and forced migration](#registry-floor-and-forced-migration)). Lazy adoption is the default; `component migrate` is the opt-in for inputs that cannot wait.

#### Registry floor and forced migration

Lazy migration means an untouched lock can sit at an old version **indefinitely** (G3 by design). That makes "keep the last *N* versions" a **correctness cliff, not a tuning knob**: if pruning drops the compute function a lock still depends on, replay becomes impossible → forced `FreshnessStale` → the mass rebuild/rewrite (and, via the downstream-consumer analysis below, mass changelog churn) the whole design exists to avoid. So the floor must be explicit and paired with an escape hatch, decided now:

- **`minSupportedLockContentVersion`** is a hard floor. A lock below it cannot be replayed and is treated as `Stale`. Dropping a registry entry is therefore a deliberate, breaking, announced act - never incidental cleanup.
- **`component migrate`** force-advances every lock to the current content version in one deliberate pass. This is the *only* sanctioned way to retire an old version: migrate the fleet first (one intentional, reviewed, fleet-wide commit), then raise the floor. Note this pass is a deliberate G1 exception - it *is* the eager migration G1 normally forbids, made safe by being explicit and operator-driven rather than a silent side effect. **Contract:** it is *offline* - it loads each lock, recomputes the fingerprint at `currentLockContentVersion`, and rewrites the token; it does **not** re-resolve upstream (`upstream-commit`/`import-commit` untouched, unlike `update --force-recalculate`) and does **not** touch the manual-bump counter (unlike `--bump`). It *does*, however, move every *fingerprint* digest when it retires a fingerprint algorithm, so a fleet-wide migrate of that kind **is a fleet-wide, release-grade event**: `FindFingerprintChanges` reads each moved digest as notable, exactly as [the synthetic-history trap](#the-synthetic-changelogrelease-path-is-the-real-hazard) warns. (A migrate that retires only a *resolution* algorithm rewrites only the bare, prefix-free `ResolutionInputHash` - which `synthistory` never reads - so it is correctly release-silent.) Migrate is therefore rare: the release churn is the deliberate cost of retiring a version. The on-disk *config* axis has its own verb, [`config migrate`](#config-schema-version-and-canonical-migration-future); the two are orthogonal - each lives with the artifact its command group already owns (`component` writes locks, `config` owns the TOML).
- **Floor-advance cadence.** Because raising the floor requires a release-grade `component migrate`, pruning cannot be routine - left alone, the registry, golden vectors, and deprecated tombstone fields grow **append-only** (a real cost the opaque-token model accepts; see the manifest alternative). Policy: piggyback floor-raises onto *already-planned* mass rebuilds (the next environment cutover or a major release), and enforce a CI ceiling on the `currentLockContentVersion - minSupportedLockContentVersion` *spread* so the backlog cannot grow unbounded between those planned events. The spread, not the absolute version number, is the quantity kept small. **Early-warning ramp:** the ceiling is a *warning at ceiling-1*, a hard failure only at the ceiling - so an approaching floor-raise surfaces as a heads-up on the PR *before* the one that registers `v(N+1)`, converting the forced migrate from a surprise blocking failure into a planned event (the design's goal that nothing *unplanned* ever forces a migrate). **Residual:** if genuine algorithm changes arrive *faster* than planned rebuilds, the ceiling still ultimately *forces* an unplanned, release-grade `component migrate`. The ceiling does not eliminate the expensive event; it bounds the backlog by *converting* an unbounded version spread into an occasional forced migrate, with one version of advance notice. This is the accepted cost of lazy-forever coexistence.

**Mixed-toolchain hazard - bounded by the version-pin, not auto-repair.** The classic trap is an older binary regressing a newer lock. Because the lock *format* never bumps, an old binary *can* write a reset lock, stamping a legacy (prefix-less) or lower-`v<N>` hash. In the **working tree** this is self-correcting: the next new-binary run detects the sub-floor token and force-rehashes it to the current version. But "self-correcting" stops at the working tree - if a downgraded lock is **committed**, `FindFingerprintChanges` reads `v1 → legacy → v1` as two real release events, and a published `%autorelease` increment cannot be withdrawn. So the load-bearing guard against *committed* phantom releases is the **CI version-pin**: post-cutover, no old binary may run the `update`-and-commit step. Concretely, that means the lock-writing CI job runs from a **pinned build image (by digest, not a floating tag)** rebuilt from the cutover commit or later, and **no other path reaches the `update`-and-commit step** - local developer binaries do not commit locks; only the pinned job does. (The force-rehash only cleans the working tree; it does not undo history.) The *symmetric* residual - a binary that predates content-version `v2` meeting a `v2` token it cannot replay - is closed by a **required** write-time guard: refuse to write a token whose version exceeds the binary's `currentLockContentVersion`, erroring rather than silently restamping at `v1`. Note this guard lives in the binary doing the write, so it constrains *newer-but-not-newest* binaries; it does **not** retroactively constrain a genuinely *old* binary - that direction is the version-pin's job.

#### Replaying across a changed input set: `{a,b,c}` → `{a,b,d}`

A lock stores **one atomic token** (`v<N>:sha256:…`); it does *not* store the individual inputs. So when the measured set changes - say the fingerprint stops measuring `c` and starts measuring `d` - an existing lock is reconciled the only way an opaque digest allows: **recompute and compare, at the lock's own version.**

Split the change into its two halves; they are handled independently:

- **Adding `d`** is the additive case - `projectV1` never listed `d`, so for any lock at v1 the digest is byte-identical whether or not the struct now has `d` (G4, *truly* - the property `hashstructure` could not give). Free. No version bump.
- **Dropping `c`** is what forces the version bump, and it is reconciled by replay:
  1. `computeFP2` (measures `{a,b,d}`) ≠ stored digest → mismatch.
  2. token version (1) < current (2) → **replay `computeFP1`** (still measures `{a,b,c}`).
  3. v1-replay == stored digest? **Yes** → `a,b,c` unchanged since the lock was written; only the *measurement* evolved → `FreshnessCurrent`, lazy re-stamp. **No** → a real input moved → `Stale`, rebuild. Both correct.

So the bump is **not breaking**: replay answers "were the *old* inputs unchanged?" without rebuilding.

**The one constraint replay still imposes: a field a retained version still measures must stay on the struct.** The projection is immune to field *additions* (a generated `projectVN` only emits the fields its version's tags include, so a new field is invisible to old versions). It is *not* immune to field *removal*: the generated `projectV1` references `c` by literal Go path, so physically deleting `c` from the struct **won't compile** while v1 is retained. Removal is therefore the one edit still gated by a **deprecate-then-delete** two-step, both non-breaking:

1. **Bump to v2 measuring `{a,b,d}` but keep field `c` on the struct** so the v1 projection can still read it for replay (close `c`'s tag to `v1..v1`, so v2 does not measure it). Every old lock replays clean at v1, is recognized as unchanged, lazy re-stamps to v2. Zero forced rebuilds.
2. **Only after the floor passes v1** (`minSupportedLockContentVersion = 2`, ideally after a deliberate `component migrate`) physically delete field `c` and `projectV1`.

> **Invariant:** a field may be physically removed from the config struct only after *every* retained version whose tag set includes it has been retired below `minSupportedLockContentVersion`. Retained versions and the struct they read must stay in sync - you cannot delete a field a live version's golden vector still sets.

This makes "drop an input" a lazy, per-component migration rather than a fleet-wide rebuild - at the cost of carrying a deprecated field on the struct until the last version measuring it ages out.

#### First post-reset customer

The reset establishes `projectV1` directly; it is *not* itself a Part 2 version event (it rides the rebuild, not replay). Part 2's machinery therefore sits idle until the **first genuine algorithm change after the cutover** - e.g. a `computeFP2` that fixes an overlay-folding bug, folds in a newly measured input, or changes a baked-in default. That change registers `computeFP2`, bumps `currentLockContentVersion` to 2, and is absorbed by replay with no second coordinated cutover. Because the projection substrate makes additive config changes hash-neutral by construction (G4), the *only* changes that ever need a Part 2 version event are genuine non-additive algorithm changes - a deliberately small set.

## Config schema version and canonical migration (future)

This is the on-disk TOML axis. It is **independent** of the fingerprint axis and only needed once we make *non-additive* TOML changes (rename/move/remove fields in the file format itself) that were *not* already absorbed by the reset's normalization pass. Most of the hardest cases are spent at the reset (load-out item 6); this axis covers whatever non-additive TOML change arises *after*.

1. Add an explicit `schema-version` to the config file (distinct from the existing `$schema` URL, which is for editor validation).
2. At **load time**, migrate older config shapes forward into the single latest canonical struct *before* anything hashes them. Fingerprinting stays blissfully unaware of file-format history. A `config migrate` command (sibling to today's `config schema` / `config dump`) makes this an explicit, reviewable pass that rewrites stale TOML files in place to the current `schema-version`.
3. The projection substrate already provides the clean seam: `projectVN` reads the post-migration canonical struct; the combiner stays in `fingerprint`. No `ConfigHash()` method is added (see [the seam note](#where-the-hashing-logic-should-live)).

The critical invariant: **migrate old TOML → latest canonical struct, then project once.** A semantically no-op migration (rename `foo`→`bar`) must produce the *same* canonical struct, hence the same projection bytes, hence no drift. This is what keeps the schema axis **orthogonal** to the lock axis: a faithful `config migrate` is a pure re-encoding that moves *no* fingerprint, so it never triggers a `component migrate`. If a TOML change genuinely alters build meaning, that is a content-version bump (Part 2), not a `config migrate`.

**Resolved by projection:** the old `hashstructure` caveat - that it mixed `reflect.Type.Name()` into the hash, so renaming a Go struct moved every fingerprint even with identical content - **no longer applies.** The generated projection emits only the explicit field bytes, under each field's **frozen TOML key**, never the Go type or field name. So *both* a struct-type rename **and** a cosmetic field rename (`Foo`→`Bar`, same `toml:` key) are genuinely drift-neutral - **pinned by golden tests** (rename a fingerprinted struct, and rename a field while keeping its TOML key → byte-identical digest in both cases), so the property is CI-enforced, not just asserted here. Renaming the *TOML key itself* is an output-changing edit and takes a version bump like any other.

## Pipeline

```text
TOML on disk ──migrate to canonical struct (schema axis)──► ComponentConfig
                                                              │
                                  projectVN: emit explicit fields, omit-if-zero
                                                              ▼
                          combiner: sha256 over projection + overlays + identity
                                                              │
                                          lazy replay + re-stamp on update
                                                              ▼
                                                      locks/<name>.lock
```

## Downstream fingerprint consumers (blast radius)

The versioned-replay story in Part 2 must hold for **every** reader of `InputFingerprint`, not just the two paths it grew up around. This is the post-reset migration blast-radius map; each consumer's behavior under a Part 2 v1→v2 algorithm switchover is stated explicitly. (The *reset itself* is invisible to these consumers as analyzed under [Back-compat invariant](#back-compat-invariant-synthetic-history-reads-stored-strings-never-recomputes): they compare stored strings, and pre-reset locks are never recomputed.)

| Consumer | Reads | Compares | Migration behavior required |
| -------- | ----- | -------- | --------------------------- |
| `checkFingerprintFreshness` (resolver) | recomputed identity | vs stored token | Replay at token version (Part 2 core) |
| `component update` `Changed` decision | recomputed identity | vs stored token | **Replay before `Changed`** (see churn policy seam) |
| `bumpComponents` (`update.go`) | recomputed identity | vs stored token | Current-tree replay (second `ComputeIdentity` caller) |
| `changed.go` `classifyComponent` (CI classifier) | stored token strings (two historical git refs) | **digest compare** (strip `v<N>:` prefix) | **String-only - must NOT replay** (no inputs available; replaying historical configs would violate the no-recompute invariant) |
| `changed.go` `haveMatchingFingerprints` (cache-poisoning integrity gate) | stored token strings | **digest compare** (strip `v<N>:` prefix) | **String-only; security-load-bearing** - a version-only delta must read as "same" or the integrity check is silently skipped |
| `synthistory.FindFingerprintChanges` | stored token strings across git history | **digest of adjacent commits** (strip `v<N>:` prefix) | **String-only; digest-compare** so a version-only re-stamp never fires a release |
| `synthistory.BuildDirtyChange` | recomputed (current ver) | vs stored `headLock` token | **Replay at headLock version** before declaring dirty |
| `ResolutionInputHash` staleness/write | recomputed resolution hash | vs stored **bare** digest | **No prefix** (bare `sha256:`); fingerprint-only bumps never touch it; replay reserved |

**Two comparator classes, not one - and only one of them can replay.** The consumers split cleanly by *what they hold*:

- **Current-tree comparators** (`checkFingerprintFreshness`, `update`'s `Changed`, `BuildDirtyChange`) recompute against *live inputs*, so they **can and must** replay at the stored token's version. Feasible and invariant-safe.
- **Stored-vs-stored historical comparators** (`FindFingerprintChanges`, `changed.go`'s `classifyComponent`/`haveMatchingFingerprints`) hold only committed token *strings* from two git refs - no config, no FS, no inputs. They **cannot** replay, and replaying would require recomputing a historical fingerprint, which the [forever-invariant](#back-compat-invariant-synthetic-history-reads-stored-strings-never-recomputes) forbids outright. Both stay **string-only**, and both compare the **digest** (stripping the `v<N>:` version prefix), which makes them inherently immune to version-only deltas - a v1→v2 re-stamp with an unchanged digest reads as "no change." (Strict-lazy churn is still the policy that keeps re-stamps from riding no-op commits in the first place, but the comparators no longer *depend* on it for correctness.)

The `changed.go` classifier is the easily-missed member of the *second* class: it must get the same **digest-compare** as `FindFingerprintChanges`, so a version-only delta reads as "no change" - not a replay (which it cannot do, holding no inputs).

**This contract is enforced by types, not prose - the `fingerprint.Token` choke-point.** A reviewer-vigilance rule across the comparison sites is the kind of discipline this RFC elsewhere converts to structure (the atomic token, D3), and digest-comparison widens the surface: `v<N>:` prefix-parsing now lives at three historical + three current-tree sites, and the "digest-compare two stored strings" pattern is **copyable**. The residual hazard is therefore not mere *omission* - a forgotten replay at a current-tree site fails *safely* toward inequality → spurious `Stale`/`Changed` → wasteful rebuild (G1 churn, never G5) - it is *mis-classification*: a future consumer that holds live inputs but copies the **historical** stored-string template never looks at those inputs → silently accepts a stale tree → reachable G5. Omission is safe; mis-classification is not, and only structure closes it.

The fix is a **two-type split**, because a single token type cannot tell the two comparator classes apart:

- **`StoredToken`** - parsed from a lock by the *sole* strict parser `ParseToken` (accepts only `sha256:<hex>` legacy and `v<N>:sha256:<hex>`; any malformed token is treated as *changed*, never normalized to an empty digest). It exposes `SameDigest(other StoredToken)` and nothing else - it holds no inputs, so a site that has only stored strings *physically cannot* perform a freshness decision.
- **`FreshToken`** - obtainable *only* from `ComputeIdentityAt(version, config, …)`, so constructing a *valid* one requires live inputs. Its zero value (`var f FreshToken`) is still syntactically constructible, so it **fails safe**: a `FreshToken` carries a validity bit set only by the constructor, and `Reconcile` on an unset one **returns `Stale`** (never errors, never `Fresh`). `Stale` is the fail-*safe* answer on a path whose job is "rebuild when in doubt" - a zero token means "no freshness evidence," so it triggers a rebuild (G1 churn at worst) and never blocks a `build`/`render`/`--check-only`, where an `error` would be fail-*stop* and could take the fleet down on an accidental zero-value path. It exposes `Reconcile(stored StoredToken) → {Fresh | Stale | RestampTo(v)}`. (Belt-and-suspenders: a named test `var f FreshToken; assert f.Reconcile(stored) == Stale`, and if feasible a vet/lint check that no site reconciles a statically-zero token - so a *programming* mistake is still caught loudly without coupling runtime behavior to it.)

A historical site holding two `StoredToken`s can call `SameDigest` but cannot fabricate a `FreshToken`, so it cannot accidentally pose as a current-tree freshness check; a current-tree site must obtain a `FreshToken` to reconcile, which forces it through live inputs. The *assignment* documents the class, and the mis-classification path is unconstructible rather than merely discouraged. Both types are **non-comparable** (an unexported `_ [0]func()` field), so a raw `==` on a token outside the `fingerprint` package fails to compile. (Unexported fields alone would *not* do this: a struct of comparable unexported fields is still `==`-comparable from any package; the non-comparable sentinel is what blocks it.)

For the choke-point to be *structural* and not merely conventional, the **lock fields must be token-typed, not raw `string`**: as long as `ComponentLock.InputFingerprint`/`ResolutionInputHash` stay exported strings, `lock.InputFingerprint == other.InputFingerprint` still compiles and the raw-compare pattern stays copyable. So PR C changes those fields to `StoredToken` (TOML marshal/unmarshal routing through `ParseToken`, so every read crosses the strict parser), or hides the raw string behind an accessor that returns a `StoredToken`. Only then does \"enforced by types, not prose\" hold end-to-end. This lands in PR C, which already edits every comparison site. **The on-disk bytes are not automatically unchanged, though; the field *form* decides it** (verified empirically against go-toml/v2 v2.3.1, the pinned version). `omitempty` decides emptiness by reflecting a struct's *exported* fields **before** consulting `TextMarshaler`, so a token struct whose digest sits in an *unexported* field is judged empty and **dropped even when set**: a populated `input-fingerprint` silently vanishes, while a *non-*`omitempty` value struct instead emits a spurious `resolution-input-hash = ''` line. Two byte-neutral forms survive: (a) an **accessor**, keeping the on-disk field a `string` and exposing a `StoredToken` via method with writes routed through `ParseToken`, byte-neutral *by construction* (the serialized type never changes) and `==`-proof for every other package; or (b) a value struct with an **exported** digest field (so `omitempty` tracks it) plus a custom marshal that renders it as a bare string. The pointer form (`*StoredToken`) is byte-neutral but reintroduces a silent pointer-`==`, so it is rejected. **PR-C acceptance gate:** a golden round-trip test proving a real local lock's bytes are unchanged across the conversion, so the property is *tested*, not asserted. Either accepted form lands in PR C with no separate on-disk-format bump.

### The synthetic changelog/release path is the real hazard

[`synthistory.go`](../../../internal/app/azldev/core/sources/synthistory.go) turns fingerprint movement into **user-visible, shipped** package state - `%autochangelog` entries and `%autorelease` increments. There are two distinct comparators, and the design resolves them asymmetrically.

- **`FindFingerprintChanges` (historical walker)** compares `InputFingerprint` across the lock's git history and emits a synthetic changelog/release entry on every change. It compares the **digest** (stripping the `v<N>:` version prefix), not the full token - a one-line string operation, not the infeasible version-aware replay (it has only committed *strings*, no inputs). So a version-only re-stamp (a lazy v1→v2 migration with an unchanged digest) is **invisible** to it; only a moved digest - a genuine input change - fires, and the migration folds into the real change's entry that carries it. The v1→v2 conversion is thus an *accepted, per-component, notable* changelog event that piggybacks a real change, guaranteed by digest-comparison rather than by lazy-discipline.
  - **`component migrate` is release-grade *when it moves digests*.** A migrate that retires a *fingerprint* algorithm re-stamps every unchanged lock from `computeFP1`'s digest to `computeFP2`'s - the digests move, the walker fires, and the fleet-wide release is the deliberate cost ([registry floor](#registry-floor-and-forced-migration)). A migrate that retires only a *resolution* algorithm rewrites only the bare `ResolutionInputHash` (which `synthistory` never reads), so it is correctly release-silent. Either way the firing tracks a real `InputFingerprint` digest move.
- **`BuildDirtyChange` (live dirty check)** compares a *recomputed* current-version (v2) hash against the *stored* (possibly v1) `headLock.InputFingerprint` and declares dirty on inequality. "Accept as notable" does **not** save this path: post-switchover an *unchanged* component would read **dirty on every `render`/`build`** until re-stamped - a persistent, recurring spurious signal, worse than a one-time entry. The fix is **free**: it is the *same replay Part 2 already owes the freshness check* - replay at `headLock`'s recorded version before declaring dirty. One additional call site for logic already being written, no new mechanism.

**Net:** the changelog-walker concern is not "make the walker version-aware" (hard, maybe infeasible). It is two cheap things - (1) the historical comparators (`FindFingerprintChanges`, `changed.go`) compare the **digest**, so a version-only delta never fires; and (2) extend the *current-tree* replay to `BuildDirtyChange` (which *does* hold live inputs), one call site for logic already being written. The reset commit is the single deliberate exception: it *is* a fleet-wide notable event, the coordinated cutover, intentionally visible.

### `ResolutionInputHash`: bare digest, replay deferred

`ComponentLock` carries a *second* persisted content hash, `ResolutionInputHash`, with its own staleness logic and its own silent-write path (it writes when only `resHashChanged`, never flipping `Changed`). It has the **identical** evolution problem as `InputFingerprint`, but two properties make its replay safe to defer:

- **Smaller blast radius.** `ResolutionInputHash` does **not** feed `synthistory`, so an algorithm change can never mint a phantom changelog/release (that hazard is fingerprint-only). Worst case is a one-line `resolution-input-hash` rewrite per lock plus a wasted re-resolution that usually yields the same commit. Churn, not corruption.
- **No pending change.** It is a flat seven-field SHA256, not a struct walk, so the projection substrate leaves it untouched. Its registry slot stays `computeRes1` until its inputs genuinely change.

**Decision (KISS/YAGNI):** wire fingerprint replay in Part 2's first PR. `ResolutionInputHash` stays a **bare `sha256:<hex>` digest with no `v<N>:` prefix** (the prefix lives only in `InputFingerprint` - see [Both hashes share one version](#both-hashes-share-one-version)), so the resolver compares it directly and a fingerprint-only bump never touches it. The day `ComputeResolutionHash` first changes, add `computeRes2` and extend replay to its one comparison site (`checkResolutionFreshness` + the `resHashChanged` silent-write guard in `update.go`); decide *then* whether resolution needs its own prefix or reads the shared one. Because resolution carries no prefix and is compared bare today, a fingerprint-only bump never touches it - so the shared-prefix desync is **dormant, not eliminated**, and wakes only when resolution gains a second algorithm. The seam: the prefix advances only on `result.Changed`, while a resolution-only write takes the independent `resHashChanged` path ([`update.go`](../../../internal/app/azldev/cmds/component/update.go)). So once `computeRes2` exists, a resolution-only write would advance the bare digest while the shared prefix stays at `v1`, and replay would select `computeRes1` → permanent false-stale. This is **safe-direction (G1 churn, never a missed rebuild) and dormant** (resolution replay is reserved; one algorithm today), so it gates no PR now. To stop it shipping silently the day it matters, the guard is **structural, not prose**: registering a second resolution algorithm **fails the build** (a registry `init()`-time assertion) unless the desync is resolved - either resolution takes its own prefix, or a resolution-only write also re-stamps `InputFingerprint`'s prefix to current (same digest) behind a CI gate mirroring the dirty-change gate. The decision is *forced* at `computeRes2`, not forgotten.

## Design decisions

### D1: Canonical projection vs `hashstructure` + `Includable`

Both can omit zero values; the decisive difference is **whether an old algorithm can be frozen**, which `Includable` cannot deliver (Problem 6).

| | Canonical projection (chosen) | `hashstructure` + `Includable` |
| --- | --- | --- |
| Old algorithm frozen | Yes - version-tagged fields, golden-vector pinned | No - reflects the live struct/method-set |
| Sound replay (Part 2) | Yes | No (the disqualifier) |
| Meaningful empties | `!`-prefixed range per field | `fingerprint:"always"` per field |
| Type-name in hash | No (rename is drift-neutral) | Yes (rename moves every hash) |
| Plumbing | Version tags + generator + golden vectors | Value-receiver `HashInclude` on every nested struct + `v.(reflect.Value)` assert |

`Includable` keeps today's hashes byte-identical, which mattered for an *incremental* rollout - but that no longer matters once the reset rebuilds everything anyway, and it comes attached to a substrate that makes replay unsound. (Verified against `hashstructure` v2.0.2, the pinned version in `go.mod`: it reflects the live struct and method set at hash time and mixes `reflect.Type.Name()` into the digest - the properties Problem 6 turns on.) Projection trades byte-compatibility (which we are spending on the coordinated cutover regardless) for frozen replay (which we need forever). Adopted at the reset.

### D2: Version-tagged field selection, generated

Field membership lives in a per-field version-set tag (`fingerprint:"v1..*"`); a `go generate` step emits the per-version `projectVN` functions from those tags. This is the chosen mechanism over both a runtime reflective walker and hand-written functions - it takes the declarative authoring of the former and the compile-time guarantees of the latter. Rationale:

- **The unsafe direction is the false-negative** (a meaningful field silently omitted → missed rebuild → stale artifact, a G5 violation). A *mandatory* tag - absent → generation fails - makes the include/exclude decision impossible to *forget*. The *wrongly-excluded* case (a `-` tag on a build-effective field) is caught by the kept exclusion ledger, and the *wrongly-included-but-unmeasured* case by the [coverage backstop](#golden-vector-coverage-the-backstop).
- **Version-awareness is declarative.** A field's whole lifecycle - introduced at v3, dropped at v5, revived at v8 - is one greppable string on the field (`v3..v4,v8..*`), inexpressible in hand-written form, with no diff smeared across function bodies.
- **Frozen-ness stays structural.** Because the generated functions are checked-in code, a retained `projectVN` references each field by literal Go path - deleting a measured field won't compile, the emit-key is a literal, and regeneration-idempotence (CI `go generate` + diff) pins a shipped version's output. Golden vectors are the semantic backstop behind that, not the sole guarantee. This recovers the hand-written model's compile guarantee that a *runtime* reflective walker gives up (its output would reflect the live struct at hash time - Problem 6 one layer down), while keeping the DSL's declarative lifecycle.

The `go generate` *infrastructure* already exists (`stringer`/`mockgen` via `mage`), so the marginal cost is low - but the projection generator's **stakes** are categorically higher than those tools, and the design treats it accordingly. A `stringer` bug is cosmetic; a `mockgen` bug breaks test compilation and is caught instantly. A **projection-generator bug silently moves a shipped version's bytes → fleet-wide G5 (stale, undetectable except by the corpus) or G1 (mass churn).** The generator is therefore a first-class, fingerprint-load-bearing production artifact with its own test suite, and **regeneration-idempotence is a required CI gate** (the [`.github/workflows/generate.yml`](../../../.github/workflows/generate.yml) check is mandatory, never skippable) - without it the freeze degrades from structural to test-discipline. That is precisely why the coverage oracle and hand-frozen golden digests above are mandatory, not optional.

### D3: Atomic self-describing token; no format bump, reconcile via force-rehash

The stored hash is a single `v<N>:sha256:<digest>` token, not separate version and digest fields. One field, written atomically, so the version and the digest can never desync (the class of bug a split-field design invites when one is written and the other is not).

The lock **format** `Version` stays at `1`. Bumping it to `2` as a poison pill - to stop old binaries touching reset locks - is too blunt: it also stops them reading pins to *queue a build*. Instead, back-compat rests on two cheaper properties: the format is unchanged so every binary parses every lock, and the content-version registry **force-rehashes** any sub-floor token (legacy, or downgraded by an old binary) up to the current version. Old binaries stay useful (read pins, build); their only possible mischief - writing a legacy-substrate hash - is self-correcting on the next new-binary run, not silent corruption. Back-compat is therefore: **same format forever, reconcile fingerprints by version, never recompute history.**

### D4: Project to bytes, not a `ConfigHash()` method on the type

`project(config, version) []byte` returns canonical bytes; the combiner in `fingerprint` owns the `sha256` and the version dispatch. A `ConfigHash()` method that returns a finished hash was rejected: it drags crypto + versioning onto a data type, and it tempts callers to route around the version registry to get a raw, version-agnostic hash. Returning bytes keeps the config type ignorant of versioning, and keeps the combiner the **sole version authority**. See [the seam note](#where-the-hashing-logic-should-live).

## Alternatives considered

- **Incremental lazy migration on the `hashstructure` substrate** (the original plan): flip the inclusion default to omitempty via `Includable`, version the lock content, and migrate lazily - *without* a reset. Rejected: Problem 6 makes its central promise unkeepable. A "frozen" replay function built on `hashstructure.Hash` reflects the live struct, so the first field addition after the switchover moves the old algorithm's output and forces a rehash anyway. The incremental path therefore does not actually avoid a coordinated cutover - it defers one to the first field addition, on a substrate that makes replay unsound. With a coordinated cutover already scheduled (the dev→prod cutover), spending it once on a clean projection substrate is the better trade.
- **Global `IgnoreZeroValue`** - a blunt switch that omits *all* zero fields with no escape hatch for build-meaningful zeros, and still on the non-frozen `hashstructure` substrate. Rejected.
- **Parallel versioned structs with per-struct `Hash()`** - couples locks to Go type identity and duplicates hashing logic per version. Rejected in favor of Part 2's integer-versioned combiner over frozen projections.
- **Bump the lock format `Version` 1→2 as a poison pill** - makes old binaries hard-reject reset locks. Rejected: it also blocks old binaries from reading pins to queue a build, and it is unnecessary, since the content-version registry already force-rehashes any sub-floor or downgraded token (D3). Same-format + force-rehash keeps old binaries useful without risking silent corruption.
- **Eager fleet-wide migration as the steady-state mechanism** - rewriting every lock on every algorithm change is the mass-churn the design exists to prevent. Rejected for the steady state. The *reset* is a deliberate, one-time, operator-driven eager pass riding an already-scheduled rebuild - the sanctioned exception, not the rule; `component migrate` is its post-reset equivalent for retiring an old version.
- **Runtime reflective walker for field selection (instead of generated functions).** One generic `project(cfg, N)` reflects the struct at hash time and emits the fields whose version-set includes N. Least code, and it shares the tag syntax with the chosen approach. Rejected: it reflects the *live* struct at hash time - Problem 6 one layer down - so its frozen-ness rests entirely on golden-vector coverage (test discipline), and field removal degrades from a compile error to a CI failure. Codegen keeps the same tags but moves the reflection to *generate* time and freezes the output as checked-in code, recovering the compile guarantee.
- **Hand-written per-version `projectVN` functions (instead of generating them from tags).** Each version gets a bespoke function with one explicit `emit`/`emitAlways` line per measured field. Same compile guarantees as codegen (removal won't compile, literal emit-key), but: membership is smeared across N function bodies; "bring a field back a few versions later" has no first-class expression (you re-add an `emit` line, nothing ties it to the field's earlier life); and the mandatory-decision and coverage properties need separate bookkeeping the tags otherwise carry. Codegen is the same runtime with declarative authoring - strictly preferable given the existing `go generate` infrastructure.
- **Per-field hash manifest in the lock (instead of one opaque token).** Store `{field → hash}` (à la `go.sum`) rather than a single `v<N>:sha256:…` digest. *Genuine wins:* dropping a field becomes ignoring its manifest line - no projection kept alive for replay, so the **deprecate-then-delete two-step and the registry-retirement deadlock** (the append-only growth above) both vanish; and the stored-vs-stored historical comparators become structural set-diffs rather than version-blind string compares. *Why the opaque token still wins for azldev:* (1) the projection substrate **already** delivers additive immunity (G4) - the manifest's headline draw - so that advantage is moot, not additive; (2) the manifest does **not** kill the false-fresh hazard - an old lock has *no line* for a newly-measured input, so there is still no baseline to detect a change to it (the blind spot is relocated, not removed); (3) it makes *algorithm evolution* - the entire point of Part 2 - **harder**, needing per-field versioning where the token needs one integer for the whole algorithm; and (4) it bloats every lock to O(fields × components) (the well-known `go.sum` size cost). The manifest is the better tool for a *static* input set that mainly grows and shrinks; the opaque token + single version is the better tool for an *evolving hashing algorithm*, which is azldev's actual problem. The reset bakes the storage model in - token-vs-manifest is irreversible after PR B - and the retirement deadlock the manifest would have dissolved is instead answered by the floor-advance cadence above.

## Incremental delivery

The reset (Part 1) must land as one coherent change at the dev→prod cutover; its pieces are independently reviewable but ship together because they all move the hash.

1. **PR A (substrate)**: the **projection generator** (`go generate`) - reads the version-set tags and emits the per-version `projectVN(cfg) []byte` functions (literal emits, sorted keys) plus golden-vector and coverage scaffolding - the canonical encoder (`canonicalBuf`, `emit`/`emitAlways`), the version-set tag parser, the frozen **TOML-key** emit rule, the **split omit-predicate** (scalar leaves `IsZero`, composites projected emptiness), the `sha256` combiner, and the golden vectors. Generate-time guards: a fingerprinted field with **no tag** fails generation; the slimmed **exclusion ledger** and **dropped-fields ledger** replace the retired `TestAllFingerprintedFieldsHaveDecision` audit; **regeneration-idempotence** (CI `go generate` + `git diff --exit-code`) pins shipped versions. Pure addition alongside the existing path; not yet wired into `ComputeIdentity`. Tests: a field tagged `v2..*` is absent from generated `projectV1`; a `!` range emits at zero; a field with **no** `fingerprint` tag fails generation; a **nested** fingerprinted struct with a tagless field fails generation; deleting a field a retained `projectVN` names **fails to compile**; a **Go-field rename keeping the TOML key** yields a byte-identical digest; two fields colliding on one emit-key fail generation; a `!`-tagged nested struct whose every child is `-` (so its mandatory `!`-zero discrimination vector is unsatisfiable) is rejected as degenerate; the coverage oracle (by struct-reflection, not the tag) fails when a build-effective field is tagged too narrowly (`v1..v1` at current `v2`) and is not in the dropped-fields ledger; golden vectors pin v1; a non-contiguous set (`v1..v1,v3..*`) round-trips through the parser.
2. **PR B (reset cutover)**: switch `ComputeIdentity` to `projectV1`; adopt the atomic `v1:sha256:` token; unify on sha256. Lock format `Version` stays `1`, asserted by a named-constant test (`currentVersion == 1`) with a comment that the *content* version lives in the token prefix, not here - so a future format bump cannot silently break every historical read through `lockfile.Parse`. Ships at the cutover; absorbed by the scheduled rebuild. The `hashstructure` import and its `go.mod` entry are removed here, since no caller survives the switch. Unit tests: a legacy prefix-less token is read as sub-floor and force-rehashed to `v1`; a `v1:` token round-trips; an old binary (format `1`) still parses pins from a reset lock.
3. **PR C (Part 2 machinery)**: the **two-type token split** - `StoredToken` (parsed by the sole strict `ParseToken`: accepts only `sha256:<hex>` and `v<N>:sha256:<hex>`, malformed → *changed*, never an empty-digest false match; exposes `SameDigest` only) and `FreshToken` (from `ComputeIdentityAt`, exposes `Reconcile(stored) → {Fresh | Stale | RestampTo(v)}`, fails closed on its zero value), both **non-comparable** (`_ [0]func()`); the version registry (`lockAlgos`, `currentLockContentVersion`, `minSupportedLockContentVersion`); `ComputeIdentityAt`; and routing **every** comparison and compute site through these types. The **current-tree** sites (via `FreshToken.Reconcile`): replay-before-`Changed` in `update.go`, `checkFingerprintFreshness`, `BuildDirtyChange`, and the second `ComputeIdentity` caller `bumpComponents` (`update.go`); plus the `computeCurrentFingerprint` (`sourceprep.go`) return-type cascade `string → FreshToken`. The **historical** sites (via `StoredToken.SameDigest`): `FindFingerprintChanges`, `changed.go`'s `classifyComponent`, **and `haveMatchingFingerprints`**. **`haveMatchingFingerprints` is security-load-bearing:** it gates the cache-poisoning integrity check (`if result.SourcesChange && haveMatchingFingerprints(...)` in `changed.go`). If only `classifyComponent` is converted and this site is missed, the first legitimate `v2` bump makes a version-only re-stamp compare unequal → the integrity violation is **never recorded → tamper evidence silently swallowed**. It must convert to digest-compare in the same PR. Resolution replay reserved (slot reuses `computeRes1`). **Ordering gate (CI-enforced):** `currentLockContentVersion > 1` is forbidden unless `BuildDirtyChange` already routes through `Reconcile` - otherwise registering `v2` makes every component read persistently dirty on every `render`/`build`. The gate is necessary but not sufficient (it does not prove `haveMatchingFingerprints` converted), so it is paired with a **named acceptance test**: `from="v1:sha256:X"`, `to="v2:sha256:X"` ⇒ `haveMatchingFingerprints` returns **true** - a missed conversion fails CI rather than silently disabling the integrity check. **Not fully inert:** this PR switches the live compares from raw-string to token-routed *on merge* - only the *registry dispatch* is dormant while just `v1` exists. Unit tests: a synthetic `v1`/`v2` pair with unchanged inputs → `Current` and **not** `Changed`; changed inputs → `Stale`; re-stamp only on an already-dirty write; a digest-identical `v1`→`v2` re-stamp is **not** a changelog event and does **not** suppress `haveMatchingFingerprints`; the reset boundary `sha256:X`→`v1:sha256:Y` fires exactly once; a malformed token is treated as changed, never silently equal; a raw `==` on a token outside the `fingerprint` package fails to compile; a zero-value `FreshToken`/`StoredToken` fails closed; a historical site cannot construct a `FreshToken`; the registry `init()` panics on a `[minSupported,current]` gap; a named `classifyComponent({name:"v1:sha256:X"}, {name:"v2:sha256:X"}) == Unchanged` (the third raw historical compare, with no CI gate of its own); a `BuildDirtyChange(v2-token, headLock-v1-same-digest) == nil` (a `RestampTo` must not mint a dirty synthetic commit; the existing "not a changelog event" test exercises `FindFingerprintChanges`, not this path); a malformed token round-trips its **original raw bytes** through `MarshalText`, so a malformed lock is never rewritten on save (no spurious `FindFingerprintChanges` event).
4. **PR D (validation)**: scenario test (in the style of `scenario/component_changed_test.go`) - add a field absent from `projectV1` and set it on one component; assert only that lock drifts and every other lock is byte-identical.
5. **PR E (config schema axis, later)**: `schema-version` field + load-time canonical migration + the `config migrate` command. Gated on the first post-reset non-additive TOML change not already absorbed by the reset's normalization pass.
6. **PR F (forced lock migration, gated on the first floor raise)**: the `component migrate` command (the only sanctioned floor-raise; the prescribed fix for a build-critical newly-measured input) and the CI spread-ceiling on `currentLockContentVersion - minSupportedLockContentVersion`. **Gating:** a `v2` bump *without* PR F is safe - v1 stays in the registry and the floor stays at 1, so unmigrated locks still replay. PR F is required only before **raising `minSupportedLockContentVersion` above 1** (retiring v1), since that is what makes un-migrated locks unreplayable. A CI gate forbids raising the floor unless `component migrate` exists. So PR F is decoupled from the first `v2` and gated on the first floor raise **or** the first content-version bump whose decision rule demands immediate fleet-wide adoption (a build-critical newly-measured input, which cannot wait for lazy migration).

Each PR is independently revertible up to the cutover. PRs A-B land together at the dev→prod cutover (they move every hash and are absorbed by the scheduled rebuild); PR C is inert until the first post-reset algorithm change; PR D follows; PR E is gated on the first post-reset schema change, PR F on the first floor raise.

## Open questions

1. For the config schema axis, does `schema-version` live per-config-file or per-component? Per-file is simpler; per-component allows mixed-version projects during migration.

## Decisions settled in the body

Indexed here so they are not re-litigated; each is argued in full at the linked section.

| Decision | Where |
| -------- | ----- |
| Reset rides the already-scheduled dev→prod rebuild as the one sanctioned coordinated cutover | §The opportunity |
| Substrate is canonical projection (generated `projectVN` + golden vectors), not `hashstructure` | [§Substrate options](#substrate-options) |
| Field selection is **codegen** from mandatory per-field version-set tags (absent ⇒ generation fails); `go generate` emits the per-version `projectVN` | [§Version-tagged field selection](#version-tagged-field-selection) |
| Emit-key = frozen TOML key (`key=` override; duplicate keys fail generation); omit-predicate splits - scalar leaves `IsZero`, composites *projected* emptiness | [§Version-tagged field selection](#version-tagged-field-selection) |
| Tag DSL frozen at three range-operators (`..` `!` `*`) plus the orthogonal `key=` | [§Version-tagged field selection](#version-tagged-field-selection) |
| Canonical byte encoding = existing length-prefixed `<len>:<key>=<len>:<value>`; maps sorted-key; per-type value slots - pinned irreversibly at the reset | §The projection substrate |
| Frozen-ness = compiler + generator + regeneration-idempotence; golden-vector coverage (tag-independent dropped-fields oracle) is the backstop; exclusion ledger kept for `-` fields | [§Golden-vector coverage](#golden-vector-coverage-the-backstop) |
| Stored hash = atomic `v<N>:sha256:` token; lock format `Version` stays `1`; sub-floor/downgraded tokens reconciled by force-rehash | §The lock changes at the reset |
| Stored hash read only through the two-type token split (`StoredToken`/`FreshToken`, non-comparable), adopted in PR C | [§Downstream consumers](#downstream-fingerprint-consumers-blast-radius) |
| Version write-guard required (refuse to write above the binary's `currentLockContentVersion`); CI version-pin blocks old-binary commits | [§Registry floor](#registry-floor-and-forced-migration) |
| Back-compat: no reader recomputes a historical fingerprint (synthetic history / overlays read stored strings only) | [§Back-compat invariant](#back-compat-invariant-synthetic-history-reads-stored-strings-never-recomputes) |
| Registry retention is a floor, not "last N"; `component migrate` is the forced-migration pass (a deliberate release-grade event) | [§Registry floor](#registry-floor-and-forced-migration) |
| One content version, stored only in `InputFingerprint`'s prefix; `ResolutionInputHash` stays bare; resolution replay reserved | [§Both hashes share one version](#both-hashes-share-one-version) |
| Historical comparators compare the digest (strip `v<N>:`), so version-only re-stamps mint no release | [§Synthetic changelog path](#the-synthetic-changelogrelease-path-is-the-real-hazard) |
