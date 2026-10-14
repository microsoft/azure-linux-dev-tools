// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build e2e && linux

package scenario_tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/containertest"
	"github.com/stretchr/testify/require"
)

func TestTMTVirtualProvisioning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test")
	}
	if os.Getenv(tmtEnableEnv) == "" {
		t.Skip("temporarily disabled; run 'mage e2e -target=tmt' to execute the virtual TMT regression")
	}

	inputs, externalInputs, err := loadTMTVirtualInputs(os.Getenv)
	require.NoError(t, err)
	if !externalInputs {
		fixture := buildAzureLinuxImage(t)
		inputs.imagePath = fixture.imagePath
		inputs.imageRevision = fixture.revision
	}

	rpmContainerPath := "/opt/azldev-tmt-fixtures/candidate.rpm"
	if externalInputs {
		rpmContainerPath = "/inputs/candidate.rpm"
	}

	kvm, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	require.NoError(t, err, "open /dev/kvm read-write as the scenario runner user")
	require.NoError(t, kvm.Close())

	kvmInfo, err := os.Stat("/dev/kvm")
	require.NoError(t, err)
	kvmStat, ok := kvmInfo.Sys().(*syscall.Stat_t)
	require.True(t, ok, "read /dev/kvm ownership")

	runID := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	artifactBase, err := filepath.Abs(filepath.Join("..", "out", "tmt-virtual-e2e"))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(artifactBase, fileperms.PrivateDir))
	artifactDir, err := os.MkdirTemp(artifactBase, runID+"-")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(artifactDir, fileperms.PrivateDir))
	t.Logf("TMT virtual e2e artifacts: %s", artifactDir)

	test := cmdtest.NewScenarioTest().
		WithCustomCmd("run-tmt-e2e").
		WithEnv(map[string]string{
			"AZL_TMT_CONTAINER_WORK_ROOT": filepath.Join("/var/tmp/tmt", filepath.Base(artifactDir)),
			"AZL_TMT_CONTAINER_RPM":       rpmContainerPath,
			"AZL_TMT_RUN_ID":              runID,
			tmtImageRevisionEnv:            inputs.imageRevision,
		}).
		WithTimeout(22 * time.Minute).
		InContainer().
		WithDockerfile("Dockerfile.tmt").
		WithInit().
		WithDevice("/dev/kvm", "/dev/kvm", "rwm").
		WithSupplementalGroup(strconv.FormatUint(uint64(kvmStat.Gid), 10)).
		WithNetwork().
		WithExtraMounts(tmtVirtualMounts(inputs, externalInputs, artifactDir))

	test.AddDirRecursive(t, "fixture", filepath.Join("testdata", "tmt-virtual"))
	requirementsPath, err := filepath.Abs(filepath.Join(
		"..", "internal", "app", "azldev", "cmds", "component", "requirements.txt",
	))
	require.NoError(t, err)
	test.AddSingleFile("tmt-requirements.txt", requirementsPath)

	results, runErr := test.Run(t)
	require.NoError(t, os.WriteFile(filepath.Join(artifactDir, "container.stdout"),
		[]byte(results.Stdout), fileperms.PrivateFile))
	require.NoError(t, os.WriteFile(filepath.Join(artifactDir, "container.stderr"),
		[]byte(results.Stderr), fileperms.PrivateFile))
	require.NoError(t, runErr)

	t.Logf("Standard output:\n%s", results.Stdout)
	t.Logf("Standard error:\n%s", results.Stderr)
	results.AssertZeroExitCode(t)
}

func tmtVirtualMounts(
	inputs tmtVirtualInputs,
	externalInputs bool,
	artifactDir string,
) []containertest.ContainerMount {
	mounts := []containertest.ContainerMount{
		containertest.NewContainerMount(
			inputs.imagePath,
			"/inputs/vm-base.qcow2",
			[]containertest.ContainerMountFlag{containertest.ContainerMountFlagReadOnly},
		),
		containertest.NewContainerMount(
			artifactDir,
			filepath.Join("/var/tmp/tmt", filepath.Base(artifactDir)),
			nil,
		),
	}

	if externalInputs {
		mounts = append(mounts, containertest.NewContainerMount(
			inputs.rpmPath,
			"/inputs/candidate.rpm",
			[]containertest.ContainerMountFlag{containertest.ContainerMountFlagReadOnly},
		))
	}

	return mounts
}
