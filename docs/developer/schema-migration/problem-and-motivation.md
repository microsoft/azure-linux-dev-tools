# Problem & Motivation

> Plain-language summary of the problem this work solves. Part of the [Schema Migration summary set](README.md).
> Source: [RFC, Background and Problem inventory](../rfc/lazy-schema-migration.md#background).

## Background in one minute

`azldev` tracks the resolved state of each component in a per-component **lock file** (`locks/<name>.lock`). Each lock
pins the upstream commit and records a **fingerprint** - a content hash of every input that affects that component's
build output. When the inputs change, the fingerprint changes, the lock file changes, and the component rebuilds. That
chain is the whole point: a changed lock is supposed to mean "this component's inputs really changed."

## The core problem: one change drifts everything

The fingerprint is computed by hashing the **entire shape** of a component's configuration. The hashing library treats
a field that is *present but empty* differently from a field that *does not exist at all*. So the moment the
configuration gains a new field in code, every component's hash changes - whether or not that component uses the field.

Concretely: add a field `foo`, then set `foo = "baz"` on a single package. The desired result is that **only that one
package's lock changes**. The actual result today is that **every lock file changes**.

The real cost is not the rebuild - it is the **diff**:

- Pull requests become unreviewable when a one-line change rewrites hundreds of lock files.
- `git blame` on a lock file stops meaning anything.
- "This lock moved" stops being a trustworthy signal that the component actually changed.

Clean diffs keep reviews honest and keep that signal trustworthy. The mass rebuild is just the knock-on effect.

## It blocks ordinary maintenance too

The same all-or-nothing behavior makes routine changes expensive, because each one re-hashes the whole fleet:

| Change | Why it hurts today |
| ------ | ------------------ |
| Add a field | Re-hashes every component, even non-users. |
| Rename or move a field | Looks like a brand-new shape to the hasher. |
| Remove a field | Same - the shape changes for everyone. |
| Change a default | Every component that relied on the default drifts. |
| Fix a bug in the hashing logic | No way to land it without re-hashing everything. |

## Why the obvious fix does not work (the substrate problem)

The natural fix is **versioned replay**: stamp the algorithm version into each lock, keep old algorithms around, and when
a lock looks out of date, recompute with *its* algorithm to ask "did the inputs really change, or did only the encoding
move?" If only the encoding moved, accept the lock with no rebuild.

Replay only works if an old algorithm can faithfully reproduce the hash it produced when the lock was written. On the
current hashing library it **cannot**: a "frozen" old algorithm still reflects the *live* code. Add a field later and
the old algorithm now sees that field too, so its output moves - and it can no longer reproduce the historical hash. The
foundation makes reliable replay impossible. This is the single fact that rules out a purely incremental fix.

## The three version axes

Three independent notions of "version" are emerging. Conflating them is the source of the confusion this work untangles:

| Axis | Versions what | Exists today? |
| ---- | ------------- | ------------- |
| Config schema version | The shape of the on-disk config file | No |
| Lock content-hash version | How inputs fold into the stored hash | No (implicitly "version 1") |
| Lock file format version | How the lock file is serialized | Yes (frozen at 1) |

## The opportunity that reframes everything

There is one unavoidable, distro-wide rebuild already scheduled: the **dev-to-prod cutover**. That changes the strategy
completely. The entire "be lazy to avoid a mass rebuild" framing exists to dodge a coordinated event - but if exactly
one such event is already on the calendar, the smart move is to **spend it deliberately**:

> Lazy migration is for the cheap and additive. The one free rebuild is a budget - spend it on the irreversible changes
> that are cheap now and would be costly later.

That single insight splits the solution into [Part 1 - The Reset](part-1-the-reset.md) (the one-time spend) and
[Part 2 - Lazy Migration](part-2-lazy-migration.md) (everything free or lazy afterward).
