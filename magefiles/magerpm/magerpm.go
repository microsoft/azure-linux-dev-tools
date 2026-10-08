// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package magerpm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
)

var (
	errArchive = errors.New("RPM source archive creation failed")
	errSRPM    = errors.New("SRPM creation failed")
	versionRE  = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

const (
	specPath = "packaging/azldev.spec"

	// sourceRef is the git ref the deterministic archive is exported from. Packit checks out the
	// release tag before creating the archive, so HEAD points at the released commit.
	sourceRef = "HEAD"
)

// Archive creates a deterministic source archive with vendored Go modules.
func Archive(ctx context.Context, version string) error {
	version, err := normalizeVersion(version)
	if err != nil {
		return fmt.Errorf("%w:\n%w", errArchive, err)
	}

	projectDir := mageutil.AzldevProjectDir()
	outputDir := filepath.Join(mageutil.OutDir(), "rpm")
	archivePath := filepath.Join(outputDir, fmt.Sprintf("azldev-%s.tar.gz", version))
	archiveRoot := "azldev-" + version

	if err := os.MkdirAll(outputDir, fileperms.PublicDir); err != nil {
		return fmt.Errorf("%w: create output directory:\n%w", errArchive, err)
	}

	tempDir, err := os.MkdirTemp("", "azldev-rpm-")
	if err != nil {
		return fmt.Errorf("%w: create temporary directory:\n%w", errArchive, err)
	}

	defer func() { _ = os.RemoveAll(tempDir) }()

	gitArchivePath := filepath.Join(tempDir, "source.tar")
	if err := sh.Run("git", "-C", projectDir, "archive", "--format=tar", "--prefix", archiveRoot+"/",
		"--output", gitArchivePath, sourceRef); err != nil {
		return fmt.Errorf("%w: export tracked source from %#q:\n%w", errArchive, sourceRef, err)
	}

	if err := sh.Run("tar", "-xf", gitArchivePath, "-C", tempDir); err != nil {
		return fmt.Errorf("%w: extract Git archive:\n%w", errArchive, err)
	}

	sourceDir := filepath.Join(tempDir, archiveRoot)
	vendorCmd := exec.CommandContext(ctx, mg.GoCmd(), "mod", "vendor")
	vendorCmd.Dir = sourceDir
	vendorCmd.Stdout = os.Stdout

	vendorCmd.Stderr = os.Stderr
	if err := vendorCmd.Run(); err != nil {
		return fmt.Errorf("%w: vendor Go modules:\n%w", errArchive, err)
	}

	epoch, err := sourceDateEpoch(projectDir, sourceRef)
	if err != nil {
		return fmt.Errorf("%w: determine source date:\n%w", errArchive, err)
	}

	if err := sh.Run("tar", "--sort=name", "--mtime=@"+epoch, "--owner=0", "--group=0", "--numeric-owner",
		"--format=gnu", "-czf", archivePath, "-C", tempDir, archiveRoot); err != nil {
		return fmt.Errorf("%w: create source archive:\n%w", errArchive, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess, "Created RPM source archive at %s\n", archivePath)

	return nil
}

// SRPM creates a source RPM from the version declared in the RPM spec.
func SRPM(ctx context.Context) error {
	projectDir := mageutil.AzldevProjectDir()

	// Read the spec from the same committed ref the archive is exported from, so uncommitted
	// working-tree edits cannot leak into the SRPM.
	specContent, err := sh.Output("git", "-C", projectDir, "show", sourceRef+":"+specPath)
	if err != nil {
		return fmt.Errorf("%w: read spec from %#q:\n%w", errSRPM, sourceRef, err)
	}

	version, err := parseSpecVersion(specContent)
	if err != nil {
		return fmt.Errorf("%w: determine package version:\n%w", errSRPM, err)
	}

	if err := Archive(ctx, version); err != nil {
		return fmt.Errorf("%w:\n%w", errSRPM, err)
	}

	outputDir := filepath.Join(mageutil.OutDir(), "rpm")

	specFile := filepath.Join(outputDir, "azldev.spec")
	if err := os.WriteFile(specFile, []byte(specContent), fileperms.PublicFile); err != nil {
		return fmt.Errorf("%w: write spec file:\n%w", errSRPM, err)
	}

	if err := sh.Run("rpmbuild", "-bs", specFile,
		"--define", "_sourcedir "+outputDir,
		"--define", "_srcrpmdir "+outputDir); err != nil {
		return fmt.Errorf("%w: run rpmbuild:\n%w", errSRPM, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess, "Created source RPM in %s\n", outputDir)

	return nil
}

func sourceDateEpoch(projectDir, ref string) (string, error) {
	if value := strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH")); value != "" {
		epoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return "", fmt.Errorf("parse 'SOURCE_DATE_EPOCH' value %#q:\n%w", value, err)
		}

		if epoch < 0 {
			return "", fmt.Errorf("'SOURCE_DATE_EPOCH' must not be negative, got %#q", value)
		}

		return value, nil
	}

	value, err := sh.Output("git", "-C", projectDir, "show", "-s", "--format=%ct", ref)
	if err != nil {
		return "", fmt.Errorf("read commit time for %#q:\n%w", ref, err)
	}

	return strings.TrimSpace(value), nil
}

func normalizeVersion(value string) (string, error) {
	version := strings.TrimPrefix(strings.TrimSpace(value), "v")
	if !versionRE.MatchString(version) {
		return "", fmt.Errorf("version must have the form X.Y.Z, got %#q", value)
	}

	return version, nil
}

func parseSpecVersion(content string) (string, error) {
	match := regexp.MustCompile(`(?m)^Version:\s*(\S+)\s*$`).FindStringSubmatch(content)
	if match == nil || !versionRE.MatchString(match[1]) {
		return "", errors.New("spec must contain a literal 'Version: X.Y.Z' field")
	}

	return match[1], nil
}
