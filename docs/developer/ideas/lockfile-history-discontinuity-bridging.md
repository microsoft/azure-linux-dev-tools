# Lock file history discontinuity: bridging branch advances and upstream rewrites

Status: idea / design sketch. Captures the problem, a recommended approach, the UX, and
the gaps in today's code. Motivated by branch advances over divergent history, with
upstream history rewrites (force-push) as a related trigger.

## Problem

Each component lock pins an upstream dist-git commit (`upstream-commit`) and a
write-once fork point (`import-commit`). At render,
[buildSyntheticCommits](../../../internal/app/azldev/core/sources/synthistory.go)
walks our project repo's lock-file git log for fingerprint changes, then
[CommitInterleavedHistory](../../../internal/app/azldev/core/sources/synthistory.go)
clones the upstream branch, walks backward from the branch tip (first-parent) looking
for both pins, and interleaves our synthetic commits so that rpmautospec can expand
`%autorelease` and `%autochangelog`.

The interesting failure is the **branch advance with divergent history**. When we move a
component from one distro branch to the next (for example f43 to f44) and the two branches
have diverged, the old pin is no longer reachable from the new branch tip. The history
walk can no longer place our prior synthetic commits, so today they are silently dropped:
the old era of changelog entries and its release count simply vanish.

If the branches have **not** diverged (the new branch is the old branch plus more commits),
the move is a normal forward walk and everything keeps working. That is the easy case and
needs no special handling. The difficulty, and the focus of this document, is the divergent
case.

An **upstream history rewrite** (force-push) is the same problem from the other direction:
the pinned commit is orphaned off the branch tip. Fedora does this periodically to scrub
leaked email addresses, parking the pre-rewrite branch tip under
`refs/archive/bademail/<branch>`. These rewrites are usually cosmetic (trees and commit
messages unchanged), so they are a milder instance of the same discontinuity in the lineage
we pin. One mechanism should cover both.

Today the only remediation is manual: rewrite both `import-commit` and `upstream-commit` to
a reachable commit. That unsticks builds but rewrites the write-once fork and silently
discards the prior synthetic history.

## The framing that resolves the model

Treat packages as **ours**, each with a linear history, in which we credit upstream by
reproducing its changelog. A discontinuity (rewrite or branch advance) is then a single event in our own timeline: "at this point we re-based our package onto `<ref>`."
The rendered shape becomes:

```text
[old era: upstream entries interleaved with our overlay commits]   (preserved)
[one "rebased onto <ref>" transition commit]                       (the bump)
[new era: our overlay commits on the new base]                     (continues)
```

This optimization significantly reduces the complexity of the history generation; we no longer need to reconcile the new and old branches commit-by-commit.

The hard requirement is that the old era is **not wholesale-replaced**. A few cosmetic
field changes leaking in from an upstream scrub are acceptable; dropping or
re-synthesizing the old era's entries is not.

## Recommended approach

1. **Record each discontinuity as a transition in the lock, applied by `update`.** When
   `update` moves a component across a discontinuity it appends a transition (old pin to
   new pin) to a transition list in the lock and emits a single transition commit at render.
   `import-commit` stays write-once; the prior pin lives in the transition record rather
   than overwriting the fork.
2. **Graft the old era from upstream; never wholesale-replace it.** At render the old
   era's lineage is fetched from live upstream and walked, so our interleaved overlay
   entries and the old release count survive. We keep no copy of upstream code. When a
   rewrite renumbered the old lineage, an old-to-new commit translation map (recorded at
   `update` time) lets us render the old era through the current post-rewrite twins, so the
   live branch is sufficient and `refs/archive/*` is consulted only once, when the map is
   built. If the old lineage is genuinely gone and no twin exists, that map is the escape
   hatch: it drops per-commit fidelity and keeps only a monotonic release (placeholder
   entries with the correct count). Reconstruction generalizes today's single mainline walk
   into one walk per era, spliced at the transition commits; the rest of the pipeline
   (interleaving, linearizing, release counting) is reused.
3. **Lump the incoming branch's divergent detail into the transition.** In the divergent
   case the new branch's internal commits are absorbed into the single "rebased onto
   `<ref>`" commit rather than replayed as separate entries. The new pin's tree is the
   package content; its history collapses to that one entry. (A surgical-graft alternative
   that preserves the new branch's per-commit detail is possible, since a graft reparents
   one commit and keeps the history above the splice, but it is deferred: it adds
   complexity for little value under the "our linear package" framing.)
4. **Keep the release monotonic.** `%autorelease` counts commits in the rebuilt history,
   so the transition must never lower the count: the preserved old era carries it forward,
   and the escape hatch emits placeholder commits to hold the count when the lineage is
   unreachable. The concrete release-bump accounting reuses logic from a separate
   workstream, so it is out of scope here. This is a hard RPM upgrade-path constraint.

## UX

Discontinuities must be deliberate, never silent. By default `update` refuses to create
undiscoverable history: if moving the pin would orphan the prior era (the old pin is not
reachable from the new pin), `update` errors and writes nothing, pointing the user at the
flag below.

`update --allow-discontinuity` opts in to recording the jump. It appends the transition
(old pin to new pin) to the lock's transition list and proceeds, producing the additive
transition commit at render. The outcome is reviewable: the lock diff shows exactly which
discontinuity was accepted, and the `reason` records why.

The same guard applies at render. If history reconstruction hits a gap with no matching
transition in the list, it errors rather than silently dropping entries (today's behavior).

## Lock file example

A component that has crossed one discontinuity (an f43 to f44 advance over divergent
history) carries a transition list:

```toml
# Managed by azldev component update. Do not edit manually.
version = 2   # transitions-bearing locks are v2 so older readers refuse rather than ignore them
import-commit = "1f0c9d2a7b4e5c6d8a9b0c1d2e3f405162738495"   # original fork point
upstream-commit = "c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7" # current pin, on f44 (post-rewrite)
input-fingerprint = "sha256:..."
resolution-input-hash = "sha256:..."

# Each entry records a re-base of our package onto a new upstream lineage.
# 'from' is the prior era's tip; 'to' is the lineage we continued on.
[[transitions]]
from = "9c8b7a6f5e4d3c2b1a0f9e8d7c6b5a4938271605"   # old pin, last commit on f43
to = "a1b2c3d4e5f60718293a4b5c6d7e8f9001122334"     # the f44 pin we advanced to
reason = "branch-advance: f43 -> f44"

# Appended later, when f44 itself was force-pushed. Nothing above is rewritten.
[[transitions]]
from = "a1b2c3d4e5f60718293a4b5c6d7e8f9001122334"   # prior tip, the f44 pin above
to = "c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7"     # new pin, post-rewrite f44
reason = "upstream-rewrite: f44 bademail"
```

The list is ordered and append-only: each entry's `to` equals the next entry's `from`,
the newest `to` is the current `upstream-commit`, and the oldest era anchors to
`import-commit`. `update` validates that chain when it writes a transition, so cycles,
forks, and gaps are rejected at write time rather than discovered during render.

## Issues in the current code

- [collectUpstreamCommits](../../../internal/app/azldev/core/sources/synthistory.go#L708-L720)
  hard-errors when `upstream-commit` or `import-commit` is unreachable from the tip. The upstream
  force push triggers this error. It would also trigger on a branch advance if the old pin is not
  reachable from the new tip (e.g., non-linear history).
- [buildInterleavedSequence](../../../internal/app/azldev/core/sources/synthistory.go#L240-L246)
  drops synthetic commits whose upstream commit is not in the walked history, after only a
  `slog.Warn`. Its own comment already says "Will be useful for when we switch branches."
  Dropping our overlay entries is the wholesale-replace failure mode, and it also breaks
  release monotonicity.
- The clone passes `--branch` (not `--single-branch`)
  ([fedorasourceprovider.go](../../../internal/providers/sourceproviders/fedorasourceprovider.go#L137),
  [WithGitBranch](../../../internal/utils/git/git.go#L207-L211)), so other branch heads are
  fetched but the walk, anchored at the new tip, cannot reach a divergent old head;
  `refs/archive/*` is outside the default refspec and is never fetched at all. Either way
  the old lineage is invisible to the current single-range walk.
- The lock schema records a single `import-commit` / `upstream-commit`
  ([ComponentLock](../../../internal/lockfile/lockfile.go#L30-L65)) with no notion of eras
  or transitions; multiple discontinuities over a component's life cannot be represented.
- `import-commit` is documented write-once, yet the only remediation today rewrites it.
  The transition record restores that invariant.

## Open questions

- Batch semantics of `--allow-discontinuity`: per-component versus a global blanket over a
  bulk `update`, batch failure handling, and whether `reason` is generated or author-supplied.
- Fetch strategy for the rewritten lineage: the explicit refspec or fetch-by-hash for
  `refs/archive/*` (outside the default `refs/heads/*`) and the cost of fetching it once when
  the translation map is built.

## Related

- [Component identity and locking](../reference/component-identity-and-locking.md)
- Synthetic history pipeline:
  [trySyntheticHistory](../../../internal/app/azldev/core/sources/sourceprep.go#L402)
