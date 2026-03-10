// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

/*
Package components provides abstractions and utilities for representing, querying, and managing software components
within an Azure Linux project. We define a software component as being a buildable entity that may produce
zero, one, or multiple packages when built.

# Responsibilities

This package is responsible for:

  - Defining types and interfaces for component metadata, configuration, and relationships.
  - Providing logic to enumerate and filter on collections of components within the environment.
  - Integrating with the broader azldev environment ([azldev.Env]) and project configuration
    ([projectconfig.ComponentConfig]) to ensure context-sensitive component handling.

# Usage Context

This package is used by command implementations and automation logic that need to operate on sets of components,
determine their properties, or perform actions such as builds, checks, or queries.
*/
package components
