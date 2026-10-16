// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

//nolint:testpackage // Intentionally verifies internal container request options.
package containertest

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContainerImageOptions(t *testing.T) {
	t.Parallel()

	collateral := NewContainerTestCollateral(t).
		WithDockerfile("Dockerfile.image-builder").
		WithInit().
		WithDevice("/dev/kvm", "/dev/kvm", "rwm").
		WithSupplementalGroup("123")
	assert.Equal(t, "Dockerfile.image-builder", collateral.dockerfile)
	assert.Equal(t, "Dockerfile.image-builder", collateral.dockerfile)
	assert.True(t, collateral.init)
	assert.Len(t, collateral.devices, 1)
	assert.Equal(t, "/dev/kvm", collateral.devices[0].PathOnHost)
	assert.Equal(t, "/dev/kvm", collateral.devices[0].PathInContainer)
	assert.Equal(t, "rwm", collateral.devices[0].CgroupPermissions)
	assert.Equal(t, []string{"123"}, collateral.groupAdd)
	assert.Equal(t, "latest-image-builder", containerImageTag(collateral.dockerfile))
	assert.Equal(t, "latest", containerImageTag(defaultDockerfile))
}

func TestContainerImageOptionsRejectInvalidValues(t *testing.T) {
	t.Parallel()

	tests := map[string]func(){
		"empty Dockerfile": func() {
			NewContainerTestCollateral(t).WithDockerfile("")
		},
		"nested Dockerfile": func() {
			NewContainerTestCollateral(t).WithDockerfile("../Dockerfile")
		},
		"empty device": func() {
			NewContainerTestCollateral(t).WithDevice("", "/dev/kvm", "rwm")
		},
		"empty group": func() {
			NewContainerTestCollateral(t).WithSupplementalGroup("")
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, test)
		})
	}
}
