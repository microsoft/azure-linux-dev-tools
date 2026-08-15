// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestManualToCounterMigration_ReplaysFullFingerprintHistory(t *testing.T) {
	release := projectconfig.ReleaseConfig{
		Calculation: projectconfig.ReleaseCalculationStatic,
		Counter: &projectconfig.ReleaseCounter{
			Source: projectconfig.ReleaseCounterSourceReleaseTag,
			Regex:  `^([0-9]+)%\{\?dist\}$`,
		},
	}

	ctrl := gomock.NewController(t)
	memFS := afero.NewMemMapFs()
	writeTestSpec(t, memFS, "test-pkg", "3%{?dist}")

	component := newMigrationTestComponent(ctrl, "test-pkg", &projectconfig.ComponentConfig{Release: release})
	preparer := newTestPreparer(memFS)

	// A component leaving 'manual' replays every historical fingerprint change as a
	// release increment. The jump is one-time and always upward, so EVR stays monotonic.
	err := preparer.tryBumpStaticRelease(component, filepath.Join(testSourcesDir, "test-pkg"), 4)
	require.NoError(t, err)
	renderedSpec, err := fileutils.ReadFile(memFS, filepath.Join(testSourcesDir, "test-pkg", "test-pkg.spec"))
	require.NoError(t, err)
	assert.Contains(t, string(renderedSpec), "Release: 7%{?dist}")
}

func newMigrationTestComponent(
	ctrl *gomock.Controller,
	name string,
	config *projectconfig.ComponentConfig,
) *components_testutils.MockComponent {
	component := components_testutils.NewMockComponent(ctrl)
	component.EXPECT().GetName().AnyTimes().Return(name)
	component.EXPECT().GetConfig().AnyTimes().Return(config)

	return component
}

func TestValidateReleaseCounterInSpec(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		content string
		counter projectconfig.ReleaseCounter
		wantErr bool
	}{
		{
			name: "release tag counter",
			content: "Name: test-pkg\nVersion: 1\nRelease: 0.007.gitabc%{?dist}\n" +
				"Summary: Test\nLicense: MIT\n",
			counter: projectconfig.ReleaseCounter{
				Source: projectconfig.ReleaseCounterSourceReleaseTag,
				Regex:  `^0\.([0-9]+)(?:\.git.*)$`,
			},
		},
		{
			name: "release tag requires one match",
			content: "Name: test-pkg\nVersion: 1\nRelease: 0.001.gitfirst%{?dist}\n%if 0\n" +
				"Release: 0.002.gitsecond%{?dist}\n%endif\nSummary: Test\nLicense: MIT\n",
			counter: projectconfig.ReleaseCounter{
				Source: projectconfig.ReleaseCounterSourceReleaseTag,
				Regex:  `^0\.([0-9]+)(?:\.git.*)$`,
			},
			wantErr: true,
		},
		{
			name: "release tag requires a matched capture",
			content: "Name: test-pkg\nVersion: 1\nRelease: prefix\n" +
				"Summary: Test\nLicense: MIT\n",
			counter: projectconfig.ReleaseCounter{
				Source: projectconfig.ReleaseCounterSourceReleaseTag,
				Regex:  `^prefix([0-9]+)?$`,
			},
			wantErr: true,
		},
		{
			name: "bare integer macro",
			content: "%global baserelease 007\nName: test-pkg\nVersion: 1\n" +
				"Release: %{baserelease}%{?dist}\nSummary: Test\nLicense: MIT\n",
			counter: projectconfig.ReleaseCounter{
				Source:    projectconfig.ReleaseCounterSourceSpecMacro,
				Directive: "global",
				Name:      "baserelease",
			},
		},
		{
			name: "transitively referenced macro",
			content: "%global baserelease 007\n%define package_release 0.%{baserelease}\n" +
				"Name: test-pkg\nVersion: 1\nRelease: %{package_release}%{?dist}\nSummary: Test\nLicense: MIT\n",
			counter: projectconfig.ReleaseCounter{
				Source:    projectconfig.ReleaseCounterSourceSpecMacro,
				Directive: "global",
				Name:      "baserelease",
			},
		},
		{
			name: "macro expression is rejected",
			content: "%global baserelease %{otherrelease}\nName: test-pkg\nVersion: 1\n" +
				"Release: %{baserelease}%{?dist}\nSummary: Test\nLicense: MIT\n",
			counter: projectconfig.ReleaseCounter{
				Source:    projectconfig.ReleaseCounterSourceSpecMacro,
				Directive: "global",
				Name:      "baserelease",
			},
			wantErr: true,
		},
		{
			name: "duplicate macro is rejected",
			content: "%global baserelease 1\n%global baserelease 2\nName: test-pkg\nVersion: 1\n" +
				"Release: %{baserelease}%{?dist}\nSummary: Test\nLicense: MIT\n",
			counter: projectconfig.ReleaseCounter{
				Source:    projectconfig.ReleaseCounterSourceSpecMacro,
				Directive: "global",
				Name:      "baserelease",
			},
			wantErr: true,
		},
		{
			name: "cross directive macro redefinition is rejected",
			content: "%global baserelease 1\n%define baserelease 2\nName: test-pkg\nVersion: 1\n" +
				"Release: %{baserelease}%{?dist}\nSummary: Test\nLicense: MIT\n",
			counter: projectconfig.ReleaseCounter{
				Source:    projectconfig.ReleaseCounterSourceSpecMacro,
				Directive: "global",
				Name:      "baserelease",
			},
			wantErr: true,
		},
		{
			name: "dead macro is rejected",
			content: "%global baserelease 7\n%global active_release 1\nName: test-pkg\nVersion: 1\n" +
				"Release: %{active_release}%{?dist}\nSummary: Test\nLicense: MIT\n",
			counter: projectconfig.ReleaseCounter{
				Source:    projectconfig.ReleaseCounterSourceSpecMacro,
				Directive: "global",
				Name:      "baserelease",
			},
			wantErr: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			memFS := afero.NewMemMapFs()
			writeTestSpecContent(t, memFS, "test-pkg", testCase.content)

			specPath := filepath.Join(testSourcesDir, "test-pkg", "test-pkg.spec")
			before, err := fileutils.ReadFile(memFS, specPath)
			require.NoError(t, err)

			err = ValidateReleaseCounterInSpec(memFS, testCase.counter, specPath, projectconfig.ComponentBuildConfig{})
			if testCase.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			after, readErr := fileutils.ReadFile(memFS, specPath)
			require.NoError(t, readErr)
			assert.Equal(t, before, after, "validation must not modify the rendered spec")
		})
	}
}

// TestValidateReleaseCounterInSpec_CaptureTarget covers release-tag counters whose regex matches
// the literal Release text but whose capture group cannot change the *rendered* Release. Each
// rejecting fixture reproduces a configuration that shipped broken.
func TestValidateReleaseCounterInSpec_CaptureTarget(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		content      string
		macrosLines  []string
		build        projectconfig.ComponentBuildConfig
		regex        string
		wantErrParts []string
	}{
		{
			// oniguruma, pcre, pcre2, rpm, rubygem-nokogiri, rubygem-rspec-*, softhsm.
			name: "capture inside undefined conditional is rejected",
			content: "%global baserelease 3\nName: test-pkg\nVersion: 1\n" +
				"Release: %{?prerelease:0.}%{baserelease}%{?dist}\nSummary: Test\nLicense: MIT\n",
			regex: `^%\{\?prerelease:([0-9]+)\.\}%\{baserelease\}%\{\?dist\}$`,
			wantErrParts: []string{
				"Release value `%{?prerelease:0.}%{baserelease}%{?dist}`",
				"is never defined by a %global or %define",
				"identical NEVRA",
				`set 'source = "spec-macro"'`,
			},
		},
		{
			name: "capture inside defined conditional is accepted",
			content: "%global prerelease rc1\n%global baserelease 3\nName: test-pkg\nVersion: 1\n" +
				"Release: %{?prerelease:0.}%{baserelease}%{?dist}\nSummary: Test\nLicense: MIT\n",
			regex: `^%\{\?prerelease:([0-9]+)\.\}%\{baserelease\}%\{\?dist\}$`,
		},
		{
			name: "conditional macro undefined by a later undefine is rejected",
			content: "%global prerelease rc1\n%undefine prerelease\n%global baserelease 3\n" +
				"Name: test-pkg\nVersion: 1\n" +
				"Release: %{?prerelease:0.}%{baserelease}%{?dist}\nSummary: Test\nLicense: MIT\n",
			regex:        `^%\{\?prerelease:([0-9]+)\.\}%\{baserelease\}%\{\?dist\}$`,
			wantErrParts: []string{"is never defined by a %global or %define"},
		},
		{
			// cpuinfo: capturing the '0' of 'shortcommit0' rewrites it to the undefined
			// 'shortcommit1', silently deleting the git hash from the Release.
			name: "capture inside a macro name is rejected",
			content: "%global patch_level 5\n%global shortcommit0 abc1234\nName: test-pkg\nVersion: 1\n" +
				"Release: %{patch_level}.git%{?shortcommit0}%{?dist}.2\nSummary: Test\nLicense: MIT\n",
			regex: `^%\{patch_level\}\.git%\{\?shortcommit([0-9]+)\}%\{\?dist\}\.2$`,
			wantErrParts: []string{
				"sits inside the name of macro reference %{shortcommit0}",
				"undefined macro",
				`set 'source = "spec-macro"'`,
			},
		},
		{
			// iscsi-initiator-utils: the leading '0.' marks a pre-release.
			name: "capture on the pre-release leading zero is rejected",
			content: "%global shortcommit0 abc1234\nName: test-pkg\nVersion: 1\n" +
				"Release: 0.git%{shortcommit0}%{?dist}.2\nSummary: Test\nLicense: MIT\n",
			regex: `^([0-9]+)\.git%\{shortcommit0\}%\{\?dist\}\.2$`,
			wantErrParts: []string{
				"leading pre-release marker",
				"sort above the final release",
				"capture the counter field after the leading '0.'",
			},
		},
		{
			name: "capture on a literal counter field is accepted",
			content: "Name: test-pkg\nVersion: 1\nRelease: 18.1%{?dist}\n" +
				"Summary: Test\nLicense: MIT\n",
			regex: `^18\.([0-9]+)%\{\?dist\}$`,
		},
		{
			name: "capture after a pre-release leading zero is accepted",
			content: "Name: test-pkg\nVersion: 1\nRelease: 0.7%{?dist}\n" +
				"Summary: Test\nLicense: MIT\n",
			regex: `^0\.([0-9]+)%\{\?dist\}$`,
		},
		{
			// 'dist' is supplied by the build environment, so its conditional body always renders.
			name: "capture inside a dist conditional body is accepted",
			content: "Name: test-pkg\nVersion: 1\nRelease: 1%{?dist:.0}\n" +
				"Summary: Test\nLicense: MIT\n",
			regex: `^1%\{\?dist:\.([0-9]+)\}$`,
		},
		{
			// Being build-environment defined does not make the *name* rewritable.
			name: "capture inside the dist macro name is rejected",
			content: "Name: test-pkg\nVersion: 1\nRelease: 1%{?dist}\n" +
				"Summary: Test\nLicense: MIT\n",
			regex:        `^1%\{\?dis(t)\}$`,
			wantErrParts: []string{"sits inside the name of macro reference %{dist}"},
		},
		{
			// kernel: the conditional macro lives only in the sibling macros file, which the
			// rendered spec pulls in with '%{load:}'. Scanning the spec alone would reject a
			// counter that does render.
			name: "capture inside a conditional defined only in the macros file is accepted",
			content: "%{load:%{_sourcedir}/test-pkg" + MacrosFileExtension + "}\n" +
				"Name: test-pkg\nVersion: 1\nRelease: %{?foo:9}%{?dist}\n" +
				"Summary: Test\nLicense: MIT\n",
			macrosLines: []string{"%foo 1"},
			regex:       `^%\{\?foo:([0-9]+)\}%\{\?dist\}$`,
		},
		{
			// A macros file that does not define the conditional macro must still reject.
			name: "capture inside a conditional missing from the macros file is rejected",
			content: "%{load:%{_sourcedir}/test-pkg" + MacrosFileExtension + "}\n" +
				"Name: test-pkg\nVersion: 1\nRelease: %{?foo:9}%{?dist}\n" +
				"Summary: Test\nLicense: MIT\n",
			macrosLines:  []string{"%bar 1"},
			regex:        `^%\{\?foo:([0-9]+)\}%\{\?dist\}$`,
			wantErrParts: []string{"is never defined by a %global or %define"},
		},
		{
			// A '%undefine' in the spec runs after the '%{load:}', so it still wins.
			name: "macros file definition undefined by the spec is rejected",
			content: "%{load:%{_sourcedir}/test-pkg" + MacrosFileExtension + "}\n%undefine foo\n" +
				"Name: test-pkg\nVersion: 1\nRelease: %{?foo:9}%{?dist}\n" +
				"Summary: Test\nLicense: MIT\n",
			macrosLines:  []string{"%foo 1"},
			regex:        `^%\{\?foo:([0-9]+)\}%\{\?dist\}$`,
			wantErrParts: []string{"is never defined by a %global or %define"},
		},
		{
			// 'build.without' reaches rpmbuild as '--without debug', which defines
			// '%_without_debug' even when no macros file carries it.
			name: "capture inside a build.without conditional is accepted",
			content: "Name: test-pkg\nVersion: 1\nRelease: %{?_without_debug:9}%{?dist}\n" +
				"Summary: Test\nLicense: MIT\n",
			build: projectconfig.ComponentBuildConfig{Without: []string{"debug"}},
			regex: `^%\{\?_without_debug:([0-9]+)\}%\{\?dist\}$`,
		},
		{
			name: "capture inside a build.with conditional is accepted",
			content: "Name: test-pkg\nVersion: 1\nRelease: %{?_with_tests:9}%{?dist}\n" +
				"Summary: Test\nLicense: MIT\n",
			build: projectconfig.ComponentBuildConfig{With: []string{"tests"}},
			regex: `^%\{\?_with_tests:([0-9]+)\}%\{\?dist\}$`,
		},
		{
			// The prefix alone proves nothing: this component never passes '--with tests'.
			name: "capture inside an unconfigured _with_ conditional is rejected",
			content: "Name: test-pkg\nVersion: 1\nRelease: %{?_with_tests:9}%{?dist}\n" +
				"Summary: Test\nLicense: MIT\n",
			build:        projectconfig.ComponentBuildConfig{With: []string{"docs"}},
			regex:        `^%\{\?_with_tests:([0-9]+)\}%\{\?dist\}$`,
			wantErrParts: []string{"is never defined by a %global or %define"},
		},
		{
			// 'build.undefines' is applied last by rpmbuild, so the flag is gone again.
			name: "capture inside a build.with conditional removed by undefines is rejected",
			content: "Name: test-pkg\nVersion: 1\nRelease: %{?_with_tests:9}%{?dist}\n" +
				"Summary: Test\nLicense: MIT\n",
			build: projectconfig.ComponentBuildConfig{
				With:      []string{"tests"},
				Undefines: []string{"_with_tests"},
			},
			regex:        `^%\{\?_with_tests:([0-9]+)\}%\{\?dist\}$`,
			wantErrParts: []string{"is never defined by a %global or %define"},
		},
		{
			name: "capture inside a rhel conditional body is accepted",
			content: "Name: test-pkg\nVersion: 1\nRelease: 1%{?rhel:.0}\n" +
				"Summary: Test\nLicense: MIT\n",
			regex: `^1%\{\?rhel:\.([0-9]+)\}$`,
		},
		{
			name: "capture inside a fedora conditional body is accepted",
			content: "Name: test-pkg\nVersion: 1\nRelease: 1%{?fedora:.0}\n" +
				"Summary: Test\nLicense: MIT\n",
			regex: `^1%\{\?fedora:\.([0-9]+)\}$`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			memFS := afero.NewMemMapFs()
			writeTestSpecContent(t, memFS, "test-pkg", testCase.content)

			if testCase.macrosLines != nil {
				writeTestMacrosFile(t, memFS, testCase.macrosLines)
			}

			counter := projectconfig.ReleaseCounter{
				Source: projectconfig.ReleaseCounterSourceReleaseTag,
				Regex:  testCase.regex,
			}
			specPath := filepath.Join(testSourcesDir, "test-pkg", "test-pkg.spec")

			err := ValidateReleaseCounterInSpec(memFS, counter, specPath, testCase.build)
			if len(testCase.wantErrParts) == 0 {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)

			for _, wantErrPart := range testCase.wantErrParts {
				assert.Contains(t, err.Error(), wantErrPart)
			}
		})
	}
}

// TestApplyReleaseCounterToFileInPlace_RejectsUnbumpableCapture proves the capture-target rule
// also guards the render path, so a bad counter fails fast instead of silently no-op bumping.
func TestApplyReleaseCounterToFileInPlace_RejectsUnbumpableCapture(t *testing.T) {
	memFS := afero.NewMemMapFs()
	writeTestSpecContent(t, memFS, "test-pkg",
		"%global baserelease 3\nName: test-pkg\nVersion: 1\n"+
			"Release: %{?prerelease:0.}%{baserelease}%{?dist}\nSummary: Test\nLicense: MIT\n")

	specPath := filepath.Join(testSourcesDir, "test-pkg", "test-pkg.spec")
	before, err := fileutils.ReadFile(memFS, specPath)
	require.NoError(t, err)

	counter := projectconfig.ReleaseCounter{
		Source: projectconfig.ReleaseCounterSourceReleaseTag,
		Regex:  `^%\{\?prerelease:([0-9]+)\.\}%\{baserelease\}%\{\?dist\}$`,
	}

	_, err = applyReleaseCounterToFileInPlace(memFS, counter, specPath, projectconfig.ComponentBuildConfig{}, 1)
	require.Error(t, err)

	after, err := fileutils.ReadFile(memFS, specPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a rejected counter must not rewrite the spec")
}

// TestClassifyCapture documents how each character position of a Release value is attributed to
// the macro construct that owns it.
func TestClassifyCapture(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		value         string
		position      int
		wantPlacement capturePlacement
		wantMacroName string
	}{
		{
			name:          "literal digit outside any macro",
			value:         `18.1%{?dist}`,
			position:      3,
			wantPlacement: capturePlacementLiteral,
		},
		{
			name:          "braced macro name",
			value:         `%{patch_level}.git%{?shortcommit0}`,
			position:      32,
			wantPlacement: capturePlacementMacroName,
			wantMacroName: "shortcommit0",
		},
		{
			name:          "bare macro name",
			value:         `1.%snapshot0`,
			position:      11,
			wantPlacement: capturePlacementMacroName,
			wantMacroName: "snapshot0",
		},
		{
			name:          "conditional body",
			value:         `%{?prerelease:0.}%{baserelease}`,
			position:      14,
			wantPlacement: capturePlacementConditionalBody,
			wantMacroName: "prerelease",
		},
		{
			name:          "macro name nested inside a conditional body",
			value:         `1%{?dev:%{dev0}}`,
			position:      13,
			wantPlacement: capturePlacementMacroName,
			wantMacroName: "dev0",
		},
		{
			name:          "digit inside an expression is literal",
			value:         `%[1 + %{azl_release}]%{?dist}`,
			position:      2,
			wantPlacement: capturePlacementLiteral,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			placement, construct := classifyCapture(testCase.position, parseMacroConstructs(testCase.value, 0))
			require.Equal(t, testCase.wantPlacement, placement)

			if testCase.wantMacroName != "" {
				assert.Equal(t, testCase.wantMacroName, testCase.value[construct.nameStart:construct.nameEnd])
			}
		})
	}
}

func TestParseSpecMacroCounterDefinition(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		rawLine     string
		directive   string
		macroName   string
		wantMatch   bool
		wantCounter string
	}{
		{
			name:        "global with leading whitespace",
			rawLine:     "  %global baserelease 007  ",
			directive:   "global",
			macroName:   "baserelease",
			wantMatch:   true,
			wantCounter: "007",
		},
		{
			name:        "define with tabs",
			rawLine:     "%define\tbaserelease\t42",
			directive:   "define",
			macroName:   "baserelease",
			wantMatch:   true,
			wantCounter: "42",
		},
		{
			name:      "directive prefix is not a match",
			rawLine:   "%globalx baserelease 1",
			directive: "global",
			macroName: "baserelease",
		},
		{
			name:      "macro name prefix is not a match",
			rawLine:   "%global baserelease_extra 1",
			directive: "global",
			macroName: "baserelease",
		},
		{
			name:        "empty body is still a definition",
			rawLine:     "%global baserelease",
			directive:   "global",
			macroName:   "baserelease",
			wantMatch:   true,
			wantCounter: "",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			definition, matches := parseSpecMacroCounterDefinition(
				testCase.rawLine, testCase.directive, testCase.macroName)
			assert.Equal(t, testCase.wantMatch, matches)

			if testCase.wantMatch {
				assert.Equal(t, testCase.wantCounter, definition.counterValue)
			}
		})
	}
}

func TestIncrementDecimalInteger(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		counter     string
		commitCount int
		want        string
		wantErr     bool
	}{
		{name: "leading zeros", counter: "007", commitCount: 3, want: "010"},
		{name: "zero increment", counter: "7", commitCount: 0, want: "7"},
		{name: "non-digits", counter: "7a", commitCount: 1, wantErr: true},
		{name: "negative increment", counter: "7", commitCount: -1, wantErr: true},
		{name: "uint64 overflow", counter: "18446744073709551615", commitCount: 1, wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := incrementDecimalInteger(testCase.counter, testCase.commitCount)
			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, result)
		})
	}
}

func TestIncrementCapturedCounterRejectsInvalidSpan(t *testing.T) {
	updatedValue, oldCounter, newCounter, err := incrementCapturedCounter("123", 2, 1, 1)
	require.Error(t, err)
	assert.Empty(t, updatedValue)
	assert.Empty(t, oldCounter)
	assert.Empty(t, newCounter)
}

func TestReplaceTagValue(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		rawLine  string
		oldValue string
		newValue string
		want     string
		wantErr  bool
	}{
		{
			name:     "preserves tag spacing and suffix",
			rawLine:  "Release:        007%{?dist}",
			oldValue: "007%{?dist}",
			newValue: "010%{?dist}",
			want:     "Release:        010%{?dist}",
		},
		{
			name:     "missing colon",
			rawLine:  "Release 1",
			oldValue: "1",
			newValue: "2",
			wantErr:  true,
		},
		{
			name:     "missing parsed value",
			rawLine:  "Release: 1",
			oldValue: "2",
			newValue: "3",
			wantErr:  true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := replaceTagValue(testCase.rawLine, testCase.oldValue, testCase.newValue)
			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, result)
		})
	}
}

// TestBumpReleaseCounterInSpecFile proves the exported bump wrapper rewrites the spec in place
// and reports the counter values, which the rebuild readiness check relies on.
func TestBumpReleaseCounterInSpecFile(t *testing.T) {
	memFS := afero.NewMemMapFs()
	writeTestSpecContent(t, memFS, "test-pkg",
		"Name: test-pkg\nVersion: 1\nRelease: 20%{?dist}\nSummary: Test\nLicense: MIT\n")

	specPath := filepath.Join(testSourcesDir, "test-pkg", "test-pkg.spec")

	oldValue, newValue, err := BumpReleaseCounterInSpecFile(
		memFS, DefaultReleaseCounter(), specPath, projectconfig.ComponentBuildConfig{}, 1)
	require.NoError(t, err)
	assert.Equal(t, "20", oldValue)
	assert.Equal(t, "21", newValue)

	updated, err := fileutils.ReadFile(memFS, specPath)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "Release: 21%{?dist}")
}

func TestBumpReleaseCounterInSpecFile_UnresolvableCounter(t *testing.T) {
	memFS := afero.NewMemMapFs()
	writeTestSpecContent(t, memFS, "test-pkg",
		"Name: test-pkg\nVersion: 1\nRelease: notacounter\nSummary: Test\nLicense: MIT\n")

	specPath := filepath.Join(testSourcesDir, "test-pkg", "test-pkg.spec")

	_, _, err := BumpReleaseCounterInSpecFile(
		memFS, DefaultReleaseCounter(), specPath, projectconfig.ComponentBuildConfig{}, 1)
	require.Error(t, err)
}
