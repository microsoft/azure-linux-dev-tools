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
	"net/url"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/samber/lo"
)

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
	// RepoID is the dnf repo id used to wire this slot up via --repofrompath /
	// --enablerepo. Callers are expected to set it before passing the row to
	// the dnf pipeline.
	RepoID string
	// GPGKey, when non-empty, is forwarded to dnf as --setopt=<id>.gpgkey=...
	// alongside gpgcheck=1. Only meaningful when the caller resolved this
	// repo from project config; the prefix-driven path leaves it empty.
	GPGKey string
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
	out := make([]InputRepo, 0, len(tmpl.Subrepos)*len(arches))

	for _, sub := range tmpl.Subrepos {
		kind := sub.Kind.Default()

		if strings.Contains(sub.Subpath, basearchPlaceholder) {
			for _, arch := range arches {
				joined, _ := url.JoinPath(prefix, strings.ReplaceAll(sub.Subpath, basearchPlaceholder, arch))
				out = append(out, InputRepo{
					TemplateName: templateName,
					SubrepoName:  sub.Name,
					Kind:         kind,
					Arch:         arch,
					URL:          joined,
				})
			}

			continue
		}

		joined, _ := url.JoinPath(prefix, sub.Subpath)
		out = append(out, InputRepo{
			TemplateName: templateName,
			SubrepoName:  sub.Name,
			Kind:         kind,
			URL:          joined,
		})
	}

	return out
}

// DedupInputRepos drops duplicate entries by URL while preserving order.
func DedupInputRepos(repos []InputRepo) []InputRepo {
	return lo.UniqBy(repos, func(r InputRepo) string { return r.URL })
}

// NormalizePrefix validates prefix as an http://, https://, or file:// URL.
// Bare paths are rejected. The returned string is the parsed URL re-serialized,
// so callers downstream can safely pass it to [url.JoinPath].
func NormalizePrefix(prefix string) (string, error) {
	if prefix == "" {
		return "", errors.New("empty prefix")
	}

	parsed, err := url.Parse(prefix)
	if err != nil {
		return "", fmt.Errorf("prefix %#q is not a valid URL: %w", prefix, err)
	}

	switch parsed.Scheme {
	case "http", "https", "file":
	default:
		return "", fmt.Errorf("prefix %#q must be an http://, https://, or file:// URL", prefix)
	}

	return parsed.String(), nil
}

// SubstituteBasearch replaces every `$basearch` occurrence in raw with arch.
// Used by the version-mode resolver to bake the host arch into a URL whose
// shape carries `$basearch` literally (dnf would substitute on its own, but
// our probe layer needs a concrete URL).
func SubstituteBasearch(raw, arch string) string {
	return strings.ReplaceAll(raw, basearchPlaceholder, arch)
}
