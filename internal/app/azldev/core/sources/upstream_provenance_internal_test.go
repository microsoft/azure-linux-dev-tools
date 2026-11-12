// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/sourceproviders_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// fakeAutoreleaseResolver is a test double for [autoreleaseResolver] that
// returns canned output/error and records the arguments it was called with.
type fakeAutoreleaseResolver struct {
	output       string
	err          error
	lastHostDir  string
	lastSpecFile string
	calls        int
	observe      func(string, string)
}

func (f *fakeAutoreleaseResolver) CalculateRelease(
	_ context.Context, specHostDir, specFilename string,
) (string, error) {
	f.lastHostDir = specHostDir
	f.lastSpecFile = specFilename

	f.calls++
	if f.observe != nil {
		f.observe(specHostDir, specFilename)
	}

	return f.output, f.err
}

const provenanceWorkDir = "/prov-work"

// writeProvenanceSpec writes a minimal spec file named "<name>.spec" into
// provenanceWorkDir, including Version/Release tags only when the corresponding
// value is non-empty.
func writeProvenanceSpec(t *testing.T, memFS afero.Fs, name, version, release string) {
	t.Helper()

	require.NoError(t, fileutils.MkdirAll(memFS, provenanceWorkDir))

	content := "Name: " + name + "\nSummary: test\nLicense: MIT\n"
	if version != "" {
		content += "Version: " + version + "\n"
	}

	if release != "" {
		content += "Release: " + release + "\n"
	}

	specPath := filepath.Join(provenanceWorkDir, name+".spec")
	require.NoError(t, fileutils.WriteFile(memFS, specPath, []byte(content), fileperms.PublicFile))
}

func TestFedoraDistTag(t *testing.T) {
	assert.Equal(t, ".fc43", FedoraDistTag("fedora", "43"))
	assert.Equal(t, ".fc43", FedoraDistTag("Fedora", "43"), "distro name match is case-insensitive")
	assert.Empty(t, FedoraDistTag("azurelinux", "3.0"), "non-Fedora distros get no dist tag")
	assert.Empty(t, FedoraDistTag("fedora", ""), "empty release version yields no dist tag")
	assert.Empty(t, FedoraDistTag("fedora", "rawhide"), "non-numeric release version yields no dist tag")
	assert.Empty(t, FedoraDistTag("fedora", "43.0"), "non-integer release version yields no dist tag")
	assert.Empty(t, FedoraDistTag("", "43"))
}

func TestParseSpecVersionRelease(t *testing.T) {
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "grub2", "2.12", "5%{?dist}")

	version, release, err := parseSpecVersionRelease(memFS, filepath.Join(provenanceWorkDir, "grub2.spec"))
	require.NoError(t, err)
	assert.Equal(t, "2.12", version)
	assert.Equal(t, "5%{?dist}", release, "release is captured verbatim, dist is expanded later")
}

func TestParseSpecVersionReleaseReadsFirstRepeatedConditionalRelease(t *testing.T) {
	memFS := afero.NewMemMapFs()
	require.NoError(t, fileutils.MkdirAll(memFS, provenanceWorkDir))
	require.NoError(t, fileutils.WriteFile(memFS, filepath.Join(provenanceWorkDir, "grub2.spec"), []byte(`Name: grub2
Version: 2.12
%if 0
Release: 5%{?dist}
%else
Release: 6%{?dist}
%endif
`), fileperms.PublicFile))

	version, release, err := parseSpecVersionRelease(memFS, filepath.Join(provenanceWorkDir, "grub2.spec"))
	require.NoError(t, err)
	assert.Equal(t, "2.12", version)
	assert.Equal(t, "5%{?dist}", release)
}

func TestParseSpecVersionReleaseSkipsEmptyRepeatedConditionalTags(t *testing.T) {
	for _, editor := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(editor), func(t *testing.T) {
			memFS := afero.NewMemMapFs()
			require.NoError(t, fileutils.MkdirAll(memFS, provenanceWorkDir))
			require.NoError(t, fileutils.WriteFile(memFS, filepath.Join(provenanceWorkDir, "grub2.spec"), []byte(`Name: grub2
%if 0
Version:
Release:
%endif
Version: 2.12
Release: 5%{?dist}
`), fileperms.PublicFile))

			version, release, err := parseSpecVersionRelease(
				memFS, filepath.Join(provenanceWorkDir, "grub2.spec"), spec.WithEditor(editor),
			)
			require.NoError(t, err)
			assert.Equal(t, "2.12", version)
			assert.Equal(t, "5%{?dist}", release)
		})
	}
}

func TestParseSpecVersionRelease_MissingFile(t *testing.T) {
	_, _, err := parseSpecVersionRelease(afero.NewMemMapFs(), "/does-not-exist.spec")
	require.Error(t, err)
}

func TestAddUpstreamProvenanceMacros_FedoraUpstream(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "grub2", "2.12", "5%{?dist}")

	comp := mockComponent(ctrl, "grub2", upstreamCfg(true))

	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc43"}
	macros := map[string]string{}
	preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

	assert.Equal(t, "2.12", macros[fedoraUpstreamVersionMacro])
	assert.Equal(t, "5.fc43", macros[fedoraUpstreamReleaseMacro], "%{?dist} is expanded to the Fedora dist tag")
}

func TestAddUpstreamProvenanceMacros_LocalComponentSkipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "mytool", "1.0", "1%{?dist}")

	comp := mockComponent(ctrl, "mytool", &projectconfig.ComponentConfig{
		Spec:  projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal},
		Build: projectconfig.ComponentBuildConfig{EmitUpstreamProvenance: true},
	})

	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc43"}
	macros := map[string]string{}
	preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

	assert.Empty(t, macros, "local components have no upstream provenance even when opted in")
}

func TestAddUpstreamProvenanceMacros_NonFedoraSkipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "grub2", "2.12", "5%{?dist}")

	comp := mockComponent(ctrl, "grub2", upstreamCfg(true))

	// Empty dist tag signals a non-Fedora upstream; no macros should be added.
	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ""}
	macros := map[string]string{}
	preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

	assert.Empty(t, macros)
}

func TestAddUpstreamProvenanceMacros_UserDefineWins(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "grub2", "2.12", "5%{?dist}")

	comp := mockComponent(ctrl, "grub2", upstreamCfg(true))

	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc43"}
	macros := map[string]string{fedoraUpstreamVersionMacro: "user-override"}
	preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

	assert.Equal(t, "user-override", macros[fedoraUpstreamVersionMacro], "existing user-defined macro is not overwritten")
	assert.Equal(t, "5.fc43", macros[fedoraUpstreamReleaseMacro])
}

func TestAddUpstreamProvenanceMacros_MissingSpecBestEffort(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()

	comp := mockComponent(ctrl, "grub2", upstreamCfg(true))

	// No spec on disk: capture is best-effort, so no macros and no panic/error.
	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc43"}
	macros := map[string]string{}
	preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

	assert.Empty(t, macros)
}

func TestAddUpstreamProvenanceMacros_EmptyReleaseSkipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "pkg", "1.0", "")

	comp := mockComponent(ctrl, "pkg", upstreamCfg(true))

	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc43"}
	macros := map[string]string{}
	preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

	assert.Equal(t, "1.0", macros[fedoraUpstreamVersionMacro])

	_, hasRelease := macros[fedoraUpstreamReleaseMacro]
	assert.False(t, hasRelease, "no Release tag means no release macro is emitted")
}

func TestAddUpstreamProvenanceMacros_OptedOutSkipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "grub2", "2.12", "5%{?dist}")

	// Fedora upstream with a parseable spec, but provenance emission is not
	// enabled, so no macros should be added.
	comp := mockComponent(ctrl, "grub2", upstreamCfg(false))

	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc43"}
	macros := map[string]string{}
	preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

	assert.Empty(t, macros, "provenance macros require opt-in via build.emit-upstream-provenance")
}

func TestAddUpstreamProvenanceMacros_AutoreleaseResolved(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "fwupd-efi", "1.6", "%autorelease")

	comp := mockComponent(ctrl, "fwupd-efi", upstreamCfg(true))

	// rpmautospec prints the release behind a "Calculated release number: "
	// prefix even with a flag; the parser must extract just the value.
	resolver := &fakeAutoreleaseResolver{output: "Calculated release number: 3\n"}

	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc41", withGitRepo: true, autoreleaseResolver: resolver}
	macros := map[string]string{}
	preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

	assert.Equal(t, "1.6", macros[fedoraUpstreamVersionMacro])
	assert.Equal(t, "3.fc41", macros[fedoraUpstreamReleaseMacro],
		"%autorelease is resolved via rpmautospec and combined with the Fedora dist tag")
	assert.Equal(t, provenanceWorkDir, resolver.lastHostDir, "the pristine sources dir is bind-mounted for resolution")
	assert.Equal(t, "fwupd-efi.spec", resolver.lastSpecFile, "the spec basename is passed to the resolver")
}

func TestAddUpstreamProvenanceMacros_AutoreleaseNoMockProcessor(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "fwupd-efi", "1.6", "%autorelease")

	comp := mockComponent(ctrl, "fwupd-efi", upstreamCfg(true))

	// No resolver configured (nil MockProcessor) → not resolvable → release macro skipped.
	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc41", withGitRepo: true}
	macros := map[string]string{}
	preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

	assert.Equal(t, "1.6", macros[fedoraUpstreamVersionMacro])

	_, hasRelease := macros[fedoraUpstreamReleaseMacro]
	assert.False(t, hasRelease, "an unresolvable %autorelease is skipped, never emitted verbatim")
}

func TestAddUpstreamProvenanceMacros_AutoreleaseResolverFailed(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "fwupd-efi", "1.6", "%autorelease")

	comp := mockComponent(ctrl, "fwupd-efi", upstreamCfg(true))

	resolver := &fakeAutoreleaseResolver{err: errors.New("mock chroot exploded")}

	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc41", withGitRepo: true, autoreleaseResolver: resolver}
	macros := map[string]string{}
	preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

	_, hasRelease := macros[fedoraUpstreamReleaseMacro]
	assert.False(t, hasRelease, "a resolver error is best-effort skipped, not fatal")
}

func TestAddUpstreamProvenanceMacros_AutoreleaseInvalidOutput(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "fwupd-efi", "1.6", "%autorelease")

	comp := mockComponent(ctrl, "fwupd-efi", upstreamCfg(true))

	// A non-release message must not be forwarded into the macro.
	resolver := &fakeAutoreleaseResolver{output: "error: could not compute release"}

	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc41", withGitRepo: true, autoreleaseResolver: resolver}
	macros := map[string]string{}
	preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

	_, hasRelease := macros[fedoraUpstreamReleaseMacro]
	assert.False(t, hasRelease, "a non-numeric rpmautospec result is rejected, not emitted")
}

// TestAddUpstreamProvenanceMacros_AutoreleaseBracedForms verifies the braced
// and conditional %autorelease spellings are recognized as autorelease (and so
// resolved via rpmautospec) rather than emitted verbatim into the macro.
func TestAddUpstreamProvenanceMacros_AutoreleaseBracedForms(t *testing.T) {
	for _, releaseTag := range []string{"%{autorelease}", "%{?autorelease}", "%{autorelease -e asan}"} {
		t.Run(releaseTag, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			memFS := afero.NewMemMapFs()
			writeProvenanceSpec(t, memFS, "fwupd-efi", "1.6", releaseTag)

			comp := mockComponent(ctrl, "fwupd-efi", upstreamCfg(true))

			resolver := &fakeAutoreleaseResolver{output: "Calculated release number: 3\n"}

			preparer := &sourcePreparerImpl{
				fs: memFS, upstreamDistTag: ".fc41", withGitRepo: true, autoreleaseResolver: resolver,
			}
			macros := map[string]string{}
			preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

			assert.Equal(t, "3.fc41", macros[fedoraUpstreamReleaseMacro],
				"braced/conditional %autorelease is resolved, never emitted verbatim")
		})
	}
}

// TestAddUpstreamProvenanceMacros_PlainDistTag verifies the unconditional
// %{dist} form is substituted with the Fedora dist tag, just like %{?dist}.
func TestAddUpstreamProvenanceMacros_PlainDistTag(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "grub2", "2.12", "5%{dist}")

	comp := mockComponent(ctrl, "grub2", upstreamCfg(true))

	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc43"}
	macros := map[string]string{}
	preparer.addUpstreamProvenanceMacros(context.Background(), macros, comp, provenanceWorkDir)

	assert.Equal(t, "5.fc43", macros[fedoraUpstreamReleaseMacro],
		"unconditional %{dist} is expanded to the Fedora dist tag")
}

func TestPrepareSources_CapturesPristineProvenance(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		release       string
		override      string
		emit          bool
		applyOverlays bool
		resolverErr   error
		wantRelease   string
		wantCalls     int
	}{
		{
			name: "autorelease", release: "%autorelease -b 2", emit: true, applyOverlays: true,
			wantRelease: "3.fc43", wantCalls: 1,
		},
		{
			name: "static release", release: "5%{?dist}", emit: true, applyOverlays: true,
			wantRelease: "5.fc43",
		},
		{
			name: "explicit release override", release: "%autorelease", override: "42.fc43",
			emit: true, applyOverlays: true, wantRelease: "42.fc43",
		},
		{
			name: "provenance disabled", release: "%autorelease", applyOverlays: true,
		},
		{
			name: "overlays disabled", release: "%autorelease", emit: true,
		},
		{
			name: "failed resolution is not retried", release: "%autorelease", emit: true, applyOverlays: true,
			resolverErr: errors.New("release unavailable"), wantCalls: 1,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ctx := testctx.NewCtx()

			config := upstreamCfg(testCase.emit)
			if testCase.override != "" {
				config.Build.Defines = map[string]string{fedoraUpstreamReleaseMacro: testCase.override}
			}

			comp := mockComponent(ctrl, "fwupd-efi", config)
			manager := sourceproviders_test.NewMockSourceManager(ctrl)
			manager.EXPECT().FetchComponent(gomock.Any(), comp, provenanceWorkDir, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ components.Component, dir string, _ ...sourceproviders.FetchComponentOption) error {
					writeProvenanceSpec(t, ctx.FS(), "fwupd-efi", "1.6", testCase.release)

					return fileutils.WriteFile(ctx.FS(), filepath.Join(dir, ".git/HEAD"),
						[]byte("pinned upstream commit\n"), fileperms.PublicFile)
				})

			prepared := false
			resolver := &fakeAutoreleaseResolver{
				output: "Calculated release number: 3\n",
				err:    testCase.resolverErr,
				observe: func(dir, filename string) {
					assert.False(t, prepared, "resolve provenance before release preparation")

					version, release, err := parseSpecVersionRelease(ctx.FS(), filepath.Join(dir, filename))
					require.NoError(t, err)
					assert.Equal(t, "1.6", version)
					assert.Equal(t, testCase.release, release)

					head, err := fileutils.ReadFile(ctx.FS(), filepath.Join(dir, ".git/HEAD"))
					require.NoError(t, err)
					assert.Equal(t, "pinned upstream commit\n", string(head))
				},
			}
			preparer, err := NewPreparer(manager, ctx.FS(), ctx, ctx,
				WithGitRepo(nil, nil, "43"), WithoutSyntheticHistory(), WithSkipLookaside(),
				WithUpstreamProvenance(".fc43"), WithAutoreleaseResolver(resolver),
				WithFetchedSources(func(_ context.Context, _ components.Component, _ string) error {
					assert.Equal(t, testCase.wantCalls, resolver.calls)

					prepared = true

					writeProvenanceSpec(t, ctx.FS(), "fwupd-efi", "99", "99%{?dist}")

					return nil
				}))
			require.NoError(t, err)
			require.NoError(t, preparer.PrepareSources(ctx, comp, provenanceWorkDir, testCase.applyOverlays))
			assert.True(t, prepared)
			assert.Equal(t, testCase.wantCalls, resolver.calls)

			path := filepath.Join(provenanceWorkDir, "fwupd-efi"+MacrosFileExtension)
			if !testCase.emit || !testCase.applyOverlays {
				exists, err := fileutils.Exists(ctx.FS(), path)
				require.NoError(t, err)
				assert.False(t, exists)

				return
			}

			contents, err := fileutils.ReadFile(ctx.FS(), path)
			require.NoError(t, err)
			assert.Contains(t, string(contents), "%fedora_upstream_version 1.6\n")

			if testCase.wantRelease == "" {
				assert.NotContains(t, string(contents), "%fedora_upstream_release ")
			} else {
				assert.Contains(t, string(contents), "%fedora_upstream_release "+testCase.wantRelease+"\n")
			}
		})
	}
}

// upstreamCfg returns a Fedora-upstream component config with provenance
// emission toggled by emit.
func upstreamCfg(emit bool) *projectconfig.ComponentConfig {
	return &projectconfig.ComponentConfig{
		Spec:  projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream},
		Build: projectconfig.ComponentBuildConfig{EmitUpstreamProvenance: emit},
	}
}
