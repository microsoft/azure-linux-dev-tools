// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldBuildCloudInit(t *testing.T) {
	tests := []struct {
		name             string
		options          ImageBootOptions
		testUserExplicit bool
		want             bool
	}{
		{
			name:             "no credentials and no explicit test-user => false",
			options:          ImageBootOptions{},
			testUserExplicit: false,
			want:             false,
		},
		{
			name:             "explicit '--test-user' alone => true (avoid silent ignore)",
			options:          ImageBootOptions{TestUserName: "foo"},
			testUserExplicit: true,
			want:             true,
		},
		{
			name:             "default test-user value but flag not changed => false",
			options:          ImageBootOptions{TestUserName: "test"},
			testUserExplicit: false,
			want:             false,
		},
		{
			name:             "password supplied => true",
			options:          ImageBootOptions{TestUserPassword: "pw"},
			testUserExplicit: false,
			want:             true,
		},
		{
			name:             "authorized public key supplied => true",
			options:          ImageBootOptions{AuthorizedPublicKeyPath: "/some/key.pub"},
			testUserExplicit: false,
			want:             true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := shouldBuildCloudInit(&testCase.options, testCase.testUserExplicit)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestBuildCloudInitConfig(t *testing.T) {
	const (
		testKeyPath = "/keys/id_ed25519.pub"
		testKey     = "ssh-ed25519 AAAAC3Nza test@example"
	)

	tests := []struct {
		name              string
		password          string
		pubKeyPath        string
		wantLockPassword  bool
		wantSSHPasswdAuth bool
		wantSSHKeys       []string
	}{
		{
			name:              "password only",
			password:          "secret",
			wantLockPassword:  false,
			wantSSHPasswdAuth: true,
			wantSSHKeys:       nil,
		},
		{
			name:              "ssh key only",
			pubKeyPath:        testKeyPath,
			wantLockPassword:  true,
			wantSSHPasswdAuth: false,
			wantSSHKeys:       []string{testKey},
		},
		{
			name:              "password and ssh key",
			password:          "secret",
			pubKeyPath:        testKeyPath,
			wantLockPassword:  false,
			wantSSHPasswdAuth: true,
			wantSSHKeys:       []string{testKey},
		},
		{
			name: "neither (explicit --test-user case)",
			// No password, no key. The account is locked; password auth is disabled
			// to avoid an unlocked no-password account reachable via SSH.
			wantLockPassword:  true,
			wantSSHPasswdAuth: false,
			wantSSHKeys:       nil,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			testEnv := testutils.NewTestEnv(t)

			if testCase.pubKeyPath != "" {
				err := fileutils.WriteFile(testEnv.Env.FS(), testCase.pubKeyPath,
					[]byte(testKey+"\n"), fileperms.PublicFile)
				require.NoError(t, err)
			}

			options := &ImageBootOptions{
				TestUserName:            "test",
				TestUserPassword:        testCase.password,
				AuthorizedPublicKeyPath: testCase.pubKeyPath,
			}

			cfg, err := buildCloudInitConfig(testEnv.Env, options)
			require.NoError(t, err)
			require.NotNil(t, cfg)

			require.NotNil(t, cfg.EnableSSHPasswordAuth)
			assert.Equal(t, testCase.wantSSHPasswdAuth, *cfg.EnableSSHPasswordAuth,
				"EnableSSHPasswordAuth")

			// Test user is the second entry (index 1); index 0 is "default".
			require.Len(t, cfg.Users, 2)
			testUser := cfg.Users[1]
			require.NotNil(t, testUser.LockPassword)
			assert.Equal(t, testCase.wantLockPassword, *testUser.LockPassword, "LockPassword")
			assert.Equal(t, testCase.password, testUser.PlainTextPassword)
			assert.Equal(t, testCase.wantSSHKeys, testUser.SSHAuthorizedKeys)
		})
	}
}

func TestBuildCloudInitConfig_MissingKeyFileErrors(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	options := &ImageBootOptions{
		TestUserName:            "test",
		AuthorizedPublicKeyPath: "/keys/does-not-exist.pub",
	}

	_, err := buildCloudInitConfig(testEnv.Env, options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read public key file")
}
