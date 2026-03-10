// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/spf13/afero"
)

// Encapsulates the abstract interfaces usable for system interaction.
type SystemInterfaces struct {
	// Factory for accessing command execution.
	CmdFactory opctx.CmdFactory
	// Factory for accessing the file system.
	FileSystemFactory opctx.FileSystemFactory
	// Factory for querying the OS environment (e.g,. current working directory).
	OSEnvFactory opctx.OSEnvFactory
}

// DefaultCmdFactory returns a default [opctx.CmdFactory] that uses the [exec.Cmd] command execution facilities.
func DefaultCmdFactory(dryRunInfo opctx.DryRunnable, eventListener opctx.EventListener) (opctx.CmdFactory, error) {
	return nil, errors.New("not yet implemented")
}

// DefaultFileSystemFactory returns a default [opctx.FileSystemFactory] that uses the OS file system.
func DefaultFileSystemFactory() opctx.FileSystemFactory {
	return &defaultFileSystemFactory{fs: afero.NewOsFs()}
}

// DefaultOSEnvFactory returns a default [opctx.OSEnvFactory] that uses the OS environment.
func DefaultOSEnvFactory() opctx.OSEnvFactory {
	return &defaultOSEnv{}
}

type defaultFileSystemFactory struct {
	fs opctx.FS
}

func (f *defaultFileSystemFactory) FS() opctx.FS {
	return f.fs
}

type defaultOSEnv struct{}

func (e *defaultOSEnv) OSEnv() opctx.OSEnv {
	return e
}

func (e *defaultOSEnv) IsCurrentUserMemberOf(groupName string) (bool, error) {
	currentUser, err := user.Current()
	if err != nil {
		return false, fmt.Errorf("failed to get current user:\n%w", err)
	}

	group, err := user.LookupGroup(groupName)
	if err != nil {
		return false, fmt.Errorf("failed to lookup group '%s':\n%w", groupName, err)
	}

	groupIDs, err := currentUser.GroupIds()
	if err != nil {
		return false, fmt.Errorf("failed to get group IDs for current user:\n%w", err)
	}

	for _, groupID := range groupIDs {
		if groupID == group.Gid {
			return true, nil
		}
	}

	return false, nil
}

func (e *defaultOSEnv) LookupGroupID(groupName string) (int, error) {
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return -1, fmt.Errorf("failed to lookup group %#q:\n%w", groupName, err)
	}

	// Convert string GID to int
	groupID, err := strconv.Atoi(group.Gid)
	if err != nil {
		return -1, fmt.Errorf("failed to parse group ID %#q for group %#q:\n%w", group.Gid, groupName, err)
	}

	return groupID, nil
}

func (e *defaultOSEnv) Getwd() (string, error) {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return os.Getwd()
}

func (e *defaultOSEnv) Chdir(dir string) error {
	//nolint:wrapcheck // We are intentionally a pass-through.
	return os.Chdir(dir)
}

func (e *defaultOSEnv) Getenv(key string) string {
	return os.Getenv(key)
}
