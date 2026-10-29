// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectValidationBeforeSetup(t *testing.T) {
	for _, test := range []struct {
		name  string
		flags []string
		want  string
	}{
		{"missing guest", []string{"--user", "tester", "--key", "key"}, "'--guest'"},
		{"missing user", []string{"--guest", "host", "--key", "key"}, "'--user'"},
		{"missing key", []string{"--guest", "host", "--user", "tester"}, "'--key'"},
		{"blank guest", []string{"--guest", " ", "--user", "tester", "--key", "key"}, "'--guest'"},
		{"negative port", []string{"--guest", "host", "--user", "tester", "--key", "key", "--port", "-1"}, "'--port'"},
		{"port zero", []string{"--guest", "host", "--user", "tester", "--key", "key", "--port", "0"}, "'--port'"},
		{"port too large", []string{"--guest", "host", "--user", "tester", "--key", "key", "--port", "65536"}, "'--port'"},
		{"missing key file", []string{"--guest", "host", "--user", "tester", "--key", "missing"}, "SSH key path"},
		{"directory key", []string{"--guest", "host", "--user", "tester", "--key", "/source"}, "regular file"},
		{"image", []string{"--image-path", ""}, "cannot be used"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := reportTestEnv(t)
			cmd := NewComponentTestCmd()
			cmd.SetArgs(append([]string{testTMTComponentName, "--provision", "connect"}, test.flags...))
			require.ErrorContains(t, cmd.ExecuteContext(env.Env), test.want)
			assert.Empty(t, env.CommandsExecuted)
			_, err := env.TestFS.Stat("/project/venv")
			require.Error(t, err)
		})
	}
}

func TestConnectPortBounds(t *testing.T) {
	for _, port := range []int{1, 22, 65535} {
		env := reportTestEnv(t)
		require.NoError(t, validateComponentTestOptions(env.Env, &ComponentTestOptions{
			Provision:  tmtProvisionConnect,
			Connection: tmtConnectionOptions{Guest: "host", Port: port, User: "root", Key: "/key"},
		}))
	}
}

func TestConnectionFlagsRejectedByOtherProvisioners(t *testing.T) {
	for _, provision := range []string{"virtual", "local"} {
		for _, flag := range []string{"guest", "user", "key", "port"} {
			t.Run(provision+"/"+flag, func(t *testing.T) {
				env := reportTestEnv(t)
				cmd := NewComponentTestCmd()
				value := ""
				if flag == "port" {
					value = "22"
				}
				cmd.SetArgs([]string{testTMTComponentName, "--provision", provision, "--"+flag, value})
				require.ErrorContains(t, cmd.ExecuteContext(env.Env), "requires '--provision connect'")
				assert.Empty(t, env.CommandsExecuted)
			})
		}
		t.Run(provision+"/requires rpm", func(t *testing.T) {
			env := reportTestEnv(t)
			cmd := NewComponentTestCmd()
			cmd.SetArgs([]string{testTMTComponentName, "--provision", provision})
			require.ErrorContains(t, cmd.ExecuteContext(env.Env), "'--rpm' is required")
			assert.Empty(t, env.CommandsExecuted)
		})
	}
}

func TestConnectCommandAndReports(t *testing.T) {
	for _, withRPM := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("rpm=%t/failure=%t", withRPM, fail), func(t *testing.T) {
				env := reportTestEnv(t)
				require.NoError(t, fileutils.WriteFile(env.TestFS, "/project/key", []byte("key"), fileperms.PrivateFile))
				for key, value := range map[string]string{
					"TMT_SSH_STRICT_HOST_KEY_CHECKING": "yes",
					"TMT_SSH_USER_KNOWN_HOSTS_FILE":   "/trusted/known_hosts",
					"TMT_SSH_BATCH_MODE":             "yes",
					"TMT_SSH_CONNECT_TIMEOUT":        "30",
					"TMT_PLUGINS":                    "/caller/plugins",
				} {
					t.Setenv(key, value)
				}
				var output strings.Builder
				env.Env.SetReportFile(&output)
				called := false
				env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
					if filepath.Base(cmd.Args[0]) != tmtProgram {
						return nil
					}
					called = true
					native := cmd.Args[slices.Index(cmd.Args, "--id")+1]
					port := "22"
					if withRPM {
						port = "22429"
					}
					want := []string{
						cmd.Args[0], "run", "--all", "--keep", "--id", native,
						"--workdir-root", filepath.Join(filepath.Dir(native), "workdir-root"), "plan",
						"provision", "--how", "connect", "--become",
						"--guest", "127.0.0.1", "--port", port, "--user", "tester", "--key", "/project/key",
					}
					if withRPM {
						want = append(want, "prepare", "--insert", "--how", "install", "--name",
							tmtCandidateRPMPrepareStepName, "--package", testRPMPath)
					}
					want = append(want, "report", "--insert", "--how", "junit", "--name",
						tmtJUnitPhase, "--no-include-output-log")
					assert.Equal(t, want, cmd.Args)
					for _, variable := range []string{
						"TMT_SSH_STRICT_HOST_KEY_CHECKING=yes", "TMT_SSH_USER_KNOWN_HOSTS_FILE=/trusted/known_hosts",
						"TMT_SSH_BATCH_MODE=yes", "TMT_SSH_CONNECT_TIMEOUT=30", "TMT_PLUGINS=/caller/plugins",
					} {
						assert.Contains(t, cmd.Env, variable)
						prefix, _, _ := strings.Cut(variable, "=")
						count := 0
						for _, entry := range cmd.Env {
							if strings.HasPrefix(entry, prefix+"=") {
								count++
							}
						}
						assert.Equal(t, 1, count)
					}
					require.NoError(t, fileutils.WriteFile(env.TestFS,
						filepath.Join(native, "report", tmtJUnitPhase, "junit.xml"),
						[]byte("<testsuites/>"), fileperms.PrivateFile))
					if fail {
						return errors.New("tmt failed")
					}
					return nil
				}
				cmd := NewComponentTestCmd()
				args := []string{testTMTComponentName, "--provision", "connect", "--guest", "127.0.0.1",
					"--user", "tester", "--key", "key", "--source-dir", "/source", "--work-dir", "/work"}
				if withRPM {
					args = append(args, "--rpm", testRPMPath, "--port", "22429")
				}
				cmd.SetArgs(args)
				err := cmd.ExecuteContext(env.Env)
				if fail {
					require.ErrorContains(t, err, "tmt failed")
				} else {
					require.NoError(t, err)
				}
				require.True(t, called)
				var results []azldev.TestRunResult
				require.NoError(t, json.Unmarshal([]byte(output.String()), &results))
				require.Len(t, results, 1)
				require.Len(t, results[0].JUnit, 1)
				_, err = env.TestFS.Stat(filepath.Join(results[0].ArtifactDir, results[0].JUnit[0]))
				require.NoError(t, err)
			})
		}
	}
}
