// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package pythonenv_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/pythonenv"
	"github.com/stretchr/testify/require"
)

func TestCreate(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		runErr error
	}{
		{name: "success"},
		{name: "command failure", runErr: os.ErrPermission},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testEnv := testutils.NewTestEnv(t)
			testEnv.CmdFactory.RegisterCommandInSearchPath("python3")

			calls := 0
			testEnv.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				calls++

				require.Equal(t, []string{"python3", "-m", "venv", "/work/venv"}, cmd.Args)
				require.Empty(t, cmd.Dir)

				return testCase.runErr
			}

			err := pythonenv.Create(testEnv.Env, "/work/venv")

			require.Equal(t, 1, calls)

			if testCase.runErr != nil {
				require.EqualError(t, err, "failed to create Python venv at `/work/venv`:\npermission denied")
				require.ErrorIs(t, err, testCase.runErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
