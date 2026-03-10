# Ideas for future consideration

## Package building

### Parameterized components

A single component can be defined to be built with a matrix of parameters, where each set of parameters results in a different "flavour" of the component being built.

Sample use cases:

- Building different configurations of the kernel.
- Building a bootstrap and full version of a component to resolve circular dependencies.

### One project can define and build different versions of the kernel

### OOT module components can build with all or selected versions of the kernels

Components providing OOT kernel modules must have a way to build against all supported kernel versions or a specific subset of them.

**IMPORTANT:** it must be possible to define OOT components in such way, that packages built for a newer kernel version are **NOT** considered as updates of the ones built for the older kernel versions. This is required, so that updating the OOT components does not inadvertently update the kernel as well due to the package's dependency on a specific kernel version.

Example:

- Project defines `kernel-older` version 1.0.0 and `kernel-newer` version 1.1.0.
- Project builds `OOT-comp` for both kernels.
- Having the package manager update `OOT-comp` on `kernel-older` does not automatically update to `kernel-newer`.

Effectively this means that the `OOT-comp` packages defined for both kernels are not considered the same package by the package manager/RPM.

## Supported origins for component sources

### Fedora SRPM repos

### Blob stores with anonymous access

### Azure blob stores requiring authentication

For such blob stores, the tool should at minimum be able to use the credentials set by logging into Azure with `az login`.
