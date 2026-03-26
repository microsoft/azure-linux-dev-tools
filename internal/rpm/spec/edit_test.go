// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetTag(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		expectedOutput  string
		expectedFailure bool
		packageName     string
		tag             string
		value           string
	}{
		{
			name:  "add tag to empty spec",
			input: "",
			expectedOutput: `Name: value
`,
			packageName: "",
			tag:         "Name",
			value:       "value",
		},
		{
			name: "add tag to non-empty spec",
			input: `OtherTag: other-value
`,
			expectedFailure: false,
			expectedOutput: `OtherTag: other-value
Name: value
`,
			packageName: "",
			tag:         "Name",
			value:       "value",
		},
		{
			name: "replace tag",
			input: `Name: old
`,
			expectedFailure: false,
			expectedOutput: `Name: value
`,
			packageName: "",
			tag:         "Name",
			value:       "value",
		},
		{
			name: "replace differently-cased tag",
			input: `nAmE: old
`,
			expectedFailure: false,
			expectedOutput: `Name: value
`,
			packageName: "",
			tag:         "Name",
			value:       "value",
		},
		{
			name: "ignoring comments",
			input: `# Name: old
`,
			expectedFailure: false,
			expectedOutput: `# Name: old
Name: value
`,
			packageName: "",
			tag:         "Name",
			value:       "value",
		},
		{
			name: "add tag to existing package",
			input: `Name: other-package
%package -n test-package
`,
			expectedFailure: false,
			expectedOutput: `Name: other-package
%package -n test-package
Name: value
`,
			packageName: "test-package",
			tag:         "Name",
			value:       "value",
		},
		{
			name: "ignoring comments",
			input: `# Name: old
`,
			expectedFailure: false,
			expectedOutput: `# Name: old
Name: value
`,
			packageName: "",
			tag:         "Name",
			value:       "value",
		},
		{
			name: "replace tag in existing package",
			input: `Name: other-package
%package -n test-package
Name: old
`,
			expectedFailure: false,
			expectedOutput: `Name: other-package
%package -n test-package
Name: value
`,
			packageName: "test-package",
			tag:         "Name",
			value:       "value",
		},
		{
			name: "add tag to non-existing package",
			input: `Name: main-package
`,
			expectedFailure: true,
			packageName:     "non-existing-package",
			tag:             "Name",
			value:           "value",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specFile, err := spec.OpenSpec(strings.NewReader(test.input))
			require.NoError(t, err)

			err = specFile.SetTag(test.packageName, test.tag, test.value)
			if test.expectedFailure {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			actualOutput := new(bytes.Buffer)

			err = specFile.Serialize(actualOutput)
			require.NoError(t, err)

			assert.Equal(t, test.expectedOutput, actualOutput.String())
		})
	}
}

func TestUpdateTag(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		expectedOutput  string
		expectedFailure bool
		packageName     string
		tag             string
		value           string
	}{
		{
			name:            "empty spec",
			input:           "",
			expectedFailure: true,
			tag:             "Name",
			value:           "value",
		},
		{
			name: "update missing tag",
			input: `OtherTag: other-value
`,
			expectedFailure: true,
			packageName:     "",
			tag:             "Name",
			value:           "value",
		},
		{
			name: "replace tag",
			input: `Name: old
`,
			expectedFailure: false,
			expectedOutput: `Name: value
`,
			packageName: "",
			tag:         "Name",
			value:       "value",
		},
		{
			name: "replace differently-cased tag",
			input: `nAmE: old
`,
			expectedFailure: false,
			expectedOutput: `Name: value
`,
			packageName: "",
			tag:         "Name",
			value:       "value",
		},
		{
			name: "missing tag in existing package",
			input: `Name: other-package
%package -n test-package
`,
			expectedFailure: true,
			packageName:     "test-package",
			tag:             "Name",
			value:           "value",
		},
		{
			name: "replace tag in existing package",
			input: `Name: other-package
%package -n test-package
Name: old
`,
			expectedFailure: false,
			expectedOutput: `Name: other-package
%package -n test-package
Name: value
`,
			packageName: "test-package",
			tag:         "Name",
			value:       "value",
		},
		{
			name: "tag in non-existing package",
			input: `Name: main-package
`,
			expectedFailure: true,
			packageName:     "non-existing-package",
			tag:             "Name",
			value:           "value",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specFile, err := spec.OpenSpec(strings.NewReader(test.input))
			require.NoError(t, err)

			err = specFile.UpdateExistingTag(test.packageName, test.tag, test.value)
			if test.expectedFailure {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			actualOutput := new(bytes.Buffer)

			err = specFile.Serialize(actualOutput)
			require.NoError(t, err)

			assert.Equal(t, test.expectedOutput, actualOutput.String())
		})
	}
}

func TestRemoveTag(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		expectedOutput  string
		expectedFailure bool
		packageName     string
		tag             string
		value           string
	}{
		{
			name:            "remove tag from empty spec",
			input:           "",
			expectedFailure: true,
			tag:             "Name",
			value:           "value",
		},
		{
			name: "missing tag",
			input: `OtherTag: other-value
`,
			expectedFailure: true,
			tag:             "Name",
			value:           "",
		},
		{
			name: "existing tag without specified value",
			input: `Name: old
`,
			expectedFailure: false,
			expectedOutput:  "",
			packageName:     "",
			tag:             "Name",
			value:           "",
		},
		{
			name: "existing tag with same value",
			input: `Name: old
`,
			expectedFailure: false,
			expectedOutput:  "",
			packageName:     "",
			tag:             "Name",
			value:           "old",
		},
		{
			name: "existing tag with differently-cased value",
			input: `Name: oLd
`,
			expectedFailure: false,
			expectedOutput:  "",
			packageName:     "",
			tag:             "Name",
			value:           "old",
		},
		{
			name: "existing tag with different value",
			input: `Name: different
`,
			expectedFailure: true,
			tag:             "Name",
			value:           "old",
		},
		{
			name: "differently-cased tag",
			input: `nAmE: old
`,
			expectedFailure: false,
			expectedOutput:  "",
			packageName:     "",
			tag:             "Name",
			value:           "old",
		},
		{
			name: "ignoring comments",
			input: `# Name: old
Name: other-old
`,
			expectedFailure: false,
			expectedOutput: `# Name: old
`,
			packageName: "",
			tag:         "Name",
			value:       "",
		},
		{
			name: "missing tag in existing package",
			input: `Name: other-package
%package -n test-package
`,
			expectedFailure: true,
			packageName:     "test-package",
			tag:             "Name",
			value:           "",
		},
		{
			name: "tag in existing package",
			input: `Name: other-package
%package -n test-package
Name: old
`,
			expectedFailure: false,
			expectedOutput: `Name: other-package
%package -n test-package
`,
			packageName: "test-package",
			tag:         "Name",
			value:       "",
		},
		{
			name: "tag in non-existing package",
			input: `Name: main-package
`,
			expectedFailure: true,
			packageName:     "non-existing-package",
			tag:             "Name",
			value:           "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specFile, err := spec.OpenSpec(strings.NewReader(test.input))
			require.NoError(t, err)

			err = specFile.RemoveTag(test.packageName, test.tag, test.value)
			if test.expectedFailure {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			actualOutput := new(bytes.Buffer)

			err = specFile.Serialize(actualOutput)
			require.NoError(t, err)

			assert.Equal(t, test.expectedOutput, actualOutput.String())
		})
	}
}

func TestAddTag(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		expectedOutput  string
		expectedFailure bool
		packageName     string
		tag             string
		value           string
	}{
		{
			name:  "add tag to empty spec",
			input: "",
			expectedOutput: `BuildRequires: value
`,
			tag:   "BuildRequires",
			value: "value",
		},
		{
			name: "add tag to non-empty spec",
			input: `OtherTag: other-value
`,
			expectedFailure: false,
			expectedOutput: `OtherTag: other-value
BuildRequires: value
`,
			packageName: "",
			tag:         "BuildRequires",
			value:       "value",
		},
		{
			name: "existing tag",
			input: `BuildRequires: old
`,
			expectedFailure: false,
			expectedOutput: `BuildRequires: old
BuildRequires: value
`,
			packageName: "",
			tag:         "BuildRequires",
			value:       "value",
		},
		{
			name: "add tag to existing package",
			input: `BuildRequires: other-package
%package -n test-package
`,
			expectedFailure: false,
			expectedOutput: `BuildRequires: other-package
%package -n test-package
BuildRequires: value
`,
			packageName: "test-package",
			tag:         "BuildRequires",
			value:       "value",
		},
		{
			name: "existing tag in existing package",
			input: `BuildRequires: other-package

%description
Some description

%package -n test-package
BuildRequires: old
`,
			expectedFailure: false,
			expectedOutput: `BuildRequires: other-package

%description
Some description

%package -n test-package
BuildRequires: old
BuildRequires: value
`,
			packageName: "test-package",
			tag:         "BuildRequires",
			value:       "value",
		},
		{
			name: "add tag to non-existing package",
			input: `BuildRequires: main-package
`,
			expectedFailure: true,
			packageName:     "non-existing-package",
			tag:             "BuildRequires",
			value:           "value",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specFile, err := spec.OpenSpec(strings.NewReader(test.input))
			require.NoError(t, err)

			err = specFile.AddTag(test.packageName, test.tag, test.value)
			if test.expectedFailure {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			actualOutput := new(bytes.Buffer)

			err = specFile.Serialize(actualOutput)
			require.NoError(t, err)

			assert.Equal(t, test.expectedOutput, actualOutput.String())
		})
	}
}

func TestInsertTag(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		expectedOutput  string
		expectedFailure bool
		packageName     string
		tag             string
		value           string
	}{
		{
			name:  "insert into empty spec",
			input: "",
			expectedOutput: `Source9999: macros.azl.macros
`,
			tag:   "Source9999",
			value: "macros.azl.macros",
		},
		{
			name: "insert after single Source0",
			input: `Name: test
Source0: test-1.0.tar.gz
BuildRequires: gcc
`,
			expectedOutput: `Name: test
Source0: test-1.0.tar.gz
Source9999: macros.azl.macros
BuildRequires: gcc
`,
			tag:   "Source9999",
			value: "macros.azl.macros",
		},
		{
			name: "insert after last of multiple Sources",
			input: `Name: test
Source0: test-1.0.tar.gz
Source1: extra.tar.gz
BuildRequires: gcc
`,
			expectedOutput: `Name: test
Source0: test-1.0.tar.gz
Source1: extra.tar.gz
Source9999: macros.azl.macros
BuildRequires: gcc
`,
			tag:   "Source9999",
			value: "macros.azl.macros",
		},
		{
			name: "insert after unnumbered Source tag",
			input: `Name: test
Source: test-1.0.tar.gz
BuildRequires: gcc
`,
			expectedOutput: `Name: test
Source: test-1.0.tar.gz
Source9999: macros.azl.macros
BuildRequires: gcc
`,
			tag:   "Source9999",
			value: "macros.azl.macros",
		},
		{
			name: "insert before macros like fontpkg",
			input: `Name: test
Source0: test-1.0.tar.gz
BuildRequires: gcc

%fontpkg -a
`,
			expectedOutput: `Name: test
Source0: test-1.0.tar.gz
Source9999: macros.azl.macros
BuildRequires: gcc

%fontpkg -a
`,
			tag:   "Source9999",
			value: "macros.azl.macros",
		},
		{
			name: "insert before conditional block",
			input: `Name: test
Source0: test-1.0.tar.gz
BuildRequires: gcc

%if "%{name}" != "autoconf"
Requires: autoconf
%endif
`,
			expectedOutput: `Name: test
Source0: test-1.0.tar.gz
Source9999: macros.azl.macros
BuildRequires: gcc

%if "%{name}" != "autoconf"
Requires: autoconf
%endif
`,
			tag:   "Source9999",
			value: "macros.azl.macros",
		},
		{
			name: "no family match falls back to last tag",
			input: `Name: test
BuildRequires: gcc
`,
			expectedOutput: `Name: test
BuildRequires: gcc
Vendor: Microsoft
`,
			tag:   "Vendor",
			value: "Microsoft",
		},
		{
			name: "insert Patch after existing Patches",
			input: `Name: test
Source0: test-1.0.tar.gz
Patch0: fix.patch
Patch1: another.patch
BuildRequires: gcc
`,
			expectedOutput: `Name: test
Source0: test-1.0.tar.gz
Patch0: fix.patch
Patch1: another.patch
Patch99: new.patch
BuildRequires: gcc
`,
			tag:   "Patch99",
			value: "new.patch",
		},
		{
			name: "insert into sub-package",
			input: `Name: main
Source0: main.tar.gz

%description
Main package

%package -n test-package
Summary: Test sub-package
`,
			expectedOutput: `Name: main
Source0: main.tar.gz

%description
Main package

%package -n test-package
Summary: Test sub-package
Requires: foo
`,
			packageName: "test-package",
			tag:         "Requires",
			value:       "foo",
		},
		{
			name: "insert into non-existing package fails",
			input: `Name: main
`,
			expectedFailure: true,
			packageName:     "non-existing-package",
			tag:             "Requires",
			value:           "foo",
		},
		{
			name: "insert after Source before description section",
			input: `Name: test
Source0: test-1.0.tar.gz

%description
Some description
`,
			expectedOutput: `Name: test
Source0: test-1.0.tar.gz
Source9999: macros.azl.macros

%description
Some description
`,
			tag:   "Source9999",
			value: "macros.azl.macros",
		},
		{
			name: "insert after Source inside conditional block",
			input: `Name: test
Source0: test-1.0.tar.gz
%if %{with extra}
Source31: extra-aarch64.h
Source32: extra-x86_64.h
%endif
BuildRequires: gcc
`,
			expectedOutput: `Name: test
Source0: test-1.0.tar.gz
%if %{with extra}
Source31: extra-aarch64.h
Source32: extra-x86_64.h
%endif
Source9999: macros.azl.macros
BuildRequires: gcc
`,
			tag:   "Source9999",
			value: "macros.azl.macros",
		},
		{
			name: "insert after Source inside nested conditional",
			input: `Name: test
Source0: test-1.0.tar.gz
%if %{with jit}
%ifarch x86_64
Source31: jit-x86.h
%endif
Source32: jit-common.h
%endif
BuildRequires: gcc
`,
			expectedOutput: `Name: test
Source0: test-1.0.tar.gz
%if %{with jit}
%ifarch x86_64
Source31: jit-x86.h
%endif
Source32: jit-common.h
%endif
Source9999: macros.azl.macros
BuildRequires: gcc
`,
			tag:   "Source9999",
			value: "macros.azl.macros",
		},
		{
			name: "insert with mixed conditional and unconditional sources",
			input: `Name: test
Source0: test-1.0.tar.gz
Source1: extra.tar.gz
%if %{with jit}
Source31: jit-aarch64.h
Source32: jit-x86_64.h
%endif
BuildRequires: gcc
`,
			expectedOutput: `Name: test
Source0: test-1.0.tar.gz
Source1: extra.tar.gz
%if %{with jit}
Source31: jit-aarch64.h
Source32: jit-x86_64.h
%endif
Source9999: macros.azl.macros
BuildRequires: gcc
`,
			tag:   "Source9999",
			value: "macros.azl.macros",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			specFile, err := spec.OpenSpec(strings.NewReader(test.input))
			require.NoError(t, err)

			err = specFile.InsertTag(test.packageName, test.tag, test.value)
			if test.expectedFailure {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			actualOutput := new(bytes.Buffer)

			err = specFile.Serialize(actualOutput)
			require.NoError(t, err)

			assert.Equal(t, test.expectedOutput, actualOutput.String())
		})
	}
}

func TestSearchAndReplace(t *testing.T) {
	t.Run("globally replace", func(t *testing.T) {
		input := `
	Name: test

	%build
	build.sh --vendor=contoso
`

		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		expected := strings.ReplaceAll(input, "contoso", "azl")

		err = specFile.SearchAndReplace("", "", `vendor=contoso`, "vendor=azl")
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		require.Equal(t, expected, actual.String())
	})

	t.Run("no match", func(t *testing.T) {
		input := `
	Name: test

	%build
	build.sh --vendor=contoso
	`

		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.SearchAndReplace("", "", `vendor=non-existent`, "vendor=azl")
		require.Error(t, err)
	})

	t.Run("replace in specific section", func(t *testing.T) {
		input := `
	Name: test
	Vendor: contoso

	%description
	Do not replace this contoso

	%description -n subpackage
	Something about contoso
`

		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		expected := strings.ReplaceAll(input, "Something about contoso", "Something about azl")

		err = specFile.SearchAndReplace("%description", "subpackage", "contoso", "azl")
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		require.Equal(t, expected, actual.String())
	})
}

func TestAddChangelogEntry(t *testing.T) {
	const (
		testUser    = "Test User"
		testEmail   = "user@example.com"
		testVersion = "1.0.0"
		testRelease = "1"
	)

	testTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("no changelog section", func(t *testing.T) {
		const input = `
Name: test
`

		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.AddChangelogEntry(testUser, testEmail, testVersion, testRelease, testTime, []string{"Initial release"})
		require.Error(t, err)
	})

	t.Run("empty changelog section", func(t *testing.T) {
		const input = `
Name: test

%changelog
`

		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.AddChangelogEntry(
			testUser, testEmail, testVersion, testRelease,
			testTime, []string{"Initial release", "Something else"},
		)
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		assert.Equal(t, `
Name: test

%changelog
* Wed Jan 01 2025 Test User <user@example.com> - 1.0.0-1
- Initial release
- Something else

`, actual.String())
	})

	t.Run("existing changelog section", func(t *testing.T) {
		input := `
Name: test

%changelog
* Wed Jan 01 2000 Test User <user@example.com> - 0.0.1-1
- Initial release
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.AddChangelogEntry(testUser, testEmail, testVersion, testRelease, testTime, []string{"Update"})
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		assert.Equal(t, `
Name: test

%changelog
* Wed Jan 01 2025 Test User <user@example.com> - 1.0.0-1
- Update

* Wed Jan 01 2000 Test User <user@example.com> - 0.0.1-1
- Initial release
`, actual.String())
	})
}

func TestPrependLinesToSection(t *testing.T) {
	t.Run("empty spec", func(t *testing.T) {
		input := ""
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.PrependLinesToSection("", "", []string{"New line", "Next line"})
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		assert.Equal(t, `New line
Next line
`, actual.String())
	})

	t.Run("global section", func(t *testing.T) {
		input := `Name: test
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.PrependLinesToSection("", "", []string{"New line", "Next line"})
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		assert.Equal(t, `New line
Next line
Name: test
`, actual.String())
	})

	t.Run("existing section", func(t *testing.T) {
		input := `
Name: test

%description -n foo
This is a test package.

%description -n other
This is another package.

%build
build.sh
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.PrependLinesToSection("%description", "foo", []string{"New line", "Next line"})
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		assert.Equal(t, `
Name: test

%description -n foo
New line
Next line
This is a test package.

%description -n other
This is another package.

%build
build.sh
`, actual.String())
	})

	t.Run("no such section", func(t *testing.T) {
		input := `
Name: test
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.PrependLinesToSection("%description", "", []string{"New line"})
		require.Error(t, err)
	})
}

func TestAppendLinesToSection(t *testing.T) {
	t.Run("existing section", func(t *testing.T) {
		input := `
Name: test

%description -n foo
This is a test package.

%description -n other
This is another package.

%build
build.sh
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.AppendLinesToSection("%description", "foo", []string{"New line", "Next line"})
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		assert.Equal(t, `
Name: test

%description -n foo
This is a test package.

New line
Next line
%description -n other
This is another package.

%build
build.sh
`, actual.String())
	})

	t.Run("no such section", func(t *testing.T) {
		input := `
Name: test
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.AppendLinesToSection("%description", "", []string{"New line"})
		require.Error(t, err)
	})
}

func TestHasSection(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		sectionName string
		expected    bool
	}{
		{
			name:        "section exists",
			input:       "Name: test\n\n%build\nmake\n",
			sectionName: "%build",
			expected:    true,
		},
		{
			name:        "section does not exist",
			input:       "Name: test\n\n%build\nmake\n",
			sectionName: "%patchlist",
			expected:    false,
		},
		{
			name:        "preamble section",
			input:       "Name: test\n",
			sectionName: "",
			expected:    true,
		},
		{
			name:        "patchlist section exists",
			input:       "Name: test\n\n%patchlist\nfix.patch\n",
			sectionName: "%patchlist",
			expected:    true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			specFile, err := spec.OpenSpec(strings.NewReader(testCase.input))
			require.NoError(t, err)

			result, err := specFile.HasSection(testCase.sectionName)
			require.NoError(t, err)
			assert.Equal(t, testCase.expected, result)
		})
	}
}

func TestGetHighestPatchTagNumber(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "no patch tags",
			input:    "Name: test\nVersion: 1.0\n",
			expected: -1,
		},
		{
			name:     "single patch tag",
			input:    "Name: test\nPatch0: fix.patch\n",
			expected: 0,
		},
		{
			name:     "multiple contiguous patch tags",
			input:    "Name: test\nPatch0: fix0.patch\nPatch1: fix1.patch\nPatch2: fix2.patch\n",
			expected: 2,
		},
		{
			name:     "non-contiguous patch tags",
			input:    "Name: test\nPatch0: fix0.patch\nPatch5: fix5.patch\nPatch3: fix3.patch\n",
			expected: 5,
		},
		{
			name:     "case insensitive tag names",
			input:    "Name: test\npatch0: fix0.patch\nPATCH1: fix1.patch\n",
			expected: 1,
		},
		{
			name:     "scans across all packages",
			input:    "Name: test\nPatch0: main.patch\n\n%package devel\nPatch5: devel.patch\n",
			expected: 5,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			specFile, err := spec.OpenSpec(strings.NewReader(testCase.input))
			require.NoError(t, err)

			result, err := specFile.GetHighestPatchTagNumber()
			require.NoError(t, err)
			assert.Equal(t, testCase.expected, result)
		})
	}
}

func TestRemoveTagsMatching(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		packageName    string
		matcher        func(tag, value string) bool
		expectedOutput string
		expectedCount  int
	}{
		{
			name:  "exact tag and value match",
			input: "Name: test\nBuildRequires: foo\nBuildRequires: bar\n",
			matcher: func(tag, value string) bool {
				return strings.EqualFold(tag, "BuildRequires") && value == "foo"
			},
			expectedOutput: "Name: test\nBuildRequires: bar\n",
			expectedCount:  1,
		},
		{
			name:  "patch tag exact match",
			input: "Name: test\nPatch0: fix-foo.patch\nPatch1: fix-bar.patch\nPatch2: add-feature.patch\n",
			matcher: func(tag, value string) bool {
				if _, ok := spec.ParsePatchTagNumber(tag); !ok {
					return false
				}

				return value == "fix-foo.patch"
			},
			expectedOutput: "Name: test\nPatch1: fix-bar.patch\nPatch2: add-feature.patch\n",
			expectedCount:  1,
		},
		{
			name:  "no matches returns zero",
			input: "Name: test\nPatch0: fix-foo.patch\n",
			matcher: func(_, value string) bool {
				return value == "nonexistent.patch"
			},
			expectedOutput: "Name: test\nPatch0: fix-foo.patch\n",
			expectedCount:  0,
		},
		{
			name:  "non-patch tags unaffected by patch matcher",
			input: "Name: test\nSource0: fix-foo.patch\nPatch0: fix-foo.patch\n",
			matcher: func(tag, value string) bool {
				if _, ok := spec.ParsePatchTagNumber(tag); !ok {
					return false
				}

				return value == "fix-foo.patch"
			},
			expectedOutput: "Name: test\nSource0: fix-foo.patch\n",
			expectedCount:  1,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			specFile, err := spec.OpenSpec(strings.NewReader(testCase.input))
			require.NoError(t, err)

			count, err := specFile.RemoveTagsMatching(testCase.packageName, testCase.matcher)
			require.NoError(t, err)
			assert.Equal(t, testCase.expectedCount, count)

			var buf bytes.Buffer
			require.NoError(t, specFile.Serialize(&buf))
			assert.Equal(t, testCase.expectedOutput, buf.String())
		})
	}
}

func TestRemovePatchEntry(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		pattern         string
		expectedOutput  string
		expectedFailure bool
		errorContains   string
	}{
		{
			name:           "removes literal patch from PatchN tags",
			input:          "Name: test\nPatch0: keep.patch\nPatch1: remove-me.patch\nPatch2: also-keep.patch\n",
			pattern:        "remove-me.patch",
			expectedOutput: "Name: test\nPatch0: keep.patch\nPatch2: also-keep.patch\n",
		},
		{
			name:           "removes patches matching glob from PatchN tags",
			input:          "Name: test\nPatch0: CVE-001.patch\nPatch1: fix-build.patch\nPatch2: CVE-002.patch\n",
			pattern:        "CVE-*.patch",
			expectedOutput: "Name: test\nPatch1: fix-build.patch\n",
		},
		{
			name:           "removes literal patch from patchlist",
			input:          "Name: test\n\n%patchlist\nkeep.patch\nremove-me.patch\n\n%build\nmake\n",
			pattern:        "remove-me.patch",
			expectedOutput: "Name: test\n\n%patchlist\nkeep.patch\n\n%build\nmake\n",
		},
		{
			name:           "removes patches matching glob from patchlist",
			input:          "Name: test\n\n%patchlist\nCVE-001.patch\nfix-build.patch\nCVE-002.patch\n\n%build\nmake\n",
			pattern:        "CVE-*.patch",
			expectedOutput: "Name: test\n\n%patchlist\nfix-build.patch\n\n%build\nmake\n",
		},
		{
			name:           "removes from both PatchN tags and patchlist",
			input:          "Name: test\nPatch0: fix-a.patch\n\n%patchlist\nfix-a.patch\n\n%build\nmake\n",
			pattern:        "fix-a.patch",
			expectedOutput: "Name: test\n\n%patchlist\n\n%build\nmake\n",
		},
		{
			name:            "returns error when no patches match literal",
			input:           "Name: test\nPatch0: keep.patch\n",
			pattern:         "nonexistent.patch",
			expectedFailure: true,
			errorContains:   "no patches matching",
		},
		{
			name:            "returns error when no patches match glob",
			input:           "Name: test\nPatch0: fix-build.patch\n",
			pattern:         "CVE-*.patch",
			expectedFailure: true,
			errorContains:   "no patches matching",
		},
		{
			name:           "glob star matches all patches",
			input:          "Name: test\nPatch0: a.patch\nPatch1: b.patch\nPatch2: c.patch\n",
			pattern:        "*.patch",
			expectedOutput: "Name: test\n",
		},
		{
			name: "removes matching patches across all packages",
			input: "Name: test\nPatch0: CVE-001.patch\nPatch1: keep.patch\n\n%package devel\n" +
				"Summary: Dev\nPatch2: CVE-002.patch\nPatch3: also-keep.patch\n",
			pattern:        "CVE-*.patch",
			expectedOutput: "Name: test\nPatch1: keep.patch\n\n%package devel\nSummary: Dev\nPatch3: also-keep.patch\n",
		},
		{
			name:            "no match across multiple packages returns error",
			input:           "Name: test\nPatch0: keep.patch\n\n%package devel\nSummary: Dev\nPatch1: also-keep.patch\n",
			pattern:         "nonexistent.patch",
			expectedFailure: true,
			errorContains:   "no patches matching",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			specFile, err := spec.OpenSpec(strings.NewReader(testCase.input))
			require.NoError(t, err)

			err = specFile.RemovePatchEntry(testCase.pattern)
			if testCase.expectedFailure {
				require.Error(t, err)

				if testCase.errorContains != "" {
					assert.Contains(t, err.Error(), testCase.errorContains)
				}

				return
			}

			require.NoError(t, err)

			var buf bytes.Buffer
			require.NoError(t, specFile.Serialize(&buf))
			assert.Equal(t, testCase.expectedOutput, buf.String())
		})
	}
}

func TestParsePatchTagNumber(t *testing.T) {
	tests := []struct {
		tag         string
		expectedNum int
		expectedOK  bool
	}{
		{"Patch0", 0, true},
		{"Patch1", 1, true},
		{"Patch99", 99, true},
		{"patch0", 0, true},
		{"PATCH5", 5, true},
		{"Patch", -1, false},
		{"PatchFoo", -1, false},
		{"Source0", -1, false},
		{"Name", -1, false},
		{"", -1, false},
	}

	for _, testCase := range tests {
		t.Run(testCase.tag, func(t *testing.T) {
			num, ok := spec.ParsePatchTagNumber(testCase.tag)
			assert.Equal(t, testCase.expectedNum, num)
			assert.Equal(t, testCase.expectedOK, ok)
		})
	}
}

func TestVisitTags(t *testing.T) {
	input := `Name: main-pkg
Version: 1.0
Patch0: main.patch

%package devel
Summary: Development files
Patch1: devel.patch

%package -n other
Summary: Other package
Patch2: other.patch
`

	tests := []struct {
		name         string
		expectedTags []string
	}{
		{
			name:         "visits tags across all packages",
			expectedTags: []string{"Name", "Version", "Patch0", "Summary", "Patch1", "Summary", "Patch2"},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			sf, err := spec.OpenSpec(strings.NewReader(input))
			require.NoError(t, err)

			var tags []string

			err = sf.VisitTags(func(tagLine *spec.TagLine, _ *spec.Context) error {
				tags = append(tags, tagLine.Tag)

				return nil
			})
			require.NoError(t, err)
			assert.Equal(t, testCase.expectedTags, tags)
		})
	}
}

func TestVisitTagsPackage(t *testing.T) {
	input := `Name: main-pkg
Version: 1.0
Patch0: main.patch

%package devel
Summary: Development files
Patch1: devel.patch

%package -n other
Summary: Other package
Patch2: other.patch
`

	tests := []struct {
		name         string
		packageName  string
		expectedTags []string
	}{
		{
			name:         "global package only",
			packageName:  "",
			expectedTags: []string{"Name", "Version", "Patch0"},
		},
		{
			name:         "devel sub-package only",
			packageName:  "devel",
			expectedTags: []string{"Summary", "Patch1"},
		},
		{
			name:         "other sub-package only",
			packageName:  "other",
			expectedTags: []string{"Summary", "Patch2"},
		},
		{
			name:         "non-existing package returns no tags",
			packageName:  "nonexistent",
			expectedTags: nil,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			sf, err := spec.OpenSpec(strings.NewReader(input))
			require.NoError(t, err)

			var tags []string

			err = sf.VisitTagsPackage(testCase.packageName, func(tagLine *spec.TagLine, _ *spec.Context) error {
				tags = append(tags, tagLine.Tag)

				return nil
			})
			require.NoError(t, err)
			assert.Equal(t, testCase.expectedTags, tags)
		})
	}
}

func TestRemoveSection(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		sectionName    string
		packageName    string
		expectedOutput string
		errorExpected  bool
		errorContains  string
	}{
		{
			name: "removes section with no package qualifier",
			input: `Name: test
Version: 1.0

%generate_buildrequires
%cargo_generate_buildrequires

%build
make
`,
			sectionName: "%generate_buildrequires",
			expectedOutput: `Name: test
Version: 1.0

%build
make
`,
		},
		{
			name: "removes section with package qualifier",
			input: `Name: test

%files
/usr/bin/test

%files devel
/usr/include/test.h

%files libs
/usr/lib/libtest.so
`,
			sectionName: "%files",
			packageName: "devel",
			expectedOutput: `Name: test

%files
/usr/bin/test

%files libs
/usr/lib/libtest.so
`,
		},
		{
			name: "removes section at end of file",
			input: `Name: test

%build
make

%check
make check
`,
			sectionName: "%check",
			expectedOutput: `Name: test

%build
make

`,
		},
		{
			name: "fails when section does not exist",
			input: `Name: test
Version: 1.0

%build
make
`,
			sectionName:   "%check",
			errorExpected: true,
			errorContains: "not found",
		},
		{
			name: "rejects removing global section",
			input: `Name: test
Version: 1.0
`,
			sectionName:   "",
			errorExpected: true,
			errorContains: "global",
		},
		{
			name: "removes section with -n package syntax",
			input: `Name: test

%files
/usr/bin/test

%files -n other-pkg
/usr/bin/other

%build
make
`,
			sectionName: "%files",
			packageName: "other-pkg",
			expectedOutput: `Name: test

%files
/usr/bin/test

%build
make
`,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			specFile, err := spec.OpenSpec(strings.NewReader(testCase.input))
			require.NoError(t, err)

			err = specFile.RemoveSection(testCase.sectionName, testCase.packageName)

			if testCase.errorExpected {
				require.Error(t, err)

				if testCase.errorContains != "" {
					assert.Contains(t, err.Error(), testCase.errorContains)
				}

				return
			}

			require.NoError(t, err)

			var buf bytes.Buffer
			require.NoError(t, specFile.Serialize(&buf))
			assert.Equal(t, testCase.expectedOutput, buf.String())
		})
	}
}
