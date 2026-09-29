// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package magerelease

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/magefile/mage/sh"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
)

var (
	ErrChangelog     = errors.New("changelog generation failed")
	ErrRelease       = errors.New("release tagging failed")
	rpmSpecVersionRE = regexp.MustCompile(`(?m)^Version:[\t ]*(\S+)[\t ]*$`)
)

const rpmSpecPath = "packaging/azldev.spec"

// Changelog generates a draft changelog section for the next release from the Conventional Commit
// history, prepends it to 'CHANGELOG.md', and synchronizes the version and release entry in the RPM
// spec.
//
// The output is a *draft*: review and curate it into user-facing notes before releasing. See
// docs/developer/how-to/releasing.md.
//
// git-cliff must be installed and on PATH; this target does not install it. The same target runs
// locally and in CI (CI just installs git-cliff first), so there is a single changelog flow.
func Changelog() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Generating changelog draft with git-cliff...")

	// git-cliff is an external (non-Go) tool installed separately, so invoke it by name and let
	// PATH resolution happen at execution time (the repo forbids exec.LookPath for tool checks).
	// Probe it once up front so a missing binary surfaces an actionable install hint.
	const cliff = "git-cliff"
	if _, err := sh.Output(cliff, "--version"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return mageutil.PrintAndReturnError(gitCliffInstallHint(), ErrChangelog, err)
		}

		return mageutil.PrintAndReturnError("git-cliff is installed but failed to run.", ErrChangelog, err)
	}

	projectDir := mageutil.AzldevProjectDir()
	configPath := filepath.Join(projectDir, "cliff.toml")
	changelogPath := filepath.Join(projectDir, "CHANGELOG.md")

	originalChangelog, err := os.ReadFile(changelogPath)
	if err != nil {
		return mageutil.PrintAndReturnError("Could not read CHANGELOG.md.", ErrChangelog, err)
	}

	// Guard against double-prepending: if CHANGELOG.md already starts with the version git-cliff
	// would bump to, the draft is already present. Re-running would add a duplicate section.
	bumped := bumpedVersion(cliff, configPath)

	top, topErr := latestChangelogVersion()
	if bumped != "" && topErr == nil && top == bumped {
		mageutil.MagePrintf(mageutil.MsgInfo,
			"CHANGELOG.md already has a draft for v%s; nothing to do "+
				"(run 'git restore CHANGELOG.md' to regenerate).\n", bumped)

		return syncRPMSpecFromChangelog(changelogPath)
	}

	// --bump computes the next version from the commits; --unreleased limits to commits since the
	// last release tag; --prepend inserts the new section at the top of the existing changelog.
	err = sh.Run(cliff, "--config", configPath, "--bump", "--unreleased", "--prepend", changelogPath)
	if err != nil {
		return mageutil.PrintAndReturnError("git-cliff failed to generate the changelog.", ErrChangelog, err)
	}

	updatedChangelog, err := os.ReadFile(changelogPath)
	if err != nil {
		return mageutil.PrintAndReturnError("Could not read the updated CHANGELOG.md.", ErrChangelog, err)
	}

	if bytes.Equal(originalChangelog, updatedChangelog) {
		mageutil.MagePrintln(mageutil.MsgInfo, "No releasable changes found; nothing to update.")

		return nil
	}

	if err := syncRPMSpecFromChangelog(changelogPath); err != nil {
		return mageutil.PrintAndReturnError("Could not update the RPM spec for the release.", ErrChangelog, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess,
		"Updated %#q and %#q. Review and curate the new changelog section before releasing.\n",
		changelogPath, rpmSpecPath)

	return nil
}

// Release creates the release tag from CHANGELOG.md. It reads the version from the top
// `## [X.Y.Z]` heading, verifies that the RPM spec has the same version, and creates a matching
// annotated git tag (vX.Y.Z) on HEAD. It does not push: pushing the tag is the point of no return
// for a release, so that stays an explicit step (manual locally, or a dedicated CI step).
//
// It is idempotent: if the changelog version is already tagged it does nothing, so it is safe to
// trigger on every merge to 'main' and only tags when the changelog carries a new version. Run it
// after the changelog PR has merged (see docs/developer/how-to/releasing.md).
func Release() error {
	mageutil.MagePrintln(mageutil.MsgStart, "Preparing release tag from CHANGELOG.md...")

	version, err := latestChangelogVersion()
	if err != nil {
		return mageutil.PrintAndReturnError("Could not read the release version from CHANGELOG.md.", ErrRelease, err)
	}

	if err := verifyRPMSpecVersion(version); err != nil {
		return mageutil.PrintAndReturnError("Could not verify the RPM spec version.", ErrRelease, err)
	}

	tag := "v" + version

	exists, err := tagExists(tag)
	if err != nil {
		return mageutil.PrintAndReturnError("Could not check existing tags.", ErrRelease, err)
	}

	if exists {
		mageutil.MagePrintf(mageutil.MsgInfo, "Tag %#q already exists; nothing to release.\n", tag)

		return nil
	}

	err = sh.Run("git", "tag", "-a", tag, "-m", tag)
	if err != nil {
		return mageutil.PrintAndReturnError(fmt.Sprintf("Failed to create tag %#q.", tag), ErrRelease, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess, "Created annotated tag %#q (matching CHANGELOG.md).\n", tag)
	mageutil.MagePrintf(mageutil.MsgInfo, "Push it to publish the release: git push origin %s\n", tag)
	mageutil.MagePrintf(mageutil.MsgInfo, "The tag is local only; to remove it before pushing: git tag -d %s\n", tag)

	return nil
}

// latestChangelogVersion returns the version from the first `## [X.Y.Z]` heading in CHANGELOG.md,
// which is the release being prepared.
func latestChangelogVersion() (string, error) {
	changelogPath := filepath.Join(mageutil.AzldevProjectDir(), "CHANGELOG.md")

	content, err := os.ReadFile(changelogPath)
	if err != nil {
		return "", fmt.Errorf("failed to read %#q:\n%w", changelogPath, err)
	}

	// Match a Keep a Changelog version heading, e.g. "## [0.2.0] - 2026-06-25".
	headingRe := regexp.MustCompile(`(?m)^##\s+\[(\d+\.\d+\.\d+)\]`)

	match := headingRe.FindStringSubmatch(string(content))
	if match == nil {
		return "", fmt.Errorf("no '## [X.Y.Z]' version heading found in %#q", changelogPath)
	}

	return match[1], nil
}

func syncRPMSpecFromChangelog(changelogPath string) error {
	changelog, err := os.ReadFile(changelogPath)
	if err != nil {
		return fmt.Errorf("read changelog %#q:\n%w", changelogPath, err)
	}

	version, releaseDate, err := parseLatestChangelogRelease(string(changelog))
	if err != nil {
		return err
	}

	specPath := filepath.Join(mageutil.AzldevProjectDir(), rpmSpecPath)

	spec, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("read RPM spec %#q:\n%w", specPath, err)
	}

	updated, err := updateRPMSpec(string(spec), version, releaseDate)
	if err != nil {
		return fmt.Errorf("update RPM spec %#q:\n%w", specPath, err)
	}

	if err := os.WriteFile(specPath, []byte(updated), fileperms.PublicFile); err != nil {
		return fmt.Errorf("write RPM spec %#q:\n%w", specPath, err)
	}

	return nil
}

func parseLatestChangelogRelease(content string) (version string, releaseDate time.Time, err error) {
	headingRe := regexp.MustCompile(`(?m)^##[\t ]+\[(\d+\.\d+\.\d+)\](?:[\t ]+-[\t ]+([^\r\n]+))?[\t ]*$`)

	match := headingRe.FindStringSubmatch(content)
	if match == nil || match[2] == "" {
		return "", time.Time{}, errors.New("no '## [X.Y.Z] - YYYY-MM-DD' release heading found in changelog")
	}

	releaseDate, err = time.Parse(time.DateOnly, strings.TrimSpace(match[2]))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("parse release date %#q:\n%w", match[2], err)
	}

	return match[1], releaseDate, nil
}

func verifyRPMSpecVersion(expectedVersion string) error {
	specPath := filepath.Join(mageutil.AzldevProjectDir(), rpmSpecPath)

	spec, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("read RPM spec %#q:\n%w", specPath, err)
	}

	if err := requireRPMSpecVersion(string(spec), expectedVersion); err != nil {
		return fmt.Errorf("validate RPM spec %#q:\n%w", specPath, err)
	}

	return nil
}

func requireRPMSpecVersion(content, expectedVersion string) error {
	actualVersion, err := parseRPMSpecVersion(content)
	if err != nil {
		return err
	}

	if actualVersion != expectedVersion {
		return fmt.Errorf("RPM spec version %#q does not match changelog version %#q", actualVersion, expectedVersion)
	}

	return nil
}

func parseRPMSpecVersion(content string) (string, error) {
	matches := rpmSpecVersionRE.FindAllStringSubmatch(content, -1)
	if len(matches) != 1 {
		return "", errors.New("spec must contain exactly one 'Version:' field")
	}

	return matches[0][1], nil
}

func updateRPMSpec(content, version string, releaseDate time.Time) (string, error) {
	if _, err := parseRPMSpecVersion(content); err != nil {
		return "", err
	}

	content = rpmSpecVersionRE.ReplaceAllString(content, "Version:        "+version)

	releaseRe := regexp.MustCompile(`(?m)^Release:[\t ]*\S+[\t ]*$`)
	if len(releaseRe.FindAllStringIndex(content, -1)) != 1 {
		return "", errors.New("spec must contain exactly one 'Release:' field")
	}

	content = releaseRe.ReplaceAllString(content, "Release:        1%{?dist}")

	entryRe := regexp.MustCompile(`(?m)^\* .+ - ` + regexp.QuoteMeta(version) + `-1$`)
	if entryRe.MatchString(content) {
		return content, nil
	}

	changelogRe := regexp.MustCompile(`(?m)^%changelog\s*$`)

	marker := changelogRe.FindStringIndex(content)
	if marker == nil {
		return "", errors.New("spec must contain a '%changelog' section")
	}

	entry := fmt.Sprintf("\n* %s Azure Linux Dev Tools <azldev@microsoft.com> - %s-1\n- Release azldev %s.\n",
		releaseDate.Format("Mon Jan 2 2006"), version, version)

	return content[:marker[1]] + entry + content[marker[1]:], nil
}

// tagExists reports whether the given git tag already exists locally.
func tagExists(tag string) (bool, error) {
	out, err := sh.Output("git", "tag", "--list", tag)
	if err != nil {
		return false, fmt.Errorf("failed to list git tags:\n%w", err)
	}

	return strings.TrimSpace(out) != "", nil
}

// gitCliffInstallHint builds the "git-cliff is missing" message, naming the version pinned in
// tools/git-cliff/Cargo.toml when it can be read.
func gitCliffInstallHint() string {
	const base = "git-cliff not found on PATH. The pin lives in tools/git-cliff/Cargo.toml."

	if version := pinnedGitCliffVersion(); version != "" {
		return fmt.Sprintf("%s Install it (e.g. 'cargo binstall git-cliff@%s' or 'brew install git-cliff'), "+
			"then re-run.", base, version)
	}

	return base + " Install it (e.g. 'cargo binstall git-cliff' or 'brew install git-cliff'), then re-run."
}

// bumpedVersion asks git-cliff for the version it would bump to next (without the leading "v"),
// or "" if it can't be determined (e.g. no eligible commits, or git-cliff fails).
func bumpedVersion(cliff, configPath string) string {
	out, err := sh.Output(cliff, "--config", configPath, "--bumped-version")
	if err != nil {
		return ""
	}

	return strings.TrimPrefix(strings.TrimSpace(out), "v")
}

// pinnedGitCliffVersion returns the git-cliff version pinned in tools/git-cliff/Cargo.toml, or ""
// if it can't be determined. The pin lives there (not in Go) so Dependabot and security scanners
// can track and update it.
func pinnedGitCliffVersion() string {
	cargoPath := filepath.Join(mageutil.AzldevProjectDir(), "tools", "git-cliff", "Cargo.toml")

	content, err := os.ReadFile(cargoPath)
	if err != nil {
		return ""
	}

	match := regexp.MustCompile(`(?m)^\s*git-cliff\s*=\s*"=?(\d+\.\d+\.\d+)"`).FindStringSubmatch(string(content))
	if match == nil {
		return ""
	}

	return match[1]
}
