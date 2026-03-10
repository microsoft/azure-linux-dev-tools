// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func testCmd(env *azldev.Env) (cmd *cobra.Command) {
	cmd = &cobra.Command{}
	cmd.SetContext(env)

	return cmd
}

func TestRunFunc(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	ran := false
	injectedEnv := testEnv.Env

	runFunc := azldev.RunFunc(func(env *azldev.Env) (results interface{}, err error) {
		ran = true

		assert.Equal(t, injectedEnv, env)

		return results, nil
	})

	if assert.NotNil(t, runFunc) {
		err := runFunc(testCmd(injectedEnv), []string{})

		assert.NoError(t, err)
		assert.True(t, ran)
	}
}

func TestRunFunc_ResultsAsJSON(t *testing.T) {
	runFunc := azldev.RunFunc(func(env *azldev.Env) (interface{}, error) {
		return []string{"a", "b", "c"}, nil
	})

	env := testutils.NewTestEnv(t)
	env.Env.SetDefaultReportFormat(azldev.ReportFormatJSON)

	reportOutput := new(strings.Builder)
	env.Env.SetReportFile(reportOutput)

	if assert.NotNil(t, runFunc) {
		err := runFunc(testCmd(env.Env), []string{})

		if assert.NoError(t, err) {
			expected := `[
  "a",
  "b",
  "c"
]
`
			assert.Equal(t, expected, reportOutput.String())
		}
	}
}

func TestRunFunc_NilContext(t *testing.T) {
	runFunc := azldev.RunFunc(func(env *azldev.Env) (results interface{}, err error) {
		assert.Fail(t, "should not be called when context is nil")

		return results, nil
	})

	err := runFunc(&cobra.Command{}, []string{})
	assert.Error(t, err)
}

func TestRunFunc_UnexpectedContext(t *testing.T) {
	runFunc := azldev.RunFunc(func(env *azldev.Env) (results interface{}, err error) {
		assert.Fail(t, "should not be called when context is unexpected")

		return results, nil
	})

	cmd := cobra.Command{}
	cmd.SetContext(t.Context())

	err := runFunc(&cmd, []string{})
	assert.Error(t, err)
}

func TestRunFunc_Args(t *testing.T) {
	runFunc := azldev.RunFunc(func(env *azldev.Env) (results interface{}, err error) {
		assert.Fail(t, "should not be called when positional args are present")

		return results, nil
	})

	testEnv := testutils.NewTestEnv(t)

	err := runFunc(testCmd(testEnv.Env), []string{"arg"})
	assert.Error(t, err)
}

func TestRunFunc_Failure(t *testing.T) {
	runFunc := azldev.RunFunc(func(env *azldev.Env) (interface{}, error) {
		return nil, errors.New("some error")
	})

	testEnv := testutils.NewTestEnv(t)

	err := runFunc(testCmd(testEnv.Env), []string{})
	assert.Error(t, err)
}

func TestRunFuncWithExtraArgs(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	ran := false
	injectedEnv := testEnv.Env
	testArgs := []string{"arg1", "arg2"}

	runFunc := azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (results interface{}, err error) {
		ran = true

		assert.Equal(t, injectedEnv, env)
		assert.ElementsMatch(t, testArgs, args)

		return results, nil
	})

	if assert.NotNil(t, runFunc) {
		err := runFunc(testCmd(injectedEnv), testArgs)

		assert.NoError(t, err)
		assert.True(t, ran)
	}
}

func TestRunFuncWithExtraArgs_Failure(t *testing.T) {
	runFunc := azldev.RunFuncWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
		return nil, errors.New("some error")
	})

	testEnv := testutils.NewTestEnv(t)

	err := runFunc(testCmd(testEnv.Env), []string{})
	assert.Error(t, err)
}
