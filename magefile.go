///usr/bin/true; exec /usr/bin/env go run "$0" "$@"
//go:build ignore
// +build ignore

// Zero-install magefile.go enables running mage without installing the mage binary.
// This file provides two execution methods:
//   - go run magefile.go <target>
//   - ./magefile.go <target> (using the shebang above)
//
// The actual mage configuration is in the magefiles/ directory.
package main

import (
	"os"

	"github.com/magefile/mage/mage"
)

func main() { os.Exit(mage.Main()) }
