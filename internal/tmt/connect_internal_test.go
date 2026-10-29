// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package tmt

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnectPortBounds(t *testing.T) {
	for _, port := range []int{1, 22, 65535} {
		env := newTMTTestEnv(t, nil)
		require.NoError(t, validateOptions(env.Env, &Options{
			Provision:  ProvisionConnect,
			Connection: ConnectionOptions{Guest: "host", Port: port, User: "root", Key: "/key"},
		}))
	}
}

