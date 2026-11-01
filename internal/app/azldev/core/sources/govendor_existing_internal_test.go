// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/govendor"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestPrepareExistingGoVendorReplacesPayload(t *testing.T) {
	ctx := newMockProcessorTestCtx()
	component := components_testutils.NewMockComponent(gomock.NewController(t))
	component.EXPECT().GetName().AnyTimes().Return("app")
	component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
		GoVendor: &projectconfig.GoVendorConfig{Enabled: lo.ToPtr(true)},
	})

	input := strings.Join([]string{
		"Name: app", "Version: 1.2", "Release: 1", "License: MIT AND Apache-2.0",
		"Source0: app.tar.gz", "Source1: app-%{version}-vendor.tar.bz2", "Source2: go-vendor-tools.toml",
		"%description", "app", "%prep", "%goprep", "tar -xf %{S:1}",
		"%build", "%global gomodulesmode GO111MODULE=on", "%gobuild -o app .",
		"%install", "%go_vendor_license_install -c %{S:2}",
		"%files -f %{go_vendor_license_filelist}", "/usr/bin/app", "",
	}, "\n")

	const (
		archiveName    = "app-1.2-vendor.tar.bz2"
		upstreamConfig = "[licensing]\ndetector = 'askalono'\n"
	)

	oldEntry := fedorasource.FormatSourcesEntry(archiveName, fileutils.HashTypeSHA512, strings.Repeat("a", 128))
	for name, data := range map[string]string{
		"app.spec": input, "app.tar.gz": "upstream source", archiveName: "old vendor",
		"go-vendor-tools.toml": upstreamConfig, "sources": oldEntry + "\n", ".gitignore": "/" + archiveName + "\n",
	} {
		require.NoError(t, fileutils.WriteFile(ctx.FS(), "/output/"+name, []byte(data), fileperms.PublicFile))
	}

	ctx.CmdFactory.RunAndGetOutputHandler = func(command *exec.Cmd) (string, error) {
		assert.Contains(t, strings.Join(command.Args, " "), "--verify")
		assert.Contains(t, strings.Join(command.Args, " "), "MIT AND Apache-2.0")
		staging := goVendorTestStaging(t, ctx)
		data, err := fileutils.ReadFile(ctx.FS(), filepath.Join(staging, govendor.ConfigFilename))
		require.NoError(t, err)
		assert.Equal(t, upstreamConfig, string(data))

		prep, err := fileutils.ReadFile(ctx.FS(), filepath.Join(filepath.Dir(staging), "sources", "app.spec"))
		require.NoError(t, err)
		assert.NotContains(t, string(prep), "Source1:")
		assert.NotContains(t, string(prep), "tar -xf %{S:1}")

		for name, data := range map[string]string{
			"azldev-generated-vendor.tar.bz2": "new vendor", "archive-name": archiveName + "\n",
			"license": "Apache-2.0 AND MIT\n", "sources-list": "Source0: app.tar.gz\nSource2: go-vendor-tools.toml\n",
		} {
			require.NoError(t, fileutils.WriteFile(ctx.FS(), filepath.Join(staging, name), []byte(data), fileperms.PublicFile))
		}

		return "", nil
	}
	preparer := &sourcePreparerImpl{fs: ctx.FS(), dryRunnable: ctx, goVendorRunner: mock.NewRunner(ctx, "/mock.cfg")}
	require.NoError(t, preparer.prepareGoVendor(ctx, component, "/output"))
	payload, err := fileutils.ReadFile(ctx.FS(), "/output/"+archiveName)
	require.NoError(t, err)
	assert.Equal(t, "new vendor", string(payload))

	finalSpec, err := fileutils.ReadFile(ctx.FS(), "/output/app.spec")
	require.NoError(t, err)
	assert.Contains(t, string(finalSpec), "Source1: app-%{version}-vendor.tar.bz2")
	assert.Contains(t, string(finalSpec), "License: MIT AND Apache-2.0")
	assert.NotContains(t, string(finalSpec), "azldev-go-vendor.toml")

	sources, err := fileutils.ReadFile(ctx.FS(), "/output/sources")
	require.NoError(t, err)
	digest, err := fileutils.ComputeFileHash(ctx.FS(), fileutils.HashTypeSHA512, "/output/"+archiveName)
	require.NoError(t, err)
	assert.Equal(t, fedorasource.FormatSourcesEntry(archiveName, fileutils.HashTypeSHA512, digest)+"\n", string(sources))
	ignore, err := fileutils.ReadFile(ctx.FS(), "/output/.gitignore")
	require.NoError(t, err)
	assert.Equal(t, "/"+archiveName+"\n", string(ignore))
}
