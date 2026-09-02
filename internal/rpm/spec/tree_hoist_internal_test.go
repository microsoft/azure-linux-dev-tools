// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoveSectionsHoistsReferencedMacroClosure(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define root %{base}/tools",
		"%define base /usr/lib",
		"%define unused ignored",
		"%description tools",
		"%{root}",
		"%install",
		"install -d %{root}",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))

	assert.Equal(t, []string{
		"%define root %{base}/tools",
		"%define base /usr/lib",
		"%install",
		"install -d %{root}",
	}, specification.rawLines)
}

func TestRemoveSectionsHoistsMacroReferencedBySurvivingSectionHeader(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define suffix tools",
		"%description tools",
		"tools",
		"%package %{name}-%{suffix}",
		"%description %{name}-%{suffix}",
		"survives",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))

	assert.Equal(t, []string{
		"%define suffix tools",
		"%package %{name}-%{suffix}",
		"%description %{name}-%{suffix}",
		"survives",
	}, specification.rawLines)
}

func TestRemoveSectionsDoesNotTreatBuildSectionMarkerAsMacroReference(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define build(arg) %{arg}",
		"%description tools",
		"tools",
		"%build",
		"make",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%build",
		"make",
	}, specification.rawLines)
}

func TestRemoveSectionsDoesNotTreatInstallSectionMarkerAsMacroReference(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define install /usr/bin/install",
		"%description tools",
		"tools",
		"%install",
		"install -d %{buildroot}",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%install",
		"install -d %{buildroot}",
	}, specification.rawLines)
}

func TestRemoveSectionsHoistsMacroReferencedByPackageHeaderArguments(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define name replacement",
		"%define suffix extras",
		"%description tools",
		"tools",
		"%package -n %{name}-%{suffix}",
		"%description -n %{name}-%{suffix}",
		"survives",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%define name replacement",
		"%define suffix extras",
		"%package -n %{name}-%{suffix}",
		"%description -n %{name}-%{suffix}",
		"survives",
	}, specification.rawLines)
}

func TestRemoveSectionsHoistsMacroReferencedByFilesHeaderArguments(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define manifest files.list",
		"%description tools",
		"tools",
		"%files -f %{manifest}",
		"/usr/bin/tool",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%define manifest files.list",
		"%files -f %{manifest}",
		"/usr/bin/tool",
	}, specification.rawLines)
}

func TestRemoveSectionsHoistsMacroReferencedByTriggerHeaderArguments(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define trigger_target other-package",
		"%description tools",
		"tools",
		"%triggerin -- %{trigger_target}",
		"echo triggered",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%define trigger_target other-package",
		"%triggerin -- %{trigger_target}",
		"echo triggered",
	}, specification.rawLines)
}

func TestRemoveSectionsPreservesQemuIssue203Macro(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define qemu_target %{_arch}",
		"%description tools",
		"tools",
		"%install",
		"echo %{qemu_target}",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%define qemu_target %{_arch}",
		"%install",
		"echo %{qemu_target}",
	}, specification.rawLines)
}

func TestRemoveSectionsAtomicallyHoistsExpandMacroWithRawBraces(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package dates",
		"%define date_corpus %{expand:",
		"for date in 2024-02-29 2025-02-28; do",
		`  if { test "${date#????-??-??}" = "$date"; }; then`,
		"    %if 0",
		"    printf '%s\\n' %{date}",
		"    %endif",
		"  fi",
		"done",
		"}",
		"%description dates",
		"dates",
		"%install",
		"printf '%s\\n' %{date_corpus}",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("dates"))
	}))
	assert.Equal(t, []string{
		"%define date_corpus %{expand:",
		"for date in 2024-02-29 2025-02-28; do",
		`  if { test "${date#????-??-??}" = "$date"; }; then`,
		"    %if 0",
		"    printf '%s\\n' %{date}",
		"    %endif",
		"  fi",
		"done",
		"}",
		"%install",
		"printf '%s\\n' %{date_corpus}",
	}, specification.rawLines)
}

func TestRemoveSectionsIgnoresUnrelatedConditionalMacroDeclarations(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%if %{with kvm}",
		"%define kvm_package qemu-kvm",
		"%else",
		"%define kvm_package qemu-system",
		"%endif",
		"%package tests",
		"%define testsdir %{_libdir}/%{name}/tests-src",
		"%description tests",
		"tests",
		"%install",
		"install -d %{testsdir}",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tests"))
	}))
	assert.Equal(t, []string{
		"%if %{with kvm}",
		"%define kvm_package qemu-kvm",
		"%else",
		"%define kvm_package qemu-system",
		"%endif",
		"%define testsdir %{_libdir}/%{name}/tests-src",
		"%install",
		"install -d %{testsdir}",
	}, specification.rawLines)
}

func TestRemoveSectionsHoistsLaterEffectiveGlobalMacro(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%global toolsdir /usr/share/tools",
		"%package tools",
		"%global toolsdir %{_libdir}/tools",
		"%description tools",
		"tools",
		"%install",
		"install -d %{toolsdir}",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))

	assert.Equal(t, []string{
		"%global toolsdir /usr/share/tools",
		"%global toolsdir %{_libdir}/tools",
		"%install",
		"install -d %{toolsdir}",
	}, specification.rawLines)
}

func TestRemoveSectionsHoistsPriorGlobalUsedByLaterGlobal(t *testing.T) {
	tests := []struct {
		name     string
		lines    []string
		expected []string
	}{
		{
			name: "later global survives",
			lines: []string{
				"%package tools",
				"%global foo old",
				"%description tools",
				"tools",
				"%install",
				"%global foo %{?foo}-new",
				"echo %{foo}",
			},
			expected: []string{
				"%global foo old",
				"%install",
				"%global foo %{?foo}-new",
				"echo %{foo}",
			},
		},
		{
			name: "later global is selected",
			lines: []string{
				"%package tools",
				"%global foo old",
				"%global foo %{?foo}-new",
				"%description tools",
				"tools",
				"%install",
				"echo %{foo}",
			},
			expected: []string{
				"%global foo old",
				"%global foo %{?foo}-new",
				"%install",
				"echo %{foo}",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specification := newTreeAPISpec(test.lines)

			require.NoError(t, specification.mutateTree(func(tree *specTree) error {
				return tree.RemoveSections(tree.SectionsByPackage("tools"))
			}))

			assert.Equal(t, test.expected, specification.rawLines)
		})
	}
}

func TestRemoveSectionsHoistCycleTerminates(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define one %{two}",
		"%define two %{one}",
		"%description tools",
		"%{one}",
		"%install",
		"echo %{one}",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%define one %{two}",
		"%define two %{one}",
		"%install",
		"echo %{one}",
	}, specification.rawLines)
}

func TestRemoveSectionsHoistsLazyDependencyEffectiveAtSurvivingUse(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define root %{base}/tools",
		"%define base /usr/lib",
		"%description tools",
		"tools",
		"%install",
		"install -d %{root}",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%define root %{base}/tools",
		"%define base /usr/lib",
		"%install",
		"install -d %{root}",
	}, specification.rawLines)
}

func TestRemoveSectionsHoistsDependencyOfSurvivingLazyMacro(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%define root %{base}/tools",
		"%package tools",
		"%define base /usr/lib",
		"%description tools",
		"tools",
		"%install",
		"install -d %{root}",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%define root %{base}/tools",
		"%define base /usr/lib",
		"%install",
		"install -d %{root}",
	}, specification.rawLines)
}

func TestRemoveSectionsRejectsAmbiguousDependencyOfSurvivingLazyMacro(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%define root %{base}/tools",
		"%package tools",
		"%define base /usr/lib",
		"%description tools",
		"tools",
		"%install",
		"install -d %{root}",
		"%files tools",
		"%define base /opt/lib",
		"%check",
		"install -d %{root}",
	})
	before := append([]string(nil), specification.rawLines...)

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	})

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
	assert.Equal(t, before, specification.rawLines)
}

func TestRemoveSectionsRejectsDifferentRemovedLazyDependenciesAtSurvivingUses(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define root %{base}/tools",
		"%define base /usr/lib",
		"%description tools",
		"tools",
		"%install",
		"install -d %{root}",
		"%files tools",
		"%define base /opt/lib",
		"%check",
		"install -d %{root}",
	})
	before := append([]string(nil), specification.rawLines...)

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	})

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
	assert.Equal(t, before, specification.rawLines)
}

func TestRemoveSectionsRejectsRemovedLazyRootWithChangedDependencyBinding(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%define base old",
		"%package tools",
		"%define root %{base}",
		"%description tools",
		"tools",
		"%install",
		"echo %{root}",
		"%files tools",
		"%define base new",
		"%check",
		"echo %{root}",
	})
	before := append([]string(nil), specification.rawLines...)

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	})

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
	assert.Equal(t, before, specification.rawLines)
}

func TestRemoveSectionsHoistsLazyDependencyUsedAfterRemovedDeclaration(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%description tools",
		"tools",
		"%package other",
		"%define root %{base}/other",
		"%description other",
		"other",
		"%files tools",
		"%define base /usr/lib",
		"%check",
		"install -d %{root}",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%define base /usr/lib",
		"%package other",
		"%define root %{base}/other",
		"%description other",
		"other",
		"%check",
		"install -d %{root}",
	}, specification.rawLines)
}

func TestRemoveSectionsRejectsHoistingAcrossSurvivingSameNameDefinition(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%description tools",
		"tools",
		"%install",
		"%define location /usr/lib",
		"%files tools",
		"%define location /opt/lib",
		"%check",
		"echo %{location}",
	})
	before := append([]string(nil), specification.rawLines...)

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	})

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
	assert.Equal(t, before, specification.rawLines)
}

func TestRemoveSectionsHoistsWithoutCrossingSameNameDefinition(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define location /opt/lib",
		"%description tools",
		"tools",
		"%install",
		"echo %{location}",
		"%check",
		"%define location /usr/lib",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%define location /opt/lib",
		"%install",
		"echo %{location}",
		"%check",
		"%define location /usr/lib",
	}, specification.rawLines)
}

func TestRemoveSectionsRejectsUnsafeMacroHoistsWithoutMutation(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "eager global dependency declared later",
			lines: []string{
				"%package tools",
				"%global one %{two}",
				"%define two value",
				"%description tools",
				"%{one}",
				"%install",
				"echo %{one}",
			},
		},
		{
			name: "self-referential eager global",
			lines: []string{
				"%package tools",
				"%global one %{?one}",
				"%description tools",
				"%{one}",
				"%install",
				"echo %{one}",
			},
		},
		{
			name: "eager global dependency has ambiguous surviving definition",
			lines: []string{
				"%global toolsdir /usr/share/tools",
				"%package tools",
				"%global toolpath %{toolsdir}/bin",
				"%description tools",
				"%{toolpath}",
				"%install",
				"echo %{toolpath}",
			},
		},
		{
			name: "surviving undefine",
			lines: []string{
				"%package tools",
				"%define one value",
				"%description tools",
				"%{one}",
				"%install",
				"echo %{one}",
				"%undefine one",
			},
		},
		{
			name: "removed undefine",
			lines: []string{
				"%package tools",
				"%define one value",
				"%undefine one",
				"%description tools",
				"%{one}",
				"%install",
				"echo %{one}",
			},
		},
		{
			name: "parameterized declaration",
			lines: []string{
				"%package tools",
				"%define one(arg) %{arg}",
				"%description tools",
				"%one value",
				"%install",
				"echo %one value",
			},
		},
		{
			name: "conditional declaration",
			lines: []string{
				"%package tools",
				"%if 1",
				"%define one value",
				"%endif",
				"%description tools",
				"%{one}",
				"%install",
				"echo %{one}",
			},
		},
		{
			name: "Lua declaration",
			lines: []string{
				"%package tools",
				"%global one %{lua:print('value')}",
				"%description tools",
				"%{one}",
				"%install",
				"echo %{one}",
			},
		},
		{
			name: "different removed declarations effective at surviving references",
			lines: []string{
				"%package tools",
				"%define one first",
				"%description tools",
				"tools",
				"%install",
				"echo %{one}",
				"%files tools",
				"%define one second",
				"%check",
				"echo %{one}",
			},
		},
		{
			name: "conditional peer declaration",
			lines: []string{
				"%if 1",
				"%define one conditional",
				"%endif",
				"%package tools",
				"%define one removed",
				"%description tools",
				"tools",
				"%install",
				"echo %{one}",
			},
		},
		{
			name: "relocation crosses earlier surviving reference",
			lines: []string{
				"%if 1",
				"echo %{one}",
				"%package tools",
				"%define one value",
				"%description tools",
				"tools",
				"%endif",
				"%install",
				"echo %{one}",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specification := newTreeAPISpec(test.lines)
			before := append([]string(nil), specification.rawLines...)

			err := specification.mutateTree(func(tree *specTree) error {
				return tree.RemoveSections(tree.SectionsByPackage("tools"))
			})

			require.ErrorIs(t, err, ErrUnsafeMacroHoist)
			assert.Equal(t, before, specification.rawLines)
		})
	}
}

func TestRemoveSectionsHoistsMultilineMacroAndLogs(t *testing.T) {
	var logs bytes.Buffer

	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define path /usr \\",
		"  /share/tools",
		"%description tools",
		"%{path}",
		"%install",
		"echo %{path}",
	})
	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))

	assert.Equal(t, []string{
		"%define path /usr \\",
		"  /share/tools",
		"%install",
		"echo %{path}",
	}, specification.rawLines)
	assert.Contains(t, logs.String(), "Hoisting macro definition from removed section")
}

func TestMacroReferencesRecognizesDefinedAndUndefined(t *testing.T) {
	refs := macroReferences("%{defined feature} %{undefined missing} %{?optional} %bare")
	assert.Equal(t, map[string]bool{
		"feature":  true,
		"missing":  true,
		"optional": true,
		"bare":     true,
	}, refs)
}

func TestMacroReferencesRecognizesNestedAndArgumentReferences(t *testing.T) {
	refs := macroReferences("%{expand:%{dep}} %{helper arg}")
	assert.Equal(t, map[string]bool{
		"expand": true,
		"dep":    true,
		"helper": true,
	}, refs)
}

func TestRemoveSectionsHoistsNestedAndArgumentMacroReferences(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define dep /usr/lib",
		"%global expanded %{expand:%{dep}}",
		"%define helper value",
		"%description tools",
		"tools",
		"%install",
		"echo %{expanded} %{helper arg}",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))

	assert.Equal(t, []string{
		"%define dep /usr/lib",
		"%global expanded %{expand:%{dep}}",
		"%define helper value",
		"%install",
		"echo %{expanded} %{helper arg}",
	}, specification.rawLines)
}

func TestRemoveSectionsRejectsLazyDependencyMovedBeforeEarlierBinding(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%if 1",
		"%define root %{base}",
		"%define base old",
		"echo %{root}",
		"%package tools",
		"%define base new",
		"%description tools",
		"tools",
		"%endif",
		"%check",
		"echo %{root}",
	})
	before := append([]string(nil), specification.rawLines...)

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	})

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
	assert.Equal(t, before, specification.rawLines)
}

func TestValidateLazyDependencyBindingsChecksEveryInvocation(t *testing.T) {
	root := &macroDefinition{
		block: &block{Name: "root"},
		order: 1,
		refs:  map[string]bool{"base": true},
	}
	oldBase := &macroDefinition{block: &block{Name: "base"}, order: 2}
	newBase := &macroDefinition{block: &block{Name: "base"}, removed: true, order: 4}
	facts := &macroFacts{
		all: map[string][]*macroDefinition{
			"root": {root},
			"base": {oldBase, newBase},
		},
		references: []macroReference{
			{name: "root", order: 3},
			{name: "root", order: 5},
		},
	}

	err := validateLazyDependencyBindings(map[*macroDefinition]bool{newBase: true}, facts, 0)

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
}

func TestRemoveSectionsRejectsConditionalDeclarationWithoutEvaluatingIt(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%if 0",
		"%define base old",
		"%endif",
		"%define root %{base}",
		"%package tools",
		"%define base new",
		"%description tools",
		"tools",
		"%install",
		"echo %{root}",
	})
	before := append([]string(nil), specification.rawLines...)

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	})

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
	assert.Equal(t, before, specification.rawLines)
}

func TestRemoveSectionsRejectsConditionalDependencyChosenAtSurvivingUse(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define root %{base}",
		"%description tools",
		"tools",
		"%install",
		"%if %{with alternate}",
		"%define base first",
		"%else",
		"%define base second",
		"%endif",
		"%check",
		"echo %{root}",
	})
	before := append([]string(nil), specification.rawLines...)

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	})

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
	assert.Equal(t, before, specification.rawLines)
}

func TestRemoveSectionsRejectsSurvivingDynamicMacroNameMatchingRemovedDefinition(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%define suffix name",
		"%package tools",
		"%define dirname value",
		"%description tools",
		"tools",
		"%install",
		"echo %{dir%{suffix}}",
	})
	before := append([]string(nil), specification.rawLines...)

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	})

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
	assert.Equal(t, before, specification.rawLines)
}

func TestRemoveSectionsRejectsSurvivingLazyMacroDynamicReferenceMatchingRemovedDefinition(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%define suffix name",
		"%define root %{dir%{suffix}}",
		"%package tools",
		"%define dirname value",
		"%description tools",
		"tools",
		"%install",
		"echo %{root}",
	})
	before := append([]string(nil), specification.rawLines...)

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	})

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
	assert.Equal(t, before, specification.rawLines)
}

func TestRemoveSectionsAllowsUnrelatedDynamicMacroName(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%define suffix name",
		"%package tools",
		"%define unrelated value",
		"%description tools",
		"tools",
		"%install",
		"echo %{dir%{suffix}}",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%define suffix name",
		"%install",
		"echo %{dir%{suffix}}",
	}, specification.rawLines)
}

func TestMacroReferencesHonorsPercentEscapes(t *testing.T) {
	assert.Empty(t, macroReferences("%%{helper} %%helper %%%%{helper} %%%%helper"))
	assert.Equal(t, map[string]bool{"helper": true},
		macroReferences("%%%{helper} %%%helper %%%%%{helper} %%%%%helper"))
	assert.Equal(t, map[string]bool{"outer": true}, macroReferences("%{outer %%{helper}}"))
}

func TestRemoveSectionsDoesNotHoistEscapedMacroReferences(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define helper value",
		"%description tools",
		"tools",
		"%install",
		"echo %%{helper} %%helper",
	})

	require.NoError(t, specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	}))
	assert.Equal(t, []string{
		"%install",
		"echo %%{helper} %%helper",
	}, specification.rawLines)
}

func TestRemoveSectionsRejectsSelectedSelfReferentialGlobal(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%global toolsdir %{toolsdir}",
		"%description tools",
		"tools",
		"%install",
		"echo %{toolsdir}",
	})
	before := append([]string(nil), specification.rawLines...)

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	})

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
	assert.Contains(t, err.Error(), "toolsdir")
	assert.Equal(t, before, specification.rawLines)
}

func TestRemoveSectionsRejectsSelectedGlobalWithDynamicName(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define suffix name",
		"%define dirname value",
		"%global selected %{dir%{suffix}}",
		"%description tools",
		"tools",
		"%install",
		"echo %{selected}",
	})
	before := append([]string(nil), specification.rawLines...)

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	})

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
	assert.Contains(t, err.Error(), "dir%{suffix}")
	assert.Equal(t, before, specification.rawLines)
}

func TestRemoveSectionsRejectsSelectedGlobalDynamicNameMatchingSurvivingDefinition(t *testing.T) {
	specification := newTreeAPISpec([]string{
		"%package tools",
		"%define suffix name",
		"%global selected %{dir%{suffix}}",
		"%description tools",
		"tools",
		"%install",
		"%define dirname value",
		"echo %{selected}",
	})
	before := append([]string(nil), specification.rawLines...)

	err := specification.mutateTree(func(tree *specTree) error {
		return tree.RemoveSections(tree.SectionsByPackage("tools"))
	})

	require.ErrorIs(t, err, ErrUnsafeMacroHoist)
	assert.Contains(t, err.Error(), "dir%{suffix}")
	assert.Equal(t, before, specification.rawLines)
}
