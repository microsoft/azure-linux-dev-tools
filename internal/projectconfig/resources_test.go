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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateRpmRepo("test", &tc.repo)
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
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

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := &ResourcesConfig{
				RpmRepos: map[string]RpmRepoResource{
					"r": {BaseURI: "https://x/", GPGKey: tc.in},
				},
			}
			got := cfg.WithAbsolutePaths("/ref")
			assert.Equal(t, tc.want, got.RpmRepos["r"].GPGKey)
			// Original must be untouched (deep copy semantics).
			assert.Equal(t, tc.in, cfg.RpmRepos["r"].GPGKey)
		})
	}
}

func TestMergeUpdatesFrom_WholesaleReplace(t *testing.T) {
	t.Parallel()

	a := &ResourcesConfig{
		RpmRepos: map[string]RpmRepoResource{
			"r": {BaseURI: "https://old/", DisableGPGCheck: true, Description: "old"},
		},
	}
	b := &ResourcesConfig{
		RpmRepos: map[string]RpmRepoResource{
			// disable-gpg-check is the zero value; must still take effect.
			"r":  {BaseURI: "https://new/", GPGKey: "https://new/key"},
			"r2": {BaseURI: "https://r2/", DisableGPGCheck: true},
		},
	}

	require.NoError(t, a.MergeUpdatesFrom(b))

	got := a.RpmRepos["r"]
	assert.Equal(t, "https://new/", got.BaseURI)
	assert.False(t, got.DisableGPGCheck, "zero value must override true")
	assert.Empty(t, got.Description, "wholesale replace must drop old description")
	assert.Equal(t, "https://new/key", got.GPGKey)
	// New entry preserved.
	assert.Equal(t, "https://r2/", a.RpmRepos["r2"].BaseURI)
}

func TestMergeUpdatesFrom_NilOther(t *testing.T) {
	t.Parallel()

	a := &ResourcesConfig{}
	require.NoError(t, a.MergeUpdatesFrom(nil))
	assert.Empty(t, a.RpmRepos)
}

func TestRpmRepoResource_IsAvailableForArch(t *testing.T) {
	t.Parallel()

	r := RpmRepoResource{}
	assert.True(t, r.IsAvailableForArch("x86_64"))
	assert.True(t, r.IsAvailableForArch("aarch64"))

	r.Arches = []string{"x86_64"}
	assert.True(t, r.IsAvailableForArch("x86_64"))
	assert.False(t, r.IsAvailableForArch("aarch64"))
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
