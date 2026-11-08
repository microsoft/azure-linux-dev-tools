// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/sourceproviders_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestLocklessPrepareSourcesPreparesDirectlyInOutput(t *testing.T) {
	const autorelease = "%autorelease"

	for _, testCase := range []struct {
		name        string
		sourceType  projectconfig.SpecSourceType
		autorelease bool
		failFetch   bool
		failPayload bool
		failArchive bool
		withoutGit  bool
		bumpspec    bool
	}{
		{name: "local", sourceType: projectconfig.SpecSourceTypeLocal},
		{name: "local without git", sourceType: projectconfig.SpecSourceTypeLocal, withoutGit: true},
		{
			name: "without git and bumpspec", sourceType: projectconfig.SpecSourceTypeLocal,
			withoutGit: true, bumpspec: true,
		},
		{name: "upstream", sourceType: projectconfig.SpecSourceTypeUpstream},
		{name: "upstream autorelease", sourceType: projectconfig.SpecSourceTypeUpstream, autorelease: true},
		{
			name: "upstream autorelease without git", sourceType: projectconfig.SpecSourceTypeUpstream,
			autorelease: true, withoutGit: true,
		},
		{name: "fetch failure", sourceType: projectconfig.SpecSourceTypeLocal, failFetch: true},
		{name: "payload failure", sourceType: projectconfig.SpecSourceTypeLocal, failPayload: true},
		{
			name: "payload failure without git", sourceType: projectconfig.SpecSourceTypeLocal,
			failPayload: true, withoutGit: true,
		},
		{name: "archive overlay failure", sourceType: projectconfig.SpecSourceTypeLocal, failArchive: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			env.Config.LocklessComponents = map[string]bool{"curl": true}
			hash := strings.Repeat("a", 128)

			env.Config.Components["curl"] = projectconfig.ComponentConfig{
				Name: "curl",
				Spec: projectconfig.SpecSource{SourceType: testCase.sourceType},
				Build: projectconfig.ComponentBuildConfig{
					Defines: map[string]string{"prepare_fixture": "ready"},
				},
				SourceFiles: []projectconfig.SourceFileReference{{
					Filename: "extra-source.tar.gz", Hash: hash, HashType: fileutils.HashTypeSHA512,
					Origin: projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.invalid/source"},
				}},
				Overlays: []projectconfig.ComponentOverlay{
					{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Summary", Value: "Prepared summary"},
					{
						Type:     projectconfig.ComponentOverlaySearchAndReplaceInFile,
						Filename: "generated-input.txt", Regex: "downloaded", Replacement: "overlaid",
					},
				},
			}
			if testCase.failArchive {
				config := env.Config.Components["curl"]
				config.SourceFiles[0].Origin = projectconfig.Origin{Type: projectconfig.OriginTypeOverlay}
				config.Overlays = append(config.Overlays, projectconfig.ComponentOverlay{
					Type: projectconfig.ComponentOverlayRemoveFile, Archive: "extra-source.tar.gz", Filename: "remove-me.txt",
				})
				env.Config.Components["curl"] = config
			}

			repo, _ := testRepoWithCommits(t, []testRepoCommit{{files: map[string][]byte{"README": []byte("project")}}})
			originalRepo, originalPreparer := openRenderRepo, newSourcePreparer
			openRenderRepo = func(_ *azldev.Env) (*gogit.Repository, string, error) {
				return repo, renderTestProjectRoot, nil
			}

			t.Cleanup(func() {
				openRenderRepo = originalRepo
				newSourcePreparer = originalPreparer
			})

			ctrl := gomock.NewController(t)
			manager := sourceproviders_test.NewMockSourceManager(ctrl)

			var preparedDir string

			fetchErr := errors.New("injected fetch failure")
			payloadErr := errors.New("injected payload failure")

			release := "7%{?dist}"
			if testCase.autorelease {
				release = autorelease
			}

			manager.EXPECT().FetchComponent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, comp components.Component, dir string, opts ...sourceproviders.FetchComponentOption) error {
					preparedDir = dir

					require.Equal(t, "curl", comp.GetName())
					stageLocklessFetchedSources(t, env.FS(), dir, release, opts)

					if testCase.failFetch {
						return fetchErr
					}

					return nil
				})

			if !testCase.failFetch {
				manager.EXPECT().FetchFiles(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, _ components.Component, dir string) error {
						require.Equal(t, preparedDir, dir)
						stageLocklessPreparePayloads(t, env.FS(), dir)

						if testCase.failPayload {
							return payloadErr
						}

						return nil
					})
			}

			newSourcePreparer = func(
				_ sourceproviders.SourceManager, fs opctx.FS, events opctx.EventListener, dry opctx.DryRunnable,
				options ...sources.PreparerOption,
			) (sources.SourcePreparer, error) {
				return sources.NewPreparer(manager, fs, events, dry, options...)
			}

			env.CmdFactory.RegisterCommandInSearchPath("rpmautospec")

			var autospecCalls []string

			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				require.Equal(t, "rpmautospec", cmd.Args[0])
				require.True(t, testCase.autorelease)
				require.Equal(t, preparedDir, cmd.Dir)

				autospecCalls = append(autospecCalls, cmd.Args[1])
				if cmd.Args[1] == "generate-changelog" {
					return "* Upstream changelog\n", nil
				}

				require.Equal(t, "calculate-release", cmd.Args[1])

				return "4", nil
			}
			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				t.Fatalf("unexpected finalization command: %v", cmd.Args)

				return nil
			}

			const output = "/external/output"
			require.NoError(t, fileutils.WriteFile(env.FS(), filepath.Join(output, "keep"),
				[]byte("original output"), fileperms.PublicFile))
			err := PrepareComponentSources(env.Env, &PrepareSourcesOptions{
				ComponentFilter: components.ComponentFilter{ComponentNamePatterns: []string{"curl"}},
				OutputDir:       output, Force: true,
				WithoutGitRepo: testCase.withoutGit, RPMDevBumpspec: testCase.bumpspec,
			})

			require.Equal(t, output, preparedDir)

			switch {
			case testCase.failFetch:
				require.ErrorIs(t, err, fetchErr)
			case testCase.failPayload || testCase.failArchive:
				if testCase.failPayload {
					require.ErrorIs(t, err, payloadErr)
				} else {
					require.ErrorContains(t, err, "extra-source.tar.gz")
				}

				specContent, err := fileutils.ReadFile(env.FS(), filepath.Join(preparedDir, "curl.spec"))
				require.NoError(t, err)
				assert.NotContains(t, string(specContent), "Summary: Prepared summary")

				content, err := fileutils.ReadFile(env.FS(), filepath.Join(preparedDir, "generated-input.txt"))
				require.NoError(t, err)
				assert.Equal(t, "downloaded", string(content))
			default:
				require.NoError(t, err)

				wantRelease := release
				if testCase.autorelease {
					wantRelease = "%autorelease -b 4"

					assert.Equal(t, []string{"generate-changelog", "calculate-release"}, autospecCalls)
				}

				assertLocklessPreparedSourceTree(t, env.FS(), preparedDir, wantRelease, hash)
			}

			wantGit := !testCase.withoutGit || err != nil
			for _, name := range []string{".git/HEAD", "unreferenced"} {
				exists, err := fileutils.Exists(env.FS(), filepath.Join(preparedDir, name))
				require.NoError(t, err)
				assert.Equal(t, name != ".git/HEAD" || wantGit, exists, "unexpected prepared file: %s", name)
			}

			exists, err := fileutils.Exists(env.FS(), filepath.Join(output, "keep"))
			require.NoError(t, err)
			assert.False(t, exists, "--force must remove existing output")

			exists, err = fileutils.Exists(env.FS(), filepath.Join(preparedDir, "extra-source.tar.gz"))
			require.NoError(t, err)
			assert.Equal(t, !testCase.failFetch, exists)
			exists, err = fileutils.Exists(env.FS(), filepath.Join(output, "c/curl"))
			require.NoError(t, err)
			assert.False(t, exists)
		})
	}
}

func TestLocklessPrepareSourcesRejectsNonemptyOutputWithoutForce(t *testing.T) {
	env := testutils.NewTestEnv(t)
	env.Config.LocklessComponents = map[string]bool{"curl": true}
	env.Config.Components["curl"] = projectconfig.ComponentConfig{
		Name: "curl", Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal},
	}

	const output = "/external/output"
	require.NoError(t, fileutils.WriteFile(env.FS(), filepath.Join(output, "keep"),
		[]byte("original output"), fileperms.PublicFile))

	originalPreparer := newSourcePreparer
	newSourcePreparer = func(
		_ sourceproviders.SourceManager, _ opctx.FS, _ opctx.EventListener, _ opctx.DryRunnable,
		_ ...sources.PreparerOption,
	) (sources.SourcePreparer, error) {
		t.Fatal("nonempty output must be rejected before source preparation")

		return nil, errors.New("unexpected source preparer construction")
	}

	t.Cleanup(func() { newSourcePreparer = originalPreparer })

	err := PrepareComponentSources(env.Env, &PrepareSourcesOptions{
		ComponentFilter: components.ComponentFilter{ComponentNamePatterns: []string{"curl"}},
		OutputDir:       output,
	})
	require.ErrorContains(t, err, "--force")
	content, err := fileutils.ReadFile(env.FS(), filepath.Join(output, "keep"))
	require.NoError(t, err)
	assert.Equal(t, "original output", string(content))
}

func TestLocklessPrepareSourcesDryRunPreservesForcedOutput(t *testing.T) {
	env := testutils.NewTestEnv(t)
	env.Config.LocklessComponents = map[string]bool{"curl": true}
	env.Config.Components["curl"] = projectconfig.ComponentConfig{
		Name: "curl", Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal},
	}
	dryEnv := azldev.NewEnv(t.Context(), azldev.EnvOptions{
		ProjectDir: env.Env.ProjectDir(), Config: env.Config, Interfaces: env.TestInterfaces,
		EventListener: env.EventListener, DryRunnable: azldev.NewAppDryRunnable(true),
	})

	const output = "/external/output"
	require.NoError(t, fileutils.WriteFile(env.FS(), filepath.Join(output, "keep"),
		[]byte("original output"), fileperms.PublicFile))

	originalPreparer := newSourcePreparer
	newSourcePreparer = func(
		_ sourceproviders.SourceManager, _ opctx.FS, _ opctx.EventListener, _ opctx.DryRunnable,
		_ ...sources.PreparerOption,
	) (sources.SourcePreparer, error) {
		t.Fatal("dry run must not prepare sources")

		return nil, errors.New("unexpected source preparer construction")
	}

	t.Cleanup(func() { newSourcePreparer = originalPreparer })

	require.NoError(t, PrepareComponentSources(dryEnv, &PrepareSourcesOptions{
		ComponentFilter: components.ComponentFilter{ComponentNamePatterns: []string{"curl"}},
		OutputDir:       output,
		Force:           true,
	}))

	content, err := fileutils.ReadFile(env.FS(), filepath.Join(output, "keep"))
	require.NoError(t, err)
	assert.Equal(t, "original output", string(content))
}

func TestLocklessPrepareSourcesUsesWorkingInputs(t *testing.T) {
	const fetchCommand = "fetch"

	for _, inputDir := range []string{"", "draft"} {
		t.Run("inputs/"+inputDir, func(t *testing.T) {
			env, repo, hashes := locklessWorkingInputsFixture(t, inputDir)
			originalRepo, originalPreparer := openRenderRepo, newSourcePreparer
			openRenderRepo = func(_ *azldev.Env) (*gogit.Repository, string, error) {
				return repo, renderTestProjectRoot, nil
			}

			t.Cleanup(func() {
				openRenderRepo = originalRepo
				newSourcePreparer = originalPreparer
			})

			var upstreamRanges []string

			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				require.Equal(t, "git", cmd.Args[0])

				switch cmd.Args[3] {
				case "--literal-pathspecs":
					assert.Equal(t, []string{
						"git", "-C", "/project", "--literal-pathspecs", "log", "-1", "--format=%H",
						"--end-of-options", hashes[1], "--", "specs/c/curl/",
					}, cmd.Args)

					return hashes[0], nil
				case fetchCommand:
					assert.Equal(t, "+refs/heads/f40:refs/remotes/origin/f40", cmd.Args[len(cmd.Args)-1])

					return "", nil
				case "rev-parse":
					assert.Equal(t, "aaaaaaa^{commit}", cmd.Args[len(cmd.Args)-1])

					return "aaaaaaa", nil
				case "log":
					upstreamRanges = append(upstreamRanges, cmd.Args[len(cmd.Args)-2])

					return "ccccccc Working upstream change\n", nil
				default:
					t.Fatalf("unexpected preparation command: %v", cmd.Args)

					return "", errors.New("unexpected preparation command")
				}
			}
			env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
				t.Fatalf("unexpected finalization command: %v", cmd.Args)

				return nil
			}

			history, err := readLastRender(env.Env, repo, "/project", "specs/c/curl", "curl", true)
			require.NoError(t, err)
			assert.Equal(t, "aaaaaaa", history.previous)
			assert.Equal(t, "ccccccc", history.current)
			assert.Equal(t, "f40", history.previousBranch)
			assert.Equal(t, "f42", history.currentBranch)
			helper, err := history.tree.File("macros.inc")
			require.NoError(t, err)
			content, err := helper.Contents()
			require.NoError(t, err)
			assert.Equal(t, "committed helper", content)

			_, err = history.tree.File("untracked.txt")
			require.Error(t, err)

			manager := sourceproviders_test.NewMockSourceManager(gomock.NewController(t))
			manager.EXPECT().FetchComponent(gomock.Any(), gomock.Any(), "/external/output", gomock.Any()).DoAndReturn(
				func(_ context.Context, comp components.Component, dir string, _ ...sourceproviders.FetchComponentOption) error {
					assert.Equal(t, "ccccccc", comp.GetConfig().Spec.UpstreamCommit)

					return fileutils.WriteFile(env.FS(), filepath.Join(dir, "curl.spec"),
						[]byte(renderTestSpec("1", "1", "* Upstream changelog\n")), fileperms.PublicFile)
				})
			manager.EXPECT().FetchFiles(gomock.Any(), gomock.Any(), "/external/output").Return(nil)

			newSourcePreparer = func(
				_ sourceproviders.SourceManager, fs opctx.FS, events opctx.EventListener, dry opctx.DryRunnable,
				options ...sources.PreparerOption,
			) (sources.SourcePreparer, error) {
				return sources.NewPreparer(manager, fs, events, dry, options...)
			}

			require.NoError(t, PrepareComponentSources(env.Env, &PrepareSourcesOptions{
				ComponentFilter: components.ComponentFilter{ComponentNamePatterns: []string{"curl"}},
				OutputDir:       "/external/output",
			}))
			assert.Equal(t, []string{"aaaaaaa..ccccccc"}, upstreamRanges)

			prepared, err := fileutils.ReadFile(env.FS(), "/external/output/curl.spec")
			require.NoError(t, err)
			assert.Contains(t, string(prepared), "Release: 9\n")
			assert.Contains(t, string(prepared), "* Committed changelog\n")
			assert.NotContains(t, string(prepared), "* Upstream changelog\n")

			copied, err := fileutils.ReadFile(env.FS(), "/external/output/overlay-input.txt")
			require.NoError(t, err)
			assert.Equal(t, "working overlay input", string(copied))

			macros, err := fileutils.ReadFile(env.FS(), "/external/output/curl.azl.macros")
			require.NoError(t, err)
			assert.Contains(t, string(macros), "%working_input working\n")
		})
	}
}

func locklessWorkingInputsFixture(
	t *testing.T, inputDir string,
) (env *testutils.TestEnv, repo *gogit.Repository, hashes []string) {
	t.Helper()

	const (
		project = `[project]
default-distro = { name = "test", version = "3" }
[distros.test.versions."1"]
release-ver = "1"
dist-git-branch = "f40"
[distros.test.versions."2"]
release-ver = "2"
dist-git-branch = "f41"
[distros.test.versions."3"]
release-ver = "3"
dist-git-branch = "f42"
`
		component = `[components.curl.spec]
type = 'upstream'
upstream-distro = { name = 'test', version = '1' }
[components.curl.build.defines]
working_input = 'committed'
[[components.curl.overlays]]
type = 'file-add'
file = 'overlay-input.txt'
source = 'input.txt'
`
	)

	root := "includes = [\"component.toml\", \"pins/*.toml\"]\n" + project
	repo, hashes = testRepoWithCommits(t, []testRepoCommit{
		{files: map[string][]byte{
			"azldev.toml":             []byte(root),
			"lockless-components.txt": []byte("curl\n"),
			"component.toml":          []byte(component),
			"pins/curl.toml":          []byte("[components.curl.spec]\nupstream-commit = 'aaaaaaa'\n"),
			"input.txt":               []byte("committed overlay input"),
			"specs/c/curl/curl.spec":  []byte(renderTestSpec("1", "9", "* Committed changelog\n")),
			"specs/c/curl/macros.inc": []byte("committed helper"),
		}},
		{files: map[string][]byte{
			"component.toml": []byte(strings.Replace(component, "version = '1'", "version = '2'", 1)),
			"pins/curl.toml": []byte("[components.curl.spec]\nupstream-commit = 'bbbbbbb'\n"),
		}},
	})
	env = testutils.NewTestEnv(t)
	tree, err := resolveTree(repo, hashes[1])
	require.NoError(t, err)
	require.NoError(t, copyTreeToFS(tree, env.FS(), "/project"))

	workingComponent := strings.Replace(component, "version = '1'", "version = '3'", 1)
	workingComponent = strings.Replace(workingComponent, "'committed'", "'working'", 1)
	working := map[string][]byte{
		"azldev.toml": []byte(fmt.Sprintf("includes = [%q, %q]\n%s",
			filepath.Join(inputDir, "component.toml"), filepath.Join(inputDir, "pins/*.toml"), project)),
		filepath.Join(inputDir, "component.toml"): []byte(workingComponent),
		filepath.Join(inputDir, "pins/curl.toml"): []byte("[components.curl.spec]\nupstream-commit = 'ccccccc'\n"),
		filepath.Join(inputDir, "input.txt"):      []byte("working overlay input"),
		"specs/c/curl/curl.spec":                  []byte("invalid uncommitted dist-git spec"),
		"specs/c/curl/macros.inc":                 []byte("uncommitted helper"),
		"specs/c/curl/untracked.txt":              []byte("untracked dist-git sidecar"),
	}
	worktree, err := repo.Worktree()
	require.NoError(t, err)

	for path, content := range working {
		require.NoError(t, fileutils.WriteFile(env.FS(), filepath.Join("/project", path), content, fileperms.PublicFile))
		file, err := worktree.Filesystem.Create(path)
		require.NoError(t, err)
		_, err = file.Write(content)
		require.NoError(t, err)
		require.NoError(t, file.Close())
	}

	_, config, err := projectconfig.LoadProjectConfig(env.FS(), env.Env.OSEnv(),
		"/project", true, "/work", nil, false, false)
	require.NoError(t, err)

	*env.Config = *config

	return env, repo, hashes
}

func TestLocklessPrepareSourcesFlags(t *testing.T) {
	const helloSHA256 = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	for _, testCase := range []struct {
		name        string
		options     PrepareSourcesOptions
		missingHash bool
		missingType bool
		archive     bool
		wantError   string
	}{
		{name: "skip overlays", options: PrepareSourcesOptions{SkipOverlays: true}},
		{name: "skip sources", options: PrepareSourcesOptions{SkipSources: true}, archive: true},
		{name: "skip both", options: PrepareSourcesOptions{SkipOverlays: true, SkipSources: true}},
		{name: "allow missing hash", options: PrepareSourcesOptions{AllowNoHashes: true}, missingHash: true},
		{
			name: "default hash algorithm", options: PrepareSourcesOptions{AllowNoHashes: true},
			missingHash: true, missingType: true,
		},
		{name: "missing hash rejected", missingHash: true, wantError: "missing required 'hash'"},
		{
			name: "missing hash needs payload", options: PrepareSourcesOptions{AllowNoHashes: true, SkipSources: true},
			missingHash: true, wantError: "downloads were skipped",
		},
		{
			name:    "skip overlays leaves missing hash unresolved",
			options: PrepareSourcesOptions{AllowNoHashes: true, SkipOverlays: true}, missingHash: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ref := projectconfig.SourceFileReference{
				Filename: "extra-source.tar.gz", Hash: helloSHA256, HashType: fileutils.HashTypeSHA256,
				Origin: projectconfig.Origin{Type: projectconfig.OriginTypeURI, Uri: "https://example.invalid/source"},
			}
			if testCase.missingHash {
				ref.Hash = ""
			}

			if testCase.missingType {
				ref.HashType = ""
			}

			env, dir, err := runLocklessPrepareFlagFixture(t, testCase.options, ref, testCase.archive)
			if testCase.wantError != "" {
				require.ErrorContains(t, err, testCase.wantError)

				return
			}

			require.NoError(t, err)
			exists, err := fileutils.Exists(env.FS(), filepath.Join(dir, ref.Filename))
			require.NoError(t, err)
			assert.Equal(t, !testCase.options.SkipSources, exists)
			exists, err = fileutils.Exists(env.FS(), filepath.Join(dir, "curl.azl.macros"))
			require.NoError(t, err)
			assert.Equal(t, !testCase.options.SkipOverlays, exists)

			content, err := fileutils.ReadFile(env.FS(), filepath.Join(dir, "sources"))
			require.NoError(t, err)
			specContent, err := fileutils.ReadFile(env.FS(), filepath.Join(dir, "curl.spec"))
			require.NoError(t, err)

			if testCase.options.SkipOverlays {
				assert.Equal(t, "# upstream metadata\n", string(content))
				assert.Equal(t, renderTestSpec("1", "7", ""), string(specContent))
			} else {
				assert.Contains(t, string(specContent), "Summary: Prepared summary\n")

				hash, hashType := helloSHA256, fileutils.HashTypeSHA256
				if testCase.missingType {
					hashType = fileutils.HashTypeSHA512
					hash = "309ecc489c12d6eb4cc40f50c902f2b4d0ed77ee511a7c7a9bcd3ca86d4cd86f" +
						"989dd35bc5ff499670da34255b45b0cfd830e81f605dcf7dc5542e93ae9cd76f"
				}

				assert.Contains(t, string(content), string(hashType)+" ("+ref.Filename+") = "+hash)
			}
		})
	}
}

func runLocklessPrepareFlagFixture(
	t *testing.T, options PrepareSourcesOptions, ref projectconfig.SourceFileReference, archiveOverlay bool,
) (env *testutils.TestEnv, dir string, err error) {
	t.Helper()

	env = testutils.NewTestEnv(t)
	env.Config.LocklessComponents = map[string]bool{"curl": true}

	config := projectconfig.ComponentConfig{
		Name: "curl", Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal},
		Build:       projectconfig.ComponentBuildConfig{Defines: map[string]string{"prepare_fixture": "ready"}},
		SourceFiles: []projectconfig.SourceFileReference{ref},
		Overlays: []projectconfig.ComponentOverlay{{
			Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Summary", Value: "Prepared summary",
		}},
	}
	if archiveOverlay {
		config.Overlays = append(config.Overlays, projectconfig.ComponentOverlay{
			Type: projectconfig.ComponentOverlayRemoveFile, Archive: "upstream.tar.gz", Filename: "vendor/**",
		})
		config.SourceFiles = append(config.SourceFiles, projectconfig.SourceFileReference{
			Filename: "upstream.tar.gz", Hash: strings.Repeat("a", 64), HashType: fileutils.HashTypeSHA256,
			Origin: projectconfig.Origin{Type: projectconfig.OriginTypeOverlay},
		})
	}

	env.Config.Components["curl"] = config
	repo, _ := testRepoWithCommits(t, []testRepoCommit{{files: map[string][]byte{"README": []byte("project")}}})
	originalRepo, originalPreparer := openRenderRepo, newSourcePreparer
	openRenderRepo = func(_ *azldev.Env) (*gogit.Repository, string, error) {
		return repo, renderTestProjectRoot, nil
	}

	t.Cleanup(func() {
		openRenderRepo = originalRepo
		newSourcePreparer = originalPreparer
	})
	manager := sourceproviders_test.NewMockSourceManager(gomock.NewController(t))
	manager.EXPECT().FetchComponent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(
			_ context.Context, _ components.Component, fetchedDir string, opts ...sourceproviders.FetchComponentOption,
		) error {
			dir = fetchedDir

			var resolved sourceproviders.FetchComponentOptions
			for _, opt := range opts {
				opt(&resolved)
			}

			assert.Equal(t, options.SkipSources, resolved.SkipLookaside)
			assert.True(t, resolved.PreserveGitDir)
			assert.True(t, resolved.SkipLocalSpecConfig)
			require.NoError(t, fileutils.WriteFile(env.FS(), filepath.Join(dir, "curl.spec"),
				[]byte(renderTestSpec("1", "7", "")), fileperms.PublicFile))
			require.NoError(t, fileutils.WriteFile(env.FS(), filepath.Join(dir, ".git/HEAD"),
				[]byte("upstream history"), fileperms.PublicFile))

			return fileutils.WriteFile(env.FS(), filepath.Join(dir, "sources"),
				[]byte("# upstream metadata\n"), fileperms.PublicFile)
		})

	if !options.SkipSources {
		acquire := func(_ context.Context, _ components.Component, fetchedDir string) error {
			exists, err := fileutils.Exists(env.FS(), filepath.Join(fetchedDir, "curl.azl.macros"))
			require.NoError(t, err)
			assert.False(t, exists, "payload acquisition must precede macro-file setup")

			return fileutils.WriteFile(env.FS(), filepath.Join(fetchedDir, ref.Filename),
				[]byte("hello world"), fileperms.PublicFile)
		}
		manager.EXPECT().FetchFiles(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(acquire)
	}

	newSourcePreparer = func(
		_ sourceproviders.SourceManager, fs opctx.FS, events opctx.EventListener, dry opctx.DryRunnable,
		opts ...sources.PreparerOption,
	) (sources.SourcePreparer, error) {
		return sources.NewPreparer(manager, fs, events, dry, opts...)
	}
	options.OutputDir = "/external/output"
	options.ComponentFilter = components.ComponentFilter{ComponentNamePatterns: []string{"curl"}}
	err = PrepareComponentSources(env.Env, &options)

	return env, dir, err
}

func TestLocklessPrepareSourcesUsesConfiguredRenderHistory(t *testing.T) {
	for _, testCase := range []struct {
		name, configuredDir, historyPath, wantError string
	}{
		{name: "default", historyPath: "specs/c/Curl"},
		{name: "relative", configuredDir: "custom-specs", historyPath: "custom-specs/c/Curl"},
		{name: "absolute", configuredDir: "/project/SPECS", historyPath: "SPECS/c/Curl"},
		{name: "outside repository", configuredDir: "/external/specs", wantError: "escapes repository root"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			env.Config.Project.RenderedSpecsDir = testCase.configuredDir

			files := map[string][]byte{"README": []byte("project")}
			if testCase.historyPath != "" {
				files[testCase.historyPath+"/Curl.spec"] = []byte(renderTestSpec("1", "9", ""))
			}

			repo, hashes := testRepoWithCommits(t, []testRepoCommit{{files: files}})
			originalRepo := openRenderRepo
			openRenderRepo = func(_ *azldev.Env) (*gogit.Repository, string, error) {
				return repo, renderTestProjectRoot, nil
			}

			t.Cleanup(func() { openRenderRepo = originalRepo })

			ctrl := gomock.NewController(t)
			component := components_testutils.NewMockComponent(ctrl)
			component.EXPECT().GetName().AnyTimes().Return("Curl")
			component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
				Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream},
			})

			historyErr := errors.New("injected history lookup failure")
			env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
				assert.Equal(t, []string{
					"git", "-C", renderTestProjectRoot, "--literal-pathspecs", "log", "-1", "--format=%H",
					"--end-of-options", hashes[0], "--", testCase.historyPath + "/",
				}, cmd.Args)

				return "", historyErr
			}

			err := prepareFetchedLocklessSources(env.Env, component, "/work/staging", spec.EditorLegacy)
			if testCase.wantError != "" {
				require.ErrorContains(t, err, testCase.wantError)
			} else {
				require.ErrorIs(t, err, historyErr)
			}
		})
	}
}

func assertLocklessPreparedSourceTree(t *testing.T, fs opctx.FS, dir, release, hash string) {
	t.Helper()

	specContent, err := fileutils.ReadFile(fs, filepath.Join(dir, "curl.spec"))
	require.NoError(t, err)
	assert.Contains(t, string(specContent), "Summary: Prepared summary\n")
	assert.Equal(t, 1, strings.Count(string(specContent), "%{load:"))
	assert.Contains(t, string(specContent), "Release: "+release+"\n")

	content, err := fileutils.ReadFile(fs, filepath.Join(dir, "generated-input.txt"))
	require.NoError(t, err)
	assert.Equal(t, "overlaid", string(content))

	manifest, err := fileutils.ReadFile(fs, filepath.Join(dir, "sources"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), "SHA512 (extra-source.tar.gz) = "+hash)
}

func stageLocklessFetchedSources(
	t *testing.T, fs opctx.FS, dir, release string, opts []sourceproviders.FetchComponentOption,
) {
	t.Helper()

	var resolved sourceproviders.FetchComponentOptions
	for _, opt := range opts {
		opt(&resolved)
	}

	assert.True(t, resolved.PreserveGitDir)
	assert.False(t, resolved.SkipLookaside)
	assert.True(t, resolved.SkipLocalSpecConfig)
	require.NoError(t, fileutils.WriteFile(fs, filepath.Join(dir, "curl.spec"),
		[]byte(renderTestSpec("1", release, "%autochangelog\n")), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(fs, filepath.Join(dir, ".git/HEAD"),
		[]byte("upstream history"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(fs, filepath.Join(dir, "unreferenced"),
		[]byte("keep sidecar"), fileperms.PublicFile))
}

func stageLocklessPreparePayloads(t *testing.T, fs opctx.FS, dir string) {
	t.Helper()

	head, err := fileutils.ReadFile(fs, filepath.Join(dir, ".git/HEAD"))
	require.NoError(t, err)
	assert.Equal(t, "upstream history", string(head), "Git metadata must remain during preparation")

	specContent, err := fileutils.ReadFile(fs, filepath.Join(dir, "curl.spec"))
	require.NoError(t, err)
	assert.NotContains(t, string(specContent), "%{load:")
	assert.NotContains(t, string(specContent), "Source9999:")
	assert.NotContains(t, string(specContent), "Summary: Prepared summary")

	exists, err := fileutils.Exists(fs, filepath.Join(dir, "curl.azl.macros"))
	require.NoError(t, err)
	assert.False(t, exists, "payload acquisition must precede macro-file setup")
	require.NoError(t, fileutils.WriteFile(fs, filepath.Join(dir, "extra-source.tar.gz"),
		[]byte("source payload"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(fs, filepath.Join(dir, "generated-input.txt"),
		[]byte("downloaded"), fileperms.PublicFile))
}
