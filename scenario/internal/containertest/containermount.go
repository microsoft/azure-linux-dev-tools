// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package containertest

import (
	"fmt"
)

type ContainerMountFlag string

const (
	ContainerMountFlagReadOnly ContainerMountFlag = "ro"
	ContainerMountFlagZ        ContainerMountFlag = "z"
)

func (f ContainerMountFlag) IsValid() error {
	switch f {
	case ContainerMountFlagReadOnly, ContainerMountFlagZ:
		// All good.
		return nil

	default:
		return fmt.Errorf("invalid container mount flag value (%v)", f)
	}
}

type ContainerMount struct {
	// Source path on the host.
	source string
	// Destination path in the container.
	destination string
	// Flags specifies the mount options.
	flags []ContainerMountFlag
}

// NewContainerMount creates a new [ContainerMount] with the specified source and destination paths
// and optional flags. It will panic if the source or destination paths are empty, or if any of the
// provided flags are invalid.
// This is a test function with inputs predetermined at test development time, so panicking
// is acceptable for invalid usage.
func NewContainerMount(source, destination string, flags []ContainerMountFlag) ContainerMount {
	if source == "" {
		panic("container mount source path cannot be empty")
	}

	if destination == "" {
		panic("container mount destination path cannot be empty")
	}

	for _, flag := range flags {
		if err := flag.IsValid(); err != nil {
			panic(fmt.Sprintf("invalid container mount flag (%v):\n%v", flag, err))
		}
	}

	return ContainerMount{
		source:      source,
		destination: destination,
		flags:       flags,
	}
}

func (cm *ContainerMount) ContainerMountString() string {
	mountString := cm.source + ":" + cm.destination

	// Append flags if any.
	if len(cm.flags) != 0 {
		mountString += ":"

		for i, flag := range cm.flags {
			if i > 0 {
				mountString += ","
			}

			mountString += string(flag)
		}
	}

	return mountString
}
