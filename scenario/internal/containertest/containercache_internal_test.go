// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package containertest

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerImageCacheConcurrentBuild(t *testing.T) {
	t.Parallel()

	var (
		cache   containerImageCache
		builds  atomic.Int32
		workers sync.WaitGroup
	)

	const callers = 16

	start := make(chan struct{})
	images := make([]string, callers)
	errs := make([]error, callers)

	for i := range callers {
		workers.Go(func() {
			<-start

			images[i], errs[i] = cache.get(defaultDockerfile, func() (string, error) {
				builds.Add(1)

				return "scenario-image", nil
			})
		})
	}

	close(start)
	workers.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		assert.Equal(t, "scenario-image", images[i])
	}

	assert.EqualValues(t, 1, builds.Load())
}

func TestContainerImageCacheSeparateDockerfiles(t *testing.T) {
	t.Parallel()

	var cache containerImageCache

	builds := make(map[string]int)

	for range 2 {
		for _, dockerfile := range []string{defaultDockerfile, "Dockerfile.image-builder", "Dockerfile.tmt-runner"} {
			image, err := cache.get(dockerfile, func() (string, error) {
				builds[dockerfile]++

				return dockerfile + "-image", nil
			})
			require.NoError(t, err)
			assert.Equal(t, dockerfile+"-image", image)
		}
	}

	for dockerfile, count := range builds {
		assert.Equal(t, 1, count, dockerfile)
	}
}

func TestContainerImageCacheBuildFailure(t *testing.T) {
	t.Parallel()

	var cache containerImageCache

	builds := 0
	buildErr := errors.New("image build failed")

	for range 2 {
		image, err := cache.get(defaultDockerfile, func() (string, error) {
			builds++

			return "", buildErr
		})
		require.ErrorIs(t, err, buildErr)
		assert.Empty(t, image)
	}

	assert.Equal(t, 1, builds)
}
