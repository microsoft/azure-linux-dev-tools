// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// The helloworld package provides a simple example that prints "Hello, World!" or "Goodbye, World!" to the console.

package helloworld

import (
	"fmt"
)

// Greets the world!
func Hello() {
	fmt.Println("Hello, world!")
}

// Says farewell to the world.
func Goodbye() {
	fmt.Println("Goodbye, world!")
}
