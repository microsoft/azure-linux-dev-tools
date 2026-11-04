// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareLocklessSpec_CarriesBaseRelease(t *testing.T) {
	for _, editor := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(editor), func(t *testing.T) {
			for _, testCase := range []struct {
				name, version, definition, release, wantBase, wantBumped string
				changed                                                  bool
			}{
				{
					name: "same upstream", version: "1", definition: "%global baserelease 18",
					release: "%{baserelease}%{?dist}", wantBase: "18", wantBumped: "19",
				},
				{
					name: "new upstream same version", version: "1", changed: true,
					definition: "%global baserelease 18", release: "%{baserelease}%{?dist}",
					wantBase: "18", wantBumped: "19",
				},
				{
					name: "new version", version: "2", changed: true,
					definition: "%global baserelease 18", release: "%{baserelease}%{?dist}",
					wantBase: "17", wantBumped: "18",
				},
				{
					name: "define and optional macro", version: "1", definition: "%define baserelease 18",
					release: "%{?baserelease}%{?dist}", wantBase: "18", wantBumped: "19",
				},
				{
					name: "unbraced macro", version: "1", definition: "%global baserelease 18",
					release: "%baserelease%{?dist}", wantBase: "18", wantBumped: "19",
				},
			} {
				t.Run(testCase.name, func(t *testing.T) {
					env := testutils.NewTestEnv(t)

					const (
						path         = "/working/curl.spec"
						oldChangelog = "* Old downstream entry\n"
						newRelease   = "%{baserelease}.upstream%{?dist}"
					)

					oldSpec := testCase.definition + "\n" +
						renderTestSpec("1", testCase.release, oldChangelog)

					history := lastRender{spec: oldSpec, previous: "old", current: "old"}
					if testCase.changed {
						history.current = "new"
					}

					// Upstream context and Release differ; only the macro definition is carried over.
					upstream := "# New upstream header\n%define baserelease 17\n" +
						renderTestSpec(testCase.version, newRelease, "* Upstream entry\n")
					require.NoError(t, fileutils.WriteFile(env.TestFS, path, []byte(upstream), fileperms.PublicFile))
					env.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")
					env.CmdFactory.RunAndGetOutputHandler = func(_ *exec.Cmd) (string, error) {
						return "abc123 Upstream change\n", nil
					}

					var calls int

					env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
						calls++

						assert.Equal(t, path, cmd.Args[len(cmd.Args)-1])
						content, err := fileutils.ReadFile(env.TestFS, path)
						require.NoError(t, err)

						definition := testCase.definition
						if testCase.changed && testCase.version != "1" {
							definition = "%define baserelease 17"
						}

						assert.Contains(t, string(content), definition+"\n")
						assert.Contains(t, string(content), "Release: "+newRelease+"\n")
						assert.Contains(t, string(content), "%changelog\n"+oldChangelog)

						return fileutils.WriteFile(env.TestFS, path,
							[]byte(strings.Replace(string(content), "baserelease "+testCase.wantBase,
								"baserelease "+testCase.wantBumped, 1)), fileperms.PublicFile)
					}
					update := &locklessRenderUpdate{subject: "updated curl"}
					require.NoError(t, collectRenderUpstreamChanges(env.Env, "/working", "curl",
						testCase.version, history, update))
					err := prepareLocklessSpec(env.Env, &renderAutospecStub{}, "/working", "curl", editor, history, update)
					require.NoError(t, err)
					assert.Zero(t, calls)

					_, err = bumpLocklessSpec(env.Env, path, update)
					require.NoError(t, err)
					assert.Equal(t, 1, calls)

					content, err := fileutils.ReadFile(env.TestFS, path)
					require.NoError(t, err)
					assert.Contains(t, string(content), "baserelease "+testCase.wantBumped+"\n")
					assert.Equal(t, oldSpec, history.spec)
				})
			}
		})
	}
}

func TestCarryStaticRenderRelease(t *testing.T) {
	for _, editor := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(editor), func(t *testing.T) {
			for _, testCase := range []struct {
				name, previous, upstream, oldRelease, wantRelease, wantError string
			}{
				{
					name:     "literal release with unrelated baserelease definition",
					previous: "%global baserelease 18\n", upstream: "%global baserelease 17\n",
					oldRelease: "9%{?dist}", wantRelease: "9%{?dist}",
				},
				{
					name: "similarly named macro", oldRelease: "%{baserelease_extra}",
					wantRelease: "%{baserelease_extra}",
				},
				{
					name: "missing committed definition", upstream: "%global baserelease 17\n",
					oldRelease: "%{baserelease}", wantError: "reading committed 'baserelease' definition",
				},
				{
					name: "missing upstream definition", previous: "%global baserelease 18\n",
					oldRelease: "%{baserelease}", wantError: "finding upstream 'baserelease' definition",
				},
				{
					name:     "ambiguous committed definition",
					previous: "%global baserelease 18\n%define baserelease 19\n",
					upstream: "%global baserelease 17\n", oldRelease: "%{baserelease}",
					wantError: "expected one 'baserelease' definition, found 2",
				},
				{
					name: "ambiguous upstream definition", previous: "%global baserelease 18\n",
					upstream:   "%global baserelease 16\n%define baserelease 17\n",
					oldRelease: "%{baserelease}", wantError: "expected one 'baserelease' definition, found 2",
				},
			} {
				t.Run(testCase.name, func(t *testing.T) {
					env := testutils.NewTestEnv(t)
					upstream := testCase.upstream + renderTestSpec("1", "1%{?dist}", "")
					opened, err := spec.OpenSpec(strings.NewReader(upstream), spec.WithEditor(editor))
					require.NoError(t, err)

					history := lastRender{spec: testCase.previous + renderTestSpec("1", testCase.oldRelease, "")}

					err = carryStaticRenderRelease(env.Env, opened, "1%{?dist}", "1", history, false, editor)
					if testCase.wantError != "" {
						require.ErrorContains(t, err, testCase.wantError)

						return
					}

					require.NoError(t, err)
					release, err := opened.GetLastTag("", "Release")
					require.NoError(t, err)
					assert.Equal(t, testCase.wantRelease, release)

					if testCase.upstream != "" {
						_, definition, err := baseReleaseDefinition(opened)
						require.NoError(t, err)
						assert.Equal(t, strings.TrimSuffix(testCase.upstream, "\n"), definition)
					}
				})
			}
		})
	}
}
