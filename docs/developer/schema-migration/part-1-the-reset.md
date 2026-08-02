# Part 1 - The Reset

> Summary of the one-time cutover. Part of the [Schema Migration summary set](README.md).
> Source: [RFC, Part 1: The reset](../rfc/lazy-schema-migration.md#part-1-the-reset).

## What "the reset" is

The reset is a **single, coordinated change** that lands at the dev-to-prod cutover and rides the distro-wide rebuild
that is already scheduled there. It is the one sanctioned exception to the rule "no change should rebuild the fleet."
Its job is to replace the fingerprinting foundation and bank every irreversible cleanup at the same time, so that
*after* the reset every ordinary change is either free or lazy.

The reset cannot be made gradual: there is no way to swap a foundation, or normalize a batch of one-way-door choices,
without a rebuild. So they ride the one rebuild we are already paying for. Everything that *can* be gradual is pushed
into [Part 2](part-2-lazy-migration.md) and costs nothing.

## The foundation swap, in plain terms

Today the fingerprint hashes the **whole shape** of a component's config, so adding a field anywhere moves every hash.

The reset replaces that with a **canonical projection**: instead of hashing the struct's shape, the tool writes out only
the fields that a given version explicitly declares, in a fixed, sorted, checked-in form, and hashes those bytes.

| | Today | After the reset |
| --- | ----- | --------------- |
| What gets hashed | The whole config shape | Only the fields a version explicitly lists |
| Add an unused field | Every lock drifts | No lock drifts |
| Old algorithm can be "frozen"? | No - it tracks live code | Yes - it is fixed, checked-in code |
| Rename a field in code | Moves every hash | No effect (the stored key is frozen) |

Two properties make this worth the switch:

- **Frozen by construction.** Because each version's projection is checked-in code, adding a field later cannot change an older version's output. This is exactly what makes [Part 2](part-2-lazy-migration.md)'s replay trustworthy.
- **Additive fields are free.** A field a component does not set produces no bytes, so it is invisible to every lock - old or new. Adding it moves no existing hash, so it needs no version bump.

The cost is owning the projection encoder and a set of **golden vectors** - a checked-in table of "this config produces
this hash" - so the encoder is guarded by tests. That cost is paid once, against a rebuild we are already doing.

## The reset load-out

The rebuild is a budget. Spend it on the irreversible, cutover-only changes; do **not** spend it on anything Part 2 can
do lazily for free. In priority order:

1. **Switch to canonical projection.** Foundational and one-way; everything else depends on it.
2. **Make the new baseline "omit unset fields" from day one.** No legacy compatibility mode to carry forever.
3. **Keep the lock file format unchanged.** Old binaries can still read a reset lock and queue a build; only the fingerprint *value* changes.
4. **Adopt a self-describing version token** for the stored hash (`v1:sha256:...`), so the version and the digest can never drift apart.
5. **Unify on one hash format everywhere**, retiring the older format from the previous library.
6. **Do every pending rename and default cleanup now.** These are one-way doors later but free during the reset.
7. **Force a deliberate decision on every fingerprinted field**, and fix existing mistakes for free while doing so.

**Anti-goal:** do not burn reset budget on additive fields. Part 2 handles those for free, forever. The success
criterion is that no *routine* change ever forces a second coordinated cutover.

## What changes in the lock file

The stored hash becomes a single self-describing token:

```text
input-fingerprint = "v1:sha256:9f86d0..."   # version : algorithm : digest
```

One field carries both the version and the digest, so they cannot be written out of step. The lock file **format**
stays at version 1, so the on-disk shape is unchanged - only the meaning of the fingerprint value evolves.

## Why it is safe

- **Old locks stay readable.** The format does not change, so every binary can still parse every lock and read what it needs to queue a build.
- **History is never recomputed.** The features that walk lock history (synthetic changelog and release calculation) only ever compare the stored hash *strings* - they never re-run the hashing algorithm against an old commit. Swapping the foundation is therefore invisible to them; they simply see one expected, deliberate change at the cutover commit.
- **Stragglers self-correct.** A pre-reset lock, or one accidentally written by an older binary, is recognized as out of date and re-stamped to the current version on its next update.

## The one-time cost

A single, distro-wide rebuild at the cutover. It is already scheduled, so the reset adds no rebuild that was not already
going to happen. After the reset, the [lazy mechanism](part-2-lazy-migration.md) ensures it never has to happen again as
a coordinated event.
