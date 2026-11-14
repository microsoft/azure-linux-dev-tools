// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package containertest

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/testhelpers"
	testcontainers "github.com/testcontainers/testcontainers-go"
)

//nolint:gochecknoglobals // The testing framework doesn't have a clean way to pass this around.
var containerImages containerImageCache

type containerImageCache struct {
	mutex  sync.Mutex
	builds map[string]func() (string, error)
}

func (c *containerImageCache) get(dockerfile string, build func() (string, error)) (string, error) {
	c.mutex.Lock()

	if c.builds == nil {
		c.builds = make(map[string]func() (string, error))
	}

	buildOnce, ok := c.builds[dockerfile]
	if !ok {
		buildOnce = sync.OnceValues(build)
		c.builds[dockerfile] = buildOnce
	}

	c.mutex.Unlock()

	return buildOnce()
}

// cacheContainerImage builds each Dockerfile once per test process. The generated tag prevents
// other test processes from replacing the image while containers are being created.
func cacheContainerImage(t *testing.T, dockerfile string, buildLogWriter io.Writer) (string, error) {
	t.Helper()

	return containerImages.get(dockerfile, func() (string, error) {
		dockerPath, err := testhelpers.FindTestDockerDirectory()
		if err != nil {
			return "", fmt.Errorf("find scenario Docker build context:\n%w", err)
		}

		provider, err := testcontainers.NewDockerProvider()
		if err != nil {
			return "", fmt.Errorf("get container provider:\n%w", err)
		}
		defer provider.Close()

		uidStr := strconv.Itoa(os.Getuid())
		gidStr := strconv.Itoa(os.Getgid())
		containerWorkDir := ContainerWorkDir

		req := testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:        dockerPath,
				Dockerfile:     dockerfile,
				Repo:           containerURI,
				KeepImage:      true,
				BuildLogWriter: buildLogWriter,
				BuildArgs: map[string]*string{
					"UID":      &uidStr,
					"GID":      &gidStr,
					"WORK_DIR": &containerWorkDir,
				},
			},
		}

		image, err := provider.BuildImage(t.Context(), &req)
		if err != nil {
			return "", fmt.Errorf("build scenario image from %#q:\n%w", dockerfile, err)
		}

		return image, nil
	})
}
