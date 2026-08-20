// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRpmRepo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		repo    RpmRepoResource
		wantErr string // substring; empty = expect no error
	}{
		{
			name:    "ok base-uri with disable-gpg-check",
			repo:    RpmRepoResource{BaseURI: "https://x/", DisableGPGCheck: true},
			wantErr: "",
		},
		{
			name:    "ok base-uri with gpg-key",
			repo:    RpmRepoResource{BaseURI: "https://x/", GPGKey: "https://x/key"},
			wantErr: "",
		},
		{
			name:    "ok metalink",
			repo:    RpmRepoResource{Metalink: "https://x/ml", DisableGPGCheck: true},
			wantErr: "",
		},
		{
			name:    "missing source",
			repo:    RpmRepoResource{DisableGPGCheck: true},
			wantErr: "exactly one of `base-uri` or `metalink`",
		},
		{
			name:    "both base-uri and metalink",
			repo:    RpmRepoResource{BaseURI: "https://x/", Metalink: "https://x/ml", DisableGPGCheck: true},
			wantErr: "must not specify both",
		},
		{
			name:    "gpg-check enabled with no key",
			repo:    RpmRepoResource{BaseURI: "https://x/"},
			wantErr: "GPG checking enabled",
		},
		{
			name:    "newline in description",
			repo:    RpmRepoResource{BaseURI: "https://x/", DisableGPGCheck: true, Description: "a\nb"},
			wantErr: "single line",
		},
		{
			name:    "unsupported type",
			repo:    RpmRepoResource{Type: "weird", BaseURI: "https://x/", DisableGPGCheck: true},
			wantErr: "unsupported type",
		},
		{
			name:    "opaque base-uri rejected",
			repo:    RpmRepoResource{BaseURI: "https:example.com", DisableGPGCheck: true},
			wantErr: "opaque URI",
		},
		{
			name:    "base-uri with empty host rejected",
			repo:    RpmRepoResource{BaseURI: "https:///path", DisableGPGCheck: true},
			wantErr: "must include a host",
		},
		{
			name:    "opaque metalink rejected",
			repo:    RpmRepoResource{Metalink: "https:example.com/ml", DisableGPGCheck: true},
			wantErr: "opaque URI",
		},
		{
			name:    "opaque gpg-key https rejected",
			repo:    RpmRepoResource{BaseURI: "https://x/", GPGKey: "https:example.com/key"},
			wantErr: "opaque URI",
		},
		{
			name:    "opaque gpg-key file rejected",
			repo:    RpmRepoResource{BaseURI: "https://x/", GPGKey: "file:relative.gpg"},
			wantErr: "opaque URI",
		},
		{
			name:    "file gpg-key with host rejected",
			repo:    RpmRepoResource{BaseURI: "https://x/", GPGKey: "file://server/share/key.gpg"},
			wantErr: "file:///absolute/path",
		},
		{
			name:    "https gpg-key with no host rejected",
			repo:    RpmRepoResource{BaseURI: "https://x/", GPGKey: "https:///path/key.gpg"},
			wantErr: "must include a host",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := validateRpmRepo("test", &testCase.repo)
			if testCase.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.wantErr)
			}
		})
	}
}

func TestWithAbsolutePaths_GPGKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "https passthrough", in: "https://example.com/key", want: "https://example.com/key"},
		{name: "file uri passthrough", in: "file:///etc/pki/key", want: "file:///etc/pki/key"},
		{name: "bare relative", in: "keys/local.gpg", want: "file:///ref/keys/local.gpg"},
		{name: "bare absolute", in: "/etc/pki/key", want: "file:///etc/pki/key"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := &ResourcesConfig{
				RpmRepos: map[string]RpmRepoResource{
					"r": {BaseURI: "https://x/", GPGKey: testCase.in},
				},
			}
			got := cfg.WithAbsolutePaths("/ref")
			assert.Equal(t, testCase.want, got.RpmRepos["r"].GPGKey)
			// Original must be untouched (deep copy semantics).
			assert.Equal(t, testCase.in, cfg.RpmRepos["r"].GPGKey)
		})
	}
}

func TestMergeUpdatesFrom_WholesaleReplace(t *testing.T) {
	t.Parallel()

	earlier := &ResourcesConfig{
		RpmRepos: map[string]RpmRepoResource{
			"r": {BaseURI: "https://old/", DisableGPGCheck: true, Description: "old"},
		},
	}
	later := &ResourcesConfig{
		RpmRepos: map[string]RpmRepoResource{
			// disable-gpg-check is the zero value; must still take effect.
			"r":  {BaseURI: "https://new/", GPGKey: "https://new/key"},
			"r2": {BaseURI: "https://r2/", DisableGPGCheck: true},
		},
	}

	earlier.MergeUpdatesFrom(later)

	got := earlier.RpmRepos["r"]
	assert.Equal(t, "https://new/", got.BaseURI)
	assert.False(t, got.DisableGPGCheck, "zero value must override true")
	assert.Empty(t, got.Description, "wholesale replace must drop old description")
	assert.Equal(t, "https://new/key", got.GPGKey)
	// New entry preserved.
	assert.Equal(t, "https://r2/", earlier.RpmRepos["r2"].BaseURI)
}

func TestMergeUpdatesFrom_NilOther(t *testing.T) {
	t.Parallel()

	cfg := &ResourcesConfig{}
	cfg.MergeUpdatesFrom(nil)
	assert.Empty(t, cfg.RpmRepos)
}

func TestRpmRepoResource_IsAvailableForArch(t *testing.T) {
	t.Parallel()

	repo := RpmRepoResource{}
	assert.True(t, repo.IsAvailableForArch("x86_64"))
	assert.True(t, repo.IsAvailableForArch("aarch64"))

	repo.Arches = []string{"x86_64"}
	assert.True(t, repo.IsAvailableForArch("x86_64"))
	assert.False(t, repo.IsAvailableForArch("aarch64"))
}

func TestHasURIScheme(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"https://x":     true,
		"file:///x":     true,
		"foo+bar.baz:x": true,
		"/abs/path":     false,
		"rel/path":      false,
		"":              false,
		":nope":         false,
		"1abc:nope":     false, // must start with alpha
	}

	for in, want := range cases {
		got := hasURIScheme(in)
		assert.Equalf(t, want, got, "hasURIScheme(%q)", in)
	}
}

// Sanity-check that the rendered TOML field tags match documented schema names
// (these are what users type in TOML files).
func TestRpmRepoResource_TOMLFieldNames(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(RpmRepoResource{})

	cases := map[string]string{
		"BaseURI":         "base-uri,omitempty",
		"DisableGPGCheck": "disable-gpg-check,omitempty",
		"GPGKey":          "gpg-key,omitempty",
		"Metalink":        "metalink,omitempty",
	}

	for fieldName, wantTag := range cases {
		f, ok := typ.FieldByName(fieldName)
		require.True(t, ok, "missing field %s", fieldName)
		assert.Equal(t, wantTag, f.Tag.Get("toml"), "field %s", fieldName)
	}
}

func TestEffectiveRpmRepos_ExpandSets(t *testing.T) {
	t.Parallel()

	cfg := &ResourcesConfig{
		RpmRepoSetTemplates: map[string]RpmRepoSetTemplate{
			"azl-standard": {
				Subrepos: []SubrepoSpec{
					{Name: "base", Kind: SubrepoKindBinary, Subpath: "base/$basearch"},
					{Name: "base-debug", Kind: SubrepoKindDebug, Subpath: "base/debuginfo/$basearch"},
					{Name: "base-src", Kind: SubrepoKindSource, Subpath: "base/srpms"},
					{Name: "sdk", Kind: SubrepoKindBinary, Subpath: "sdk/$basearch"},
					{Name: "sdk-debug", Kind: SubrepoKindDebug, Subpath: "sdk/debuginfo/$basearch"},
					{Name: "sdk-src", Kind: SubrepoKindSource, Subpath: "sdk/srpms"},
				},
			},
		},
		RpmRepoSets: map[string]RpmRepoSet{
			"azl4": {
				Template:        "azl-standard",
				BaseURI:         "https://example.com/azl4",
				NamePrefix:      "azl4-",
				DisableGPGCheck: true,
				// Allowlist: take only the binary + source channels (drop debug).
				Subrepos: []string{"base", "base-src", "sdk", "sdk-src"},
			},
		},
	}

	effective, err := cfg.EffectiveRpmRepos()
	require.NoError(t, err)

	assert.Len(t, effective, 4)
	assert.Equal(t, "https://example.com/azl4/base/$basearch", effective["azl4-base"].BaseURI)
	assert.Equal(t, "https://example.com/azl4/base/srpms", effective["azl4-base-src"].BaseURI)
	assert.Equal(t, "https://example.com/azl4/sdk/$basearch", effective["azl4-sdk"].BaseURI)
	assert.NotContains(t, effective, "azl4-base-debug")
	assert.NotContains(t, effective, "azl4-sdk-debug")
}

func TestEffectiveRpmRepos_DefaultIncludesAllSubrepos(t *testing.T) {
	t.Parallel()

	cfg := &ResourcesConfig{
		RpmRepoSetTemplates: map[string]RpmRepoSetTemplate{
			"t": {
				Subrepos: []SubrepoSpec{
					{Name: "a", Subpath: "a"},
					{Name: "b", Subpath: "b"},
					{Name: "c", Subpath: "c"},
				},
			},
		},
		RpmRepoSets: map[string]RpmRepoSet{
			"s": {Template: "t", BaseURI: "https://x/", NamePrefix: "s-", DisableGPGCheck: true},
		},
	}

	effective, err := cfg.EffectiveRpmRepos()
	require.NoError(t, err)
	assert.Len(t, effective, 3)
	assert.Contains(t, effective, "s-a")
	assert.Contains(t, effective, "s-b")
	assert.Contains(t, effective, "s-c")
}

func TestEffectiveRpmRepos_SubreposAllowlist(t *testing.T) {
	t.Parallel()

	cfg := &ResourcesConfig{
		RpmRepoSetTemplates: map[string]RpmRepoSetTemplate{
			"t": {
				Subrepos: []SubrepoSpec{
					{Name: "a", Subpath: "a"},
					{Name: "b", Subpath: "b"},
					{Name: "c", Subpath: "c"},
				},
			},
		},
		RpmRepoSets: map[string]RpmRepoSet{
			"s": {
				Template:        "t",
				BaseURI:         "https://x/",
				NamePrefix:      "s-",
				DisableGPGCheck: true,
				Subrepos:        []string{"a", "c"},
			},
		},
	}

	effective, err := cfg.EffectiveRpmRepos()
	require.NoError(t, err)
	assert.Len(t, effective, 2)
	assert.Contains(t, effective, "s-a")
	assert.Contains(t, effective, "s-c")
	assert.NotContains(t, effective, "s-b")
}

func TestEffectiveRpmRepos_CollisionWithExplicit(t *testing.T) {
	t.Parallel()

	cfg := &ResourcesConfig{
		RpmRepos: map[string]RpmRepoResource{
			"s-a": {BaseURI: "https://other/", DisableGPGCheck: true},
		},
		RpmRepoSetTemplates: map[string]RpmRepoSetTemplate{
			"t": {Subrepos: []SubrepoSpec{{Name: "a", Subpath: "a"}}},
		},
		RpmRepoSets: map[string]RpmRepoSet{
			"s": {Template: "t", BaseURI: "https://x/", NamePrefix: "s-", DisableGPGCheck: true},
		},
	}

	_, err := cfg.EffectiveRpmRepos()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestEffectiveRpmRepos_UndefinedTemplate(t *testing.T) {
	t.Parallel()

	cfg := &ResourcesConfig{
		RpmRepoSets: map[string]RpmRepoSet{
			"s": {Template: "missing", BaseURI: "https://x/", DisableGPGCheck: true},
		},
	}

	_, err := cfg.EffectiveRpmRepos()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "undefined `template`")
}

func TestEffectiveRpmRepos_BadAllowlist(t *testing.T) {
	t.Parallel()

	cfg := &ResourcesConfig{
		RpmRepoSetTemplates: map[string]RpmRepoSetTemplate{
			"t": {Subrepos: []SubrepoSpec{{Name: "a", Subpath: "a"}}},
		},
		RpmRepoSets: map[string]RpmRepoSet{
			"s": {
				Template: "t", BaseURI: "https://x/", DisableGPGCheck: true,
				Subrepos: []string{"nonesuch"},
			},
		},
	}

	_, err := cfg.EffectiveRpmRepos()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subrepos")
}

func TestEffectiveRpmRepos_RejectsInvalidSynthesizedID(t *testing.T) {
	t.Parallel()

	cfg := &ResourcesConfig{
		RpmRepoSetTemplates: map[string]RpmRepoSetTemplate{
			"t": {Subrepos: []SubrepoSpec{{Name: "a", Subpath: "a"}}},
		},
		RpmRepoSets: map[string]RpmRepoSet{
			"s": {
				Template:        "t",
				BaseURI:         "https://x/",
				NamePrefix:      "-bad",
				DisableGPGCheck: true,
			},
		},
	}

	_, err := cfg.EffectiveRpmRepos()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid repo ID")
}

func TestJoinSetBaseURI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		base, sub, want, err string
	}{
		{base: "https://x", sub: "a/b", want: "https://x/a/b"},
		{base: "https://x/", sub: "a/b", want: "https://x/a/b"},
		{base: "https://x/", sub: "/abs", err: "relative"},
		{base: "https://x/", sub: "../escape", err: ".."},
		{base: "https://x/", sub: "", err: "empty"},
		{base: "https://x?token=1", sub: "a", err: "query string or fragment"},
		{base: "https://x#frag", sub: "a", err: "query string or fragment"},
		{base: "https://x/", sub: "a?token=1", err: "query string or fragment"},
		{base: "https://x/", sub: "a#frag", err: "query string or fragment"},
	}

	for _, testCase := range cases {
		got, err := joinSetBaseURI("s", testCase.base, testCase.sub)
		if testCase.err == "" {
			require.NoError(t, err, testCase.sub)
			assert.Equal(t, testCase.want, got)
		} else {
			require.Error(t, err, testCase.sub)
			assert.Contains(t, err.Error(), testCase.err)
		}
	}
}

func TestEffectiveInputRepos_ExpandsAndDedupes(t *testing.T) {
	t.Parallel()

	resources := &ResourcesConfig{
		RpmRepos: map[string]RpmRepoResource{
			"explicit": {BaseURI: "https://e/", DisableGPGCheck: true},
		},
		RpmRepoSetTemplates: map[string]RpmRepoSetTemplate{
			"t": {
				Subrepos: []SubrepoSpec{
					{Name: "binary", Subpath: "main/$basearch"},
					{Name: "src", Kind: SubrepoKindSource, Subpath: "src"},
				},
			},
		},
		RpmRepoSets: map[string]RpmRepoSet{
			"s": {Template: "t", BaseURI: "https://x/", NamePrefix: "s-", DisableGPGCheck: true},
		},
	}

	version := &DistroVersionDefinition{
		Inputs: DistroVersionInputs{
			RpmBuild: []DistroVersionInput{
				{Repo: "explicit"},
				{Set: "s"},
			},
		},
	}

	got, err := version.EffectiveRpmBuildRepos(resources)
	require.NoError(t, err)
	assert.Equal(t, []string{"explicit", "s-binary", "s-src"}, got)

	// Introduce a duplicate (direct repo whose name matches a set's expansion).
	version.Inputs.RpmBuild = []DistroVersionInput{
		{Repo: "s-binary"},
		{Set: "s"},
	}
	_, err = version.EffectiveRpmBuildRepos(resources)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than once")
}

func TestEffectiveInputRepos_RejectsAmbiguousEntry(t *testing.T) {
	t.Parallel()

	resources := &ResourcesConfig{}

	cases := []struct {
		name  string
		entry DistroVersionInput
		want  string
	}{
		{name: "both repo and set", entry: DistroVersionInput{Repo: "r", Set: "s"}, want: "exactly one"},
		{name: "neither repo nor set", entry: DistroVersionInput{}, want: "exactly one"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			version := &DistroVersionDefinition{
				Inputs: DistroVersionInputs{RpmBuild: []DistroVersionInput{testCase.entry}},
			}

			_, err := version.EffectiveRpmBuildRepos(resources)
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.want)
		})
	}
}
