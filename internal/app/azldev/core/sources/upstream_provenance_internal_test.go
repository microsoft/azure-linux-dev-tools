// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

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
	preparer.addUpstreamProvenanceMacros(macros, comp, provenanceWorkDir)

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
	preparer.addUpstreamProvenanceMacros(macros, comp, provenanceWorkDir)

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
	preparer.addUpstreamProvenanceMacros(macros, comp, provenanceWorkDir)

	assert.Empty(t, macros)
}

func TestAddUpstreamProvenanceMacros_UserDefineWins(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "grub2", "2.12", "5%{?dist}")

	comp := mockComponent(ctrl, "grub2", upstreamCfg(true))

	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc43"}
	macros := map[string]string{fedoraUpstreamVersionMacro: "user-override"}
	preparer.addUpstreamProvenanceMacros(macros, comp, provenanceWorkDir)

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
	preparer.addUpstreamProvenanceMacros(macros, comp, provenanceWorkDir)

	assert.Empty(t, macros)
}

func TestAddUpstreamProvenanceMacros_EmptyReleaseSkipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeProvenanceSpec(t, memFS, "pkg", "1.0", "")

	comp := mockComponent(ctrl, "pkg", upstreamCfg(true))

	preparer := &sourcePreparerImpl{fs: memFS, upstreamDistTag: ".fc43"}
	macros := map[string]string{}
	preparer.addUpstreamProvenanceMacros(macros, comp, provenanceWorkDir)

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
	preparer.addUpstreamProvenanceMacros(macros, comp, provenanceWorkDir)

	assert.Empty(t, macros, "provenance macros require opt-in via build.emit-upstream-provenance")
}

// upstreamCfg returns a Fedora-upstream component config with provenance
// emission toggled by emit.
func upstreamCfg(emit bool) *projectconfig.ComponentConfig {
	return &projectconfig.ComponentConfig{
		Spec:  projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream},
		Build: projectconfig.ComponentBuildConfig{EmitUpstreamProvenance: emit},
	}
}
