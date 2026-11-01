// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

var (
	existingVendorLicense = regexp.MustCompile(
		`^%go_vendor_license_(?:buildrequires|install|check)\s+-c\s+%\{(?:S:|SOURCE)([0-9]+)\}\s*$`)
	existingVendorExtract = regexp.MustCompile(`^tar\s+-xf\s+%\{(?:S:|SOURCE)([0-9]+)\}\s*$`)
)

func prepareExistingGoVendor(tree *specTree, configSource string) (*GoVendorSpec, error) {
	if err := validateGoVendorSection(tree, "%prep", true); err != nil {
		return nil, err
	}

	configName, err := existingVendorTagValue(tree, "Source"+configSource)
	if err != nil {
		return nil, err
	}

	if err := fileutils.ValidateFilename(configName); err != nil || strings.Contains(configName, "%") {
		return nil, fmt.Errorf("existing Go vendor config Source%s must name a plain file, got %#q", configSource, configName)
	}

	extraction, sourceNumber, err := existingVendorExtraction(tree)
	if err != nil {
		return nil, err
	}

	if sourceNumber == configSource {
		return nil, errors.New("go vendor archive and tool configuration cannot use the same Source tag")
	}

	archiveValue, err := existingVendorTagValue(tree, "Source"+sourceNumber)
	if err != nil {
		return nil, err
	}

	suffix, err := existingVendorArchiveSuffix(archiveValue)
	if err != nil {
		return nil, err
	}

	license, err := existingVendorLicenseExpression(tree)
	if err != nil {
		return nil, err
	}

	// Keep upstream's Source numbers, macro settings, file lists, and custom commands.
	for _, sectionName := range []string{"%build", "%install", "%check", "%generate_buildrequires"} {
		if err := validateGoVendorSection(tree, sectionName, false); err != nil {
			return nil, err
		}
	}

	addGoVendorEnvironment(tree)
	finalSpec := serializeGoVendorTree(tree)

	if err := removeExistingVendorInput(tree, sourceNumber, extraction); err != nil {
		return nil, err
	}

	tree.Section("%prep", "").AppendLines([]string{
		`pwd -P > "%{_topdir}/azldev-go-module-root"`,
	})

	return &GoVendorSpec{
		Spec: finalSpec, PrepSpec: goVendorPrepSpec(tree, archiveValue),
		ArchiveFile: "azldev-generated-vendor" + suffix, ArchiveSuffix: suffix, ConfigName: configName,
		ExpectedLicense: license, AllowReplacement: true,
	}, nil
}

func existingVendorLicenseExpression(tree *specTree) (string, error) {
	license, err := existingVendorTagValue(tree, "License")
	if err != nil {
		return "", err
	}

	if strings.ContainsAny(license, "%\r\n") || license == "" {
		return "", errors.New("regenerating an existing Go vendor archive requires an explicit main-package 'License' tag")
	}

	return license, nil
}

// An empty result means the spec has no existing vendor license commands.
func existingVendorConfigSource(tree *specTree) (string, error) {
	source := ""

	err := tree.VisitAllLines(func(_, _ string, line *lineHandle) error {
		text := strings.TrimSpace(line.Text)
		if !strings.HasPrefix(text, "%go_vendor_license_") {
			return nil
		}

		matches := existingVendorLicense.FindStringSubmatch(text)
		if len(matches) == 0 {
			return fmt.Errorf("unsupported existing Go vendor license command %#q", text)
		}

		if source != "" && source != matches[1] {
			return errors.New("existing Go vendor license commands reference different configuration sources")
		}

		source = matches[1]

		return nil
	})
	if err != nil {
		return "", err
	}

	return source, nil
}

func existingVendorExtraction(tree *specTree) (line, source string, err error) {
	count := 0

	err = tree.Section("%prep", "").VisitLines(func(candidate *lineHandle) error {
		matches := existingVendorExtract.FindStringSubmatch(strings.TrimSpace(candidate.Text))
		if len(matches) != 0 {
			if !slices.Contains(tree.Section("%prep", "").block.Children, candidate.block) {
				return errors.New("conditional Go vendor extraction requires an explicit preparation strategy")
			}

			count++
			line = strings.TrimSpace(candidate.Text)
			source = matches[1]
		}

		return nil
	})
	if err != nil {
		return "", "", err
	}

	if count != 1 {
		return "", "", errors.New("existing Go vendoring requires one unambiguous 'tar -xf %{S:N}' prep command")
	}

	return line, source, nil
}

func existingVendorTagValue(tree *specTree, name string) (string, error) {
	var values []string

	err := tree.VisitAllLines(func(section, pkg string, line *lineHandle) error {
		tag, value, ok := parseTagLine(line.Text)
		if ok && section == "" && pkg == "" && strings.EqualFold(tag, name) {
			if !slices.Contains(tree.Section("", "").block.Children, line.block) {
				return fmt.Errorf("existing Go vendoring requires an unconditional %#q tag", name)
			}

			values = append(values, value)
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	if len(values) != 1 {
		return "", fmt.Errorf("expected exactly one %#q declaration for existing Go vendoring", name)
	}

	return values[0], nil
}

func existingVendorArchiveSuffix(value string) (string, error) {
	if strings.Contains(value, "://") {
		return "", errors.New("existing Go vendor archive must use a local source filename")
	}

	extension := filepath.Ext(value)
	switch extension {
	case ".bz2", ".gz", ".xz", ".zst", ".zstd":
		return ".tar" + extension, nil
	case ".tar":
		return ".tar", nil
	default:
		return "", fmt.Errorf("unsupported existing Go vendor archive format %#q", value)
	}
}

func removeExistingVendorInput(tree *specTree, sourceNumber, extraction string) error {
	return tree.VisitAllLines(func(section, pkg string, line *lineHandle) error {
		tag, _, ok := parseTagLine(line.Text)
		if ok && section == "" && pkg == "" && strings.EqualFold(tag, "Source"+sourceNumber) {
			line.Remove()
		}

		if section == "%prep" && strings.TrimSpace(line.Text) == extraction {
			line.Remove()
		}

		return nil
	})
}
