# Azure Linux Dev Tools

Azure Linux Dev Tools is a collection of utilities useful for development
of the Azure Linux distro.

## `azldev`

`azldev` is a developer tool for working on the
[Azure Linux](https://github.com/microsoft/azurelinux) distro.

It supports:

* Parsing, resolving, and querying TOML-based metadata defining the Azure Linux distro.
* Preparing the sources for a source component for building with `mock`, `koji`, or similar standard build tool/service.
* Fetching source archives from lookaside caches.
* Developer convenience utilities for locally building individual packages and images.

### Quick start

1. Install `golang` and other prerequisites via your system's package manager, e.g.:

   ```console
   dnf install -y golang mock dnf-utils mock-rpmautospec
   ```

   Note: `mock-rpmautospec` plugin hooks `rpmautospec` into mock's build lifecycle. It pulls `rpmautospec` as a dependency which processes `%autorelease` and `%autochangelog` macros in spec files.

1. Install `azldev`:

    ```console
    go install github.com/microsoft/azure-linux-dev-tools/cmd/azldev@main
    ```

1. To ensure you can build using `mock` you must be a member of the `mock` group, e.g.:

   ```console
   usermod -aG mock $USER
   ```

   Note that this may require re-login or `newgrp` to ensure your environment registers
   the group update.

### Completion

You can install completion extensions for `azldev` with your shell by running:

```console
eval "$(azldev completion bash)"
```

Similar support exists for `fish` and `zsh`.

### User documentation

To learn more about the TOML-based metadata that `azldev` supports, `azldev`'s
command-line usage, or other usage information, please consult
this repo's [User Guide](./docs/user).

### Developing and contributing

Please see our [Contribution Guidelines](./CONTRIBUTING.md) for our project.

For development setup and workflow, please consult our [Developer Guide](./docs/developer).

## Getting Help

Have questions, found a bug, or need a new feature? Open an issue in our [GitHub
repository](https://github.com/microsoft/azure-linux-dev-tools/issues/new).

For security issues, please see the [security policy](./SECURITY.md).

## License

This project is licensed under the MIT License. See the [LICENSE](./LICENSE)
file for details.

## Trademarks

This project may contain trademarks or logos for projects, products, or
services. Authorized use of Microsoft trademarks or logos is subject to and must
follow [Microsoft's Trademark & Brand
Guidelines](https://www.microsoft.com/en-us/legal/intellectualproperty/trademarks/usage/general).
Use of Microsoft trademarks or logos in modified versions of this project must
not cause confusion or imply Microsoft sponsorship. Any use of third-party
trademarks or logos are subject to those third-party's policies.
