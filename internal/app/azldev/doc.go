// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

/*
Package azldev implements the core of the azldev command-line tool.

# Contents

To the executable, it provides:

  - The [App] type: implementation of the azldev command-line tool, usable by the executable.

To packages implementing commands, it provides:

  - The [Env] type: high-level environment and context for tool commands. Implements the [opctx.Ctx]
    interface for use with helper packages that do not take dependencies on this package ([azldev]).
  - [RunFunc] and [RunFuncWithExtraArgs]: helpers that *should* be used for azldev commands to
    implement [cobra.Command].

The [core] package provides additional utilities for command implementations.

# Background

The azldev command-line tool is focused on development-oriented operations for developing, building,
analyzing, and maintaining the Azure Linux distro or its components. This package ([azldev]) is
focused on providing common infrastructure to (1) simplify adding new commands, and (2) ensuring
that commands are exposed to the user in a consistent and discoverable manner.

Core to usage of this tool is the notion of a "project". A project contains configuration, components,
checkers, and other elements pertinent to building and maintaining components of the distro. A
project is typically defined at the git repo-level, although it is possible to have multiple
projects living in a single repo.

A file called `azldev.toml` defines a project. The directory containing that file is considered
the project root. This configuration file may, in turn, include other config files. A full
definition of the file format is outside the scope of this documentation.

# Execution Flow

When this command-line tool is invoked, it uses the [cobra] package to construct a root [cobra.Command]
containing global command-line options, and under which top-level tool commands will be
registered.

The executable that wraps an [App] instance will statically register the top-level commands
that will always be available through the tool. Command packages that include functionality
only relevant in certain projects or configurations will register a post-initialization callback
with the [App]. These callbacks will be invoked after configuration has been loaded
and processed.

When the executable transfers control to [App.Execute], the [App] instance will:

  - Inspect the command line for a core set of options that affect general tool execution.
    These include logging verbosity as well as options that influence how the project root
    and its configuration are found.
  - Initialize early logging, without any persistent log files.
  - Load and process project configuration. On error, execution will return to the executable wrapper.
  - Construct the singleton instance of [Env] that will be used for the duration of the tool's execution.
  - Re-initialize logging, if the configuration specified paths to log files.
  - Invoke any registered post-init callbacks.
  - Prune any intermediate sub-commands that have no leaf children.
  - Invoke the [cobra.Command.Execute] method to execute the command line.

Individual command packages implement instances of [cobra.Command] and use [RunFunc] or
[RunFuncWithExtraArgs] to implement the [cobra.Command]'s RunE callback function, providing
to them an "inner" function that implements the core logic of the command.

The [App] threads through the [Env] instance to [cobra.Command] instances by associating it as the
[context.Context] of the root-level [cobra.Command]. This allows [RunFunc] and [RunFuncWithExtraArgs]
to extract the [Env] instance and pass it through to the inner command implementations.

These inner implementations are expected to return an `interface{}` and an `error`. Upon regaining
control, [RunFunc] and [RunFuncWithExtraArgs] will format the returned `interface{}` value and
display it to the user using the encoding format requested on the command-line (e.g., JSON,
human-readable table, etc.).
*/
package azldev
