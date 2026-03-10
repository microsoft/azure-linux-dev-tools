# `azldev` command guidelines

This document outlines a set of guidelines for the design of `azldev`'s command-line interface. It is a living document that will evolve as `azldev` evolves.

## General goals

`azldev`'s command-line interface aims to be:

* Consistent / predictable
* Discoverable
* Easy to use
* Extensible for the future

## Top-level commands

`azldev`'s top-level commands are the "nouns" of its world--i.e., the first-class entities that exist in the Azure Linux distro ecosystem. This includes, but is not limited to:

* `project`
* `component`
* `package`
* `package-repo`
* `image`
* `distro`

## Verbs

Under the top-level commands are sub-commands (verbs) that act on these nouns. Where applicable, we make every effort to use a consistent set of actions, e.g.:

* `<noun> check` - performs static checks against one or more instances of this entity.
* `<noun> list` - lists all instances of this entity known within the current project; should *not* perform any non-trivial computation.
* `<noun> publish` - if applicable, publishes one or more instances of this entity to a target store.
* `<noun> query` - looks up 1 or more instances of this entity within the current project and provides detailed summaries; may perform more expensive computation (e.g., parsing, database lookups, service calls).

Any additional type-specific commands may be included.
