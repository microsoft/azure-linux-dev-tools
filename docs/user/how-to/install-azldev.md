# How to: install azldev

The Fedora COPR repository provides prebuilt `azldev` RPMs, so installing Go is
not required.

Enable the repository and install the package:

```console
sudo dnf install dnf-plugins-core
sudo dnf copr enable liunan/azure-linux-dev-tools
sudo dnf install azldev
```

Verify the installation:

```console
azldev version
```

Command-specific tools such as `mock`, KIWI, QEMU, and TMT remain optional.
Install them when needed for the corresponding workflows.

To remove the package and disable the repository:

```console
sudo dnf remove azldev
sudo dnf copr disable liunan/azure-linux-dev-tools
```
