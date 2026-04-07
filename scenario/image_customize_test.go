// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/containertest"
	"github.com/stretchr/testify/require"
)

const (
	minimalOS30ImageTag = "3.0"
	commonTestDataDir   = "scenario/testdata/imagecustomizer"
)

// Tests that azldev can download and customize an image. The test also verifies
// that the customized image contains the expected changes.
func TestImageCustomize(t *testing.T) {
	t.Parallel()

	// Issue #71: tests are failing due to disk space errors?
	t.Skip()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	// 'currentDir' should be the <enlistment> folder.
	currentDir, err := os.Getwd()
	require.NoError(t, err)

	testDataDir := filepath.Join(currentDir, commonTestDataDir, "e2e")

	azldevConfigDir := filepath.Join(testDataDir, "azldev-config")
	azldevConfig := filepath.Join(azldevConfigDir, "azldev.toml")
	imageCustomizerConfigFile := filepath.Join(testDataDir, "image-config.yaml")
	outputDir := t.TempDir()
	outputPath := path.Join(outputDir, "customized-image.raw")

	testScript := `
set -euxo pipefail

# constants
mountDir=/mnt/image

# clean-up
function cleanUp() {
	if mountpoint -q $mountDir; then
	sudo umount $mountDir
	fi
	if [[ -n $loopBackDevice ]]; then
	sudo losetup -d $loopBackDevice
	fi
	sudo rm -rf $mountDir
	sudo rm -f ` + outputPath + `
}

currentUser=$(whoami)
currentGroup=$(id -nG $currentUser)

# If logDir is not absolute, it is appended to the azldev config dir - which
# is owned by root since it is mapped into the container.
# If logDir is absolute, it requires root privilege to be created.
# Either way, azldev will fail because it is owned by the root while azldev
# runs as testuser.
# To avoid this problem, we create the logDir here and give the testuser
# ownership of it.
logDir=$(grep -E "^log-dir\s*=" "` + azldevConfig + `" | sed -E "s/^log-dir\s*=\s*'([^']+)'/\1/")
sudo mkdir -p $logDir
sudo chown -R $currentUser:$currentGroup $logDir

# configure docker
sudo usermod -aG docker $currentUser
sudo chmod 666 /var/run/docker.sock

# setup clean-up handlers
trap cleanUp EXIT
trap cleanUp ERR

# customize the image
azldev image customize \
	--project ` + azldevConfigDir + ` \
	--image-tag ` + minimalOS30ImageTag + ` \
	--image-config ` + imageCustomizerConfigFile + ` \
	--output-image-format raw \
	--output-path ` + outputPath + `

# mount the image
sudo mkdir -p $mountDir
loopBackDevice=$(sudo losetup -f -P --show ` + outputPath + `)
sudo mount ${loopBackDevice}p2 $mountDir

# verify the file we expect to exist
if [[ ! -f $mountDir/usr/bin/jq ]]; then
  echo "$mountDir/usr/bin/jq does NOT exist"
  exit 1
fi
`

	test := cmdtest.NewScenarioTest().
		AddDirRecursive(t, testDataDir, testDataDir).
		WithScript(strings.NewReader(testScript)).
		InContainer().
		WithPrivilege().
		WithExtraMounts([]containertest.ContainerMount{
			// Mount the docker socket to allow the container to run docker commands
			containertest.NewContainerMount("/var/run/docker.sock", "/var/run/docker.sock", nil),
			// Need to mount /dev to allow device mounting inside the container
			containertest.NewContainerMount("/dev", "/dev", nil),
			// Mount the output dir so we can see the output of the inner container in the outer container
			containertest.NewContainerMount(outputDir, outputDir, nil),
		})

	results, err := test.Run(t)

	// Display stderr from the command execution
	t.Logf("Command stderr:\n%s", results.Stderr)

	require.NoError(t, err)
	require.Zero(t, results.ExitCode, "Expected test script exit code to be zero found (%d). Stdout:\n%s", results.ExitCode, results.Stdout)
}

// Tests that azldev does not allow both --image-file and --image-tag to be
// specified at the same time.
func TestImageCustomizeImageParamsBoth(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	test := cmdtest.NewScenarioTest(
		"image", "customize",
		"--image-file", "dummy-input.qcow2",
		"--image-tag", "dummy-tag",
		"--image-config", "dummy-config.yaml",
		"--output-image-format", "qcow2",
		"--output-path", "dummy-output.qcow2",
	).Locally()

	results, err := test.Run(t)
	require.NoError(t, err)

	// Display stderr from the command execution
	t.Logf("Command stderr:\n%s", results.Stderr)

	require.Contains(t, results.Stderr, "if any flags in the group [image-file image-tag] are set none"+
		" of the others can be; [image-file image-tag] were all set")
}

// Tests that azldev requires either --image-file or --image-tag to be
// specified.
func TestImageCustomizeImageParamsNone(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	test := cmdtest.NewScenarioTest(
		"image", "customize",
		"--image-config", "dummy-config.yaml",
		"--output-image-format", "qcow2",
		"--output-path", "dummy-output.qcow2",
	).Locally()

	results, err := test.Run(t)
	require.NoError(t, err)

	// Display stderr from the command execution
	t.Logf("Command stderr:\n%s", results.Stderr)

	require.Contains(t, results.Stderr, "at least one of the flags in the group [image-file image-tag] is required")
}
