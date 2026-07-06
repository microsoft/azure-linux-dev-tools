// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Command azldev is a developer tool for working on the Azure Linux distro.
//
// It parses, resolves, and queries the TOML-based metadata that defines Azure
// Linux, prepares component sources for building with mock, fetches source
// archives from lookaside caches, and offers convenience utilities for
// locally building individual packages and images.
//
// Install the latest release with:
//
//	go install github.com/microsoft/azure-linux-dev-tools/cmd/azldev@latest
//
// Run "azldev --help" for usage information, or see the user guide under docs/user.
package main

import (
	"github.com/microsoft/azure-linux-dev-tools/pkg/app/azldev_cli"
)

func main() {
	azldev_cli.Main()
}
