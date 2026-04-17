// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldBuildCloudInit(&tc.options, tc.testUserExplicit)
			assert.Equal(t, tc.want, got)
		})
	}
}
