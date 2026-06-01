// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"
	"path"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/gitfs"
	"github.com/spf13/afero"
)

// historicDryRunnable reports that we are not in dry-run mode: the historic
// loader genuinely writes the embedded default configs into its in-memory
// scratch overlay.
type historicDryRunnable struct{}

func (historicDryRunnable) DryRun() bool { return false }

// historicOSEnv is a deliberately inert OS environment. Historic config loading
// must depend only on what is in the git tree, never on the host's working
// directory or user-level XDG config. Returning empty values causes the
// user-config lookup to resolve to nothing.
type historicOSEnv struct{}

func (historicOSEnv) Getwd() (string, error) { return "", nil }
func (historicOSEnv) Chdir(string) error     { return nil }
func (historicOSEnv) Getenv(string) string   { return "" }
func (historicOSEnv) IsCurrentUserMemberOf(string) (bool, error) {
	return false, nil
}
func (historicOSEnv) LookupGroupID(string) (int, error) { return 0, nil }

// LoadProjectConfigAtCommit loads the project configuration exactly as it
// existed at a specific commit in the project repository, without checking
// anything out to disk.
//
// It reads files through a read-only [gitfs.Fs] backed by the commit's tree,
// layered under an in-memory writable overlay so the loader can stage its
// embedded default configs. The resolved configuration therefore combines the
// commit's in-tree config with azldev's built-in embedded defaults; the latter
// are part of every load and are not drawn from the git tree. Host working
// directory and user-level config are intentionally excluded, so the only
// per-invocation input is the embedded defaults baked into the binary.
//
// referenceDir is interpreted relative to the tree root (e.g. the project
// subdirectory containing azldev.toml). Both absolute ("/sub") and relative
// ("sub") forms are accepted.
func LoadProjectConfigAtCommit(
	repo *gogit.Repository,
	commitHash plumbing.Hash,
	referenceDir string,
	permissiveConfigParsing bool,
) (projectDir string, config *ProjectConfig, err error) {
	base, err := gitfs.NewFromCommit(repo, commitHash)
	if err != nil {
		return "", nil, fmt.Errorf("failed to open git filesystem at commit %s:\n%w", commitHash, err)
	}

	// Layer a writable in-memory overlay so the loader can stage its embedded
	// default configs (and any other scratch writes) without touching the
	// read-only git tree underneath.
	fs := afero.NewCopyOnWriteFs(base, afero.NewMemMapFs())

	// Interpret referenceDir relative to the git tree root, never the host
	// process working directory. path.Join against "/" makes relative forms
	// ("sub", "./sub") and absolute forms ("/sub") resolve identically; an
	// empty referenceDir collapses to the tree root "/".
	referenceDir = path.Join("/", referenceDir)

	return LoadProjectConfig(
		historicDryRunnable{},
		fs,
		historicOSEnv{},
		referenceDir,
		false, // disableDefaultConfig: defaults are part of resolved overlays.
		"",    // tempDirPath: empty lets the loader pick a default temp dir.
		nil,   // extraConfigFilePaths: none for historic loads.
		permissiveConfigParsing,
	)
}

// ResolveComponentOverlaysAtCommit loads the project config as of the given
// commit and returns the resolved overlays for the named component, combining
// project-level defaults, component-group defaults, and the component's own
// overlays.
//
// Distro-level default overlays are intentionally excluded: resolving them
// requires distro/version selection (which depends on the live invocation, not
// the historic tree), and distro defaults are not used for version-setting
// overlays. This keeps historic resolution self-contained and deterministic.
//
// Returns (nil, nil) when the component is absent at that commit.
func ResolveComponentOverlaysAtCommit(
	repo *gogit.Repository,
	commitHash plumbing.Hash,
	referenceDir string,
	componentName string,
	permissiveConfigParsing bool,
) ([]ComponentOverlay, error) {
	_, config, err := LoadProjectConfigAtCommit(repo, commitHash, referenceDir, permissiveConfigParsing)
	if err != nil {
		return nil, err
	}

	explicit, ok := config.Components[componentName]
	if !ok {
		return nil, nil
	}

	resolved, err := ResolveComponentConfig(
		explicit,
		config.DefaultComponentConfig,
		ComponentConfig{}, // distro defaults excluded; see doc comment.
		config.ComponentGroups,
		config.GroupsByComponent[componentName],
	)
	if err != nil {
		return nil, fmt.Errorf("resolving overlays for component %#q at commit %s:\n%w",
			componentName, commitHash, err)
	}

	return resolved.Overlays, nil
}
