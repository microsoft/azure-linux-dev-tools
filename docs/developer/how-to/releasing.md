# How to: cut a release

This guide covers releasing the `azldev` Go module so that
`go install ...@version` and the [pkg.go.dev][pkgsite] reference page work.

## TL;DR

Releases run through CI — the recommended path, with nothing to install
locally:

1. Trigger the [**Prepare release** workflow][prepare-release-run]
   (**Run workflow** → `main`). It drafts the next changelog section and pushes
   a `release/vX.Y.Z` branch.
2. Wait ~30 seconds, the output summary of the workflow will generate a link to create the PR.
3. Edit the draft changelog into user-facing notes, then merge the PR to `main`. Consider using '@copilot Update the new changelog section into user-facing release notes' in GitHub to help rewrite the changelog.
4. On merge, the [**release** workflow][release-run] tags `vX.Y.Z` and publishes
   a GitHub Release from the changelog — no further action needed.

See [Automated releases (CI)](#automated-releases-ci) for what each workflow
does.

Local steps are:

```console
# One-time: install the changelog generator (git-cliff)
cargo binstall git-cliff         # or: cargo install git-cliff --locked, or: brew install git-cliff

# 1. Draft the changelog, curate it into user-facing notes, then PR + merge to main
mage changelog                   # prepends a draft ## [X.Y.Z] section to CHANGELOG.md

# 2. Tag the release from the changelog version, then publish by pushing the tag
mage release                     # creates annotated tag vX.Y.Z on HEAD (does not push)

# Undo a local tag created by mistake (before pushing)
git tag -d vX.Y.Z

git push origin vX.Y.Z           # pushing the tag publishes the version to the Go proxy

# 3. Create the GitHub Release with this version's changelog notes
gh release create vX.Y.Z --title vX.Y.Z \
  --notes-file <(awk '/^## \[[0-9]/{n++; next} n==1' CHANGELOG.md)
```

Each manual step is explained in full under [Cut a release](#cut-a-release)
below.

## Versioning policy

We follow [Semantic Versioning][semver] with a `v` prefix on tags
(`vMAJOR.MINOR.PATCH`).

* **`v0.x.y` (current).** The pre-1.0 phase: the CLI and any exported Go API may
  change in breaking ways on a *minor* bump. Use this while the surface is still
  settling.
* **`v1.0.0` and later.** Commits to SemVer stability: breaking changes require a
  major bump.
* **Major versions `>= 2`** require a `/vN` suffix on the module path
  (e.g. `github.com/microsoft/azure-linux-dev-tools/v2`). Staying on `v0`/`v1`
  avoids that. Don't cut a `v2.0.0` tag without first updating the module path.

## How publishing actually works

There is no separate "upload" step. The public [Go module proxy][proxy] and
pkg.go.dev fetch directly from this repository's Git tags:

* `go install .../cmd/azldev@vX.Y.Z` resolves the tag through the proxy.
* `azldev version` reports the right version automatically for proxy installs —
  Go embeds the module version into the binary's build info.
* pkg.go.dev renders the package doc comments plus this repo's `README.md`. Our
  `LICENSE` (MIT) marks the module redistributable so docs are shown. Only the
  public `cmd/` and `pkg/` packages appear; `internal/` is hidden by design.

## Cut a release

1. Make sure `main` is green and up to date locally.

2. Generate and curate the changelog. Run `mage changelog` to prepend a draft
   section for the next version to [`CHANGELOG.md`](../../../CHANGELOG.md), then
   edit it down into user-facing notes. See [Changelog](#changelog) below.

3. Tag the release. Once the changelog change is on `main`, run `mage release`:
   it reads the version from the top `## [X.Y.Z]` heading in
   [`CHANGELOG.md`](../../../CHANGELOG.md) and creates a matching annotated tag
   (`vX.Y.Z`), so the tag and the changelog can't disagree. Then push the tag:

   ```console
   mage release
   git push origin vX.Y.Z
   ```

   `mage release` creates the tag locally but never pushes — pushing the tag is
   what publishes the release. The version lives only in the git tag (there is no
   version file); `azldev version` reads it from the build. To sign the tag,
   create it manually with `git tag -s` instead.

   `mage release` is idempotent: if that version is already tagged it does
   nothing, so the same command is safe to automate on every merge to `main`.

4. (Optional) The proxy and pkg.go.dev index a new version on the first request,
   so nothing is required here. To make a release discoverable immediately, warm
   them by hand — this only triggers indexing of an already-public tag:

   ```console
   GOPROXY=https://proxy.golang.org go list \
     -m github.com/microsoft/azure-linux-dev-tools@vX.Y.Z
   ```

   Then visit
   `https://pkg.go.dev/github.com/microsoft/azure-linux-dev-tools@vX.Y.Z` once to
   prompt the docs build.

5. (Optional) Create a GitHub Release for the tag with that version's
   `CHANGELOG.md` section as the notes. The CI release workflow does this
   automatically; for a manual release, use `gh release create`.

## Changelog

[`CHANGELOG.md`](../../../CHANGELOG.md) at the repo root follows the
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. New sections are
drafted from the Conventional Commit history with
[git-cliff](https://git-cliff.org) and then curated by hand. Generate a draft
with:

```console
mage changelog
```

This prepends a `## [X.Y.Z]` section (the version is inferred from the commits)
above the previous release, skipping internal commit types — docs, test, chore,
build, ci, style, refactor, and dependency bumps — per
[`cliff.toml`](../../../cliff.toml). The result is a **draft**: git-cliff emits
commit subjects, not release prose, so prune and reword the entries into
user-facing notes before committing.

`mage changelog` is the single changelog flow — it runs identically locally and
in CI (CI just installs git-cliff first), so there is nothing to keep in sync
between the two. It needs git-cliff on your `PATH`; the version is pinned in
[`tools/git-cliff/Cargo.toml`](../../../tools/git-cliff/Cargo.toml) so Dependabot
and security scanners track it. Install it once:

```console
cargo binstall git-cliff   # or: cargo install git-cliff --locked, or: brew install git-cliff
```

> Tip: the generated draft is a natural place to let Copilot help rewrite commit
> subjects into concise, user-facing notes before you commit.

## Automated releases (CI)

Two workflows automate the manual steps above, reusing the same mage targets so
there is no second code path:

* [`prepare-release.yml`](../../../.github/workflows/prepare-release.yml)
  (manual **Run workflow**): checks out the repo's default branch (`main`),
  installs the pinned git-cliff, runs `mage changelog`, and pushes a
  `release/vX.Y.Z` branch. It does **not** open the PR — open it yourself from
  that branch, curate the draft, and merge.
* [`release.yml`](../../../.github/workflows/release.yml) (on pushes to `main`
  that change `CHANGELOG.md`): runs `mage release`, pushes the tag, and publishes
  a GitHub Release whose notes are that version's `CHANGELOG.md` section. The path
  filter keeps ordinary merges from triggering it; it also stays idempotent — a
  `CHANGELOG.md` edit that doesn't bump the version is a no-op because the top
  version is already tagged.

Both push with the default `GITHUB_TOKEN` (`contents: write`) — no PAT needed.
A tag pushed by `GITHUB_TOKEN` does not itself trigger further workflows, which
only matters if a tag-triggered build is added later.

## Fixing a bad release

### If the release workflow fails partway

The [release workflow][release-run] is idempotent: it skips the tag push and the
GitHub Release when they already exist, so the fix is usually just to **re-run it
before pushing anything else to `main`**. The tag is created on whatever commit
is at the top of `main` at run time, so if an unrelated commit lands before you
re-run, the tag could point at it instead of the release commit. If that happens
— and no one has fetched `module@vX.Y.Z` through the proxy yet — delete the
remote tag and GitHub Release, then re-run. Once the proxy has served the
version it is immutable; retract it instead (below).

### Withdrawing a published version

A version becomes immutable the first time the [Go module proxy][proxy] serves
`module@vX.Y.Z` — after that you cannot delete or move it. To withdraw one,
[retract][retract] it: add a `retract` directive to `go.mod` describing the bad
version(s) and release a new patch. `go get` will then skip the retracted
versions.

```go
// in go.mod
retract (
    v0.1.1 // contains a build-breaking bug; use v0.1.2.
)
```

[pkgsite]: https://pkg.go.dev/github.com/microsoft/azure-linux-dev-tools
[semver]: https://semver.org/
[proxy]: https://proxy.golang.org/
[retract]: https://go.dev/ref/mod#go-mod-file-retract
[prepare-release-run]: https://github.com/microsoft/azure-linux-dev-tools/actions/workflows/prepare-release.yml
[release-run]: https://github.com/microsoft/azure-linux-dev-tools/actions/workflows/release.yml
