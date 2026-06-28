// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// projectV1 is the hand-written v1 reference projection. A future generator must
// reproduce this projection tree (proven by the golden vectors, which pin the
// sha256 of its RFC 8785 canonical form), not this source. Do NOT change v1's
// output - a new encoding is a new version, never an edit here.
//
// Each measured field is emitted by literal Go path under its frozen emit-key
// (the field's toml key), so deleting a measured field will not compile and the
// key can never drift to the Go identifier. Map key order is irrelevant: the
// assembled document is canonicalized (RFC 8785) at serialization. projectV1
// reads the resolved config directly; the omit predicate treats a nil-or-empty
// scalar slice as zero (see isScalarZero), so no config-normalization pre-pass is
// needed before projecting.
func projectV1(config projectconfig.ComponentConfig) (map[string]any, error) {
	var builder treeBuilder

	build, err := projectV1Build(config.Build)
	builder.emitComposite("build", build, err)

	overlays, err := projectV1Slice(len(config.Overlays), func(index int) (map[string]any, error) {
		return projectV1Overlay(config.Overlays[index])
	})
	builder.emitSlice("overlays", overlays, err)

	packages, err := projectV1Packages(config.Packages)
	builder.emitComposite("packages", packages, err)

	release, err := projectV1Release(config.Release)
	builder.emitComposite("release", release, err)

	render, err := projectV1Render(config.Render)
	builder.emitComposite("render", render, err)

	sourceFiles, err := projectV1Slice(len(config.SourceFiles), func(index int) (map[string]any, error) {
		return projectV1SourceFile(config.SourceFiles[index])
	})
	builder.emitSlice("source-files", sourceFiles, err)

	spec, err := projectV1Spec(config.Spec)
	builder.emitComposite("spec", spec, err)

	return builder.result()
}

func projectV1Spec(spec projectconfig.SpecSource) (map[string]any, error) {
	var builder treeBuilder

	builder.emit("type", spec.SourceType)
	builder.emit("upstream-commit", spec.UpstreamCommit)

	distro, err := projectV1Distro(spec.UpstreamDistro)
	builder.emitComposite("upstream-distro", distro, err)
	builder.emit("upstream-name", spec.UpstreamName)

	return builder.result()
}

func projectV1Distro(distro projectconfig.DistroReference) (map[string]any, error) {
	var builder treeBuilder

	builder.emit("name", distro.Name)
	builder.emit("version", distro.Version)

	return builder.result()
}

func projectV1Release(release projectconfig.ReleaseConfig) (map[string]any, error) {
	var builder treeBuilder

	builder.emit("calculation", release.Calculation)

	return builder.result()
}

func projectV1Build(build projectconfig.ComponentBuildConfig) (map[string]any, error) {
	var builder treeBuilder

	check, err := projectV1Check(build.Check)
	builder.emitComposite("check", check, err)
	builder.emitMap("defines", build.Defines)
	builder.emit("undefines", build.Undefines)
	builder.emit("with", build.With)
	builder.emit("without", build.Without)

	return builder.result()
}

func projectV1Check(check projectconfig.CheckConfig) (map[string]any, error) {
	var builder treeBuilder

	builder.emit("skip", check.Skip)

	return builder.result()
}

func projectV1Render(render projectconfig.ComponentRenderConfig) (map[string]any, error) {
	var builder treeBuilder

	builder.emit("skip-file-filter", render.SkipFileFilter)

	return builder.result()
}

func projectV1Overlay(overlay projectconfig.ComponentOverlay) (map[string]any, error) {
	var builder treeBuilder

	builder.emit("file", overlay.Filename)
	builder.emit("lines", overlay.Lines)
	builder.emit("package", overlay.PackageName)
	builder.emit("regex", overlay.Regex)
	builder.emit("replacement", overlay.Replacement)
	builder.emit("section", overlay.SectionName)
	builder.emit("tag", overlay.Tag)
	builder.emit("type", overlay.Type)
	builder.emit("value", overlay.Value)

	return builder.result()
}

func projectV1SourceFile(sourceFile projectconfig.SourceFileReference) (map[string]any, error) {
	var builder treeBuilder

	builder.emit("filename", sourceFile.Filename)
	builder.emit("hash", sourceFile.Hash)
	builder.emit("hash-type", sourceFile.HashType)
	builder.emit("replace-upstream", sourceFile.ReplaceUpstream)

	return builder.result()
}

// projectV1Package projects a single per-package override. Every PackageConfig
// leaf is publish-only (fingerprint:"-"), so this projects empty today and each
// map entry projects empty - yet the map stays measured (walked), so a future
// build-effective PackageConfig field is auto-measured.
func projectV1Package(_ projectconfig.PackageConfig) (map[string]any, error) {
	var builder treeBuilder

	return builder.result()
}

// projectV1Packages projects the per-package overrides map, each value via its
// frozen sub-projector. An entry whose value projects empty contributes nothing
// (projected emptiness), so an all-publish-only map adds nothing while remaining
// in the measured graph. Key ordering is left to RFC 8785 at serialization.
func projectV1Packages(packages map[string]projectconfig.PackageConfig) (map[string]any, error) {
	var builder treeBuilder

	for key, value := range packages {
		projected, err := projectV1Package(value)
		builder.emitComposite(key, projected, err)
	}

	return builder.result()
}

// projectV1Slice projects the elements of a struct slice in resolved slice order
// as a JSON array, so distinct positions cannot collide. Element order is a
// frozen semantic: reordering elements is a different encoding. An element that
// projects empty is kept as an empty object so positions stay aligned.
func projectV1Slice(length int, element func(index int) (map[string]any, error)) ([]any, error) {
	if length == 0 {
		return nil, nil
	}

	out := make([]any, 0, length)

	for index := range length {
		encoded, err := element(index)
		if err != nil {
			return nil, fmt.Errorf("element %d:\n%w", index, err)
		}

		if encoded == nil {
			encoded = map[string]any{}
		}

		out = append(out, encoded)
	}

	return out, nil
}
