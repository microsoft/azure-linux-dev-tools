# How To: Extract a Component's Dist-Git Repository

Use `azldev comp dist-git-repo` to extract one component's committed rendered
files and their Git history from the local project into a standalone repository.
This works for both locked and lockless components.

Install **git-filter-repo** first. If `git filter-repo --version` is unavailable,
the command fails with an installation message before replacing any output.

## Extract a component

Specify exactly one component name and a required output directory:

```bash
azldev comp dist-git-repo curl -o ./dist-git/curl
```

`--output-dir` is the long form of `-o`. Relative output paths are relative to
your current working directory. To select a project elsewhere, use the global
`-C` option:

```bash
azldev -C /path/to/project comp dist-git-repo curl -o ./dist-git/curl
```

The command clones the local project repository's current branch using
`git clone --single-branch --no-local`. It then runs
`git filter-repo --subdirectory-filter` in the clone, selecting:

```text
<project.rendered-specs-dir>/<lowercase-first-letter>/<component-name>
```

The path passed to the filter is relative to the project repository's Git root.
For example, `project.rendered-specs-dir = "specs"` selects `specs/c/curl`;
`SLOF` selects `specs/s/SLOF`. A configured nested rendered-specs directory is
respected.

The selected files become the standalone repository's root:

```bash
git -C ./dist-git/curl log --oneline
ls ./dist-git/curl
```

Only committed content is exported. This command does not render files, refresh pins,
or fetch the component's upstream Fedora/dist-git repository. Render and commit
changes first if you want them included.

## Replace an existing output

Even an empty existing directory is rejected unless you provide `--force`:

```bash
azldev comp dist-git-repo curl --output-dir ./dist-git/curl --force
```

**`--force` deletes the existing output directory and its contents.** Prerequisite,
source-path, and output-safety checks run before deletion. The source
repository and home directory (including their ancestors), Git metadata, and paths
containing symbolic links are not allowed as destinations.

If cloning or filtering fails, any partial output remains for inspection.
Resolve the cause and retry with `--force`. A global `--dry-run` makes no
filesystem changes and does not run Git commands.
