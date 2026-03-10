// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// The helloworld program is a simple example that prints "Hello, World!" to the console.

package main

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/helloworld"
)

func main() {
	helloworld.Hello()
	helloworld.Goodbye()
}
