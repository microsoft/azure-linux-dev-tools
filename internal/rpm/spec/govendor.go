// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package spec

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// GoVendorOptions names generated artifacts. All paths except ModuleDir are basenames.
type GoVendorOptions struct {
	ArchiveName string
	ConfigName  string
	ModuleDir   string
}

// GoVendorSpec is the common plan for converting or refreshing Go vendoring.
type GoVendorSpec struct {
	Spec             []byte
	PrepSpec         []byte
	ArchiveFile      string // Filename used inside generation staging.
	ArchiveSuffix    string // Required suffix of the RPM-expanded destination name, if any.
	ConfigName       string
	ExpectedLicense  string // Empty: detect and update. Otherwise: verify and preserve.
	AllowReplacement bool   // Permit replacing the existing vendor payload and configuration.
}

const goVendorEnvironment = `export GO111MODULE=on GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local
export GOFLAGS="${GOFLAGS:+$GOFLAGS }-mod=vendor"`

var goBuildRequirement = regexp.MustCompile(`^golang\([^)]+\)(\s*(>=|<=|=|>|<)\s*[^\s,]+)?$`)

var (
	goVendorUnsupportedMacro = regexp.MustCompile(
		`%(?:\{[!?]*)?(?:gopkg\w*|goinstall|gofiles?\w*|go_vendor_\w*|include)\b`)
	goVendorRequiresMacro = regexp.MustCompile(`%(?:\{[!?]*)?go_generate_buildrequires\b`)
	goVendorCheckMacro    = regexp.MustCompile(`%(?:\{[!?]*)?gocheck\b`)
)

// ConvertGoVendor changes only recognized Go application packaging constructs.
// It always uses the lossless tree, independently of the selected overlay editor.
// Unsupported Go macros and ambiguous section ownership fail before returning output.
func ConvertGoVendor(data []byte, options GoVendorOptions) (*GoVendorSpec, error) {
	if err := options.validate(); err != nil {
		return nil, err
	}

	root, err := parseTree(strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"))
	if err != nil {
		return nil, err
	}

	tree := &specTree{root: root}

	configSource, err := existingVendorConfigSource(tree)
	if err != nil {
		return nil, err
	}

	if configSource != "" {
		return prepareExistingGoVendor(tree, configSource)
	}

	if err := validateGoVendorTree(tree); err != nil {
		return nil, err
	}

	if err := convertGoVendorLines(tree); err != nil {
		return nil, err
	}

	// Do not replace upstream prep or build scripts: capture the directory after
	// their existing prep commands, including patches and module-file edits.
	prep := tree.Section("%prep", "")
	prep.PrependLines([]string{"export GOTOOLCHAIN=local"})
	prep.AppendLines([]string{"", `pwd -P > "%{_topdir}/azldev-go-module-root"`})

	prepSpec := goVendorPrepSpec(tree, options.ArchiveName)

	sourceNumber, err := freeGoVendorSources(tree, options)
	if err != nil {
		return nil, err
	}

	configRef := fmt.Sprintf("%%{SOURCE%d}", sourceNumber+1)

	extract := fmt.Sprintf("tar -xf %%{SOURCE%d}", sourceNumber)
	if options.ModuleDir != "" {
		extract += " -C " + shellquote.Join(options.ModuleDir)
	}

	prep.AppendLines([]string{"", extract})
	tree.Section("", "").AppendLines([]string{
		fmt.Sprintf("Source%d: %s", sourceNumber, options.ArchiveName),
		fmt.Sprintf("Source%d: %s", sourceNumber+1, options.ConfigName),
		"BuildRequires: go-vendor-tools",
		"BuildRequires: go-rpm-macros",
		"BuildRequires: golang",
		"%global gomodulesmode GO111MODULE=on",
		"%global go_vendor_license_filelist() %{_topdir}/azldev-go-vendor-licenses.list",
		"",
	})

	addGoVendorBuildSections(tree, options, configRef)

	return &GoVendorSpec{
		Spec: serializeGoVendorTree(tree), PrepSpec: prepSpec,
		ArchiveFile: options.ArchiveName, ConfigName: options.ConfigName,
	}, nil
}

func addGoVendorBuildSections(tree *specTree, options GoVendorOptions, configRef string) {
	requires := tree.Section("%generate_buildrequires", "")
	if requires == nil {
		blk := &block{Kind: sectionBlock, Name: "%generate_buildrequires", Header: "%generate_buildrequires"}
		buildIndex := slices.Index(tree.root.Children, tree.Section("%build", "").block)
		tree.root.Children = slices.Insert(tree.root.Children, buildIndex, blk)
		requires = &sectionHandle{block: blk, tree: tree}
	}

	requires.PrependLines([]string{"%go_vendor_license_buildrequires -c " + configRef})

	addGoVendorEnvironment(tree)

	tree.Section("%install", "").AppendLines([]string{
		"", goVendorInModule(options.ModuleDir, "%go_vendor_license_install -c "+configRef),
	})
	// Licensing is a build invariant, even when the component disables %check.
	tree.Section("%build", "").PrependLines([]string{
		goVendorInModule(options.ModuleDir, "%go_vendor_license_check -c "+configRef),
	})
	files := tree.Section("%files", "")
	files.block.Header += " -f %{go_vendor_license_filelist}"
}

func (o GoVendorOptions) validate() error {
	for _, name := range []string{o.ArchiveName, o.ConfigName} {
		if err := fileutils.ValidateFilename(name); err != nil {
			return fmt.Errorf("invalid Go vendor artifact name:\n%w", err)
		}

		if strings.ContainsAny(name, "%\r\n") {
			return errors.New("go vendor artifact name must not contain macros or newlines")
		}
	}

	if o.ModuleDir != "" {
		if err := fileutils.ValidateRelPath(o.ModuleDir); err != nil {
			return fmt.Errorf("invalid Go module directory:\n%w", err)
		}

		if strings.ContainsAny(o.ModuleDir, "%\r\n") {
			return errors.New("go module directory must not contain macros or newlines")
		}
	}

	return nil
}

func serializeGoVendorTree(tree *specTree) []byte {
	return []byte(strings.Join(serializeTree(tree.root), "\n") + "\n")
}

func addGoVendorEnvironment(tree *specTree) {
	for _, name := range []string{"%build", "%install", "%check", "%generate_buildrequires"} {
		if section := tree.Section(name, ""); section != nil {
			section.PrependLines([]string{goVendorEnvironment})
		}
	}
}

func goVendorPrepSpec(tree *specTree, archiveValue string) []byte {
	prep := tree.Section("%prep", "")
	prep.AppendLines([]string{
		"printf '%%s\\n' %{shescape:" + archiveValue + `} > "%{_topdir}/azldev-go-archive-name"`,
	})

	result := serializeGoVendorTree(tree)
	prep.block.Children = prep.block.Children[:len(prep.block.Children)-1]

	return result
}

// FinalizeLicense preserves a verified upstream expression or writes the detected
// expression for a newly converted spec. It does not mutate the plan.
func (p *GoVendorSpec) FinalizeLicense(expression string) ([]byte, error) {
	if p.ExpectedLicense != "" {
		return p.Spec, nil
	}

	editor, err := openStructuralSpec(strings.NewReader(string(p.Spec)))
	if err != nil {
		return nil, err
	}

	if err := editor.UpdateExistingTag("", "License", expression); err != nil {
		return nil, err
	}

	return []byte(strings.Join(editor.rawLines, "\n") + "\n"), nil
}

func goVendorInModule(moduleDir, command string) string {
	lines := []string{`( cd "$(cat "%{_topdir}/azldev-go-module-root")"`}
	if moduleDir != "" {
		lines = append(lines, "cd "+shellquote.Join(moduleDir))
	}

	lines = append(lines, command, ")")

	return strings.Join(lines, "\n")
}

func validateGoVendorTree(tree *specTree) error {
	if tree.HasSection("%sourcelist") {
		return errors.New("go vendoring requires explicit Source tags instead of '%sourcelist'")
	}

	for _, name := range []string{"", "%prep", "%build", "%install", "%files"} {
		if err := validateGoVendorSection(tree, name, true); err != nil {
			return err
		}
	}

	for _, name := range []string{"%check", "%generate_buildrequires"} {
		if err := validateGoVendorSection(tree, name, false); err != nil {
			return err
		}
	}

	var invalid error

	walkBlocks(tree.root, func(blk *block) bool {
		if invalid != nil {
			return false
		}

		invalid = validateGoVendorBlock(blk)

		return true
	})

	return invalid
}

func validateGoVendorSection(tree *specTree, name string, required bool) error {
	sections := tree.Sections(name, "")
	if len(sections) == 0 && !required {
		return nil
	}

	if len(sections) != 1 {
		return fmt.Errorf("go vendoring requires one main-package %#q section; use overlays to disambiguate", name)
	}

	if !slices.Contains(tree.root.Children, sections[0].block) {
		return fmt.Errorf("go vendoring does not support conditional %#q sections; use an overlay", name)
	}

	return nil
}

func validateGoVendorBlock(blk *block) error {
	if blk.Kind == macroDefBlock && (blk.Name == "gomodulesmode" || strings.HasPrefix(blk.Name, "go_vendor_")) {
		return fmt.Errorf("explicit %#q conflicts with Go vendoring; remove it with an overlay", blk.Name)
	}

	for _, line := range append([]string{blk.Header}, blk.Lines...) {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}

		if macro := goVendorUnsupportedMacro.FindString(line); macro != "" {
			return fmt.Errorf("unsupported Go vendoring macro %#q in %#q; adapt it with an overlay", macro, line)
		}

		if blk.Kind == macroDefBlock &&
			(goVendorRequiresMacro.MatchString(line) || goVendorCheckMacro.MatchString(line) ||
				strings.Contains(line, "golang(")) {
			return fmt.Errorf("go dependency or check logic inside a macro definition requires an explicit overlay: %#q", line)
		}

		for _, macro := range []string{"-mod=mod", "GO111MODULE=off"} {
			if strings.Contains(line, macro) {
				return fmt.Errorf("unsupported Go vendoring construct %#q in %#q; adapt it with an overlay", macro, line)
			}
		}
	}

	return nil
}

func convertGoVendorLines(tree *specTree) error {
	return tree.VisitAllLines(func(section, _ string, line *lineHandle) error {
		text := strings.TrimSpace(line.Text)
		if strings.HasPrefix(text, "#") {
			return nil
		}

		if tag, value, ok := parseTagLine(text); ok && isTagBearingSection(section) &&
			strings.EqualFold(tag, "BuildRequires") && strings.Contains(value, "golang(") {
			if !goBuildRequirement.MatchString(value) {
				return fmt.Errorf("ambiguous Go BuildRequires %#q; split it into individual tags with an overlay", line.Text)
			}

			line.Remove()
		}

		switch {
		case goVendorRequiresMacro.MatchString(text):
			if text != "%go_generate_buildrequires" || section != "%generate_buildrequires" {
				return fmt.Errorf("unsupported Go dependency generator %#q; adapt it with an overlay", text)
			}

			line.Remove()
		case goVendorCheckMacro.MatchString(text):
			if text != "%gocheck" || section != "%check" {
				return fmt.Errorf("unsupported Go check command %#q; use a module-aware check overlay", text)
			}

			line.Replace("%gotest ./...")
		}

		return nil
	})
}

func freeGoVendorSources(tree *specTree, options GoVendorOptions) (int, error) {
	used := make(map[int]bool)

	err := tree.VisitAllLines(func(section, _ string, line *lineHandle) error {
		tag, value, ok := parseTagLine(line.Text)
		if !ok || !isTagBearingSection(section) || !strings.HasPrefix(strings.ToLower(tag), "source") {
			return nil
		}

		suffix := tag[len("source"):]
		number := 0

		if suffix != "" {
			var err error

			number, err = strconv.Atoi(suffix)
			if err != nil || number < 0 {
				return fmt.Errorf("cannot allocate vendor Source tags alongside %#q", tag)
			}
		}

		used[number] = true

		if value == options.ArchiveName || value == options.ConfigName {
			return fmt.Errorf("generated Go vendor source collides with existing Source tag %#q", line.Text)
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	for number := 0; ; number++ {
		if !used[number] && !used[number+1] {
			return number, nil
		}
	}
}
