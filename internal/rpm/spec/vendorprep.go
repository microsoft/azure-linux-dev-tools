// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// VendorPrep describes a private prep spec, never a replacement published spec.
type VendorPrep struct {
	Spec       []byte
	ConfigName string
}

var (
	vendorExtraction = regexp.MustCompile(`^tar\s+-xf\s+%\{(?:S:|SOURCE)([0-9]+)\}\s*$`)
	vendorConfigRef  = regexp.MustCompile(`^%go_vendor_license_\w+\s+-c\s+%\{(?:S:|SOURCE)([0-9]+)\}\s*$`)
	secondarySetup   = regexp.MustCompile(`%(?:\{[!?]*)?(?:(?:auto)?setup|goprep)\b.*\s-[a-zA-Z]*[ab]`)
	knownPrepMacro   = regexp.MustCompile(`^%(?:setup|autosetup|goprep|autopatch|patch[0-9]*)(?:\s|$)`)
)

// PrepareVendorSource removes only the selected vendor input from private prep.
// The caller remains responsible for the original spec's archive consumption.
func PrepareVendorSource(data []byte, filename string) (*VendorPrep, error) {
	if err := fileutils.ValidateFilename(filename); err != nil {
		return nil, fmt.Errorf("validating vendor filename:\n%w", err)
	}

	if strings.ContainsAny(filename, "%\r\n") {
		return nil, errors.New("vendor 'filename' must be a basename without RPM macros or newlines")
	}

	root, err := parseTree(strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"))
	if err != nil {
		return nil, err
	}

	tree := &specTree{root: root}

	prep := tree.Section("%prep", "")
	if prep == nil || !slices.Contains(root.Children, prep.block) {
		return nil, errors.New("go vendor generation requires an unconditional '%prep' section")
	}

	sources, configNumber, err := vendorSourceTags(tree)
	if err != nil {
		return nil, err
	}

	candidates, err := vendorExtractions(prep, sources)
	if err != nil {
		return nil, err
	}

	number, err := selectVendorSource(sources, candidates, filename)
	if err != nil {
		return nil, err
	}

	if err := removeVendorInput(tree, number); err != nil {
		return nil, err
	}

	configName := sources[configNumber]
	if configName != "" {
		if err := fileutils.ValidateFilename(configName); err != nil || strings.Contains(configName, "%") {
			return nil, errors.New("vendor config Source must name a plain sidecar file")
		}
	}

	if number != "" {
		prep.AppendLines([]string{"test %{shescape:" + sources[number] + "} = %{shescape:" + filename + "}"})
	}

	prep.AppendLines([]string{`pwd -P > "%{_topdir}/azldev-vendor-root"`})

	return &VendorPrep{
		Spec:       []byte(strings.Join(serializeTree(root), "\n") + "\n"),
		ConfigName: configName,
	}, nil
}

func vendorSourceTags(tree *specTree) (sources map[string]string, configNumber string, err error) {
	sources = map[string]string{}
	err = tree.VisitAllLines(func(section, pkg string, line *lineHandle) error {
		tag, value, ok := parseTagLine(line.Text)
		if ok && section == "" && pkg == "" && strings.HasPrefix(strings.ToLower(tag), "source") {
			number := strings.TrimPrefix(strings.ToLower(tag), "source")
			if number == "" {
				number = "0"
			}

			if _, duplicate := sources[number]; duplicate || !slices.Contains(tree.Section("", "").block.Children, line.block) {
				return fmt.Errorf("ambiguous vendor source declaration %#q", tag)
			}

			sources[number] = value
		}

		matches := vendorConfigRef.FindStringSubmatch(strings.TrimSpace(line.Text))
		if len(matches) != 0 {
			if configNumber != "" && configNumber != matches[1] {
				return errors.New("vendor license macros reference different configuration files")
			}

			configNumber = matches[1]
		}

		return nil
	})

	return sources, configNumber, err
}

func vendorExtractions(prep *sectionHandle, sources map[string]string) ([]string, error) {
	var candidates []string

	err := prep.VisitLines(func(line *lineHandle) error {
		text := strings.TrimSpace(line.Text)
		if strings.HasPrefix(text, "#") {
			return nil
		}

		if secondarySetup.MatchString(text) {
			return errors.New("secondary setup extraction requires an explicit prep overlay for vendor generation")
		}

		if strings.HasPrefix(text, "%") && !knownPrepMacro.MatchString(text) {
			return fmt.Errorf("opaque vendor prep macro %#q; make extraction explicit", text)
		}

		match := vendorExtraction.FindStringSubmatch(text)
		if len(match) != 0 && match[1] != "0" {
			if !slices.Contains(prep.block.Children, line.block) {
				return errors.New("conditional vendor extraction is unsupported")
			}

			if sources[match[1]] == "" {
				return errors.New("vendor extraction references a missing Source")
			}

			candidates = append(candidates, match[1])
		} else if strings.HasPrefix(text, "tar ") && strings.Contains(text, "%{S") {
			return fmt.Errorf("unsupported source extraction %#q; use a simple 'tar -xf' prep overlay", text)
		}

		return nil
	})

	return candidates, err
}

func selectVendorSource(
	sources map[string]string, candidates []string, filename string,
) (string, error) {
	if len(candidates) > 1 {
		return "", errors.New("ambiguous vendor extraction; simplify prep with an overlay")
	}

	if len(candidates) == 0 {
		return vendorSourceWithFilename(sources, filename)
	}

	number := candidates[0]
	if !strings.Contains(sources[number], "%") && sources[number] != filename {
		return "", errors.New("configured vendor filename does not match the extracted Source")
	}

	return number, nil
}

func vendorSourceWithFilename(sources map[string]string, filename string) (string, error) {
	number := ""

	for key, value := range sources {
		if value == filename {
			if key == "0" || number != "" {
				return "", errors.New("vendor output must select a unique Source other than Source0")
			}

			number = key
		}
	}

	return number, nil
}

func removeVendorInput(tree *specTree, number string) error {
	return tree.VisitAllLines(func(section, pkg string, line *lineHandle) error {
		tag, _, ok := parseTagLine(line.Text)
		if number != "" && ok && section == "" && pkg == "" && strings.EqualFold(tag, "Source"+number) {
			line.Remove()
		}

		match := vendorExtraction.FindStringSubmatch(strings.TrimSpace(line.Text))
		if section == "%prep" && len(match) != 0 && match[1] == number {
			line.Remove()
		}

		return nil
	})
}
