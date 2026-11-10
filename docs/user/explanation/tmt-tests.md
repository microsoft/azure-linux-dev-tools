# TMT Tests & fmf Metadata

This page explains how [TMT](https://tmt.readthedocs.io/) (Test Management Tool)
and its [fmf](https://fmf.readthedocs.io/) (Flexible Metadata Format) backing
store fit into azldev: what the metadata looks like, how azldev pins and runs a
plan, and — most importantly — the different ways plans and tests can be laid out
across repositories.

For field-level reference documentation, see:

- [Tests — TMT Fields](../reference/config/tests.md#tmt-fields) — the
  `[tests.<name>.tmt]` subtable (`source`, `plan`)
- [Components](../reference/config/components.md) — associating tests with a
  component via `tests.tests`

## Why?

Azure Linux wants to run the same functional test suites that Fedora and CentOS
Stream already maintain for a package, rather than re-authoring equivalents. Those
suites are written for TMT and described with fmf metadata. azldev therefore needs
to:

- **pin** an exact upstream metadata revision, so a rerun uses the same plan and
  tests instead of a moving branch head;
- **map** a plan to a component, so `azldev component test <component>` knows what
  to run; and
- **run** the plan against a freshly built Azure Linux image with the
  candidate RPMs installed.

The catalog that does this lives in
[`base/comps/tmt.tests.toml`](../reference/config/tests.md#tmt-fields) in the
distro repo; each `[tests.tmt-*]` entry is one pinned plan.

## Concepts

### fmf (Flexible Metadata Format)

fmf is a plain-text metadata store: YAML-ish `*.fmf` files arranged in a
directory tree, rooted at a `.fmf/` directory (which contains a `version` file).
fmf gives every node an identifier derived from its path, and supports
inheritance — a child node inherits and can override keys from its parents. TMT
reads fmf to discover **tests**, **plans**, and **stories**.

A typical dist-git / test repo layout:

```
.fmf/
  version                # marks the fmf root ("1")
plans/
  all.fmf                # a plan: discover + provision + prepare + execute + report
  ci.fmf
tests/
  smoke/
    main.fmf             # a test: what to run, its duration, requires, etc.
    test.sh
```

### Test

A **test** is a single checkable unit. Its fmf node declares how to run it
(`test:`), plus metadata like `duration:`, `require:`, `tag:`, `tier:`. Tests are
what actually execute on the guest.

### Plan

A **plan** is the orchestration. Its fmf node wires together the TMT *steps*:

| Step | Purpose |
|------|---------|
| `discover` | Which tests to run and **where they come from** |
| `provision` | Where to run (guest/VM/container) |
| `prepare` | Setup before tests (install packages, run scripts) |
| `execute` | Run the discovered tests |
| `report` | Emit results |
| `finish` | Teardown |

The `discover` step is the pivotal one for layout: it decides whether tests are
read from the same repo as the plan, from a different repo, or extracted from the
packaged source tarball.

## How azldev pins and runs a plan

An entry in the catalog names one plan and the repo it lives in:

```toml
[tests.tmt-hostname-all]
type = "tmt"
description = "Hostname upstream functional plan"
kind = "functional"
required-capabilities = ["machine-bootable"]
[tests.tmt-hostname-all.tmt]
source = { git-url = "https://src.fedoraproject.org/tests/hostname.git", ref = "4aab6d5718c252dcf6fc7fe975c8caaa10b5acf9" }
plan = "/plans/all"
```

- **`source`** pins the repository that holds the **plan**. azldev clones this
  repo at `ref` (a full 40-char commit SHA) and treats it as the fmf/plan root.
- **`plan`** is the absolute fmf plan name to run (e.g. `/plans/all`).

**Both are optional.** When a component has no catalog entry at all (common for
packages whose upstream dist-git already ships `plans/`/`tests/`), `azldev
component test` still needs a test source to run: pass `--source-dir` pointed
at a local fmf tree (see below). Without a catalog entry **and** without
`--source-dir`, the command fails outright — there is no implicit default
source to fall back to. When `plan` is omitted **and `--provision local` is
used**, tmt discovers and runs every enabled plan it finds — the same default
Fedora's own Testing Farm uses (`tmt plan ls --filter enabled:true`, not a fixed
name) — instead of guessing one. `--provision virtual` (the default) always
requires an explicit plan (via `tmt.plan` or `--plan`), since hardware export —
used to build the VM — is resolved per-plan.
Pass `--plan` to pin a specific plan at the CLI, overriding a catalog entry's
`plan` when both are given.

At runtime [`azldev component test`](../reference/cli/azldev_component_test.md)
either clones `source@ref`, or (with `--source-dir`) runs straight from a local
fmf tree instead — the output of either `azldev component render` (with
`render.skip-file-filter = true`) or `azldev component prepare-sources`. Either
way, it provisions the supplied image with TMT's virtual (QEMU/testcloud) or
local provisioner, installs the candidate RPMs passed via `--rpm`, and executes
the selected plan(s). **The plan's own `discover` step then decides where the
tests come from** — azldev does not model that; it only pins (or locally
materializes) the plan's repo.

### Candidate RPMs: `--rpm` (loose files) vs. build repos

`azldev component test --rpm` installs **exactly** the RPM files you pass, as
mandatory targets (`dnf install ./a.rpm ./b.rpm …`). If two of them conflict —
e.g. `systemd` vs `systemd-standalone-*` — the transaction fails, so you must
hand-filter the glob to drop the ones the tests don't need.

Fedora CI avoids this by **installing from a repo**, not loose files. It has **no
per-package "which RPMs" YAML**: the RPM set is the whole Koji build, delivered to
Testing Farm as an `artifacts` entry. tmt puts those RPMs in an on-the-fly repo
(`createrepo_c`) and runs a resolved `dnf install`/`upgrade`. There the RPMs are a
**candidate pool + a goal**, so the solver honors `Conflicts:`/`Obsoletes:`
metadata and simply omits conflicting extras (like `systemd-standalone-*`) instead
of erroring — no manual exclusion list. (`gating.yaml` is the only per-package
file, and it declares which *test results* are required, not which RPMs to
install.)

The same technique works locally: `createrepo_c` the built RPMs, then
`dnf --repofrompath=cand,/absolute/path/to/repo --setopt=cand.priority=1 upgrade '<pkg>*'`
(dnf accepts a local directory as the `--repofrompath` baseurl) resolves conflicts
for you instead of a hand-rolled `--rpm` filter.

## Where plans and tests can live

Because `source` pins the **plan** and `discover` resolves the **tests**, the two
artifacts are decoupled. There are three repository categories a plan or test can
live in:

| Category | Typical URL shape | Contains |
|----------|-------------------|----------|
| **Package / RPM dist-git** | `src.fedoraproject.org/rpms/<name>.git` | Spec + lookaside sources; may also carry `plans/` and/or `tests/`; can expose the packaged tarball to `--dist-git-source` |
| **Dedicated test repo** | `src.fedoraproject.org/tests/<name>.git`, `gitlab.com/redhat/centos-stream/tests/<name>.git` | Standalone `plans/` + `tests/`, versioned independently of the package |
| **Upstream source repo** | e.g. `github.com/containers/skopeo.git` | The project's own code, often with `plans/` + `tests/` alongside it |

How `discover` resolves tests:

- `discover --how fmf` with no `url` → tests come from the **same repo as the
  plan** (co-located).
- `discover --how fmf --url <repo>` → tests come from a **different repo**.
- `discover --how fmf --dist-git-source` → tests are extracted from the
  **packaged source tarball** referenced by the dist-git spec (upstream sources
  fetched via the lookaside cache).

### Scenario matrix

Rows = where the **plan** lives (what `source` points to); columns = where the
**tests** are discovered from. `source` always tracks the plan's repo.

| Plan location ↓ / Tests location → | Same repo as plan | Package dist-git | Dedicated test repo | Upstream source (tarball or repo) |
|---|---|---|---|---|
| **Package / RPM dist-git** | S1 | — | S2 | S3 |
| **Dedicated test repo** | S4 | S5 | — | S6 |
| **Upstream source repo** | S7 | S8 | S9 | — |

### Scenarios in detail

- **S1 — Plan + tests both in the package dist-git.**
  `source` → `rpms/<name>`. The plan's `discover` reads `tests/` from the same
  repo. Example shape: `rpms/util-linux` `/plans/ci`, `rpms/glibc` `/plans/ci`.

- **S2 — Plan in package dist-git, tests in a dedicated test repo.**
  `source` → `rpms/<name>`; the plan's `discover --url` points at
  `tests/<name>`.

- **S3 — Plan in package dist-git, tests from the packaged upstream tarball.**
  `source` → `rpms/<name>`; the plan uses `discover --dist-git-source`, so the
  tests ship inside the upstream source archive. Example shape: `rpms/buildah`
  `/plans`, `rpms/podman` `/plans/system/...`.

- **S4 — Plan + tests both in a dedicated test repo.**
  `source` → `tests/<name>`; everything is co-located there. Most common shape in
  the catalog: `tests/dhcpcd`, `tests/selinux`, `tests/systemd`,
  `tests/hostname`, `tests/rust`, `tests/nodejs`, `tests/php`, `tests/shell`
  (bash), plus centos-stream `tests/chrony` / `tests/net-tools` /
  `tests/memcached`.

- **S5 — Plan in a dedicated test repo, tests in the package dist-git.**
  `source` → `tests/<name>`; the plan's `discover --url` reaches back into
  `rpms/<name>` (or its packaged sources).

- **S6 — Plan in a dedicated test repo, tests from upstream.**
  `source` → `tests/<name>`; the plan discovers tests from the upstream project
  repo or its packaged tarball via a remote `discover --url` /
  `--dist-git-source`.

- **S7 — Plan + tests both in the upstream source repo.**
  `source` → the upstream repo; plan and tests are versioned with the code.
  Example shape: `github.com/containers/skopeo` `/plans`.

- **S8 — Plan in upstream source repo, tests from the package dist-git.**
  `source` → upstream repo; the plan's `discover --url` points at the packaging
  repo. Rare, but expressible.

- **S9 — Plan in upstream source repo, tests in a dedicated test repo.**
  `source` → upstream repo; the plan discovers from a separate `tests/<name>`
  repo. Rare, but expressible.

> **Configuration rule of thumb:** set `source` to whichever repo you would
> `git clone` and then run `tmt plan show <plan>` in. If the plan file is there,
> `source` is correct — regardless of where the tests ultimately resolve from.

## Pinning discipline

- `ref` must be a full 40-character hex commit SHA, never a branch or tag, so
  reruns are reproducible.
- When a plan lives in a component's **RPM dist-git** repo, its `ref` should match
  that component's lock upstream commit, so the plan tracks the packaged version.
- **Independent test repositories** (the `tests/*` and centos-stream repos) do not
  carry Fedora release branches, so they are pinned to their own maintained
  default branch head at the time of authoring.

## Relationship to component rendering

Fedora dist-gits ship their TMT metadata (`.fmf/version`, `plans/*.fmf`,
sometimes `tests/`) right next to the spec. azldev's render step normally filters
rendered output down to files referenced by `Source`/`Patch` tags, which drops
that metadata. If you want to consume a plan straight out of a component's
rendered dist-git instead of pinning a separate `source`, the render file filter
has to be relaxed for that component — see
[`render.skip-file-filter`](../reference/config/components.md#skip-file-filter).
Otherwise, keep the plan pinned via `source` in the catalog as shown above.

## See also

- [Tests — TMT Fields](../reference/config/tests.md#tmt-fields) — reference for
  the `[tests.<name>.tmt]` subtable
- [`azldev component test`](../reference/cli/azldev_component_test.md) — run a
  mapped plan locally
- [TMT documentation](https://tmt.readthedocs.io/) and
  [fmf documentation](https://fmf.readthedocs.io/)
