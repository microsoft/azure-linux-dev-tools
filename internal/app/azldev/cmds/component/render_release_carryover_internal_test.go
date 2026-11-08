// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareLocklessSpec_AutoreleaseToStatic(t *testing.T) {
	for _, editor := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		for _, testCase := range []struct {
			name, version, baseline, previous string
			fail                              bool
		}{
			{name: "same version", version: "1", baseline: "12%{?dist}"},
			{name: "new version", version: "2", baseline: "1%{?dist}"},
			{name: "without dist", version: "1", baseline: "12", previous: "%{autorelease -b 8 -n}"},
			{name: "release query fails", version: "1", fail: true},
		} {
			t.Run(string(editor)+"/"+testCase.name, func(t *testing.T) {
				env := testutils.NewTestEnv(t)
				env.Config.Project.RenderedSpecsDir = "/project/specs"
				env.Config.Components["curl"] = projectconfig.ComponentConfig{Name: "curl"}
				env.CmdFactory.RegisterCommandInSearchPath("git")
				env.CmdFactory.RegisterCommandInSearchPath("rpmdev-bumpspec")

				previous := testCase.previous
				if previous == "" {
					previous = "%autorelease -b 8"
				}

				history := lastRender{spec: renderTestSpec("1", previous, "%autochangelog\n")}

				const path = "/working/curl.spec"
				require.NoError(t, fileutils.WriteFile(env.TestFS, path,
					[]byte(renderTestSpec(testCase.version, "1%{?dist}", "* Upstream entry\n")),
					fileperms.PublicFile))

				var snapshot string

				env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
					switch cmd.Args[3] {
					case renderTestRevParse:
						if cmd.Args[4] == "--git-common-dir" {
							return "/project/.git", nil
						}

						return renderTestProjectRoot, nil
					case "clone":
						snapshot = cmd.Args[len(cmd.Args)-1]
						require.NoError(t, fileutils.MkdirAll(env.TestFS, filepath.Join(snapshot, ".git")))
						require.NoError(t, fileutils.WriteFile(env.TestFS, filepath.Join(snapshot, "curl.spec"),
							[]byte(history.spec), fileperms.PublicFile))
					}

					return "", nil
				}
				queryErr := errors.New("release query failed")
				autospec := &renderAutospecStub{expand: func(dir, filename string) (string, error) {
					assert.Equal(t, snapshot, dir)
					assert.Equal(t, "curl.spec", filename)
					content, err := fileutils.ReadFile(env.TestFS, filepath.Join(dir, filename))
					require.NoError(t, err)
					assert.Equal(t, history.spec, string(content))

					if testCase.fail {
						return "", queryErr
					}

					return "Calculated release number: 12\n", nil
				}}

				err := prepareLocklessSpec(env.Env, autospec, "/working", "curl", editor, history)
				if testCase.fail {
					require.ErrorIs(t, err, queryErr)
				} else {
					require.NoError(t, err)

					content, readErr := fileutils.ReadFile(env.TestFS, path)
					require.NoError(t, readErr)
					assert.Contains(t, string(content), "Release: "+testCase.baseline+"\n")
					assert.Contains(t, string(content), "* Full upstream history\n")
					assert.NotContains(t, string(content), "%autorelease")
					assert.NotContains(t, string(content), "%autochangelog")

					bumped := false
					env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
						bumped = true

						assert.Equal(t, "rpmdev-bumpspec", cmd.Args[0])

						return nil
					}
					_, err = bumpLocklessSpec(env.Env, path, &locklessRenderUpdate{subject: "test transition"})
					require.NoError(t, err)
					assert.True(t, bumped)
				}

				exists, checkErr := fileutils.Exists(env.TestFS, snapshot)
				require.NoError(t, checkErr)
				assert.False(t, exists, "remove the temporary history on success and failure")
			})
		}
	}
}

func TestPrepareLocklessSpec_CarriesBaseRelease(t *testing.T) {
	for _, editor := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(editor), func(t *testing.T) {
			for _, testCase := range []struct {
				name, version, definition, release, wantBase, wantBumped string
				changed                                                  bool
				initial                                                  bool
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
				{
					name: "initial render", version: "1", initial: true,
					release: "%{baserelease}%{?dist}", wantBase: "17", wantBumped: "18",
				},
			} {
				t.Run(testCase.name, func(t *testing.T) {
					env := testutils.NewTestEnv(t)

					const (
						path         = "/working/curl.spec"
						oldChangelog = "* Old downstream entry\n"
						newRelease   = "%{baserelease}.upstream%{?dist}"
						releaseTag   = "Release:\t  "
					)

					oldSpec := testCase.definition + "\n" +
						renderTestSpec("1", testCase.release, oldChangelog)

					history := lastRender{spec: oldSpec, previous: "old", current: "old"}
					wantChangelog := oldChangelog

					if testCase.initial {
						oldSpec = ""
						history = lastRender{}
						wantChangelog = "* Upstream entry\n"
					}

					if testCase.changed {
						history.current = "new"
					}

					// Upstream context and Release differ; only the macro definition is carried over.
					upstream := "# New upstream header\n%define baserelease 17\n" +
						renderTestSpec(testCase.version, newRelease, "* Upstream entry\n")
					upstream = strings.Replace(upstream, "Release: ", releaseTag, 1)
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
						if testCase.wantBase == "17" {
							definition = "%define baserelease 17"
						}

						assert.Contains(t, string(content), definition+"\n")
						assert.Contains(t, string(content), releaseTag+newRelease+"\n")
						assert.Contains(t, string(content), "%changelog\n"+wantChangelog)

						return fileutils.WriteFile(env.TestFS, path,
							[]byte(strings.Replace(string(content), "baserelease "+testCase.wantBase,
								"baserelease "+testCase.wantBumped, 1)), fileperms.PublicFile)
					}
					update := &locklessRenderUpdate{subject: "updated curl"}
					require.NoError(t, collectRenderUpstreamChanges(env.Env, "/working", "curl",
						testCase.version, history, update))
					err := prepareLocklessSpec(env.Env, &renderAutospecStub{}, "/working", "curl", editor, history)
					require.NoError(t, err)
					assert.Zero(t, calls)

					_, err = bumpLocklessSpec(env.Env, path, update)
					require.NoError(t, err)
					assert.Equal(t, 1, calls)

					content, err := fileutils.ReadFile(env.TestFS, path)
					require.NoError(t, err)
					assert.Contains(t, string(content), "baserelease "+testCase.wantBumped+"\n")
					assert.Contains(t, string(content), releaseTag+newRelease+"\n")
					assert.Equal(t, oldSpec, history.spec)
				})
			}
		})
	}
}

func TestPrepareLocklessSpec_PreservesAutoreleaseSpacing(t *testing.T) {
	for _, editor := range []spec.EditorMode{spec.EditorLegacy, spec.EditorStructural} {
		t.Run(string(editor), func(t *testing.T) {
			for _, spacing := range []string{"        ", "\t \t", ""} {
				t.Run(spacing, func(t *testing.T) {
					env := testutils.NewTestEnv(t)

					const path = "/working/curl.spec"

					upstream := strings.Replace(renderTestSpec("1", "%autorelease -b 2 -e test", "%autochangelog\n"),
						"Release: ", "Release:"+spacing, 1)
					require.NoError(t, fileutils.WriteFile(env.TestFS, path, []byte(upstream), fileperms.PublicFile))

					err := prepareLocklessSpec(env.Env, &renderAutospecStub{base: "9"},
						"/working", "curl", editor, lastRender{})
					require.NoError(t, err)
					content, err := fileutils.ReadFile(env.TestFS, path)
					require.NoError(t, err)
					assert.Contains(t, string(content), "Release:"+spacing+"%autorelease -b 9 -e test\n")
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

					err = carryStaticRenderRelease(env.Env, opened, "1%{?dist}", history, editor)
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
