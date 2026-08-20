// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
)

// buildProvenanceMockSubdir is the work-dir subdirectory under which the build
// command isolates its %autorelease mock root, keeping it separate from the
// per-component build chroot that shares the same mock config.
const buildProvenanceMockSubdir = "azldev-provenance-mock"

// mockProcessorCleanupTimeout bounds how long boundary cleanup may spend
// scrubbing the mock chroot, so a hung scrub can't block command shutdown.
const mockProcessorCleanupTimeout = 5 * time.Minute

// createMockProcessor creates a lazily initialized [sources.MockProcessor]
// using the project/build distro's mock config. Source providers may use a
// different per-component upstream distro, which must not select the build
// chroot. Returns nil when the project mock config is unavailable.
func createMockProcessor(env *azldev.Env) *sources.MockProcessor {
	return newMockProcessor(env, "")
}

// createBuildMockProcessor creates a [sources.MockProcessor] for the build
// command. Unlike render, build creates a per-component build chroot from the
// same project mock config, and scrubs it after each component. Isolating the
// provenance root under the work dir keeps that scrub from destroying the
// shared %autorelease chroot mid-build. Returns nil when the project mock
// config is unavailable.
func createBuildMockProcessor(env *azldev.Env) *sources.MockProcessor {
	var isolatedBaseDir string
	if workDir := env.WorkDir(); workDir != "" {
		isolatedBaseDir = filepath.Join(workDir, buildProvenanceMockSubdir)
	}

	return newMockProcessor(env, isolatedBaseDir)
}

// newMockProcessor resolves the project/build distro mock config and builds a
// [sources.MockProcessor]. When isolatedBaseDir is non-empty, the processor's
// mock root lives under it instead of mock's shared default location. Returns
// nil when the project mock config is unavailable.
func newMockProcessor(env *azldev.Env, isolatedBaseDir string) *sources.MockProcessor {
	_, distroVerDef, err := env.Distro()
	if err != nil {
		slog.Info("Mock processor unavailable; could not resolve project distro", "error", err)

		return nil
	}

	if distroVerDef.MockConfigPath == "" {
		slog.Info("Mock processor unavailable; no project mock config path configured")

		return nil
	}

	slog.Info("Mock processor available",
		"mockConfig", distroVerDef.MockConfigPath, "isolatedBaseDir", isolatedBaseDir)

	return sources.NewMockProcessor(env, distroVerDef.MockConfigPath,
		sources.WithIsolatedMockBaseDir(isolatedBaseDir))
}

// destroyMockProcessor scrubs the mock chroot at a command boundary. It detaches
// from the command context so that cancellation (e.g. Ctrl-C) still cleans up
// the chroot, while bounding the scrub with a timeout so a hung cleanup can't
// block shutdown. A nil processor is a no-op.
func destroyMockProcessor(env *azldev.Env, processor *sources.MockProcessor) {
	if processor == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(env), mockProcessorCleanupTimeout)
	defer cancel()

	processor.Destroy(ctx)
}
