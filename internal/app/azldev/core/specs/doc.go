// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

/*
Package specs provides abstractions and utilities for accessing and parsing RPM spec files associated with components
in Azure Linux projects.

# Responsibilities

This package is responsible for:

  - Defining the [ComponentSpec] interface, which abstracts access to a component's build specification (spec file),
    including methods for parsing and retrieving the spec.
  - Providing the [ComponentSpecDetails] type, which encapsulates parsed information about a component's spec,
    leveraging the underlying RPM spec parsing utilities.
  - Implementing logic to ensure spec files are accessible in a local or isolated environment, and to parse them in
    a way that honors the component's build configuration (macros, with/without flags, etc.).
  - Integrating with the broader azldev environment ([azldev.Env]) and project configuration
    ([projectconfig.ComponentConfig]) to provide context-sensitive spec handling.

# Usage Context

This package is used by higher-level command implementations and tooling that need to inspect, validate, or operate
on component spec files as part of build, analysis, or maintenance workflows. It ensures that spec parsing is
performed in an environment that matches the target distro and component configuration, using mock and isolated
environments as needed.

# Design Notes

- Spec parsing is performed using a mock isolated environment to avoid side effects and to ensure correctness.
*/
package specs
