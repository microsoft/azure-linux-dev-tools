# RFC 002: Lock-File Fingerprint Reset and Lazy Schema Migration

- **Status**: Draft
- **Author**: @damcilva
- **Created**: 2026-06-04
- **Related code**:
  - [`internal/fingerprint/fingerprint.go`](../../../internal/fingerprint/fingerprint.go) — `ComputeIdentity`, `ComputeResolutionHash`, `combineInputs`
  - [`internal/lockfile/lockfile.go`](../../../internal/lockfile/lockfile.go) — `ComponentLock`, `Parse` format-version gate
  - [`internal/projectconfig/fingerprint_test.go`](../../../internal/projectconfig/fingerprint_test.go) — field-inclusion audit
  - [`internal/app/azldev/core/components/resolver.go`](../../../internal/app/azldev/core/components/resolver.go) — `computeFreshnessStatus`, `checkFingerprintFreshness`
  - [`internal/app/azldev/cmds/component/update.go`](../../../internal/app/azldev/cmds/component/update.go) — `Changed` decision, re-stamp write
  - [`internal/app/azldev/cmds/component/changed.go`](../../../internal/app/azldev/cmds/component/changed.go) — `classifyComponent`, `haveMatchingFingerprints` (CI classification)
  - [`internal/app/azldev/core/sources/synthistory.go`](../../../internal/app/azldev/core/sources/synthistory.go) — `FindFingerprintChanges`, `BuildDirtyChange` (synthetic changelog/release)
  - [`internal/app/azldev/core/sources/sourceprep.go`](../../../internal/app/azldev/core/sources/sourceprep.go) — `computeCurrentFingerprint`

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

| Axis | Versions what | Lives where | Exists today? | Forced-migration verb |
| ---- | ------------- | ----------- | ------------- | --------------------- |
| **Config schema version** | on-disk TOML field shape | load / migration layer | No | `config migrate` (future) |
| **Lock content-hash version** | how inputs fold into the lock's stored hashes (`InputFingerprint` *and* `ResolutionInputHash`) | `fingerprint` combiner | No (implicitly v1) | `component migrate` |
| **Lock file format version** | lock file serialization | `lockfile` | Yes (`Version = 1`) | — (frozen at `1`) |

### The problem

Because field inclusion defaults to *included*, **adding any new fingerprinted config field re-hashes every component**, even components that never set the field. `hashstructure` hashes a zero-value field identically to a present-but-empty field — but *differently* from a field that does not exist in the struct at all. So the moment the Go struct gains `Foo string`, every component's `configHash` changes, every `InputFingerprint` changes, and every `*.lock` shows drift on the next `component update`.

Concretely: we add field `foo` and set `foo = "baz"` on package `bar`. The desired outcome is that **only** `bar.lock` drifts. The actual outcome today is that **all** lock files drift.

**The root concern is git churn, not rebuilds.** The mass rebuild is a knock-on effect; the thing we actually want to protect is the **lock-file diff in a PR**. A change that touches one package should produce exactly one changed `*.lock` — ideally zero changed bytes in any other lock file, in any way. Lock files should change *only* when there is a real, per-component change. Clean diffs keep PRs reviewable, keep `git blame` meaningful, and make "this lock moved" a trustworthy signal that *that component's* inputs actually changed. The rebuild fan-out follows for free once the diffs are clean.

There is a harder variant lurking behind the additive case: **non-additive** schema changes — renaming a field, removing one, changing a baked-in default, or fixing a bug in the hashing logic itself. These legitimately change the *meaning* of the config without changing user intent, and we will eventually need to absorb them without forcing every consumer to rebuild.

### The substrate problem: replay only works if old algorithms stay frozen

The natural fix for non-additive change is **versioned replay**: stamp an algorithm version into the lock, keep the old algorithm around, and when a lock is behind, recompute with *its* algorithm to ask "were the inputs actually unchanged, or did only the encoding move?" If unchanged, accept the lock without a rebuild.

Replay only works if an old algorithm function can faithfully reproduce the hash it produced when the lock was written. **On the current `hashstructure` substrate, it cannot** — a "frozen" algorithm function is not actually frozen:

- Its body is `hashstructure.Hash(component, …)`, which **reflects over the live Go struct**. Add a field later and the old function now sees that field (at zero value, included) → its output moves → it can no longer reproduce the historical hash. So *adding* a field breaks *replay of older versions*, which is exactly the additive case we are trying to make free.
- It also resolves the live **method set**: once `ComponentConfig` implements `Includable`, the same `hashstructure.Hash` call silently switches inclusion behavior, with no per-call opt-out (the interface is resolved automatically).

The consequence is sharp: an incremental "flip the default to omitempty, lazily migrate" plan **cannot keep its central promise.** "Additive fields are drift-neutral by construction" holds only for locks already at the new version; for the older locks that lazy migration deliberately leaves alone, the next field addition forces a hash change anyway. You do not avoid the mass rebuild — you defer it to the first field addition, and you build the whole replay apparatus on a substrate that makes replay unsound.

### The opportunity: a coordinated cutover is already scheduled

The project has a **dev→prod environment cutover** coming that forces a full rebuild regardless. This is a *coordinated cutover* — a one-time, distro-wide switch with no mixed-version window, the sanctioned moment to make changes that cannot be made lazily. That changes the calculus completely. The entire "lazy" framing exists to *avoid* a mass update; if exactly one sanctioned mass update is already on the calendar, the strategy inverts:

> **Lazy migration is for the cheap and additive. The one free rebuild is a budget — spend it exclusively on the one-way doors that are cheap now and a coordinated-cutover-only change later.**

This RFC therefore has two parts: **(1)** a one-time **reset** at the dev→prod cutover that replaces the hashing substrate with one whose old algorithms are *genuinely* frozen, and **(2)** a **post-reset lazy migration** mechanism (versioned registry + replay) that rides that clean substrate for the rare genuine algorithm change thereafter. Part 2 is what the original "lazy" design was reaching for; part 1 is what makes it sound.

### Goals

- **G1 (primary, non-functional): no spurious lock-file diffs *after the reset*.** Once prod locks exist, landing a config-schema or hashing change must not rewrite `*.lock` files for components whose effective inputs are unchanged. The reset itself is the *one* sanctioned exception, absorbed by the already-scheduled rebuild.
- **G2: only real changes drift.** Post-reset, a lock changes iff that component's build-effective inputs changed.
- **G3: piecemeal, lazy migration post-reset.** Genuine algorithm evolution after the reset rolls out per-component, riding independent changes, never as a big-bang.
- **G4: additive fields are drift-neutral by construction — *truly*, not just for new locks.** On the projection substrate (below) an unset additive field is invisible to *every* lock including old ones, because old versions emit only the fields their tags include — a field added later is not in any shipped version's tag set, so it cannot move an existing hash.
- **G5: correctness backstop preserved.** Never silently under-rebuild: a genuine input change must always drift its lock. Replay may accept encoding/over-capture changes; it must never mask a behavior-changing one.
- **G6 (new, hard): back-compatible reads for synthetic history.** The new binary must still **read** pre-reset locks across git history (synthetic changelog/release walks them), even though it **writes** only the new format. Reading never recomputes a historical hash — it compares stored strings only.

## Problem inventory

| # | Problem | Root cause | Severity |
| - | ------- | ---------- | -------- |
| 1 | Adding a config field drifts every lock, even unaffected components | Field inclusion defaults to *included*; zero-value ≠ absent in struct hash | Mass rebuild |
| 2 | No way to land a semantically no-op schema change (rename/move) without drift | Fingerprint hashes raw struct shape, not normalized intent | Mass rebuild |
| 3 | No way to evolve the hashing algorithm (bugfix, input reorder) without drift | `combineInputs` has no version; old and new outputs are incomparable | Mass rebuild + lock churn |
| 4 | No on-disk config schema version | `ConfigFile` has a `$schema` URL but no version field | Blocks managed migration |
| 5 | Migration is all-or-nothing | Freshness check is binary match/no-match against one stored hash | No piecemeal rollout |
| 6 | Versioned replay is unsound on the current substrate | "Frozen" algorithm = `hashstructure.Hash` over the **live** struct/method-set; adding a field moves the old function's output | Replay cannot reproduce historical hashes |

Problems 1–5 share a shape: a change that *should* be invisible to most components is forced to be visible to all of them, because the fingerprint cannot distinguish "input changed" from "encoding changed." Problem 4 is the missing primitive for managed config evolution. Problem 5 is the property we want from any post-reset solution — **per-component, lazy** migration. Problem 6 is the one that kills the *incremental* path outright: the very mechanism that would make problems 1–3 free (versioned replay) is unsound while the substrate reflects the live struct. Fixing 6 is what the reset buys.

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

- The set of fields walked is whatever the struct has *now* — add a field, and last year's `computeFP1` (whose body is still just `hashstructure.Hash(component)`) now includes it.
- Whether `Includable` is consulted depends on whether the type implements it *now* — not on what was true when v1 locks were written.
- A `value` vs `pointer` receiver subtlety even decides whether the root struct's `HashInclude` is seen at all (the top-level value is not addressable).

A function meant to be "the v1 algorithm, forever" therefore changes meaning every time the struct or its method set changes. That is the disqualifier for the incremental plan (Problem 6) and the motivation for the projection substrate below, whose v1 projection emits only its version-tagged fields and reads neither the method set nor the type name — immune to all three.

## Change taxonomy

Not every config change should be treated the same way. The right mechanism depends on what kind of change it is. This taxonomy drives the design.

| Class | Example | Should unaffected locks drift? | Mechanism |
| ----- | ------- | ------------------------------ | --------- |
| **Additive field** | new `foo` field, unset on most components | No — only setters drift | **Free, no bump.** Tag the new field `vN..*` (current version, omit-if-zero); a component that leaves it unset emits identical bytes, so no shipped hash moves — adding an omit-if-zero field to the live version is the one output-preserving no-bump edit. Setters drift (correct). |
| **Additive with non-zero default** | new field defaulted to `"auto"` via defaults merge | No | **Bump + replay.** The default resolves non-zero on *every* component, so it is emitted everywhere and would move every hash — omit-if-zero can't save it. Bump and tag the field `v(N+1)..*`; old locks **replay at their version** (whose set excludes it), match their stored digest → recognized unchanged → lazy re-stamp, no rebuild. |
| **Default change on an *existing* field** | bump `jobs` default `4`→`8` | Yes — every component's effective input moved | **Not lazy-maskable.** Replay recomputes the *current* config (now resolving to `8`) under the old algorithm → `jobs=8` ≠ stored `jobs=4` → genuine fleet-wide drift; replay cannot suppress it because the resolved value genuinely changed for everyone. Escape hatch: `config migrate` writes the *old* resolved value explicitly (`jobs=4`) into each config **before** moving the default — existing components then pin the old value (no drift) and only new components pick up `8`. Without that pre-pass it is a legitimate (if large) fleet rebuild, not a bug. |
| **Rename / move** | `foo` → `bar`, same semantics | No | **Schema migration + bump + replay.** Migrate old TOML → canonical struct (the rename lands in the struct), then tag the renamed field `v(N+1)..*`. Old locks replay at their version and are recognized unchanged → lazy re-stamp, no rebuild. |
| **Semantic change** | meaning of `foo` changes; output differs | Yes — that's correct | **None.** The build output genuinely differs, so the lock *should* drift. Replay at the old version would (correctly) mismatch → `Stale` → rebuild. Nothing to suppress. |
| **Hashing bugfix** | overlay ordering bug in the combiner | No | **Bump + replay.** Ship the fixed combiner as the version-`N+1` half of `computeFP(N+1)`; old locks replay at the old (buggy) version. If their inputs are unchanged the buggy digest still matches → recognized unchanged → lazy re-stamp to the fixed version, no rebuild. |
| **Newly measured input** | start folding in a new overlay source or identity element | No | **Bump + replay.** A non-config input is added in the combiner half of `computeFP(N+1)` (a config field would be tagged `v(N+1)..*` instead). Old locks replay at their version, which didn't fold it in, match their stored digest → recognized unchanged → lazy re-stamp, no rebuild. **Caveat:** until a lock migrates, replay is *blind* to the new input, so a change to it reads as fresh (false-fresh) — if it is build-critical, force a `component migrate` pass instead of riding lazy adoption (see [churn-avoidance](#churn-avoidance-policies-g1)). |
| **Field removal** | drop deprecated `foo` | No, if nobody set it | **Deprecate-then-delete (+ bump for setters).** Close the field's range at the prior version (`vK..*` → `vK..vN`, so v(N+1) stops measuring it) but **keep the field on the struct** so older versions can still read it for replay. Only after the floor passes vN (ideally after a `component migrate`) physically delete the field. Setters drift on the bump; non-setters replay clean. |
| **Resurrected field** | re-measure a previously-dropped `foo` | Depends — only if its value moved | **Tag edit (+ bump).** Append a new range to the field's set (`v1..v3,v8..*`) so v8+ measures it again while v1–v7 stay byte-identical (golden-vector-enforced). If the field was already physically deleted, bring it back as a fresh additive field tagged `v8..*`. The earlier life and the revival never collide because each version's output is pinned independently. |

The recurring requirement across the "No" rows is the same: **distinguish a change in user intent from a change in encoding, and only drift on the former.** Note the first row: on the projection substrate, a new field is added to `projectVN` as *omit-if-zero*, so a component that does not set it emits identical bytes and stays hash-neutral — *for every lock, old or new*, because old configs never set the brand-new field. Adding it does not move any existing hash (no shipped lock set it), so it needs no version bump. Part 2 then carries only the genuinely hard cases (rows 2, 5, and post-reset renames/removals). The shared move in every "Bump + replay" row is the same primitive — **increment the content version, keep the old `projectVN` as a frozen replay projection, and let unchanged locks re-stamp lazily** — detailed in [Part 2](#part-2--post-reset-lazy-migration).

> **`projectVN`** is shorthand used throughout this RFC for the canonical *projection at content-version N* introduced by this design (defined in [Substrate options](#substrate-options) and [The projection substrate](#the-projection-substrate)). It is **not** N hand-written functions: it is a single generic walker, `project(cfg, N)`, whose per-field membership is declared in version-set tags on the struct fields (see [Version-tagged field selection](#version-tagged-field-selection)). `projectV1` means `project(cfg, 1)` — the fields whose tag set includes v1; `projectV2` the next version, and so on. Each version's projection is frozen once shipped (its tags never move; golden vectors enforce it) — that is the whole point.

## Research

### Substrate options

Two substrates can produce a content fingerprint of the resolved config. The difference that matters here is **whether an old algorithm function can be frozen.**

- **`hashstructure` + `Includable` (rejected as the substrate).** Keeps existing hashes byte-identical and gives per-field omission via `HashInclude`. But, as established above (Problem 6), a function built on `hashstructure.Hash` reflects over the live struct and method set, so it cannot be a frozen historical algorithm. It also requires a value-receiver `HashInclude` on *every* nested fingerprinted struct and a subtle `v.(reflect.Value)` type-assert to work at all — brittle plumbing in service of a substrate that still can't host sound replay.
- **Canonical projection + stdlib hash (chosen).** Split the two jobs `hashstructure` fuses — *field selection* and *hashing* — into explicit steps. Field selection is **declared per field** as a version-set in the `fingerprint` tag (`fingerprint:"v1..*"`); a single generic walker, `project(cfg, N)`, emits the fields whose set includes version N in a canonical, sorted, self-delimiting byte form, and an stdlib `sha256` hashes those bytes. Because a shipped version's tag membership is **fixed and golden-vector-pinned**, `project(cfg, 1)` does not see fields added later, does not depend on the type's method set, and does not depend on receiver subtleties. It is a genuinely frozen pure function of `(cfg, version)` — the property replay requires. The cost is owning a small projection encoder, the version-set tags, and **golden hash vectors** per version (a checked-in `(config, version) → hash` table) so "frozen" is CI-enforced, not merely intended.

The projection substrate is what makes G4 true for old locks and what makes Part 2's replay sound. It is adopted at the reset (below), not incrementally.

### How other tools version lock state

- **Cargo (`Cargo.lock`)** carries an explicit `version = 4` at the top of the lock and teaches `cargo` to read older versions, upgrading in place on the next write. Migration is lazy — touching the lock upgrades it.
- **npm (`package-lock.json`)** uses `lockfileVersion` and supports reading v1/v2/v3, rewriting to the current version on install.
- **Terraform state** stores a `version` and a `terraform_version`; state is upgraded forward on use, never downgraded.
- **Go modules** avoid the problem entirely by hashing *content* (`h1:` dirhashes) rather than a struct shape, so adding metadata fields never perturbs existing sums.

The common pattern: an **integer version stamped into the persisted artifact**, plus the ability to **read and replay older versions**, plus **lazy forward-migration on write**. We keep `ComponentLock.Version` (the lock *format* slot) fixed at `1` and carry the *content* version **inside the `InputFingerprint` token** (`v<N>:sha256:…`) rather than in a separate struct field — one atomic value, no version/digest desync, no new TOML field for an old binary to mishandle. The Go-modules lesson is the deepest one: hashing *content* rather than struct shape is what makes additive metadata free — the canonical-projection substrate is our version of that lesson.

**Where this design goes beyond the precedent.** All four tools above keep exactly **one** active algorithm: Cargo/npm/Terraform rewrite the *whole* artifact to the current version on next touch (eager-on-write), and Go modules sidestep replay entirely by never re-migrating semantics. **None of them keeps N historical hashing algorithms alive simultaneously across an indefinitely-unmigrated fleet** — which is exactly Part 2's behavior. The citations support "version stamp + lazy forward-migrate on write"; they do *not* cover "frozen algorithms coexisting forever." That coexistence is justified here on its own terms (it is what avoids a fleet rebuild on every algorithm change), and its one real cost — append-only registry growth — is bounded by the [floor-advance cadence](#registry-floor-and-forced-migration), not by precedent.

### Where the hashing logic should live

With the projection substrate the fingerprint algorithm decomposes into two steps. **Both are versioned together** by the single lock content version — the version pins the *entire* fingerprint computation, not just the field list:

1. **Projection** — `projectVN(config)` names and serializes the config fields version N measures. This is *about the config type*, but it is data extraction, not hashing: it returns canonical **bytes**, not a hash.
2. **Combiner / orchestration** — reads overlay file contents (needs `opctx.FS`), folds in source identity / releasever / bump, applies domain separation, and runs `sha256` over the projection bytes plus those non-config inputs. None of these are config fields, but the combiner equally decides *what is measured*: starting to fold in a new overlay source, adding an identity input, or reordering the fold all change the digest exactly as a projection change does.

So the per-version compute function in the registry is the **whole algorithm** — `computeFPN` = `projectVN` + the combiner step frozen at version N. "Watching another field" splits cleanly: if it is a *config* field, it goes in `projectV(N+1)`; if it is a *non-config* input (a new overlay source, a new identity element), it goes in the combiner half of `computeFP(N+1)`. Either way it is a content-version bump absorbed by replay, never a silent hash move. The combiner is the **sole version authority**: it owns the registry and the dispatch, and `projectVN` is just the frozen config-extraction step it calls.

Expose the projection on (or beside) the config type and keep the combiner in `fingerprint`. **Do not** expose a `ConfigHash()` method on the type: a method that returns a finished hash both drags a hashing concern onto a data type *and* tempts callers to route around the version registry to get a raw, version-agnostic hash. Returning bytes from `projectVN` keeps the type ignorant of versioning and crypto.

## Proposed approach

The design has **two parts** with very different cost profiles:

1. **Part 1 — the reset (one coordinated cutover).** At the dev→prod cutover, swap the hashing substrate to canonical projection, declare the post-cutover projection as content-version **v1**, and spend the already-scheduled rebuild on every change that is *cheap now and a one-way door later* (the irreversible changes). Pre-reset locks already committed to **git history** stay readable and are never recomputed (the back-compat invariant below); a pre-reset lock in the **working tree** is force-rehashed to the `v1:` token on its first post-reset `update`.
2. **Part 2 — post-reset lazy migration (below).** A versioned registry + replay, now riding the *frozen* projection functions, absorbs the rare genuine algorithm change after the cutover, lazily and per-component, with no second coordinated cutover.

The original "lazy" instinct was right for Part 2 and wrong for Part 1: there is no way to make a substrate swap or a batch of one-way-door normalizations free, so they must ride the one rebuild we are already paying for. Everything that *can* be lazy (additive fields) is pushed into Part 2 and costs nothing.

## Part 1 — The reset

### The projection substrate

Replace `hashstructure.Hash(component, …)` with an explicit two-step pipeline:

```text
ComponentConfig ──project(cfg,1)──► canonical bytes ──sha256──► configHash
                  (version-tagged fields,              (stdlib)
                   sorted keys, emit-if-nonzero)
```

`projectV1` is the projection at version 1 — `project(cfg, 1)`. Field membership is declared **on each struct field** as a version-set in the `fingerprint` tag (`fingerprint:"v1..*"`); a single generic walker emits, in stable key order, every field whose set includes the target version, length-prefixing key+value so distinct field sets cannot collide. It omits a field when its **resolved value is zero** (omit-if-zero, an encoder property now, not a struct-tag toggle); a range prefixed with `!` (e.g. `!v1..*`) always-emits, for fields whose zero is build-meaningful. There is no per-version function — only the generic walker parametrized by version. (Grammar and recovery semantics: [Version-tagged field selection](#version-tagged-field-selection) below.)

Three things this buys that `hashstructure` could not:

- **Frozen by construction.** A version's field set is fixed by tags that never change for a shipped version (golden vectors enforce it), so adding `Foo` to the struct later is invisible to `project(cfg, 1)` — its output for an old config is unchanged. This is what makes Part 2's replay sound (Problem 6) and G4 true for *old* locks, not just new ones.
- **No method-set / receiver magic.** No `Includable`, no per-nested-struct method, no `v.(reflect.Value)` type-assert footgun. Selection is a declarative tag the walker reads.
- **Golden-vector enforced.** A checked-in table of `(config, version) → hash` vectors is asserted in CI, so any accidental change to a historical projection — a tag edit that moves a shipped version's membership — fails the build. "Frozen" stops being a promise and becomes a test.

The cost is owning the projection encoder and the golden vectors. That cost is paid once, at the reset, against a rebuild we are already doing.

### Version-tagged field selection

Field membership in each version's projection is declared **on the struct field**, as a version-set in the existing `fingerprint` tag — not in a hand-written per-version function. One generic walker, `project(cfg, N)`, emits every field whose set includes `N`. This is the chosen mechanism; hand-written `projectVN` functions are the [Option B alternative](#alternatives-considered).

**Grammar** (deliberately small):

```ebnf
tag     = "-" | member, { ",", member } ;
member  = [ "!" ], range ;            (* leading "!" ⇒ always-emit for this range *)
range   = version, [ "..", ( version | "*" ) ] ;
version = "v", digit, { digit } ;
```

| Tag | Meaning |
| --- | --- |
| *(absent)* | **build failure** — every fingerprinted field must carry an explicit decision |
| `-` | never measured (unchanged from today) |
| `v1..*` | measured from v1 onward, omit-if-zero — the common "active field" case |
| `v1..v4` | measured v1–v4, then dropped |
| `v3..*` | introduced at v3 |
| `v1..v4,v6..*` | measured v1–v4, **dropped at v5, brought back at v6** |
| `!v1..*` | measured v1 onward, **always-emit** (zero value still hashes) |
| `v1..v4,!v5..*` | omit-if-zero v1–v4, then **always-emit from v5** (the temporal toggle) |

`*` resolves to "this version and every later one," so an *active* field never needs a tag edit across a version bump — only a field that is *dropped* at the bump gets its range closed (`v1..*` → `v1..vN`).

**The emit-key is the field's TOML key, frozen — never the Go identifier.** The walker emits each field under a stable string key and sorts by it; that key is the field's **`toml:` name**, or — for a field with no usable TOML key — an explicit `key=<name>` member in the `fingerprint` tag (grammar below). The key is pinned once and treated as part of the frozen output. It is deliberately *not* the Go field name, so a cosmetic Go rename (`Foo`→`Bar`, same TOML key, same tag) is genuinely byte-neutral — making the [struct-rename drift-neutral claim](#config-schema-version-and-canonical-migration-future) true at the *field* level too, not just the type level. Renaming the *emit-key* is an output-changing edit and therefore a version bump like any other. **Duplicate emit-keys within one retained version fail the build** (two fields resolving to the same key would collide and alias — a silent G5 hazard), so the key set is checked for uniqueness at every retained version.

**The grammar is frozen at three range-operators** (`..` range, `!` always-emit, `*` open-end), plus the orthogonal `key=` override:

```ebnf
tag     = "-" | [ keyopt, "," ], set ;
keyopt  = "key=", identifier ;       (* emit-key override; default is the toml: name *)
set     = member, { ",", member } ;
member  = [ "!" ], range ;            (* leading "!" ⇒ always-emit for this range *)
range   = version, [ "..", ( version | "*" ) ] ;
version = "v", digit, { digit } ;
```

It deliberately re-invents protobuf's `reserved` field-range discipline, and protobuf survived because `reserved` never grew. Adding a fourth *range*-operator is an RFC-grade change, not a tag edit — cheap insurance against a bespoke mini-language accreting edge cases.

**Recovery is the property that justifies the range syntax.** The hard requirement: if we drop a field, then versions later realize we need it again, we must be able to bring it back *without* disturbing any frozen historical hash. The rule that guarantees it: **you only ever *add* a range for the *new* version; you never edit a shipped version's membership.** Walk it:

- `Foo` tagged `v1..*`. At the v2 bump we drop it → edit to `v1..v1`. v1 still emits `Foo`; v2+ does not.
- At v5 we need it back → edit to `v1..v1,v5..*`. **v1's membership is unchanged (still in the set), v2–v4 unchanged (still out), only v5+ is added.**

Every frozen output is byte-preserved, and the **golden vectors prove it**: the edit `v1..v1` → `v1..v1,v5..*` must leave the v1–v4 vectors identical or CI fails. The grammar lets you *express* the non-contiguous set; the golden vectors *forbid* rewriting history while doing so. Two recovery flavors, both covered: (a) field still on the struct (lingering for replay) → reopen its range; (b) field already physically deleted (floor passed it) → bring-back is just a fresh additive field tagged `vN..*`. Same outcome, no special case.

**Always-emit is per-range, for the same reason.** Whether a field's *zero value emits* can change over time just as its membership can — so `!` flags an individual range, not the whole field. `v1..v4,!v5..*` means omit-if-zero through v4, then always-emit from v5. Toggling it is an *output-changing* edit (a zero-valued field starts or stops emitting), so it lands as a new range at a new version exactly like a drop/re-add — same output-preservation rule, same golden-vector enforcement. The walker just asks "which range holds N, and is it `!`?" A whole-field always-flag could not express this temporal toggle without a per-version walker.

**What tags version, and what they don't.** Tags version *membership* — which fields a version measures. They do **not** version *encoding* — how a field's bytes are formed, or how the combiner folds non-field inputs. So the generic walker absorbs additive / removal / bring-back changes as pure tag edits (zero code), while a genuine encoding or combiner change still ships as versioned code in `computeFP(N+1)` (the walker output + the combiner step frozen at N). The taxonomy's non-additive rows are exactly that small set.

> **The walker still reflects the live struct — tags freeze only *membership*, golden vectors freeze the rest.** `project(cfg, N)` reflects the live struct exactly as the rejected `hashstructure` substrate did (Problem 6). The version-set tag re-freezes *which fields* a version measures, but three other things the walker reads from the live struct are **not** frozen by the tag — the **emit-key** (frozen instead by the immutable-TOML-key rule above), the **per-field encoding/type** (how a value becomes bytes), and the **zero-predicate** (what counts as omittable). All three are re-frozen by **golden-vector coverage**, not by code structure: a change to any of them moves a covered vector and fails CI. [Golden-vector coverage](#golden-vector-coverage-is-the-load-bearing-invariant) is therefore the load-bearing invariant of the whole substrate — the one place this design trades a *structural* guarantee for a *test* guarantee. It is sound only because the coverage rule below is stated and enforced.

**Enforcement**, four layers — the first three are cheap syntactic guards, the fourth is the load-bearing one:

1. **No tag → build failure** makes the include/exclude decision impossible to *forget*: a field with no `fingerprint` tag fails the build. This restores the safe inclusion default the bare projection gives up — a forgotten field would otherwise drop silently out of the hash (a G5 stale-artifact hazard).
2. **Well-formedness:** ranges parse, are sorted and non-overlapping, and name no version above `currentLockContentVersion` (open `*` excepted); a `!` prefix is per-range and orthogonal; **emit-keys are unique within every retained version**. A malformed, future-referencing, or key-colliding set fails the build.
3. **Exclusion ledger (kept, not retired).** A field tagged `fingerprint:"-"` is the **dangerous** direction — it removes the field from the hash, the G5-violating way to ship a stale artifact — so it gets *more* scrutiny, not less. An enumerated list (the surviving half of today's `expectedExclusions`) names every `-` field with a one-line justification; adding or removing a `-` requires editing the list, so an accidental exclusion fails CI. Mandatory tags fix the *forgotten*-field default, but only this independent ledger catches a *wrongly-excluded* field — no coverage test exercises a field that claims to be unmeasured.
4. **Golden-vector coverage** — the keystone (next).

#### Golden-vector coverage is the load-bearing invariant

Frozen-ness moved from a *structural* property (the rejected hand-written functions would not *compile* if you deleted a named field) to a *test* property (the golden-vector table forbids the live walker from drifting). That trade is sound **only if the corpus actually exercises every field of every retained version.** State it as a first-class, enforced invariant:

> **Coverage invariant:** every field whose tag set includes a retained version MUST appear, **non-zero**, in at least one retained golden vector — and a discrimination check must vary it and assert the version's hash *moves*. A field that is never exercised non-zero is invisible to CI, so any drift in its emit-key, encoding, or zero-handling would pass silently.

**The oracle must be independent of the tag.** The subtle trap: if the discrimination check derives *which versions to test* from the same range tag it is meant to police, a wrong-narrow tag silences its own check. A field build-effective today but mistagged `v1..v1` (while `currentLockContentVersion` is `v2`) tells a tag-derived generator "only check v1" — so the v2 discrimination check the invariant promises **never runs**, and the field silently drops out of the current hash (G5). The fix is a tag-independent oracle: **every fingerprinted field on the struct is expected to be measured-and-discriminating at the *current* version, unless it appears in a reviewed *dropped-fields ledger*** (sibling to the `-` exclusion ledger, naming each field intentionally closed at version N with a justification). The coverage test reads that ledger, *not* the range tag, to decide what to assert — so a range that excludes the current version without a matching ledger entry fails CI. The tag is the implementation; the ledger is the oracle; they are checked against each other.

This single rule mechanically closes three holes at once, each otherwise a silent stale-artifact (G5) bug that escapes CI *precisely because* some field is unset in the vectors:

- **Wrong/narrow range** (e.g. `v1..v1` on a field that is build-effective at v2, or a typo'd gap `v1..v4,v6..*` dropping v5): the field is not in the dropped-fields ledger, so the oracle still expects a *current-version* discrimination move — the tag says it is unmeasured there, the assertion fires, CI fails. (This is the case the tag-independent oracle exists to catch.)
- **Emit-key drift** (a field rename that reached the key): the covered config now emits under a different key → its vector moves → CI fails.
- **Encoding/type drift** (a field's bytes change shape under the live walker): same — the covered vector moves.

Without coverage, all three pass CI whenever the field happens to be zero in the corpus. With it, "the golden vectors prove frozen-ness" becomes a true statement instead of an aspiration. This is the substrate's one *test*-enforced (not structurally-enforced) guarantee, and it must be wired in PR A alongside the vectors themselves.

### Baseline v1 — omit-if-zero, no include-always legacy

Because the reset rebuilds everything, there is **no pre-existing population to stay byte-compatible with.** That removes the single biggest constraint of the incremental plan: we do **not** need an `include-always` compatibility mode to preserve today's hashes. `projectV1` is the omit-if-zero projection from day one. There is no `computeFP1 = legacy include-always` entry to carry forever — the registry's floor *starts* at the clean projection.

```go
// Membership is declared per field as a version-set tag; one generic walker
// emits the fields whose set includes the target version, in stable key order.
// What freezes a version is that its tags never change once shipped (golden
// vectors enforce it) — not that the walker is bespoke.
type ComponentConfig struct {
    Upstream   string   `toml:"upstream"    fingerprint:"v1..*"`   // key "upstream", omit-if-zero
    Patches    []string `toml:"patches"     fingerprint:"v1..*"`   // omit-if-zero
    StripDebug bool     `toml:"strip-debug" fingerprint:"!v1..*"`  // always-emit: zero (false) is build-meaningful
    Internal   string   `toml:"-"           fingerprint:"-"`       // never measured
    // … every fingerprinted field carries an explicit tag; absent ⇒ build failure …
}

// fingerprintFields recurses into nested fingerprinted structs (same scope as
// the retired audit), returning each leaf field with its frozen TOML key, its
// version-set, and the resolved value. The key — not the Go field name — is what
// gets emitted, so a Go rename is byte-neutral.
func project(c *ComponentConfig, version int) []byte {
    var b canonicalBuf
    for _, f := range fingerprintFields(c) {     // reflection, cached, sorted by TOML key
        r := f.set.rangeContaining(version)
        if r == nil {
            continue                             // field not measured at this version
        }
        // omit when the resolved value IsZero, UNLESS this range is '!' (always-emit).
        if !r.always && f.value.IsZero() {
            continue
        }
        b.emit(f.key, f.value)
    }
    return b.Bytes()
}
```

**Why omit-if-zero is safe — fingerprints see the resolved config.** The usual objection to blanket omit-if-zero is the false-negative footgun: a field whose zero is meaningful gets omitted and collides with "unset," so two semantically different configs hash the same and a rebuild is missed. That objection assumes we hash *raw user input*. We do not. `ComputeIdentity` runs on the **resolved, post-merge** config (`*result.config`, after defaults are applied). The omit predicate is therefore "the *resolved value* equals Go-zero," not "the user didn't type it." Consequences:

- Two configs that both resolve a field to zero build identically → hashing them the same is **correct**, not a collision.
- "Unset" never reaches the hasher — it has already been resolved to its default. If the default is non-zero, the field is non-zero and is emitted anyway. If the default *is* zero, then unset and explicit-zero resolve identically → same build → same hash → correct.

So the classic false-negative requires absence ≠ zero-default *at the point of hashing*, and post-merge resolution closes that gap. The load-bearing invariant is **G5's guarantee restated structurally: the fingerprint must see exactly the build-effective resolved config.** That invariant must already hold, or fingerprinting is broken independently of this change. A `!`-prefixed range is the escape hatch for the rare field whose zero value is build-meaningful.

**Result:** additive fields are drift-neutral **by construction** (G4) — a newly added field, listed omit-if-zero in `projectVN`, emits nothing for any component that does not set it, so it is invisible to every lock that leaves it unset, old or new. Adding it moves no existing hash (no shipped lock could have set a field that did not yet exist), so it needs no version bump. Only setters drift (G2).

#### Edge cases under omit-if-zero

The omit predicate is **`reflect.Value.IsZero()`**, one global rule for every field (resolving former Open Q#3); `!` is the only per-field override. The consequences need stating because `IsZero` is type-specific:

- **Meaningful zero with a non-zero default** (e.g. `int Jobs` defaulting to `4`, where `0` means serial). Post-merge: unset → `4` (emitted), explicit `0` → omitted. These build differently *and* hash differently, so there is no collision — they are consistent. Use a `!` range only if a zero value must be distinguishable from a future change of default.
- **nil vs empty slice — they hash *differently* under `IsZero`.** A nil slice is zero → omitted; a non-nil empty slice (`[]`) is **not** zero → emitted. If post-merge resolution can produce *either* nil or `[]` for the same intent, that ambiguity would move a hash — so the rule is: **resolution must normalize to one canonical form**, and where an explicit-empty value is build-meaningful and reachable, tag the field `!` so nil and empty both emit and stay distinguishable. This is a constraint on the resolver, pinned by a golden vector, not a free-for-all.

### The reset load-out — what to spend the free rebuild on

The reset rebuild is a budget. Spend it on the irreversible / cutover-only changes; **do not** spend it on anything Part 2 can do lazily for free. Priority order:

1. **Switch the substrate to canonical projection.** Foundational, one-way, enables everything else. (Above.)
2. **Establish `projectV1` as omit-if-zero with no include-always legacy.** The compatibility mode never enters the registry, so it never has to age out.
3. **Keep the lock *format* `Version` at `1` — the content-version token carries the reset.** The reset adds **no new TOML field** (the atomic token in item 4 reuses `InputFingerprint`) and touches **no** pinning field (`upstream-commit`, `import-commit`, `manual-bump`), so an old binary still parses a reset lock and reads everything it needs to *queue a build*. The substrate swap rides entirely on the content-version machinery (Part 2): pre-reset locks carry a legacy (prefix-less) token below the registry floor, and the reset is simply the **first forced upgrade** of the fleet to the `v1:` token. This also makes the one real mixed-toolchain risk self-correcting: if an old binary ever rewrites a reset lock with its legacy-substrate hash, the next new-binary run sees a sub-floor token and **force-rehashes** it back to `v1` — a clean forced upgrade, never silent corruption (next subsection).
4. **Adopt an atomic, self-describing `v1:sha256:…` token** for the stored hash, so the version and the digest can never desync (closes the re-stamp/desync class of bug where the version field and the hash field are written independently).
5. **Unify on `sha256` everywhere**, retiring the `uint64`→decimal-string wart from the `hashstructure` era. One hash format, one encoding.
6. **Do every pending rename / default-normalization now.** Renaming a field, moving content between structs, or changing a baked-in default is a one-way door under Part 2 (it needs a version bump + replay); at the reset it is free because everything rebuilds anyway. This is where the schema-axis "hardest cases" get absorbed cheaply.

**Anti-goal:** do *not* burn reset budget on additive fields — Part 2 handles those for free, forever. The success criterion for the load-out is that **no *routine* change ever forces a second coordinated cutover**: after the reset, every ordinary change must be expressible as either a free additive field or a lazy Part 2 version bump. Retiring an *old* content version is the one sanctioned exception — a fleet-wide `component migrate` is itself a deliberate, planned, reset-grade event (see [Registry floor](#registry-floor-and-forced-migration)); the goal is that nothing *unplanned* ever forces one.

### The lock changes at the reset — atomic token + forced upgrade

The stored hash becomes a single self-describing token:

```text
input-fingerprint = "v1:sha256:9f86d0…"   # <content-version>:<algo>:<digest>
```

One field carries both the content version and the digest, so they cannot be written out of step (the desync bug a split version/digest field invites). Parsing splits on `:`; an absent prefix on a pre-reset lock reads as the legacy format.

The lock **format** `Version` stays at `1`. The on-disk *schema* is unchanged — same fields, same TOML shape — so an old binary still parses a reset lock and reads its pins (`upstream-commit`, `import-commit`, `manual-bump`), which is all it needs to queue a build. What changes is the *value* of `InputFingerprint`: the substrate swap is expressed purely as a content-version step, and the reset is the **first forced upgrade** to the `v1:` token. The existing singleton `Parse` gate (`Version == 1`) is left untouched; all substrate/version reconciliation routes through the content-version registry instead of a format gate.

Recovery from a sub-`v1` token is the **same mechanism** as the reset itself: a token with no `v<N>:` prefix (or a version below `minSupportedLockContentVersion`) cannot be replayed, so it is treated as `Stale` and **force-rehashed** to the current version on the next `update`. One code path unifies three cases:

- **Pre-reset locks** carry a legacy decimal hash with no prefix → force-rehashed to `v1` at the reset.
- **An old binary that rewrites a reset lock** stamps its legacy-substrate hash (no prefix) → the next new-binary run force-rehashes it back to `v1`. The mischief is self-correcting, never silent corruption.
- **A future floor raise** (after a deliberate `component migrate`) retires an old `v<N>` the same way.

This is the one place back-compatibility is load-bearing, and it is satisfied without a format bump: old binaries read pins and build; the fingerprint value reconciles by version. See the next section for why reading *historical* locks never needs to recompute their hash at all.

### Back-compat invariant — synthetic history reads stored strings, never recomputes

The reset is only safe because of a property of the codebase verified against the source: **nothing that reads a *historical* lock ever recomputes a fingerprint for it.** Every historical reader compares the *stored* hash strings; the only code that recomputes a fingerprint does so for the **current working tree against HEAD**, never against an arbitrary past commit. Concretely:

| Reader | What it does with a historical lock | Recomputes? |
| ------ | ----------------------------------- | ----------- |
| `synthistory.FindFingerprintChanges` | walks `lockfile.ShowAtCommit`→`Parse`, compares `InputFingerprint` *strings* between adjacent commits | No |
| `synthistory.BuildDirtyChange` | compares the precomputed current fingerprint to HEAD's stored string | No (HEAD only) |
| `sourceprep.computeCurrentFingerprint` | the *only* `ComputeIdentity` call on this surface — computes for the **current tree**, compares to HEAD's stored hash | Current tree only |

The consequence: **swapping the substrate is invisible to synthetic history.** A pre-reset (legacy-token) lock and a post-reset `v1:` lock are just two different opaque strings at two different commits; the walker reports "changed" across the reset commit (correct — it *is* a notable, deliberate, fleet-wide event, the coordinated cutover) and never tries to recompute either side. Applying historic overlays likewise reads stored lock fields and needs no hash recomputation.

> **Invariant (must hold forever):** synthetic history and historic-overlay application operate on **stored lock fields only.** No reader recomputes a fingerprint for a historical commit. This is precisely what lets a frozen `projectVN` be *forward-only*: it never has to reproduce a hash from a different substrate generation, only hashes the lock that the *current* binary writes. A future change that recomputes a historical fingerprint would break this and must be rejected in review.

This invariant — no reader recomputes a historical fingerprint — is the complete back-compatibility story: **new-reads-old by string, never-recompute-old by algorithm.** The lock *format* never bumps, so old and new binaries parse every lock identically; only the *interpretation* of the fingerprint value evolves, and that rides the content-version registry.

## Part 2 — Post-reset lazy migration

The reset gives us a clean, frozen substrate. Part 2 is the machinery that rides it for the rare genuine algorithm change *after* the cutover — lazily, per-component, with no second coordinated cutover. This is the original "lazy" design, now sound because `projectVN` is genuinely frozen.

### Versioned lock content with lazy replay (algorithm changes)

Stamp one **lock content-hash version** into the lock (the `v1:` prefix of the atomic token) and teach the freshness check to **replay** older versions. The version governs *both* stored hashes (`InputFingerprint` and `ResolutionInputHash`) — they live in one lock, share one write event, and a single integer is the natural fit (see [scope note](#both-hashes-share-one-version) for why one version, not two):

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

3. In `checkFingerprintFreshness`, compute at the **current** version. On mismatch, if the lock's token version `< current`, recompute at the lock's token version. If *that* matches the stored digest, the inputs are unchanged and only the algorithm evolved → treat as `FreshnessCurrent` and flag for silent re-stamp. Otherwise → `FreshnessStale`. (The resolution hash reuses `computeRes1` until its algorithm first changes — see scope note.)
4. `component update` re-stamps the token to the **current** version **only when it is already writing for an independent reason** (see the churn policy below). Migration is therefore **lazy and per-component**: a lock upgrades only when something independently touches it.

This resolves Problems 2 (for default changes), 3 (hashing bugfixes), and 5 (piecemeal rollout). It is the same lazy-forward-migration pattern Cargo/npm use, specialized to a content hash.

#### Both hashes share one version

`ComponentLock` carries two persisted content hashes: `InputFingerprint` (render inputs, via `projectVN` + `sha256`) and `ResolutionInputHash` (upstream-resolution inputs — a flat SHA256 over seven explicit fields in `ComputeResolutionHash`). Both have the **same evolution problem**: appending an input or reordering the fold moves every lock's hash → G1 churn.

We version them with **one shared integer** (the token's `v<N>` prefix), not two axes, because: they co-locate in a single lock, they are written in the same `update` pass, and a paired registry lets either evolve independently while the other reuses its prior function. Two separate version fields would double the floor/replay/migrate machinery for an input set (`ResolutionInputHash`) that changes rarely — YAGNI. **The shared integer is permanent, made safe by digest-comparison.** The one hazard a shared prefix could create: a *resolution-only* algorithm bump drags the `InputFingerprint` token's prefix `v1`→`v2` while its digest is unchanged (the fingerprint algorithm was reused), and a *full-token* changelog walker would misread that prefix move as a release. We close it not by splitting the version but by having the historical changelog/classifier comparators compare the **digest** (the `<algo>:<digest>` tail), stripping the `v<N>:` prefix: a resolution-only bump moves the prefix but not the digest → no phantom release; a real input change moves the digest → fires. Both fields are always co-written in the same `update` pass and the prefix advances whenever *either* algorithm advances, so the single prefix is a correct version for both. (See [the synthetic-history path](#the-synthetic-changelogrelease-path-is-the-real-hazard).)

**Phasing.** The atomic token format (`v<N>:sha256:…`) is fixed at the reset. Fingerprint replay is wired in Part 2's first PR; **resolution-hash replay is reserved, not yet wired** — the slot exists and `computeRes1` is reused, so the day `ComputeResolutionHash` first changes we add `computeRes2` and extend replay to its one comparison site (`checkResolutionFreshness` + the `resHashChanged` silent-write guard in `update.go`), with no schema change. The deferral is safe because of its smaller blast radius — see [`ResolutionInputHash`](#resolutioninputhash--shares-the-version-replay-deferred).

#### Churn-avoidance policies (G1)

The version stamp is itself a potential source of spurious diffs — the exact thing G1 forbids. The rule that prevents it is one idea: **judge "changed?" by replaying the lock's *own* version, not the current one.** Everything below follows from that.

**Why the obvious approach is wrong.** Today `update.go` sets `result.Changed = true` the instant `lock.InputFingerprint != identity.Fingerprint`, where `identity` is computed at the **current** version. That comparison sits *upstream* of the write guard `if !result.Changed && !resHashChanged { return false, nil }`. So the moment you ship a v1→v2 *algorithm* change, the current-version hash differs from every stored v1 token, `Changed` flips for **~every component at once**, and you get the mass auto-release-bump + mass lock rewrite G1 exists to prevent. The version stamp cannot "harmlessly ride the `Changed` path" — it *triggers* it.

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
// real reason — the version upgrade piggybacks a real write, never triggers one.
if result.Changed {
    lock.InputFingerprint = identity.Token() // current version + digest, written together
}
```

This makes migration strictly **opportunistic**: a lock advances its version the next time its component changes for real, and not one commit sooner. Because the version lives *inside* the atomic token, a lock at `v1` with unchanged inputs keeps its exact `v1:sha256:…` bytes — there is no separate version field to materialize and no zero-diff bookkeeping. (When resolution replay is wired, the same replay-before-compare guards the `resHashChanged` write.)

**The unavoidable flip side — false-fresh on a newly-measured input.** "Replay at the lock's own version" is what buys churn-avoidance, but it is the *same* property that creates a blind spot, because replaying `computeFP(old)` is **blind to any input that version did not measure.** Concretely, when v2 starts folding in an input v1 never touched (the [*Newly measured input*](#change-taxonomy) row):

- A change to that **new** input on a still-`v1` lock replays at v1, which ignores it → digest still matches → **`Changed = false`** → the change is silently treated as fresh.
- The new input only takes effect on that lock when the lock migrates to v2 — i.e. the next time it is dirtied for an *independent* reason, or via `component migrate`.

This is correct *by contract* (a v1 lock promises freshness under the v1 input set, which excludes the new input), and harmless for a cosmetic input. But for a **build-critical** new input it is a latent-stale hazard: artifacts can lag the new input by an unbounded number of commits. **Decision rule:** if a newly-measured input must take effect fleet-wide immediately, do **not** rely on lazy adoption — pair the version bump with a deliberate `component migrate` (see [Registry floor and forced migration](#registry-floor-and-forced-migration)). Lazy adoption is the default; `component migrate` is the opt-in for inputs that cannot wait.

#### Registry floor and forced migration

Lazy migration means an untouched lock can sit at an old version **indefinitely** (G3 by design). That makes "keep the last *N* versions" a **correctness cliff, not a tuning knob**: if pruning drops the compute function a lock still depends on, replay becomes impossible → forced `FreshnessStale` → the mass rebuild/rewrite (and, via the downstream-consumer analysis below, mass changelog churn) the whole design exists to avoid. So the floor must be explicit and paired with an escape hatch, decided now:

- **`minSupportedLockContentVersion`** is a hard floor. A lock below it cannot be replayed and is treated as `Stale`. Dropping a registry entry is therefore a deliberate, breaking, announced act — never incidental cleanup.
- **`component migrate`** (Open Q#5, promoted to a requirement) force-advances every lock to the current content version in one deliberate pass. This is the *only* sanctioned way to retire an old version: migrate the fleet first (one intentional, reviewed, fleet-wide commit), then raise the floor. Note this pass is a deliberate G1 exception — it *is* the eager migration G1 normally forbids, made safe by being explicit and operator-driven rather than a silent side effect. **Contract:** it is *offline* — it loads each lock, recomputes the fingerprint at `currentLockContentVersion`, and rewrites the token; it does **not** re-resolve upstream (`upstream-commit`/`import-commit` untouched, unlike `update --force-recalculate`) and does **not** touch the manual-bump counter (unlike `--bump`). It *does*, however, move every *fingerprint* digest when it retires a fingerprint algorithm — advancing that algorithm is the whole point — so a fleet-wide migrate of that kind **is a fleet-wide, release-grade event**: `FindFingerprintChanges` reads each moved digest as notable, exactly as [the synthetic-history trap](#the-synthetic-changelogrelease-path-is-the-real-hazard) warns. (A migrate that retires only a *resolution* algorithm moves the shared prefix but not the `InputFingerprint` digest, so it is correctly release-silent.) That is *why* migrate is reset-grade and rare, not a free background sweep — the release churn is the deliberate cost of retiring a version. The on-disk *config* axis has its own verb, [`config migrate`](#config-schema-version-and-canonical-migration-future); the two are orthogonal — each lives with the artifact its command group already owns (`component` writes locks, `config` owns the TOML).
- **Floor-advance cadence.** Because raising the floor requires a release-grade `component migrate`, pruning cannot be routine — left alone, the registry, golden vectors, and deprecated tombstone fields grow **append-only** (a real cost the opaque-token model accepts; see the manifest alternative). Policy: piggyback floor-raises onto *already-planned* mass rebuilds (the next environment cutover or a major release), and enforce a CI ceiling on the `currentLockContentVersion − minSupportedLockContentVersion` *spread* so the backlog cannot grow unbounded between those planned events. The spread, not the absolute version number, is the quantity kept small. **Early-warning ramp:** the ceiling is a *warning at ceiling−1*, a hard failure only at the ceiling — so an approaching floor-raise surfaces as a heads-up on the PR *before* the one that registers `v(N+1)`, converting the forced migrate from a surprise blocking failure into a planned event (the design's goal that nothing *unplanned* ever forces a migrate). **Residual:** if genuine algorithm changes arrive *faster* than planned rebuilds, the ceiling still ultimately *forces* an unplanned, release-grade `component migrate`. The ceiling does not eliminate the expensive event; it bounds the backlog by *converting* an unbounded version spread into an occasional forced migrate, with one version of advance notice. This is the accepted cost of lazy-forever coexistence.

**Mixed-toolchain hazard — bounded by the version-pin, not auto-repair.** The classic trap is an older binary regressing a newer lock. Because the lock *format* never bumps, an old binary *can* write a reset lock, stamping a legacy (prefix-less) or lower-`v<N>` hash. In the **working tree** this is self-correcting: the next new-binary run detects the sub-floor token and force-rehashes it to the current version. But "self-correcting" stops at the working tree — if a downgraded lock is **committed**, `FindFingerprintChanges` reads `v1 → legacy → v1` as two real release events, and a published `%autorelease` increment cannot be withdrawn. So the load-bearing guard against *committed* phantom releases is the **CI version-pin**: post-cutover, no old binary may run the `update`-and-commit step. (The force-rehash only cleans the working tree; it does not undo history.) The *symmetric* residual — a binary that predates content-version `v2` meeting a `v2` token it cannot replay — is closed by a **required** write-time guard (Open Q#5, now a requirement): refuse to write a token whose version exceeds the binary's `currentLockContentVersion`, erroring rather than silently restamping at `v1`. Note this guard lives in the binary doing the write, so it constrains *newer-but-not-newest* binaries; it does **not** retroactively constrain a genuinely *old* binary — that direction is the version-pin's job.

#### Replaying across a changed input set — `{a,b,c}` → `{a,b,d}`

A lock stores **one atomic token** (`v<N>:sha256:…`); it does *not* store the individual inputs. So when the measured set changes — say the fingerprint stops measuring `c` and starts measuring `d` — an existing lock is reconciled the only way an opaque digest allows: **recompute and compare, at the lock's own version.**

Split the change into its two halves; they are handled independently:

- **Adding `d`** is the additive case — `projectV1` never listed `d`, so for any lock at v1 the digest is byte-identical whether or not the struct now has `d` (G4, *truly* — the property `hashstructure` could not give). Free. No version bump.
- **Dropping `c`** is what forces the version bump, and it is reconciled by replay:
  1. `computeFP2` (measures `{a,b,d}`) ≠ stored digest → mismatch.
  2. token version (1) < current (2) → **replay `computeFP1`** (still measures `{a,b,c}`).
  3. v1-replay == stored digest? **Yes** → `a,b,c` unchanged since the lock was written; only the *measurement* evolved → `FreshnessCurrent`, lazy re-stamp. **No** → a real input moved → `Stale`, rebuild. Both correct.

So the bump is **not breaking**: replay answers "were the *old* inputs unchanged?" without rebuilding.

**The one constraint replay still imposes: a field a retained version still measures must stay on the struct.** The projection is immune to field *additions* (the walker only emits fields whose tag set includes the target version, so a new field is invisible to old versions). It is *not* immune to field *removal*: v1 still measures `c` (its tag set includes v1) and the retained v1 golden vector sets `c`, so physically deleting `c` from the struct makes that vector's config unconstructable → the golden-vector test fails to build. (Hand-written `projectVN` functions would make this a *compile* error instead — a marginally stronger guard the tag walker trades for an equally-blocking CI one; see [D2](#d2--version-tagged-field-selection--golden-vector-coverage).) Removal is therefore the one edit still gated by a **deprecate-then-delete** two-step, both non-breaking:

1. **Bump to v2 measuring `{a,b,d}` but keep field `c` on the struct** so the v1 projection can still read it for replay (close `c`'s tag to `v1..v1`, so v2 does not measure it). Every old lock replays clean at v1, is recognized as unchanged, lazy re-stamps to v2. Zero forced rebuilds.
2. **Only after the floor passes v1** (`minSupportedLockContentVersion = 2`, ideally after a deliberate `component migrate`) physically delete field `c` and `projectV1`.

> **Invariant:** a field may be physically removed from the config struct only after *every* retained version whose tag set includes it has been retired below `minSupportedLockContentVersion`. Retained versions and the struct they read must stay in sync — you cannot delete a field a live version's golden vector still sets.

This makes "drop an input" a lazy, per-component migration rather than a fleet-wide rebuild — at the cost of carrying a deprecated field on the struct until the last version measuring it ages out.

#### First post-reset customer

The reset establishes `projectV1` directly; it is *not* itself a Part 2 version event (it rides the rebuild, not replay). Part 2's machinery therefore sits idle until the **first genuine algorithm change after the cutover** — e.g. a `computeFP2` that fixes an overlay-folding bug, folds in a newly measured input, or changes a baked-in default. That change registers `computeFP2`, bumps `currentLockContentVersion` to 2, and is absorbed by replay with no second coordinated cutover. Because the projection substrate makes additive config changes hash-neutral by construction (G4), the *only* changes that ever need a Part 2 version event are genuine non-additive algorithm changes — a deliberately small set.

## Config schema version and canonical migration (future)

This is the on-disk TOML axis. It is **independent** of the fingerprint axis and only needed once we make *non-additive* TOML changes (rename/move/remove fields in the file format itself) that were *not* already absorbed by the reset's normalization pass. Most of the hardest cases are spent at the reset (load-out item 6); this axis covers whatever non-additive TOML change arises *after*.

1. Add an explicit `schema-version` to the config file (distinct from the existing `$schema` URL, which is for editor validation).
2. At **load time**, migrate older config shapes forward into the single latest canonical struct *before* anything hashes them. Fingerprinting stays blissfully unaware of file-format history. A `config migrate` command (sibling to today's `config schema` / `config dump`) makes this an explicit, reviewable pass that rewrites stale TOML files in place to the current `schema-version`.
3. The projection substrate already provides the clean seam: `projectVN` reads the post-migration canonical struct; the combiner stays in `fingerprint`. No `ConfigHash()` method is added (see [the seam note](#where-the-hashing-logic-should-live)).

The critical invariant: **migrate old TOML → latest canonical struct, then project once.** A semantically no-op migration (rename `foo`→`bar`) must produce the *same* canonical struct, hence the same projection bytes, hence no drift. This is what keeps the schema axis **orthogonal** to the lock axis: a faithful `config migrate` is a pure re-encoding that moves *no* fingerprint, so it never triggers a `component migrate`. If a TOML change genuinely alters build meaning, that is a content-version bump (Part 2), not a `config migrate`.

**Resolved by projection:** the old `hashstructure` caveat — that it mixed `reflect.Type.Name()` into the hash, so renaming a Go struct moved every fingerprint even with identical content — **no longer applies.** The walker hashes only the explicit field bytes it emits, under each field's **frozen TOML key**, never the Go type or field name. So *both* a struct-type rename **and** a cosmetic field rename (`Foo`→`Bar`, same `toml:` key) are genuinely drift-neutral — **pinned by golden tests** (rename a fingerprinted struct, and rename a field while keeping its TOML key → byte-identical digest in both cases), so the property is CI-enforced, not just asserted here. Renaming the *TOML key itself* is an output-changing edit and takes a version bump like any other.

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

The versioned-replay story in Part 2 must hold for **every** reader of `InputFingerprint`, not just the two paths it grew up around. This is the post-reset migration blast-radius map; each consumer's behavior under a Part 2 v1→v2 algorithm switchover is stated explicitly. (The *reset itself* is invisible to these consumers as analyzed under [Back-compat invariant](#back-compat-invariant--synthetic-history-reads-stored-strings-never-recomputes): they compare stored strings, and pre-reset locks are never recomputed.)

| Consumer | Reads | Compares | Migration behavior required |
| -------- | ----- | -------- | --------------------------- |
| `checkFingerprintFreshness` (resolver) | recomputed identity | vs stored token | Replay at token version (Part 2 core) |
| `component update` `Changed` decision | recomputed identity | vs stored token | **Replay before `Changed`** (see churn policy seam) |
| `changed.go` `classifyComponent` / `haveMatchingFingerprints` (CI classifier) | stored token strings (two historical git refs) | **digest compare** (strip `v<N>:` prefix) | **String-only — must NOT replay** (no inputs available, and replaying historical configs would violate the no-recompute invariant); comparing the digest makes it immune to version-only deltas |
| `synthistory.FindFingerprintChanges` | stored token strings across git history | **digest of adjacent commits** (strip `v<N>:` prefix) | **String-only; digest-compare** so a version-only re-stamp (including a resolution-only bump) never fires a release |
| `synthistory.BuildDirtyChange` | recomputed (current ver) | vs stored `headLock` token | **Replay at headLock version** before declaring dirty |
| `ResolutionInputHash` staleness/write | recomputed resolution hash | vs stored | **Shares the version; replay reserved, not yet wired** |

**Two comparator classes, not one — and only one of them can replay.** The consumers split cleanly by *what they hold*:

- **Current-tree comparators** (`checkFingerprintFreshness`, `update`'s `Changed`, `BuildDirtyChange`) recompute against *live inputs*, so they **can and must** replay at the stored token's version. Feasible and invariant-safe.
- **Stored-vs-stored historical comparators** (`FindFingerprintChanges`, `changed.go`'s `classifyComponent`/`haveMatchingFingerprints`) hold only committed token *strings* from two git refs — no config, no FS, no inputs. They **cannot** replay, and replaying would require recomputing a historical fingerprint, which the [forever-invariant](#back-compat-invariant--synthetic-history-reads-stored-strings-never-recomputes) forbids outright. Both stay **string-only**, and both compare the **digest** (stripping the `v<N>:` version prefix), which makes them inherently immune to version-only deltas — a v1→v2 re-stamp with an unchanged digest reads as "no change." (Strict-lazy churn is still the policy that keeps re-stamps from riding no-op commits in the first place, but the comparators no longer *depend* on it for correctness.)

The `changed.go` classifier is the easily-missed member of the *second* class: it must get the same **digest-compare** as `FindFingerprintChanges`, so a version-only delta reads as "no change" — not a replay (which it cannot do, holding no inputs).

**This contract is enforced by a type, not prose — the `fingerprint.Token` choke-point.** A reviewer-vigilance rule across the five-plus comparison sites is the kind of discipline this RFC elsewhere converts to structure (the atomic token, D3), and digest-comparison widens the surface: `v<N>:` prefix-parsing now lives at two historical + three current-tree sites, and the "digest-compare two stored strings" pattern is **copyable**. The residual hazard is therefore not mere *omission* — a forgotten replay at a current-tree site fails *safely* toward inequality → spurious `Stale`/`Changed` → wasteful rebuild (G1 churn, never G5) — it is *mis-classification*: a future consumer that holds live inputs but copies the **historical** stored-string template never looks at those inputs → silently accepts a stale tree → reachable G5. Omission is safe; mis-classification is not, and only structure closes it. So an opaque `fingerprint.Token` type (unexported internals) carries **one strict parser** — `ParseToken` accepts only `sha256:<hex>` (legacy) and `v<N>:sha256:<hex>`, treats any malformed token as *changed* (never normalizing a parse failure to an empty digest), and is the *sole* way to read a stored hash — routed through a single `Reconcile(lock) → {Fresh | Stale | RestampTo(v)}` API. A raw `==` on a stored hash outside that package will not compile. This lands in PR C, which already edits every one of those sites; it has no on-disk-format dependency, so there is no reason to touch all five sites twice and carry the mis-classification window in between.

### The synthetic changelog/release path is the real hazard

[`synthistory.go`](../../../internal/app/azldev/core/sources/synthistory.go) turns fingerprint movement into **user-visible, shipped** package state — `%autochangelog` entries and `%autorelease` increments. There are two distinct comparators, and the design resolves them asymmetrically.

- **`FindFingerprintChanges` (historical walker)** compares `InputFingerprint` across the lock's git history and emits a synthetic changelog/release entry on every change. It compares the **digest** (stripping the `v<N>:` version prefix), not the full token — a one-line string operation, not the infeasible version-aware replay (it has only committed *strings*, no inputs). So a version-only re-stamp (a lazy v1→v2 with an unchanged digest, or a resolution-only bump that advances the shared prefix) is **invisible** to it; only a moved digest — a genuine input change — fires, and the migration folds into the real change's entry that carries it. The v1→v2 conversion is thus an *accepted, per-component, notable* changelog event that piggybacks a real change, guaranteed by digest-comparison rather than by lazy-discipline.
  - **`component migrate` is release-grade *when it moves digests*.** A migrate that retires a *fingerprint* algorithm re-stamps every unchanged lock from `computeFP1`'s digest to `computeFP2`'s — the digests move, the walker fires, and the fleet-wide release is the deliberate cost ([registry floor](#registry-floor-and-forced-migration)). A migrate that retires only a *resolution* algorithm moves the shared prefix but not the `InputFingerprint` digest, so it is correctly release-silent. Either way the firing tracks a real digest move, never a bare prefix change.
- **`BuildDirtyChange` (live dirty check)** compares a *recomputed* current-version (v2) hash against the *stored* (possibly v1) `headLock.InputFingerprint` and declares dirty on inequality. "Accept as notable" does **not** save this path: post-switchover an *unchanged* component would read **dirty on every `render`/`build`** until re-stamped — a persistent, recurring spurious signal, worse than a one-time entry. The fix is **free**: it is the *same replay Part 2 already owes the freshness check* — replay at `headLock`'s recorded version before declaring dirty. One additional call site for logic already being written, no new mechanism.

**Net:** the changelog-walker concern is not "make the walker version-aware" (hard, maybe infeasible). It is two cheap things — (1) the historical comparators (`FindFingerprintChanges`, `changed.go`) compare the **digest**, so a version-only delta never fires; and (2) extend the *current-tree* replay to `BuildDirtyChange` (which *does* hold live inputs), one call site for logic already being written. The reset commit is the single deliberate exception: it *is* a fleet-wide notable event, the coordinated cutover, intentionally visible.

### `ResolutionInputHash` — shares the version, replay deferred

`ComponentLock` carries a *second* persisted content hash, `ResolutionInputHash`, with its own staleness logic and its own silent-write path (it writes when only `resHashChanged`, never flipping `Changed`). It has the **identical** evolution problem as `InputFingerprint`, and the single shared content version covers it (see [Both hashes share one version](#both-hashes-share-one-version)). Two things make its replay safe to defer:

- **Smaller blast radius.** `ResolutionInputHash` does **not** feed `synthistory`, so an algorithm change can never mint a phantom changelog/release (that hazard is fingerprint-only). Worst case is a one-line `resolution-input-hash` rewrite per lock plus a wasted re-resolution that usually yields the same commit. Churn, not corruption.
- **No pending change.** It is a flat seven-field SHA256, not a struct walk, so the projection substrate leaves it untouched. Its registry slot stays `computeRes1` until its inputs genuinely change.

**Decision (KISS/YAGNI):** wire fingerprint replay in Part 2's first PR; reserve resolution replay (slot present, prior fn reused) and wire it the day `ComputeResolutionHash` first changes — add `computeRes2`, bump the shared version, re-stamp both fields together. No separate resolution prefix is needed: digest-comparison keeps the shared version correct for both ([above](#both-hashes-share-one-version)).

## Design decisions

### D1 — Canonical projection vs `hashstructure` + `Includable`

Both can omit zero values; the decisive difference is **whether an old algorithm can be frozen**, which `Includable` cannot deliver (Problem 6).

| | Canonical projection (chosen) | `hashstructure` + `Includable` |
| --- | --- | --- |
| Old algorithm frozen | Yes — version-tagged fields, golden-vector pinned | No — reflects the live struct/method-set |
| Sound replay (Part 2) | Yes | No (the disqualifier) |
| Meaningful empties | `!`-prefixed range per field | `fingerprint:"always"` per field |
| Type-name in hash | No (rename is drift-neutral) | Yes (rename moves every hash) |
| Plumbing | Generic walker + version tags + golden vectors | Value-receiver `HashInclude` on every nested struct + `v.(reflect.Value)` assert |

`Includable` keeps today's hashes byte-identical, which mattered for an *incremental* rollout — but that property is worthless once the reset rebuilds everything anyway, and it comes attached to a substrate that makes replay unsound. Projection trades byte-compatibility (which we are spending on the coordinated cutover regardless) for frozen replay (which we need forever). Adopted at the reset.

### D2 — Version-tagged field selection + golden-vector coverage

Field membership lives in a per-field version-set tag (`fingerprint:"v1..*"`) read by one generic walker — not in N hand-written functions, and not in the binary include/exclude tag of today's reflective audit. Rationale:

- **The unsafe direction is the false-negative** (a meaningful field silently omitted → missed rebuild → stale artifact, a G5 violation). A *mandatory* tag — absent → build failure — makes the include/exclude decision impossible to *forget*. The *wrongly-excluded* case (a `-` tag on a build-effective field) is caught by the kept exclusion ledger, and the *wrongly-included-but-unmeasured* case by golden-vector coverage — see [Enforcement](#golden-vector-coverage-is-the-load-bearing-invariant).
- **Version-awareness is declarative.** A field's whole lifecycle — introduced at v3, dropped at v5, revived at v8 — is one greppable string on the field (`v3..v4,v8..*`), not a diff smeared across three function bodies. Recovery (bring-back) is *expressible* precisely because the set is non-contiguous.
- **Cost: frozen-ness is *test*-enforced, not *structurally* enforced.** The walker reflects the live struct (the very thing Problem 6 rejected); only the version-set tag re-freezes membership, and golden-vector **coverage** re-freezes the emit-key, encoding, and zero-predicate. A hand-written `projectVN` would make field removal a *compile* error; the coverage invariant turns it into an equally-blocking CI failure instead, in exchange for the declarative lifecycle, native completeness, and first-class recovery. The hand-written variant is kept as [Option B](#alternatives-considered).

### D3 — Atomic self-describing token; no format bump, reconcile via force-rehash

The stored hash is a single `v<N>:sha256:<digest>` token, not separate version and digest fields. One field, written atomically, so the version and the digest can never desync (the class of bug a split-field design invites when one is written and the other is not).

The lock **format** `Version` stays at `1`. Bumping it to `2` as a poison pill — to stop old binaries touching reset locks — is too blunt: it also stops them reading pins to *queue a build*. Instead, back-compat rests on two cheaper properties: the format is unchanged so every binary parses every lock, and the content-version registry **force-rehashes** any sub-floor token (legacy, or downgraded by an old binary) up to the current version. Old binaries stay useful (read pins, build); their only possible mischief — writing a legacy-substrate hash — is self-correcting on the next new-binary run, not silent corruption. Back-compat is therefore: **same format forever, reconcile fingerprints by version, never recompute history.**

### D4 — Project to bytes, not a `ConfigHash()` method on the type

`project(config, version) []byte` returns canonical bytes; the combiner in `fingerprint` owns the `sha256` and the version dispatch. A `ConfigHash()` method that returns a finished hash was rejected: it drags crypto + versioning onto a data type, and it tempts callers to route around the version registry to get a raw, version-agnostic hash. Returning bytes keeps the config type ignorant of versioning, and keeps the combiner the **sole version authority**. See [the seam note](#where-the-hashing-logic-should-live).

## Alternatives considered

- **Incremental lazy migration on the `hashstructure` substrate** (the original plan): flip the inclusion default to omitempty via `Includable`, version the lock content, and migrate lazily — *without* a reset. Rejected: Problem 6 makes its central promise unkeepable. A "frozen" replay function built on `hashstructure.Hash` reflects the live struct, so the first field addition after the switchover moves the old algorithm's output and forces a rehash anyway. The incremental path therefore does not actually avoid a coordinated cutover — it defers one to the first field addition, on a substrate that makes replay unsound. With a coordinated cutover already scheduled (the dev→prod cutover), spending it once on a clean projection substrate strictly dominates.
- **Global `IgnoreZeroValue`** — a blunt switch that omits *all* zero fields with no escape hatch for build-meaningful zeros, and still on the non-frozen `hashstructure` substrate. Rejected.
- **Parallel versioned structs with per-struct `Hash()`** — couples locks to Go type identity and duplicates hashing logic per version. Rejected in favor of Part 2's integer-versioned combiner over frozen projections.
- **Bump the lock format `Version` 1→2 as a poison pill** — makes old binaries hard-reject reset locks. Rejected: it also blocks old binaries from reading pins to queue a build, and it is unnecessary, since the content-version registry already force-rehashes any sub-floor or downgraded token (D3). Same-format + force-rehash keeps old binaries useful without risking silent corruption.
- **Eager fleet-wide migration as the steady-state mechanism** — rewriting every lock on every algorithm change is the mass-churn the design exists to prevent. Rejected for the steady state. The *reset* is a deliberate, one-time, operator-driven eager pass riding an already-scheduled rebuild — the sanctioned exception, not the rule; `component migrate` is its post-reset equivalent for retiring an old version.
- **Hand-written per-version `projectVN` selection functions (instead of version tags).** Each version gets a bespoke `func projectVN(c) []byte` with one explicit `emit`/`emitAlways` line per measured field. *Win:* field removal is **compile-enforced** — deleting a struct field a retained `projectVN` still names won't compile (the tag walker downgrades this to a CI-time golden-vector failure). *Losses:* membership is smeared across N function bodies instead of one declarative tag per field; "bring a field back a few versions later" has no first-class expression (you re-add an `emit` line, with nothing tying it to the field's earlier life); and the mandatory-decision property needs a *separate* completeness test with an awkward field→emit-key bridge, where the tag simply *is* the ledger. Rejected in favor of version tags: the declarative lifecycle, native completeness, and expressible recovery outweigh trading one compile-time guard for an equally-blocking CI guard.
- **Per-field hash manifest in the lock (instead of one opaque token).** Store `{field → hash}` (à la `go.sum`) rather than a single `v<N>:sha256:…` digest. *Genuine wins:* dropping a field becomes ignoring its manifest line — no projection kept alive for replay, so the **deprecate-then-delete two-step and the registry-retirement deadlock** (the append-only growth above) both vanish; and the stored-vs-stored historical comparators become structural set-diffs rather than version-blind string compares. *Why the opaque token still wins for azldev:* (1) the projection substrate **already** delivers additive immunity (G4) — the manifest's headline draw — so that advantage is moot, not additive; (2) the manifest does **not** kill the false-fresh hazard — an old lock has *no line* for a newly-measured input, so there is still no baseline to detect a change to it (the blind spot is relocated, not removed); (3) it makes *algorithm evolution* — the entire point of Part 2 — **harder**, needing per-field versioning where the token needs one integer for the whole algorithm; and (4) it bloats every lock to O(fields × components) (the well-known `go.sum` size cost). The manifest is the better tool for a *static* input set that mainly grows and shrinks; the opaque token + single version is the better tool for an *evolving hashing algorithm*, which is azldev's actual problem. Recorded explicitly because the reset bakes the storage model in — token-vs-manifest is irreversible after PR B — and the retirement deadlock the manifest would have dissolved is instead answered by the floor-advance cadence above.

## Incremental delivery

The reset (Part 1) must land as one coherent change at the dev→prod cutover; its pieces are independently reviewable but ship together because they all move the hash.

1. **PR A (substrate)**: the canonical encoder (`canonicalBuf`, `emit` with a per-range always flag), the generic tag-driven `project(cfg, N)` walker (recursing into nested fingerprinted structs) + version-set tag parser, **version tags on every fingerprinted field** (absent → build failure), the frozen **TOML-key** emit rule, the `reflect.Value.IsZero()` omit-predicate, the `sha256` combiner, the golden vectors **and the coverage invariant** (every field measured at a retained version appears non-zero in ≥1 vector, with a discrimination check). The mandatory-tag test plus the slimmed exclusion ledger replace the retired `TestAllFingerprintedFieldsHaveDecision` audit — the inclusion default is now native to the tag, the exclusion default stays ledgered. Pure addition alongside the existing path; not yet wired into `ComputeIdentity`. Unit tests: a field tagged `v2..*` is invisible to a v1 projection; a `!`-prefixed range hashes even at zero; a field with **no** `fingerprint` tag fails the build; a **nested** fingerprinted struct with a tagless field fails the build; a **Go-field rename keeping the TOML key** yields a byte-identical digest; two fields colliding on one emit-key fail the build; a field whose range excludes the current version without a **dropped-fields-ledger** entry fails the coverage/discrimination check; the **coverage/discrimination** test fails when a build-effective field is tagged too narrowly (`v1..v1` at current `v2`); golden vectors pin v1; editing a shipped version's output for an existing config fails a golden vector; a non-contiguous set (`v1..v1,v3..*`) round-trips through the parser.
2. **PR B (reset cutover)**: switch `ComputeIdentity` to `projectV1`; adopt the atomic `v1:sha256:` token; unify on sha256. Lock format `Version` stays `1`. Ships at the cutover; absorbed by the scheduled rebuild. Unit tests: a legacy prefix-less token is read as sub-floor and force-rehashed to `v1`; a `v1:` token round-trips; an old binary (format `1`) still parses pins from a reset lock.
3. **PR C (Part 2 machinery)**: the opaque **`fingerprint.Token` type** (unexported internals) with the single strict `ParseToken` (accepts only `sha256:<hex>` and `v<N>:sha256:<hex>`, malformed → *changed*, never an empty-digest false match) and the `Reconcile(lock) → {Fresh | Stale | RestampTo(v)}` API; the version registry (`lockAlgos`, `currentLockContentVersion`, `minSupportedLockContentVersion`); `ComputeIdentityAt`; and routing **all five** comparison sites through `Token`/`Reconcile` — replay-before-`Changed` in `update.go`, `checkFingerprintFreshness`, `BuildDirtyChange` (the three current-tree sites), plus digest-compare in `FindFingerprintChanges` and `changed.go`'s `classifyComponent` (the two historical sites). Resolution replay reserved (slot reuses `computeRes1`). **Not fully inert:** this PR switches the live compares from raw-string to `Token`-routed *on merge* — only the *registry dispatch* is dormant while just `v1` exists, and `BuildDirtyChange`'s replay is a hard prerequisite for any later PR that registers `v2`. Unit tests: a synthetic `v1`/`v2` pair with unchanged inputs → `Current` and **not** `Changed`; changed inputs → `Stale`; re-stamp only on an already-dirty write; a digest-identical `v1`→`v2` re-stamp is **not** a changelog event; the reset boundary `sha256:X`→`v1:sha256:Y` fires exactly once; a malformed token is treated as changed, never silently equal; a raw `==` on a stored hash outside the `fingerprint` package fails to compile.
4. **PR D (validation)**: scenario test (in the style of `scenario/component_changed_test.go`) — add a field absent from `projectV1` and set it on one component; assert only that lock drifts and every other lock is byte-identical.
5. **PR E (config schema axis, later)**: `schema-version` field + load-time canonical migration + the `config migrate` command. Gated on the first post-reset non-additive TOML change not already absorbed by the reset's normalization pass.

Each PR is independently revertible up to the cutover. PRs A–B land together at the dev→prod cutover (they move every hash and are absorbed by the scheduled rebuild); PR C is inert until the first post-reset algorithm change; PRs D–E follow.

## Open questions

1. Should a lazy re-stamp during a *read-only* command (`render`, `build` freshness check) write the lock back, or defer all writes to `component update`? Writing on read is surprising; deferring means freshness checks stay slightly slower until the next update. (Leaning: defer all writes to `update`, keeping reads side-effect-free.)
2. For the config schema axis, does `schema-version` live per-config-file or per-component? Per-file is simpler; per-component allows mixed-version projects during migration.

*Resolved in-text (recorded here so they aren't re-litigated):* the reset rides the already-scheduled dev→prod rebuild as the one sanctioned coordinated cutover; the substrate is canonical projection (frozen `projectVN` + golden vectors), not `hashstructure`; the **canonical byte encoding is the existing length-prefixed `<len>:<key>=<len>:<value>` form** used by `combineInputs`, committed and pinned by golden vectors at the reset (former Open Q#4 — a precondition for PR A, not an open question, because the reset makes it irreversible); the **version write-guard is a requirement, not an option** (former Open Q#5): a binary refuses to write a token whose version exceeds its own `currentLockContentVersion`, and the CI version-pin prevents *old* binaries from committing downgrades; **field membership is declared in mandatory per-field version-set tags** (`fingerprint:"v1..*"`; absent → build failure, `!`-prefix for always-emit), read by one generic walker — restoring "forgotten field → loud build failure" natively; the **emit-key is the frozen TOML key** (never the Go field name, so a field rename is byte-neutral; `key=` overrides for keyless fields; duplicate emit-keys fail the build), the **omit-predicate is `reflect.Value.IsZero()`** (former Open Q#3), and the tag DSL is **frozen at three range-operators** (`..`, `!`, `*`) plus the orthogonal `key=`; frozen-ness rests on the **golden-vector coverage invariant** (every field measured at a retained version appears non-zero in ≥1 vector, with a discrimination check) whose oracle is a **tag-independent dropped-fields ledger** (a field absent from it must discriminate at the *current* version), plus a **kept exclusion ledger** for `-` fields (the inclusion default is native to the tag; the *exclusion* default stays ledgered because it is the G5-dangerous direction); the stored hash is read only through an opaque **`fingerprint.Token`** type with one strict parser and a `Reconcile` API (adopted in PR C, closing the comparator mis-classification hazard at compile time); baseline `v1` is omit-if-zero with **no** include-always legacy in the registry; the lock format `Version` stays at `1` (old binaries keep reading pins to build); the substrate swap and any old-binary downgrade are reconciled by **force-rehashing** sub-floor tokens, not a format gate; the stored hash is an **atomic** `v<N>:sha256:` token; back-compat rests on the verified invariant that **no reader recomputes a historical fingerprint** (synthetic history and historic-overlay application read stored strings only); registry retention is a **floor**, not "last N"; `component migrate` is the post-reset forced-migration pass (lock axis; `config migrate` is its schema-axis sibling) and is itself a deliberate release-grade event; one shared content version covers both stored hashes **permanently** (no split) — the historical changelog/classifier comparators compare the **digest** (stripping the `v<N>:` prefix), so advancing the shared prefix for a resolution-only algorithm change moves no digest and mints no release; resolution replay stays reserved (slot present, `computeRes1` reused) until `ComputeResolutionHash` first changes.
