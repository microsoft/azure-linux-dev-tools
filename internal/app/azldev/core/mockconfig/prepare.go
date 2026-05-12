// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package mockconfig prepares a per-build mock config directory that injects
// TOML-derived RPM repository definitions into mock without modifying any
// checked-in mock config files.
//
// The approach takes advantage of mock's built-in extensibility:
//
//  1. mock loads `site-defaults.cfg` from `--configdir` *before* the chroot
//     `.cfg`, then enables Jinja expansion only after all configs have loaded
//     (see mockbuild/config.py:load_config). We use this to seed
//     `config_opts['azl_repos']` with a list-of-dicts derived from TOML.
//  2. The chroot `.cfg`'s `include('foo.tpl')` calls are resolved relative to
//     `config_opts["config_path"]` (i.e., `--configdir`), so symlinking the
//     `.cfg`/`.tpl` files into a temp configdir lets includes keep working
//     unchanged (see mockbuild/config.py:include).
//  3. The `.tpl` then iterates `azl_repos` via Jinja inside
//     `config_opts['dnf.conf']`, rendering one `[name]` block per repo.
//
// This avoids both passing dicts via `--config-opts` (which only supports
// strings/bools/ints) and rewriting the `.tpl` at runtime.
package mockconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// PrepareForRPMBuild prepares a per-build mock config directory wired up with
// the repos configured for the active distro version's `inputs.rpm-build`.
//
// The returned path lives inside a freshly allocated temp directory under
// [azldev.Env.WorkDir]; that directory contains symlinks back to the original
// `.cfg`/`.tpl` files plus a generated `site-defaults.cfg` that sets
// `config_opts['azl_repos'] = [...]`.
//
// The site-defaults file is **always** generated, even when `inputs.rpm-build`
// is empty (in which case it sets `azl_repos = []` and a warning is logged).
// This keeps the chroot template's `{% for r in azl_repos %}` loop happy
// regardless of whether a particular distro version has opted into TOML-driven
// repo injection. Templates that don't reference `azl_repos` (e.g., the
// hand-maintained stage1 mock) are unaffected.
//
// Pass the returned path to [mock.NewRunner] (which derives `--configdir` from
// `filepath.Dir(returnedPath)`).
//
// Returns an error if any referenced repo name does not resolve to a defined
// resource in [projectconfig.ResourcesConfig], or if the staging itself fails.
func PrepareForRPMBuild(env *azldev.Env) (string, error) {
	_, distroVerDef, err := env.Distro()
	if err != nil {
		return "", fmt.Errorf("failed to resolve distro for mock config preparation:\n%w", err)
	}

	srcCfgPath := distroVerDef.MockConfigPath
	if srcCfgPath == "" {
		return "", errors.New("no mock config file configured for active distro version")
	}

	cfg := env.Config()
	if cfg == nil {
		return "", errors.New("no project config loaded")
	}

	repoNames := distroVerDef.Inputs.RpmBuild

	repos, err := resolveRepos(cfg.Resources.RpmRepos, repoNames)
	if err != nil {
		return "", err
	}

	// Mock chroots only ever target the host architecture (mock-config-x86_64 vs
	// mock-config-aarch64 is selected by runtime.GOARCH at config-load time). Apply
	// the same arch filter here so a repo restricted to a different arch is silently
	// omitted instead of forcing an unusable URL into the chroot.
	mockArch := goArchToRPMArch(runtime.GOARCH)

	filtered := repos[:0:len(repos)]

	for _, repo := range repos {
		if !repo.Repo.IsAvailableForArch(mockArch) {
			slog.Warn("Skipping rpm-repo for rpm-build (arch mismatch)",
				"repo", repo.Name, "mockArch", mockArch, "repoArches", repo.Repo.Arches)

			continue
		}

		filtered = append(filtered, repo)
	}

	repos = filtered

	if len(repoNames) == 0 {
		// Stage1-style configs: hand-maintained mock template owns its own repos.
		// We still stage the configdir + emit `azl_repos = []` so the template can
		// rely on the variable existing if it ever decides to iterate it.
		slog.Info(
			"No TOML-driven rpm-build inputs configured; generated `azl_repos` will be empty " +
				"(this is expected for hand-maintained mock templates).",
		)
	} else if len(repos) == 0 {
		slog.Warn("All TOML-driven rpm-build inputs were filtered out for the host arch",
			"hostArch", mockArch)
	}

	return stageMockConfigDir(env, srcCfgPath, repos)
}

// goArchToRPMArch maps Go's GOARCH values to the rpm/mock arch naming convention used
// in [RpmRepoResource.Arches]. Unknown values are returned unchanged so tests/dev
// environments on unusual platforms still get some signal in log messages.
func goArchToRPMArch(goArch string) string {
	switch goArch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return goArch
	}
}

// namedRepo pairs an [projectconfig.RpmRepoResource] with the TOML map key it
// was registered under. The key is the canonical repo identifier used both for
// the dnf section name and for log messages.
type namedRepo struct {
	Name string
	Repo projectconfig.RpmRepoResource
}

// resolveRepos looks up each name in `names` in the resources map.
// Returns the resolved repos in the order given.
func resolveRepos(
	defs map[string]projectconfig.RpmRepoResource,
	names []string,
) ([]namedRepo, error) {
	resolved := make([]namedRepo, 0, len(names))

	for _, name := range names {
		repo, ok := defs[name]
		if !ok {
			return nil, fmt.Errorf(
				"rpm repo %q referenced from inputs is not defined under [resources.rpm-repos]",
				name,
			)
		}

		resolved = append(resolved, namedRepo{Name: name, Repo: repo})
	}

	return resolved, nil
}

// stageMockConfigDir allocates a temp configdir under env.WorkDir(), symlinks
// the source .cfg + .tpl files into it, generates site-defaults.cfg, and
// returns the path to the staged .cfg file.
func stageMockConfigDir(
	env *azldev.Env,
	srcCfgPath string,
	repos []namedRepo,
) (string, error) {
	envFS := env.FS()

	tempDir, err := fileutils.MkdirTemp(envFS, env.WorkDir(), "azldev-mock-config-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp mock config dir:\n%w", err)
	}

	srcDir := filepath.Dir(srcCfgPath)

	entries, err := fileutils.ReadDir(envFS, srcDir)
	if err != nil {
		return "", fmt.Errorf("failed to read source mock config dir %q:\n%w", srcDir, err)
	}

	// Stage the chroot .cfg itself plus any .tpl files it might include().
	// We deliberately don't copy arbitrary files that may live in the same
	// directory (the mock config dir might be co-located with project files
	// in some layouts).
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if name == siteDefaultsFileName {
			continue
		}

		ext := filepath.Ext(name)
		if ext != ".cfg" && ext != ".tpl" {
			continue
		}

		src := filepath.Join(srcDir, name)
		dst := filepath.Join(tempDir, name)

		if symErr := fileutils.SymLinkOrCopy(env, envFS, src, dst, fileutils.CopyFileOptions{}); symErr != nil {
			return "", fmt.Errorf("failed to stage mock config file %q:\n%w", name, symErr)
		}
	}

	siteDefaultsPath := filepath.Join(tempDir, siteDefaultsFileName)

	siteDefaultsContent := []byte(renderSiteDefaults(repos))
	if writeErr := fileutils.WriteFile(envFS, siteDefaultsPath, siteDefaultsContent, siteDefaultsPerm); writeErr != nil {
		return "", fmt.Errorf("failed to write generated site-defaults.cfg:\n%w", writeErr)
	}

	stagedCfgPath := filepath.Join(tempDir, filepath.Base(srcCfgPath))

	return stagedCfgPath, nil
}

const (
	siteDefaultsFileName = "site-defaults.cfg"
	// siteDefaultsPerm is the file mode used for the generated site-defaults.cfg.
	// 0644 = owner read/write, group/other read; matches the mock config files
	// shipped by the mock package itself.
	siteDefaultsPerm = 0o644
)

// renderSiteDefaults produces a Python-syntax mock config fragment that sets
// `config_opts['azl_repos']` to a list of dicts. It is consumed by a Jinja
// `{% for r in azl_repos %}` loop inside the `.tpl`'s `dnf.conf` body.
//
// The dict keys map directly to dnf .repo file directives (`baseurl`,
// `metalink`, `gpgkey`, `gpgcheck`). The repo's [namedRepo.Name] (= TOML map
// key) drives the dnf section header and is also used as `name=` since dnf's
// name field is just a UI label.
//
// We deliberately do NOT project the repo's `description` into dnf — it'd
// require single-line validation and offers no functional value (dnf treats
// `name=` as opaque). `description` stays as a TOML-only diagnostic field.
func renderSiteDefaults(repos []namedRepo) string {
	var buf strings.Builder

	buf.WriteString("# GENERATED BY azldev — do not edit\n")
	buf.WriteString("# Sets config_opts['azl_repos'] from the active project's TOML configuration.\n")
	buf.WriteString("# The chroot template is expected to iterate this list to build dnf.conf.\n\n")
	buf.WriteString("config_opts['azl_repos'] = [\n")

	for _, repo := range repos {
		buf.WriteString("    {")
		writeStrField(&buf, "name", repo.Name)
		writeStrField(&buf, "baseurl", repo.Repo.BaseURI)
		writeStrField(&buf, "metalink", repo.Repo.Metalink)
		writeStrField(&buf, "gpgkey", repo.Repo.GPGKey)
		// Note: TOML field is `disable-gpg-check`; dnf wants `gpgcheck=1/0`.
		// Invert here so the dnf side stays normal-shaped.
		writeBoolField(&buf, "gpgcheck", !repo.Repo.DisableGPGCheck)
		buf.WriteString("},\n")
	}

	buf.WriteString("]\n")

	return buf.String()
}

func writeStrField(buf *strings.Builder, key, value string) {
	if value == "" {
		return
	}

	fmt.Fprintf(buf, " %s: %s,", pyRepr(key), pyRepr(value))
}

func writeBoolField(buf *strings.Builder, key string, value bool) {
	if value {
		fmt.Fprintf(buf, " %s: True,", pyRepr(key))
	} else {
		fmt.Fprintf(buf, " %s: False,", pyRepr(key))
	}
}

// pyRepr formats a string as a Python string literal safe for embedding inside a mock
// .cfg file (which Python loads via exec()). We delegate to encoding/json: every JSON
// string literal is also a valid Python string literal (both accept double-quoted
// strings with the same set of \escape sequences). Crucially this handles edge cases
// the previous handwritten escaper missed: NUL bytes, U+2028/U+2029 line separators,
// and arbitrary control characters.
func pyRepr(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		// json.Marshal of a string never fails in practice; log and fall back to a
		// double-quoted empty string to keep the generated file syntactically valid.
		slog.Error("json.Marshal failed for python string literal (treating as empty)", "error", err)

		return `""`
	}

	// json.Marshal already %u-escapes U+2028/U+2029 in modern Go, but be explicit
	// just in case its behavior changes.
	out := string(encoded)
	out = strings.ReplaceAll(out, "\u2028", `\u2028`)
	out = strings.ReplaceAll(out, "\u2029", `\u2029`)

	return out
}
