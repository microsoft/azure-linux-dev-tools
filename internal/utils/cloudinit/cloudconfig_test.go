// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cloudinit_test

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/cloudinit"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalToYAML(t *testing.T) {
	t.Parallel()

	boolTrue := true
	boolFalse := false

	tests := []struct {
		name           string
		config         *cloudinit.Config
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:   "empty config",
			config: &cloudinit.Config{},
			wantContains: []string{
				"#cloud-config",
				"{}", // Empty struct serializes to empty map
			},
			wantNotContain: []string{
				"chpasswd",
				"users",
			},
		},
		{
			name: "config with password changes",
			config: &cloudinit.Config{
				ChangePasswords: &cloudinit.PasswordConfig{
					List:   "root:secret",
					Expire: &boolFalse,
				},
			},
			wantContains: []string{
				"#cloud-config",
				"chpasswd:",
				"list: root:secret",
				"expire: false",
			},
		},
		{
			name: "config with SSH password auth enabled",
			config: &cloudinit.Config{
				EnableSSHPasswordAuth: &boolTrue,
			},
			wantContains: []string{
				"#cloud-config",
				"ssh_pwauth: true",
			},
		},
		{
			name: "config with users",
			config: &cloudinit.Config{
				Users: []cloudinit.UserConfig{
					{
						Name:              "testuser",
						Description:       "Test User",
						Groups:            []string{"wheel", "sudo"},
						PlainTextPassword: "testpass",
						Shell:             "/bin/bash",
						LockPassword:      &boolFalse,
					},
				},
			},
			wantContains: []string{
				"#cloud-config",
				"users:",
				"name: testuser",
				"gecos: Test User",
				"groups:",
				"- wheel",
				"- sudo",
				"plain_text_passwd: testpass",
				"shell: /bin/bash",
				"lock_passwd: false",
			},
		},
		{
			name: "config with disable root",
			config: &cloudinit.Config{
				DisableRootUser: &boolFalse,
			},
			wantContains: []string{
				"#cloud-config",
				"disable_root: false",
			},
		},
		{
			name: "config with SSH authorized keys",
			config: &cloudinit.Config{
				Users: []cloudinit.UserConfig{
					{
						Name: "sshuser",
						SSHAuthorizedKeys: []string{
							"ssh-rsa AAAAB3NzaC1yc2EAAAA... user@host",
						},
					},
				},
			},
			wantContains: []string{
				"#cloud-config",
				"ssh_authorized_keys:",
				"- ssh-rsa AAAAB3NzaC1yc2EAAAA... user@host",
			},
		},
		{
			name: "config with sudo rules",
			config: &cloudinit.Config{
				Users: []cloudinit.UserConfig{
					{
						Name: "adminuser",
						Sudo: []string{"ALL=(ALL) NOPASSWD:ALL"},
					},
				},
			},
			wantContains: []string{
				"#cloud-config",
				"sudo:",
				"- ALL=(ALL) NOPASSWD:ALL",
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result, err := cloudinit.MarshalToYAML(testCase.config)

			require.NoError(t, err)
			require.NotEmpty(t, result)

			resultStr := string(result)
			for _, want := range testCase.wantContains {
				assert.Contains(t, resultStr, want)
			}

			for _, notWant := range testCase.wantNotContain {
				assert.NotContains(t, resultStr, notWant)
			}
		})
	}
}

func TestMarshalToYAML_HeaderFormat(t *testing.T) {
	t.Parallel()

	config := &cloudinit.Config{}

	result, err := cloudinit.MarshalToYAML(config)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(result), len("#cloud-config\n"))
	assert.Equal(t, "#cloud-config\n", string(result[:14]),
		"YAML output must start with #cloud-config header")
}

func TestGenerateMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		hostname       string
		expectedOutput string
	}{
		{
			name:           "simple hostname",
			hostname:       "myhost",
			expectedOutput: "local-hostname: myhost\n",
		},
		{
			name:           "fqdn hostname",
			hostname:       "myhost.example.com",
			expectedOutput: "local-hostname: myhost.example.com\n",
		},
		{
			name:           "empty hostname",
			hostname:       "",
			expectedOutput: "local-hostname: \n",
		},
		{
			name:           "hostname with hyphen",
			hostname:       "my-test-host",
			expectedOutput: "local-hostname: my-test-host\n",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := cloudinit.GenerateMetadata(testCase.hostname)

			assert.Equal(t, testCase.expectedOutput, string(result))
		})
	}
}

func TestWriteDataFiles(t *testing.T) {
	t.Parallel()

	boolFalse := false

	tests := []struct {
		name         string
		hostname     string
		config       *cloudinit.Config
		wantMetadata string
		wantUserData []string
	}{
		{
			name:         "basic config",
			hostname:     "testhost",
			config:       &cloudinit.Config{},
			wantMetadata: "local-hostname: testhost\n",
			wantUserData: []string{"#cloud-config"},
		},
		{
			name:     "full config",
			hostname: "fullhost",
			config: &cloudinit.Config{
				ChangePasswords: &cloudinit.PasswordConfig{
					List:   "root:password",
					Expire: &boolFalse,
				},
				Users: []cloudinit.UserConfig{
					{
						Name:  "testuser",
						Shell: "/bin/bash",
					},
				},
			},
			wantMetadata: "local-hostname: fullhost\n",
			wantUserData: []string{
				"#cloud-config",
				"chpasswd:",
				"list: root:password",
				"users:",
				"name: testuser",
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := testctx.NewCtx()
			dir := "/cloud-init"

			require.NoError(t, ctx.FS().MkdirAll(dir, 0o755))

			metaDataPath, userDataPath, err := cloudinit.WriteDataFiles(ctx, dir, testCase.hostname, testCase.config)

			require.NoError(t, err)

			// Verify paths are correct
			assert.Equal(t, filepath.Join(dir, "meta-data"), metaDataPath)
			assert.Equal(t, filepath.Join(dir, "user-data"), userDataPath)

			// Verify metadata file content
			metadataContent, err := fileutils.ReadFile(ctx.FS(), metaDataPath)
			require.NoError(t, err)
			assert.Equal(t, testCase.wantMetadata, string(metadataContent))

			// Verify user-data file content
			userDataContent, err := fileutils.ReadFile(ctx.FS(), userDataPath)
			require.NoError(t, err)

			for _, want := range testCase.wantUserData {
				assert.Contains(t, string(userDataContent), want)
			}
		})
	}
}

func TestWriteDataFiles_WriteFails(t *testing.T) {
	t.Parallel()

	// Use a read-only filesystem to force write failures
	ctx := testctx.NewCtx(testctx.WithFS(afero.NewReadOnlyFs(afero.NewMemMapFs())))
	dir := "/cloud-init"

	_, _, err := cloudinit.WriteDataFiles(ctx, dir, "host", &cloudinit.Config{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write cloud-init metadata")
}
