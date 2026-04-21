// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package cloudinit provides utilities for generating cloud-init configuration.
package cloudinit

import (
	"fmt"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"gopkg.in/yaml.v3"
)

// Config represents the cloud-init configuration.
// N.B. Minimal definition with what we're using.
//
//nolint:tagliatelle // We don't control the schema for this struct; it's an external format.
type Config struct {
	ChangePasswords       *PasswordConfig `yaml:"chpasswd,omitempty"`
	EnableSSHPasswordAuth *bool           `yaml:"ssh_pwauth,omitempty"`
	DisableRootUser       *bool           `yaml:"disable_root,omitempty"`
	Users                 []UserConfig    `yaml:"users,omitempty"`
}

// PasswordConfig contains password change configuration for cloud-init.
// N.B. Minimal definition with what we're using.
type PasswordConfig struct {
	List   string `yaml:"list,omitempty"`
	Expire *bool  `yaml:"expire,omitempty"`
}

// UserConfig contains user configuration for cloud-init.
// N.B. Minimal definition with what we're using.
//
//nolint:tagliatelle // We don't control the schema for this struct; it's an external format.
type UserConfig struct {
	Description       string   `yaml:"gecos,omitempty"`
	Groups            []string `yaml:"groups,omitempty"`
	LockPassword      *bool    `yaml:"lock_passwd,omitempty"`
	Name              string   `yaml:"name,omitempty"`
	PlainTextPassword string   `yaml:"plain_text_passwd,omitempty"`
	Shell             string   `yaml:"shell,omitempty"`
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys,omitempty"`
	Sudo              []string `yaml:"sudo,omitempty"`
}

// MarshalToYAML serializes the given cloud-init Config to YAML format.
func MarshalToYAML(config *Config) ([]byte, error) {
	bytes, err := yaml.Marshal(config)
	if err != nil {
		return []byte{}, fmt.Errorf("failed to serialize cloud config to YAML:\n%w", err)
	}

	// Prepend the cloud-config header.
	return append([]byte("#cloud-config\n"), bytes...), nil
}

// GenerateMetadata generates cloud-init metadata YAML content for the given hostname.
func GenerateMetadata(hostname string) []byte {
	return []byte(fmt.Sprintf("local-hostname: %s\n", hostname))
}

// WriteDataFiles writes the cloud-init meta-data and user-data files to the specified directory.
// Returns the paths to the created meta-data and user-data files.
func WriteDataFiles(
	ctx opctx.Ctx, dir string, hostname string, config *Config,
) (metaDataPath, userDataPath string, err error) {
	metaDataPath = filepath.Join(dir, "meta-data")
	userDataPath = filepath.Join(dir, "user-data")

	// Write metadata
	metadataContent := GenerateMetadata(hostname)

	err = fileutils.WriteFile(ctx.FS(), metaDataPath, metadataContent, fileperms.PrivateFile)
	if err != nil {
		return metaDataPath, userDataPath, fmt.Errorf("failed to write cloud-init metadata to %#q:\n%w", metaDataPath, err)
	}

	// Write user-data
	userDataContent, err := MarshalToYAML(config)
	if err != nil {
		return metaDataPath, userDataPath, fmt.Errorf("failed to marshal cloud config to YAML:\n%w", err)
	}

	err = fileutils.WriteFile(ctx.FS(), userDataPath, userDataContent, fileperms.PrivateFile)
	if err != nil {
		return metaDataPath, userDataPath, fmt.Errorf("failed to write cloud-init user data to %#q:\n%w", userDataPath, err)
	}

	return metaDataPath, userDataPath, nil
}
