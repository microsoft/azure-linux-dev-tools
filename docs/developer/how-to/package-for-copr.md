# How to: package azldev for Fedora COPR

COPR builds source RPMs in clean Fedora chroots and publishes the resulting
RPMs through a DNF repository. It does not create the RPM recipe itself. The
recipe is [`packaging/azldev.spec`](../../../packaging/azldev.spec), and the
repository provides Mage targets that produce its build inputs.

The binary build is offline. `mage archive VERSION` downloads the Go modules,
vendors them into a temporary Git export, and writes a deterministic
`out/rpm/azldev-VERSION.tar.gz`. The export uses committed `HEAD`. `mage srpm`
reads `Version` from the spec, creates that archive, and writes the source RPM
under `out/rpm/`.

## Build and test locally

Install Go 1.26 or newer and the RPM tools. On Fedora 43:

```console
sudo dnf upgrade golang
sudo dnf install mock rpm-build rpmdevtools
```

Build the source RPM from a committed tree, then rebuild it in the same chroot
that COPR will use:

```console
mage srpm
mock -r fedora-43-x86_64 --rebuild out/rpm/azldev-*.src.rpm
```

The archive intentionally uses committed `HEAD`, not uncommitted working-tree
content. This prevents local files from leaking into release artifacts.

To package a specific released tag, check it out first so `HEAD` points at that
release, then build:

```console
git checkout vX.Y.Z
mage srpm
mock -r fedora-43-x86_64 --rebuild out/rpm/azldev-X.Y.Z-*.src.rpm
```

## Configure Packit and COPR

Packit publishes each GitHub Release to COPR using [`.packit.yaml`](../../../.packit.yaml).
Complete these steps once:

1. Install the Packit GitHub App for this repository.
2. Create a [Fedora account](https://accounts.fedoraproject.org/) and sign in to
   [Fedora COPR](https://copr.fedorainfracloud.org/).
3. In the `liunan/azure-linux-dev-tools` COPR project, grant the `packit`
   Fedora account builder permission:

   ```console
   copr-cli edit-permissions --builder packit liunan/azure-linux-dev-tools
   ```

   Alternatively, approve Packit's builder request from the project's
   **Permissions** page after its first attempted build.

4. In the COPR project settings, add
   `github.com/microsoft/azure-linux-dev-tools` to **Packit allowed forge
   projects**.
5. Enable the `fedora-43-x86_64` chroot. Keep this synchronized with the target
   in `.packit.yaml` and move it forward when the package's minimum supported
   Fedora release changes.

For local fallback publication, open the COPR API page and save its generated
configuration as `~/.config/copr`. Treat the token in that file as a secret.
Install the client and inspect the currently available chroots:

```console
sudo dnf install copr-cli
copr-cli list-chroots
```

The package is published from the
[`liunan/azure-linux-dev-tools`](https://copr.fedorainfracloud.org/coprs/liunan/azure-linux-dev-tools/)
project. It targets Fedora chroots that provide Go 1.26 or newer.

Team members who perform fallback publication also need builder permission:

```console
copr-cli edit-permissions --builder USERNAME liunan/azure-linux-dev-tools
```

## Publish a build

For each release, the normal path is automatic:

1. Confirm that `mage changelog` updated `Version`, reset `Release` to 1, and
   added the new entry in [`packaging/azldev.spec`](../../../packaging/azldev.spec).
   These changes are normally committed by the Prepare release workflow before
   the release tag is created.
2. Merge the release PR. The Release workflow creates the tag and GitHub
   Release.
3. Packit handles the GitHub Release event, checks out its tag, runs the custom
   `create-archive` action, creates an SRPM, and submits it to the permanent
   COPR project.
4. Follow the Packit check to COPR and verify that every configured chroot
   succeeds.

Packit source preparation needs network access to download Go modules before
placing them in the archive's `vendor/` directory. The RPM build itself remains
independent of the network: the spec sets `GOFLAGS=-mod=vendor`, so compilation
uses only those archived dependencies.

If Packit is unavailable, build and submit the tagged release manually:

```console
git checkout vX.Y.Z
mage srpm
mock -r fedora-43-x86_64 --rebuild out/rpm/azldev-X.Y.Z-*.src.rpm
copr-cli build --enable-net off liunan/azure-linux-dev-tools out/rpm/azldev-X.Y.Z-*.src.rpm
```

COPR automatically signs successful RPMs and refreshes the project's DNF
repositories.

## Install from the repository

After the first successful COPR build, users run:

```console
sudo dnf install dnf-plugins-core
sudo dnf copr enable liunan/azure-linux-dev-tools
sudo dnf install azldev
```

Installing the RPM does not install Go. Command-specific tools such as `mock`,
KIWI, QEMU, and TMT remain optional and are checked by azldev when their
workflows are invoked.

## Retry a failed Packit build

After correcting an infrastructure or configuration problem, rerun the Packit
release job with a commit comment:

```text
/packit build --release vX.Y.Z
```
