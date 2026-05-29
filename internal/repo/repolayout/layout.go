// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package repolayout resolves rpm-repo-set templates and expands them into a
// concrete list of repo URLs for the `azldev repo` subcommands. Callers
// supply the templates map (typically `Resources.RpmRepoSetTemplates` from a
// loaded `*projectconfig.ProjectConfig`), so user/project overrides on top
// of the embedded defaults flow through naturally.
package repolayout

import (
	"errors"
	"fmt"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// DefaultTemplateName is the name of the built-in standard Azure Linux layout
// template defined in `defaultconfigs/content/defaults.toml`.
const DefaultTemplateName = "azl-standard"

// basearchPlaceholder is the substring expanded per arch in subrepo subpaths.
const basearchPlaceholder = "$basearch"

// DefaultArches is the per-arch expansion used when the user does not pass
// `--arch`.
//
//nolint:gochecknoglobals // effectively a constant; Go has no const slices.
var DefaultArches = []string{"x86_64", "aarch64"}

// InputRepo is one concrete (post-`$basearch`-expansion) upstream repo to query.
type InputRepo struct {
	// TemplateName is the rpm-repo-set-template the repo was expanded from.
	TemplateName string
	// SubrepoName is the [projectconfig.SubrepoSpec.Name] this repo was expanded from.
	SubrepoName string
	// Kind mirrors [projectconfig.SubrepoSpec.Kind], defaulted.
	Kind projectconfig.SubrepoKind
	// Arch is the substituted `$basearch` value, or "" when the subpath has no
	// `$basearch` (e.g., a single source/SRPM repo).
	Arch string
	// URL is the fully-resolved repo base URL.
	URL string
	// PrefixIndex is the 1-based position of the originating --repo-prefix among
	// all prefixes; 0 if not set by the caller.
	PrefixIndex int
	// PrefixCount is the total number of --repo-prefix values supplied; 0 if not
	// set by the caller. Used together with [InputRepo.PrefixIndex] to mint
	// human-readable repo ids that disambiguate multi-prefix runs.
	PrefixCount int
}

// ResolveTemplate looks up name in the supplied templates map (typically
// `Resources.RpmRepoSetTemplates` from a loaded project config, which already
// has the embedded defaults — `azl-standard`, `koji-dist-repo` — merged in
// along with any project/user overrides). Errors when the template is not
// defined. The returned template is a copy; callers may mutate it freely.
func ResolveTemplate(
	templates map[string]projectconfig.RpmRepoSetTemplate, name string,
) (projectconfig.RpmRepoSetTemplate, error) {
	if name == "" {
		return projectconfig.RpmRepoSetTemplate{}, errors.New("template name must not be empty")
	}

	if tmpl, ok := templates[name]; ok {
		return tmpl, nil
	}

	return projectconfig.RpmRepoSetTemplate{},
		fmt.Errorf("rpm-repo-set-template %#q is not defined", name)
}

// ExpandTemplate expands a template into one [InputRepo] per sub-repo, fanning
// out per arch where the subpath contains `$basearch`.
func ExpandTemplate(
	prefix, templateName string,
	tmpl projectconfig.RpmRepoSetTemplate,
	arches []string,
) []InputRepo {
	base := strings.TrimRight(prefix, "/")
	out := make([]InputRepo, 0, len(tmpl.Subrepos)*len(arches))

	for _, sub := range tmpl.Subrepos {
		kind := sub.Kind.Default()

		if strings.Contains(sub.Subpath, basearchPlaceholder) {
			for _, arch := range arches {
				out = append(out, InputRepo{
					TemplateName: templateName,
					SubrepoName:  sub.Name,
					Kind:         kind,
					Arch:         arch,
					URL:          base + "/" + strings.ReplaceAll(sub.Subpath, basearchPlaceholder, arch),
				})
			}

			continue
		}

		out = append(out, InputRepo{
			TemplateName: templateName,
			SubrepoName:  sub.Name,
			Kind:         kind,
			URL:          base + "/" + sub.Subpath,
		})
	}

	return out
}

// DedupInputRepos drops duplicate entries by URL while preserving order.
func DedupInputRepos(repos []InputRepo) []InputRepo {
	seen := make(map[string]struct{}, len(repos))
	out := make([]InputRepo, 0, len(repos))

	for _, repo := range repos {
		if _, ok := seen[repo.URL]; ok {
			continue
		}

		seen[repo.URL] = struct{}{}

		out = append(out, repo)
	}

	return out
}

// NormalizePrefix validates p as an http://, https://, or file:// URL and
// returns it with any trailing slash stripped. Bare paths are rejected.
func NormalizePrefix(prefix string) (string, error) {
	if prefix == "" {
		return "", errors.New("empty prefix")
	}

	if !strings.HasPrefix(prefix, "http://") &&
		!strings.HasPrefix(prefix, "https://") &&
		!strings.HasPrefix(prefix, "file://") {
		return "", fmt.Errorf("prefix %#q must be an http://, https://, or file:// URL", prefix)
	}

	return strings.TrimRight(prefix, "/"), nil
}
