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

	t.Run("replace in macro definition", func(t *testing.T) {
		input := `
%global with_doc 1
Name: test

%build
make
`

		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		expected := strings.ReplaceAll(input, "%global with_doc 1", "%global with_doc 0")

		err = specFile.SearchAndReplace("", "", `%global with_doc 1`, "%global with_doc 0")
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		require.Equal(t, expected, actual.String())
	})

	t.Run("replace in conditional header", func(t *testing.T) {
		input := `
Name: test

%if 0%{?fedora}
BuildRequires: fedora-only
%endif

%build
make
`

		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		expected := strings.ReplaceAll(input, "%if 0%{?fedora}", "%if 0%{?rhel}")

		err = specFile.SearchAndReplace("", "", `^%if 0%\{\?fedora\}$`, "%if 0%{?rhel}")
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		require.Equal(t, expected, actual.String())
	})

	t.Run("replace in multi-line macro definition", func(t *testing.T) {
		input := `
%global cfg_content --gcc-triple=%{_target_cpu}-redhat-linux \
  --extra-flag=old
Name: test

%build
make
`

		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		expected := strings.ReplaceAll(input, "redhat-linux", "azl-linux")

		err = specFile.SearchAndReplace("", "", `redhat-linux`, "azl-linux")
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		require.Equal(t, expected, actual.String())
	})

	t.Run("replace in wrapper else branch with section filter", func(t *testing.T) {
		input := `
Name: test

%files
/usr/bin/test

%ifarch x86_64
%files nonlinux
%{_datadir}/syslinux/*.exe
%else
%exclude %{_datadir}/syslinux/*.exe
%endif
`

		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		expected := strings.ReplaceAll(input, "%exclude %{_datadir}/syslinux/*.exe", "")

		err = specFile.SearchAndReplace("%files", "", `^%exclude %\{_datadir\}/syslinux/\*\.exe$`, "")
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

	// ---- Conditional boundary tests ----
	//
	// These tests document AppendLinesToSection behavior at conditional boundaries.
	// When a section is followed by a %if wrapper that contains the next section,
	// the tree parser correctly identifies the wrapper boundary. Appended lines
	// land at the end of the section body, before the wrapper.

	t.Run("appends before conditional wrapping next section", func(t *testing.T) {
		// The %if wraps %install, not %build. "echo done" belongs in %build.
		input := `
Name: test

%build
make

%if %{with_docs}
%install
install.sh
%endif
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.AppendLinesToSection("%build", "", []string{"echo done"})
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		// Tree-based code correctly places "echo done" before the wrapper %if.
		assert.Equal(t, `
Name: test

%build
make

echo done
%if %{with_docs}
%install
install.sh
%endif
`, actual.String())
	})

	t.Run("appends before nested conditionals wrapping next section", func(t *testing.T) {
		input := `
Name: test

%build
make

%if %{with_docs}
%ifarch x86_64
%install
install.sh
%endif
%endif
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.AppendLinesToSection("%build", "", []string{"echo done"})
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		// Tree-based code correctly places "echo done" before the outer wrapper.
		assert.Equal(t, `
Name: test

%build
make

echo done
%if %{with_docs}
%ifarch x86_64
%install
install.sh
%endif
%endif
`, actual.String())
	})

	t.Run("appends correctly when section has own balanced conditional", func(t *testing.T) {
		// %if/%endif is fully within %build, so the boundary is correct.
		input := `
Name: test

%build
%if %{with_docs}
make docs
%endif
make

%install
install.sh
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.AppendLinesToSection("%build", "", []string{"echo done"})
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		assert.Equal(t, `
Name: test

%build
%if %{with_docs}
make docs
%endif
make

echo done
%install
install.sh
`, actual.String())
	})

	t.Run("appends correctly when no conditionals at boundary", func(t *testing.T) {
		input := `
Name: test

%build
make

%install
install.sh
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.AppendLinesToSection("%build", "", []string{"echo done"})
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		assert.Equal(t, `
Name: test

%build
make

echo done
%install
install.sh
`, actual.String())
	})

	t.Run("appends before preamble conditional wrapping next section", func(t *testing.T) {
		// In the preamble, a trailing %if wrapper contains %description.
		// The tree parser correctly identifies the wrapper boundary.
		input := `
Name: test
Source0: test.tar.gz

%if %{with_docs}
%description
A test package.
%endif

%build
make
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		err = specFile.AppendLinesToSection("", "", []string{"Vendor: Microsoft"})
		require.NoError(t, err)

		actual := new(bytes.Buffer)

		err = specFile.Serialize(actual)
		require.NoError(t, err)

		// Tree-based code correctly places "Vendor: Microsoft" before the wrapper.
		assert.Equal(t, `
Name: test
Source0: test.tar.gz

Vendor: Microsoft
%if %{with_docs}
%description
A test package.
%endif

%build
make
`, actual.String())
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
		{
			name:     "unnumbered patch tags counted as auto-numbered from 0",
			input:    "Name: test\nPatch: fix1.patch\nPatch: fix2.patch\n",
			expected: 1,
		},
		{
			name:     "unnumbered and numbered patch tags mixed",
			input:    "Name: test\nPatch: fix1.patch\nPatch: fix2.patch\nPatch5: fix5.patch\n",
			expected: 5,
		},
		{
			name:     "single unnumbered patch tag",
			input:    "Name: test\nPatch: fix.patch\n",
			expected: 0,
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

func TestContinuationSuppressesStructuralParsing(t *testing.T) {
	t.Run("section keyword in continuation body is not a section start", func(t *testing.T) {
		input := `Name: test
Version: 1.0

%description
A package.

%install
echo \
%files \
done

%files
/usr/bin/test
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		// %files inside the continuation should NOT be treated as a section start.
		// Only one real %files section should exist.
		found, err := specFile.HasSection("%files")
		require.NoError(t, err)
		assert.True(t, found, "real %%files section should be found")
	})

	t.Run("tag-like line in continuation body is not a tag", func(t *testing.T) {
		input := `Name: test
Version: 1.0

%description
A package.

%install
echo \
Name: fake \
done
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		// The "Name: fake" inside the continuation should not be found as a tag.
		// GetTag should return the real "Name: test" from the preamble.
		value, err := specFile.GetTag("", "Name")
		require.NoError(t, err)
		assert.Equal(t, "test", value, "should find the real Name tag, not the continuation body")
	})

	t.Run("normal parsing resumes after continuation ends", func(t *testing.T) {
		input := `Name: test
Version: 1.0

%description
A package.

%build
echo \
%install \
done

%install
make install

%files
/usr/bin/test
`
		specFile, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		found, err := specFile.HasSection("%install")
		require.NoError(t, err)
		assert.True(t, found, "real %%install section after continuation should be found")

		found, err = specFile.HasSection("%files")
		require.NoError(t, err)
		assert.True(t, found, "%%files section should be found")
	})

	t.Run("chained multi-line continuation", func(t *testing.T) {
		input := `Name: test
Version: 1.0

%build
echo \
%description \
%files \
%install \
done
`
		sf, err := spec.OpenSpec(strings.NewReader(input))
		require.NoError(t, err)

		// None of the keywords in the continuation chain should create sections.
		for _, sect := range []string{"%description", "%files", "%install"} {
			found, err := sf.HasSection(sect)
			require.NoError(t, err)
			assert.False(t, found, "%%s in continuation chain should not be a section", sect)
		}
	})
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
		{
			// Locks in the contract documented on RemoveSection: when a spec lexically
			// contains multiple sections with the same (section, package) identity (e.g.
			// inside mutually-exclusive %if/%else branches), every such section is removed.
			name: "removes every match when (section, package) appears more than once",
			input: `Name: test

%description
Main.

%files devel
/usr/include/v1.h

%files
/usr/bin/test

%files devel
/usr/include/v2.h

%changelog
`,
			sectionName: "%files",
			packageName: "devel",
			expectedOutput: `Name: test

%description
Main.

%files
/usr/bin/test

%changelog
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

//nolint:maintidx // Test table complexity scales with the number of conditional handling scenarios.
func TestRemoveSubpackage(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		packageName    string
		expectedOutput string
		errorExpected  bool
		errorContains  string
	}{
		{
			name: "removes all sections for a sub-package",
			input: `Name: test
Version: 1.0

%description
Main description.

%package devel
Summary: Devel files
Requires: test = 1.0

%description devel
Devel description.

%files
/usr/bin/test

%files devel
/usr/include/test.h

%post devel
echo posting

%changelog
`,
			packageName: "devel",
			expectedOutput: `Name: test
Version: 1.0

%description
Main description.

%files
/usr/bin/test

%changelog
`,
		},
		{
			name: "handles -n style sub-package",
			input: `Name: test

%description
Main.

%package -n other-pkg
Summary: Other

%description -n other-pkg
Other description.

%files -n other-pkg
/usr/bin/other

%files
/usr/bin/test
`,
			packageName: "other-pkg",
			expectedOutput: `Name: test

%description
Main.

%files
/usr/bin/test
`,
		},
		{
			name: "removes sub-package whose final section runs to EOF",
			input: `Name: test

%description
Main.

%package devel
Summary: Devel

%files devel
/usr/include/test.h
`,
			packageName: "devel",
			expectedOutput: `Name: test

%description
Main.

`,
		},
		{
			name: "fails when sub-package has no sections",
			input: `Name: test

%description
Main.

%files
/usr/bin/test
`,
			packageName:   "devel",
			errorExpected: true,
			errorContains: "not found",
		},
		{
			name: "fails on empty package name",
			input: `Name: test
%description
Main.
`,
			packageName:   "",
			errorExpected: true,
			errorContains: "empty",
		},
		{
			name: "does not affect main-package sections with the same name",
			input: `Name: test

%description
Main description.

%package devel
Summary: Devel

%description devel
Devel description.

%files
/usr/bin/test

%files devel
/usr/include/test.h
`,
			packageName: "devel",
			expectedOutput: `Name: test

%description
Main description.

%files
/usr/bin/test

`,
		},
		{
			name: "handles balanced conditional inside section",
			input: `Name: test

%description
Main.

%package devel
Summary: Devel
%ifarch x86_64
Requires: special-x86-lib
%endif

%description devel
Devel description.

%files
/usr/bin/test
`,
			packageName: "devel",
			expectedOutput: `Name: test

%description
Main.

%files
/usr/bin/test
`,
		},
		{
			name: "trims trailing conditional opener belonging to next section",
			input: `Name: test

%description
Main.

%files foo
/usr/share/foo

%if 0
%files bar
/usr/share/bar
%endif

%files
/usr/bin/test
`,
			packageName: "foo",
			expectedOutput: `Name: test

%description
Main.

%if 0
%files bar
/usr/share/bar
%endif

%files
/usr/bin/test
`,
		},
		{
			name: "trims trailing endif from section wrapped in conditional",
			input: `Name: test

%description
Main.

%if 0%{?with_devel}
%package devel
Summary: Devel

%description devel
Devel description.

%files devel
/usr/include/test.h
%endif

%files
/usr/bin/test
`,
			packageName: "devel",
			expectedOutput: `Name: test

%description
Main.

%if 0%{?with_devel}
%endif

%files
/usr/bin/test
`,
		},
		{
			name: "errors on conditional spanning across sections",
			input: `Name: test

%files foo
/usr/share/foo1
%if 0%{?with_extra}
/usr/share/foo-extra

%files bar
/usr/share/bar
%endif

%files
/usr/bin/test
`,
			packageName:   "foo",
			errorExpected: true,
			errorContains: "conditional block spans",
		},
		{
			name: "removes section from one branch while else branch retains sections",
			input: `Name: test

%description
Main.

%if cond
%files foo
/usr/share/foo
%else
%files bar
/usr/share/bar
%endif

%files
/usr/bin/test
`,
			packageName: "foo",
			expectedOutput: `Name: test

%description
Main.

%if cond
%else
%files bar
/usr/share/bar
%endif

%files
/usr/bin/test
`,
		},
		{
			name: "errors when trimmed zone contains section content in a balanced conditional",
			input: `Name: test

%description
Main.

%if 0%{?with_devel}
%files devel
/usr/include/test.h
%endif
%if 0%{?with_extra}
/usr/include/extra.h
%endif

%files
/usr/bin/test
`,
			packageName:   "devel",
			errorExpected: true,
			errorContains: "conditional block spans",
		},
		{
			name: "trims consecutive endifs from nested wrapping conditionals",
			input: `Name: test

%description
Main.

%if A
%if B
%files devel
/usr/include/test.h
%endif
%endif

%files
/usr/bin/test
`,
			packageName: "devel",
			expectedOutput: `Name: test

%description
Main.

%if A
%if B
%endif
%endif

%files
/usr/bin/test
`,
		},
		{
			name: "trims consecutive if openers belonging to next sections",
			input: `Name: test

%description
Main.

%files foo
/usr/share/foo

%if 0
%if 0
%files bar
/usr/share/bar
%endif
%endif

%files
/usr/bin/test
`,
			packageName: "foo",
			expectedOutput: `Name: test

%description
Main.

%if 0
%if 0
%files bar
/usr/share/bar
%endif
%endif

%files
/usr/bin/test
`,
		},
		{
			name: "trims mixed endif then if at tail",
			input: `Name: test

%description
Main.

%if A
%files devel
/usr/include/test.h
%endif
%if 0
%files bar
/usr/share/bar
%endif

%files
/usr/bin/test
`,
			packageName: "devel",
			expectedOutput: `Name: test

%description
Main.

%if A
%endif
%if 0
%files bar
/usr/share/bar
%endif

%files
/usr/bin/test
`,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			specFile, err := spec.OpenSpec(strings.NewReader(testCase.input))
			require.NoError(t, err)

			err = specFile.RemoveSubpackage(testCase.packageName)

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
