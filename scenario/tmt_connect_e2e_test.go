// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build e2e && linux

package scenario_tests

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/buildtestenv"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/containertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"
)

func TestTMTConnectProvisioning(t *testing.T) {
	if testing.Short() || os.Getenv(buildtestenv.TestingDisableNetworkTestsEnvVar) != "" {
		t.Skip("skipping network end-to-end test")
	}
	base, err := filepath.Abs(filepath.Join("..", "out", "tmt-connect-e2e"))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(base, fileperms.PrivateDir))
	artifacts, err := os.MkdirTemp(base, "run-")
	require.NoError(t, err)
	t.Logf("Retained connect TMT artifacts: %s", artifacts)

	net, err := network.New(t.Context())
	require.NoError(t, err)
	testcontainers.CleanupNetwork(t, net)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	publicKey, err := ssh.NewPublicKey(public)
	require.NoError(t, err)
	privateKey, err := ssh.MarshalPrivateKey(private, "")
	require.NoError(t, err)

	guest, err := testcontainers.GenericContainer(t.Context(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context: "docker", Dockerfile: "Dockerfile.tmt-ssh", KeepImage: true,
				Repo: "github.com/azldev/azldev-test/scenario/container", Tag: "latest-tmt-ssh",
			},
			Networks:       []string{net.Name},
			NetworkAliases: map[string][]string{net.Name: {"guest"}},
			Files: []testcontainers.ContainerFile{{
				Reader:            bytes.NewReader(ssh.MarshalAuthorizedKey(publicKey)),
				ContainerFilePath: "/home/tester/.ssh/authorized_keys",
				FileMode:          int64(fileperms.PublicFile),
			}},
			WaitingFor: wait.ForLog("Server listening on").WithStartupTimeout(time.Minute),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, guest)
	require.NoError(t, err)
	hostKey, err := guest.CopyFileFromContainer(t.Context(), "/etc/ssh/ssh_host_ed25519_key.pub")
	require.NoError(t, err)
	hostPublic, err := io.ReadAll(hostKey)
	require.NoError(t, err)
	require.NoError(t, hostKey.Close())

	test := cmdtest.NewScenarioTest("fixture/run.sh").
		WithCustomCmd("bash").
		WithTimeout(20 * time.Minute).
		InContainer().
		WithDockerfile("Dockerfile.tmt").
		WithInit().
		WithNetwork(net.Name).
		WithExtraMounts([]containertest.ContainerMount{
			containertest.NewContainerMount(artifacts, "/artifacts", nil),
		})
	test.AddDirRecursive(t, "fixture", filepath.Join("testdata", "tmt-connect"))
	test.AddSingleFile("project/azldev.toml", filepath.Join("testdata", "tmt-virtual", "azldev.toml"))
	test.AddSingleFile("project/azldev-tmt-probe.spec", filepath.Join("testdata", "tmt-virtual", "azldev-tmt-probe.spec"))
	// Credentials are copied only into the disposable runner, outside its host-mounted work directory.
	test.AddFileContents("/home/testuser/.ssh/id_ed25519", bytes.NewReader(pem.EncodeToMemory(privateKey)))
	test.AddFileContents("/home/testuser/.ssh/known_hosts", strings.NewReader("[guest]:2222 "+string(hostPublic)))
	results, runErr := test.Run(t)
	require.NoError(t, os.WriteFile(filepath.Join(artifacts, "container.stdout"), []byte(results.Stdout), fileperms.PrivateFile))
	require.NoError(t, os.WriteFile(filepath.Join(artifacts, "container.stderr"), []byte(results.Stderr), fileperms.PrivateFile))
	require.NoError(t, runErr)
	results.AssertZeroExitCode(t)

	roots := map[string]bool{}
	for _, tc := range []struct {
		name          string
		exit, tmtExit int
		counts        [4]int
	}{
		{"unknown", 1, 2, [4]int{0, 0, 0, 1}},
		{"pass", 0, 0, [4]int{1, 0, 0, 0}},
		{"mixed", 1, 2, [4]int{1, 1, 0, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(artifacts, tc.name+".json"))
			require.NoError(t, err)
			var rows []tmtArtifactRow
			decoder := json.NewDecoder(bytes.NewReader(data))
			require.NoError(t, decoder.Decode(&rows), "%s", data)
			require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
			require.Len(t, rows, 1)
			row := rows[0]
			assert.Equal(t, "azldev-tmt-probe", row.Test)
			require.NotNil(t, row.ExitCode)
			assert.Equal(t, tc.tmtExit, *row.ExitCode)
			exit, err := os.ReadFile(filepath.Join(artifacts, tc.name+".exit"))
			require.NoError(t, err)
			assert.Equal(t, strconv.Itoa(tc.exit)+"\n", string(exit))
			assert.False(t, roots[row.ArtifactDir], "each invocation needs a fresh artifact root")
			roots[row.ArtifactDir] = true
			rel, err := filepath.Rel("/var/tmp/azldev-connect", row.ArtifactDir)
			require.NoError(t, err)
			require.True(t, filepath.IsLocal(rel))
			root := filepath.Join(artifacts, rel)
			counts := [4]int{}
			for _, report := range row.JUnit {
				require.True(t, filepath.IsLocal(report))
				for index, count := range localTMTJUnitCounts(t, filepath.Join(root, report)) {
					counts[index] += count
				}
			}
			assert.Equal(t, tc.counts, counts)
			log, err := os.ReadFile(filepath.Join(root, "001-azldev-tmt-probe", "runner.log"))
			require.NoError(t, err)
			if tc.name == "unknown" {
				assert.Contains(t, string(log), "Failed to connect to the guest")
				nativeLog, err := os.ReadFile(filepath.Join(root, "001-azldev-tmt-probe/native/log.txt"))
				require.NoError(t, err)
				assert.Contains(t, string(nativeLog), "Host key verification failed")
				require.Len(t, row.JUnit, 1)
				report, err := os.ReadFile(filepath.Join(root, row.JUnit[0]))
				require.NoError(t, err)
				assert.Contains(t, string(report), `message="pending"`)
				outputs, err := filepath.Glob(filepath.Join(root, "*/native/plans/pass/execute/data/guest/*/tests/*/output.txt"))
				require.NoError(t, err)
				assert.Empty(t, outputs, "unknown host must not execute a test")

				return
			}
			require.Len(t, row.JUnit, 1)
			logs, err := filepath.Glob(filepath.Join(root, "*/native/plans/"+tc.name+
				"/execute/data/guest/*/tests/pass-*/output.txt"))
			require.NoError(t, err)
			require.Len(t, logs, 1)
			output, err := os.ReadFile(logs[0])
			require.NoError(t, err)
			assert.Contains(t, string(output), "root-helper-sourced")
			assert.Contains(t, string(output), "connect-pass-stdout")
			assert.Contains(t, string(output), "connect-pass-stderr")
		})
	}
}
