// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

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
	//   * java-srpm-macros, javapackages-tools — %mvn_package, %mvn_install,
	//                                            auto -javadoc subpackages,
	//                                            jp_minimal bcond default
	//   * ghc-rpm-macros          — %ghc_lib_subpackage and ghc_prof/haddock
	//                                bcond defaults. Requires the
	//                                ghc_version_override define set by
	//                                query_process.py to avoid shelling out
	//                                to a `ghc` binary that isn't installed
	//                                in the chroot.
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
		"ghc-rpm-macros",
	}
}
