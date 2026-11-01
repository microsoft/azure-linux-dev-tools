// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/govendor"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/sourceproviders_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func goVendorTestSpec() string {
	return strings.Join([]string{
		"Name: app", "Version: 1", "Release: 1", "Summary: app", "License: MIT",
		"Source0: app.tar.gz", "Patch0: fix.patch", "BuildRequires: golang(example.com/dep)",
		"%description", "app", "%prep", "%goprep -p1", "%build", "%gobuild -o app .",
		"%install", "install -D app %{buildroot}%{_bindir}/app", "%files", "%{_bindir}/app", "",
	}, "\n")
}

func TestPrepareSourcesGoVendor(t *testing.T) {
	for _, skipLookaside := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepare", true: "render"}[skipLookaside], func(t *testing.T) {
			ctx := newMockProcessorTestCtx()
			ctrl := gomock.NewController(t)
			component := components_testutils.NewMockComponent(ctrl)
			manager := sourceproviders_test.NewMockSourceManager(ctrl)
			config := &projectconfig.ComponentConfig{
				GoVendor: &projectconfig.GoVendorConfig{Enabled: lo.ToPtr(true), PrepPackages: []string{"git"}},
				Overlays: []projectconfig.ComponentOverlay{{
					Type: "spec-set-tag", Tag: "Summary", Value: "overlaid",
				}},
			}

			component.EXPECT().GetName().AnyTimes().Return("app")
			component.EXPECT().GetConfig().AnyTimes().Return(config)
			manager.EXPECT().FetchComponent(gomock.Any(), component, "/output", gomock.Any()).DoAndReturn(
				func(_ context.Context, _ any, _ string, _ ...sourceproviders.FetchComponentOption) error {
					for name, content := range map[string]string{
						"app.spec": goVendorTestSpec(), "app.tar.gz": "upstream", "fix.patch": "patch",
						"sources": "# upstream\n", ".gitignore": "/app.tar.gz\n",
					} {
						require.NoError(t, fileutils.WriteFile(ctx.FS(), "/output/"+name, []byte(content), fileperms.PublicFile))
					}

					return nil
				})
			manager.EXPECT().FetchFiles(gomock.Any(), component, "/output").Return(nil)

			var calls []string

			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				calls = append(calls, strings.Join(cmd.Args, " "))

				return nil
			}
			ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				assert.Contains(t, cmd.Args, "--unpriv")
				assert.Contains(t, cmd.Args, "--enable-network")
				assert.Contains(t, strings.Join(cmd.Args, " "), "go_vendor_archive")
				staging := goVendorTestStaging(t, ctx)
				prep, err := fileutils.ReadFile(ctx.FS(), filepath.Join(filepath.Dir(staging), "sources", "app.spec"))
				require.NoError(t, err)
				assert.Contains(t, string(prep), "Summary: overlaid", "overlays must run before generation")

				for name, content := range map[string]string{
					"app-vendor.tar.bz2": "generated archive",
					"archive-name":       "app-vendor.tar.bz2\n",
					"license":            "Apache-2.0 AND MIT\n",
					"sources-list":       "Source0: app.tar.gz\nPatch0: fix.patch\n",
				} {
					require.NoError(t, fileutils.WriteFile(ctx.FS(),
						filepath.Join(staging, name), []byte(content), fileperms.PublicFile))
				}

				return "", nil
			}

			options := []PreparerOption{WithGoVendorRunner(mock.NewRunner(ctx, "/mock.cfg"))}
			if skipLookaside {
				options = append(options, WithSkipLookaside())
			}

			preparer, err := NewPreparer(manager, ctx.FS(), ctx, ctx, options...)
			require.NoError(t, err)
			require.NoError(t, preparer.PrepareSources(ctx, component, "/output", true))
			output, err := fileutils.ReadFile(ctx.FS(), "/output/app.spec")
			require.NoError(t, err)
			assert.Contains(t, string(output), "License: Apache-2.0 AND MIT")
			assert.Contains(t, string(output), "tar -xf %{SOURCE1}")
			assert.NotContains(t, string(output), "BuildRequires: golang(")

			digest, err := fileutils.ComputeFileHash(ctx.FS(), fileutils.HashTypeSHA512, "/output/app-vendor.tar.bz2")
			require.NoError(t, err)
			sources, err := fileutils.ReadFile(ctx.FS(), "/output/sources")
			require.NoError(t, err)
			assert.Contains(t, string(sources), "# upstream\n")
			assert.Contains(t, string(sources),
				fedorasource.FormatSourcesEntry("app-vendor.tar.bz2", fileutils.HashTypeSHA512, digest))
			ignored, err := fileutils.ReadFile(ctx.FS(), "/output/.gitignore")
			require.NoError(t, err)
			assert.Equal(t, "/app.tar.gz\n/app-vendor.tar.bz2\n", string(ignored))
			assert.Contains(t, strings.Join(calls, "\n"), "--install go-vendor-tools askalono-cli")
			assert.Contains(t, strings.Join(calls, "\n"), " git")
			assert.Contains(t, strings.Join(calls, "\n"), "--scrub all")
			assert.NotContains(t, strings.Join(calls, "\n"), "go2rpm")

			matches, err := fileutils.Glob(ctx.FS(), "/tmp/azldev-go-vendor-*")
			require.NoError(t, err)
			assert.Empty(t, matches)
		})
	}
}

func goVendorTestStaging(t *testing.T, ctx *testctx.TestCtx) string {
	t.Helper()

	matches, err := fileutils.Glob(ctx.FS(), "/tmp/azldev-go-vendor-*/work/azldev-go-vendor.toml")
	require.NoError(t, err)
	require.Len(t, matches, 1)

	return filepath.Dir(matches[0])
}

func TestGoVendorFailuresDoNotPublishSpec(t *testing.T) {
	failure := errors.New("tool failed")

	for _, stage := range []string{
		"init", "install", "generate", "missing-archive", "empty-archive",
		"license", "missing-source", "source-list", "scrub", "archive-name", "unexpected-name",
	} {
		t.Run(stage, func(t *testing.T) {
			ctx := newMockProcessorTestCtx()
			ctrl := gomock.NewController(t)
			component := components_testutils.NewMockComponent(ctrl)
			component.EXPECT().GetName().AnyTimes().Return("app")
			component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
				GoVendor: &projectconfig.GoVendorConfig{Enabled: lo.ToPtr(true)},
			})
			require.NoError(t, fileutils.WriteFile(ctx.FS(),
				"/output/app.spec", []byte(goVendorTestSpec()), fileperms.PublicFile))
			require.NoError(t, fileutils.WriteFile(ctx.FS(), "/output/app.tar.gz", []byte("source"), fileperms.PublicFile))
			ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				if slices.Contains(cmd.Args, "--"+stage) {
					return failure
				}

				return nil
			}
			ctx.CmdFactory.RunAndGetOutputHandler = func(_ *exec.Cmd) (string, error) {
				if stage == "generate" {
					return "license failure details", failure
				}

				staging := goVendorTestStaging(t, ctx)
				files := map[string]string{
					"app-vendor.tar.bz2": "archive", "license": "MIT\n", "sources-list": "Source0: app.tar.gz\n",
					"archive-name": "app-vendor.tar.bz2\n",
				}

				switch stage {
				case "missing-archive":
					delete(files, "app-vendor.tar.bz2")
				case "empty-archive":
					files["app-vendor.tar.bz2"] = ""
				case "license":
					files["license"] = "bad\nLicense"
				case "missing-source":
					files["sources-list"] += "Patch0: absent.patch\n"
				case "source-list":
					files["sources-list"] = "Source0: /etc/passwd\n"
				case "archive-name":
					files["archive-name"] = "../outside.tar.bz2\n"
				case "unexpected-name":
					files["archive-name"] = "other.tar.bz2\n"
				}

				for name, data := range files {
					require.NoError(t, fileutils.WriteFile(ctx.FS(), filepath.Join(staging, name), []byte(data), fileperms.PublicFile))
				}

				return "", nil
			}
			p := &sourcePreparerImpl{fs: ctx.FS(), dryRunnable: ctx, goVendorRunner: mock.NewRunner(ctx, "/mock.cfg")}
			err := p.prepareGoVendor(ctx, component, "/output")
			require.Error(t, err)

			unchanged, readErr := fileutils.ReadFile(ctx.FS(), "/output/app.spec")
			require.NoError(t, readErr)
			assert.Equal(t, goVendorTestSpec(), string(unchanged))

			exists, readErr := fileutils.Exists(ctx.FS(), "/output/app-vendor.tar.bz2")
			require.NoError(t, readErr)
			assert.False(t, exists)
		})
	}
}

func TestGoVendorSourceCollisions(t *testing.T) {
	for _, name := range []string{"app-vendor.tar.bz2", govendor.ConfigFilename, "sources", "invalid-sources"} {
		t.Run(name, func(t *testing.T) {
			ctx := testctx.NewCtx()
			preparer := &sourcePreparerImpl{fs: ctx.FS()}
			content := "existing"

			filename := name
			switch name {
			case "sources":
				content = fedorasource.FormatSourcesEntry(
					"app-vendor.tar.bz2", fileutils.HashTypeSHA512, strings.Repeat("a", 128)) + "\n"
			case "invalid-sources":
				filename = "sources"
				content = "SHA512 (broken"
			}

			require.NoError(t, fileutils.WriteFile(ctx.FS(), "/output/"+filename, []byte(content), fileperms.PublicFile))
			require.Error(t, preparer.checkGoVendorSourceCollision("/output", "app-vendor.tar.bz2", govendor.ConfigFilename))
		})
	}
}
