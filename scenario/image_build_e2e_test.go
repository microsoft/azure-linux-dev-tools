// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build e2e && linux

package scenario_tests

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/buildtestenv"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/containertest"
	"github.com/stretchr/testify/require"
)

const (
	azureLinuxRepositoryURL = "https://github.com/microsoft/azurelinux.git"
	azureLinuxImageBranch   = "4.0"
	azureLinuxImage         = "1p-vm-base-gen2"
	imageBuildTimeout       = 15 * time.Minute
)

type azureLinuxImageFixture struct {
	imagePath string
	revision  string
	sha256    string
}

func TestAzureLinuxImageBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test")
	}

	fixture := buildAzureLinuxImage(t)
	t.Logf("Built Azure Linux image revision %s with SHA-256 %s", fixture.revision, fixture.sha256)
}

func buildAzureLinuxImage(t *testing.T) azureLinuxImageFixture {
	t.Helper()

	if os.Getenv(buildtestenv.TestingDisableNetworkTestsEnvVar) != "" {
		t.Skip("skipping image build because network tests are disabled")
	}

	artifactDirectory := t.TempDir()
	start := time.Now()

	test := cmdtest.NewScenarioTest().
		WithCustomCmd("build-image").
		WithArgs("x86_64", azureLinuxImage, azureLinuxRepositoryURL, azureLinuxImageBranch).
		WithTimeout(imageBuildTimeout).
		InContainer().
		WithDockerfile("Dockerfile.image-builder").
		WithPrivilege().
		WithNetwork().
		WithExtraMounts([]containertest.ContainerMount{
			containertest.NewContainerMount(artifactDirectory, "/artifacts", nil),
		})

	results, err := test.Run(t)
	require.NoError(t, err)
	t.Logf("Image build standard output:\n%s", results.Stdout)
	t.Logf("Image build standard error:\n%s", results.Stderr)
	require.Zero(t, results.ExitCode)

	revisionBytes, err := os.ReadFile(filepath.Join(artifactDirectory, "source-revision"))
	require.NoError(t, err)
	revision := strings.TrimSpace(string(revisionBytes))
	require.Len(t, revision, 40, "resolved Azure Linux revision must be a full commit SHA")

	imagePath := filepath.Join(artifactDirectory, azureLinuxImage+".qcow2")
	image, err := os.Open(imagePath)
	require.NoError(t, err)
	defer image.Close()

	hash := sha256.New()
	_, err = io.Copy(hash, image)
	require.NoError(t, err)

	sha := hex.EncodeToString(hash.Sum(nil))
	info, err := image.Stat()
	require.NoError(t, err)
	require.Positive(t, info.Size())

	t.Logf(
		"Built Azure Linux image %s at revision %s in %s: size=%d sha256=%s",
		azureLinuxImage,
		revision,
		time.Since(start),
		info.Size(),
		sha,
	)

	return azureLinuxImageFixture{
		imagePath: imagePath,
		revision:  revision,
		sha256:    sha,
	}
}
