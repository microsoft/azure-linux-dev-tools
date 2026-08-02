// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"log/slog"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
)

// Required-package presets for the shared MockProcessor.
//
// Render needs rpmautospec (macro expansion), rpmdevtools (spectool), and git
// (required for rpmautospec to read commit history). python3-click is required
// by rpmautospec but not declared as an RPM dependency. Ecosystem macro
// packages (go-srpm-macros, etc.) are already present via @buildsys-build →
// azurelinux-rpm-config.
//
// Query needs rpm-build for the `rpmspec` binary. It's typically already
// pulled in via @buildsys-build, but we install it explicitly so we don't
// depend on a particular buildgroup composition.
func mockPackagesForRender() []string {
	return []string{"rpmautospec", "rpmdevtools", "git", "python3-click"}
}

func mockPackagesForQuery() []string {
	// rpm-build provides rpmspec; python3 is needed to run query_process.py.
	// (The render path gets python3 transitively via python3-click, but the
	// query path doesn't install rpmautospec/python3-click.)
	//
	// Additional macro packages are installed so that build-time macros
	// affecting %files / %package expansion (and therefore --builtrpms
	// output) resolve during rpmspec parsing. Without these, --builtrpms
	// under-reports subpackages for specs that generate their %files
	// sections via macros, or that use macros like %pyproject_extras_subpkg
	// to emit whole subpackage stanzas at parse time.
	//
	// Curated list of common macro packages that emit %package / %files in
	// the Azure Linux spec corpus:
	//   * fonts-rpm-macros        — %fontfiles, %fontfamily_subpkg, etc.
	//   * pyproject-rpm-macros    — %pyproject_extras_subpkg
	//   * java-srpm-macros, javapackages-tools, javapackages-common —
	//                                            %mvn_package, %mvn_install,
	//                                            %javadoc_package (auto
	//                                            -javadoc subpackages, from
	//                                            macros.fjava in
	//                                            javapackages-common),
	//                                            jp_minimal bcond default.
	//                                            javapackages-common is
	//                                            normally pulled in via
	//                                            javapackages-tools, but we
	//                                            install it explicitly so
	//                                            %javadoc_package never
	//                                            silently disappears.
	//   * ghc-rpm-macros          — %ghc_lib_subpackage and ghc_prof/haddock
	//                                bcond defaults. Requires
	//                                query_process.py to prime
	//                                _ghc_version_cache so the macros don't
	//                                shell out to a `ghc` binary that isn't
	//                                installed in the chroot.
	//
	// We install `java-srpm-macros` (the actual binary RPM) rather than
	// `java-rpm-macros`, which is the SRPM name; the latter has no
	// `%files` section for the main package and is not a buildable binary.
	//
	// Macros that only affect %prep/%build/%install (e.g. %cargo_install,
	// %py3_build) don't need to be added — they don't change which binary
	// RPMs would be built.
	return []string{
		"rpm-build",
		"python3",
		"fonts-rpm-macros",
		"pyproject-rpm-macros",
		"java-srpm-macros",
		"javapackages-tools",
		"javapackages-common",
		"ghc-rpm-macros",
	}
}

// createMockProcessor creates a [sources.MockProcessor] using the project's
// mock config. Returns nil if the mock config is not available (e.g., no project
// config loaded, or no mock config path configured).
//
// requiredPackages is the set of packages to install in the chroot on first
// use. Use one of the mockPackagesFor* presets above to pick the right set
// for the calling command.
func createMockProcessor(env *azldev.Env, requiredPackages []string) *sources.MockProcessor {
	_, distroVerDef, err := env.Distro()
	if err != nil {
		slog.Info("Mock processor unavailable; could not resolve distro", "error", err)

		return nil
	}

	if distroVerDef.MockConfigPath == "" {
		slog.Info("Mock processor unavailable; no mock config path configured")

		return nil
	}

	slog.Info("Mock processor available", "mockConfig", distroVerDef.MockConfigPath)

	return sources.NewMockProcessor(env, distroVerDef.MockConfigPath, requiredPackages)
}
