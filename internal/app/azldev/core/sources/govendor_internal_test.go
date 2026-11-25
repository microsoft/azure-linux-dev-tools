// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/sourceproviders_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestGoVendorSourcePreparation(t *testing.T) {
	const newSource = "new"
	for _, mode := range []string{
		"discover", newSource, "expected hash", "mismatch", "collision", "diff", "skip overlays",
	} {
		t.Run(mode, func(t *testing.T) {
			ctx := newMockProcessorTestCtx()
			ctrl := gomock.NewController(t)
			comp := components_testutils.NewMockComponent(ctrl)
			manager := sourceproviders_test.NewMockSourceManager(ctrl)

			ref := projectconfig.SourceFileReference{Origin: projectconfig.Origin{
				Type: projectconfig.OriginTypeGoVendor, ConfigFile: "/project/vendor.toml",
			}}
			if mode == newSource || mode == "collision" {
				ref.Filename = "vendor.tar.bz2"
			}

			if mode == "mismatch" {
				ref.Hash, ref.HashType = strings.Repeat("a", 128), fileutils.HashTypeSHA512
			}

			const payload = "regenerated vendor"
			if mode == "expected hash" {
				require.NoError(t, fileutils.WriteFile(ctx.FS(), "/expected", []byte(payload), fileperms.PublicFile))
				ref.Hash, _ = fileutils.ComputeFileHash(ctx.FS(), fileutils.HashTypeSHA256, "/expected")
				ref.HashType = fileutils.HashTypeSHA256
			}

			config := &projectconfig.ComponentConfig{SourceFiles: []projectconfig.SourceFileReference{ref}}

			comp.EXPECT().GetName().AnyTimes().Return("app")
			comp.EXPECT().GetConfig().AnyTimes().Return(config)
			require.NoError(t, fileutils.WriteFile(ctx.FS(), "/project/vendor.toml", []byte(""), fileperms.PublicFile))

			input := strings.Join([]string{
				"Name: app", "Version: 1", "Release: 1", "License: MIT",
				"Source0: app.tar.gz", "Source5: vendor.tar.bz2", "%description", "App", "%prep",
				"%setup -q", "tar -xf %{S:5}", "%build", "make build", "%install", "make install",
				"%files", "/usr/bin/app", "",
			}, "\n")
			if mode == newSource {
				input = strings.ReplaceAll(input, "Source5: vendor.tar.bz2\n", "")
				input = strings.ReplaceAll(input, "tar -xf %{S:5}\n", "")
			}

			manager.EXPECT().FetchComponent(gomock.Any(), comp, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ any, directory string, options ...sourceproviders.FetchComponentOption) error {
					var fetch sourceproviders.FetchComponentOptions
					for _, option := range options {
						option(&fetch)
					}

					assert.True(t, fetch.PreserveGitDir)
					assert.False(t, fetch.SkipLookaside)

					files := map[string]string{"app.spec": input, "app.tar.gz": "source", ".git/HEAD": "history"}
					if mode != newSource {
						files["vendor.tar.bz2"] = "old vendor"
						files["sources"] = fedorasource.FormatSourcesEntry("vendor.tar.bz2", fileutils.HashTypeSHA512,
							strings.Repeat("a", 128)) + "\n"
					}

					for name, content := range files {
						require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(filepath.Join(directory, name))))
						require.NoError(t, fileutils.WriteFile(ctx.FS(), filepath.Join(directory, name),
							[]byte(content), fileperms.PublicFile))
					}

					return nil
				})
			manager.EXPECT().FetchFiles(gomock.Any(), comp, gomock.Any()).Return(nil)

			ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				assert.Contains(t, strings.Join(cmd.Args, " "), "go_vendor_archive create")

				matches, err := fileutils.Glob(ctx.FS(), "/project/build/vendor/azldev-vendor-*/output/config.toml")
				require.NoError(t, err)
				require.Len(t, matches, 1)
				assert.NotContains(t, matches[0], "*")
				directory := filepath.Dir(matches[0])
				prep, err := fileutils.ReadFile(ctx.FS(), filepath.Join(filepath.Dir(directory), "sources", "app.spec"))
				require.NoError(t, err)
				assert.NotContains(t, string(prep), "tar -xf %{S:5}")
				assert.NotContains(t, string(prep), "Source5:")

				if mode == newSource {
					assert.Contains(t, string(prep), "%{shescape:vendor.tar.bz2}")
				}

				history, err := fileutils.ReadFile(ctx.FS(), filepath.Join(filepath.Dir(directory), "sources", ".git", "HEAD"))
				require.NoError(t, err)
				assert.Equal(t, "history", string(history))

				for name, content := range map[string]string{"name": "vendor.tar.bz2\n", "vendor.tar.bz2": payload} {
					require.NoError(t, fileutils.WriteFile(ctx.FS(), filepath.Join(directory, name),
						[]byte(content), fileperms.PublicFile))
				}

				return "", nil
			}
			preparer, err := NewPreparer(manager, ctx.FS(), ctx, ctx,
				WithGoVendorRunner(mock.NewRunner(ctx, "/mock.cfg").WithBaseDir("/project/build/vendor")), WithSkipLookaside())
			require.NoError(t, err)

			if mode == "diff" {
				require.NoError(t, fileutils.MkdirAll(ctx.FS(), "/output"))
				diff, err := preparer.DiffSources(ctx, comp, "/output")
				require.NoError(t, err)
				assert.Contains(t, diff.String(), "vendor.tar.bz2")
				assert.NotContains(t, diff.String(), ".git/HEAD")

				return
			}

			err = preparer.PrepareSources(ctx, comp, "/output", mode != "skip overlays")
			if mode == "mismatch" || mode == "collision" {
				require.Error(t, err)

				data, readErr := fileutils.ReadFile(ctx.FS(), "/output/vendor.tar.bz2")
				require.NoError(t, readErr)
				assert.Equal(t, "old vendor", string(data))

				return
			}

			require.NoError(t, err)
			spec, err := fileutils.ReadFile(ctx.FS(), "/output/app.spec")
			require.NoError(t, err)
			assert.Contains(t, string(spec), input, "published spec is not converted or rewritten by generation")
			assert.NotContains(t, string(spec), "GOFLAGS")

			data, err := fileutils.ReadFile(ctx.FS(), "/output/vendor.tar.bz2")
			require.NoError(t, err)
			assert.Equal(t, payload, string(data))

			manifest, err := fileutils.ReadFile(ctx.FS(), "/output/sources")
			require.NoError(t, err)

			algorithm := ref.HashType
			if algorithm == "" {
				algorithm = fileutils.HashTypeSHA512
			}

			hash, err := fileutils.ComputeFileHash(ctx.FS(), algorithm, "/output/vendor.tar.bz2")
			require.NoError(t, err)
			assert.Contains(t, string(manifest), fedorasource.FormatSourcesEntry("vendor.tar.bz2", algorithm, hash))

			exists, err := fileutils.Exists(ctx.FS(), "/output/.git")
			require.NoError(t, err)
			assert.False(t, exists)
			assert.Equal(t, ref, config.SourceFiles[0], "runtime output must not mutate declarative inputs")
		})
	}
}

func TestRenderVendorPin(t *testing.T) {
	ctx := newMockProcessorTestCtx()
	comp := components_testutils.NewMockComponent(gomock.NewController(t))
	comp.EXPECT().GetName().AnyTimes().Return("app")

	ref := projectconfig.SourceFileReference{Origin: projectconfig.Origin{Type: projectconfig.OriginTypeGoVendor}}
	config := &projectconfig.ComponentConfig{SourceFiles: []projectconfig.SourceFileReference{ref}}
	comp.EXPECT().GetConfig().AnyTimes().Return(config)

	preparer := sourcePreparerImpl{fs: ctx.FS()}
	WithRenderManifest()(&preparer)
	require.False(t, preparer.needsVendorInputs(config))
	require.True(t, preparer.skipLookaside)

	_, err := preparer.updateSourcesFile(comp, "/staged", nil)
	require.ErrorContains(t, err, "run 'component prepare-sources'")

	pin := strings.Repeat("b", 128)
	config.SourceFiles[0].HashType, config.SourceFiles[0].Hash = fileutils.HashTypeSHA512, pin
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/staged/app.spec",
		[]byte("Name: app\nSource7: %{name}-%{version}-vendor.tar.gz\n%prep\ntar -xf %{S:7}\n"), fileperms.PublicFile))
	entry := fedorasource.FormatSourcesEntry("app-2-vendor.tar.gz", fileutils.HashTypeSHA512, strings.Repeat("a", 128))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/staged/sources", []byte(entry+"\n"), fileperms.PublicFile))

	_, err = preparer.updateSourcesFile(comp, "/staged", nil)
	require.NoError(t, err)
	require.NoError(t, RenderVendorPin(ctx.FS(), comp, "/staged/app.spec", "Source7: app-2-vendor.tar.gz"))
	manifest, err := fileutils.ReadFile(ctx.FS(), "/staged/sources")
	require.NoError(t, err)

	want := fedorasource.FormatSourcesEntry("app-2-vendor.tar.gz", fileutils.HashTypeSHA512, pin)
	assert.Equal(t, want+"\n", string(manifest))
}

func TestVendorSettings(t *testing.T) {
	const (
		invalid = "invalid"
		missing = "missing"
	)
	for _, mode := range []string{"explicit", "empty", "upstream", "default", missing, "no settings", invalid} {
		t.Run(mode, func(t *testing.T) {
			ctx := newMockProcessorTestCtx()
			preparer := &sourcePreparerImpl{fs: ctx.FS()}
			origin := projectconfig.Origin{}
			name := "native.toml"
			expected := "[archive]\ntidy = false\n"

			switch mode {
			case "explicit", "empty", invalid, missing:
				origin.ConfigFile = "/project/vendor.toml"

				content := expected
				if mode == "empty" {
					content, expected = "", ""
				}

				if mode == invalid {
					content = "invalid toml"
				}

				if mode != missing {
					require.NoError(t, fileutils.WriteFile(ctx.FS(), origin.ConfigFile,
						[]byte(content), fileperms.PublicFile))
				}
			case "upstream":
				require.NoError(t, fileutils.WriteFile(ctx.FS(), "/sources/"+name, []byte(expected), fileperms.PublicFile))
			case "default":
				name = ""

				require.NoError(t, fileutils.WriteFile(ctx.FS(), "/sources/go-vendor-tools.toml",
					[]byte(expected), fileperms.PublicFile))
			case "no settings":
				name, expected = "", ""
			}

			actual, err := preparer.vendorSettings("/sources", name, origin)
			if mode == missing || mode == invalid {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, expected, actual)
		})
	}
}

func TestVendorFingerprintUsesDeclaredInputs(t *testing.T) {
	declared := &projectconfig.ComponentConfig{SourceFiles: []projectconfig.SourceFileReference{
		{Filename: "other.tar.gz", Hash: "other"},
		{Origin: projectconfig.Origin{Type: projectconfig.OriginTypeGoVendor}},
	}}
	effective := *declared
	effective.SourceFiles = append([]projectconfig.SourceFileReference(nil), declared.SourceFiles...)
	effective.SourceFiles[1].Filename = "vendor.tar.bz2"
	effective.SourceFiles[1].Hash = "generated"
	restored := vendorFingerprintConfig(&effective, declared)
	assert.Equal(t, declared.SourceFiles, restored.SourceFiles)
	assert.Equal(t, "generated", effective.SourceFiles[1].Hash, "must not mutate runtime manifest inputs")
}
