// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/brunoga/deep"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// ComponentOverlay represents an overlay that may be applied to a component's spec and/or its sources.
type ComponentOverlay struct {
	// The type of overlay to apply.
	Type ComponentOverlayType `toml:"type" json:"type" validate:"required" jsonschema:"enum=spec-add-tag,enum=spec-insert-tag,enum=spec-set-tag,enum=spec-update-tag,enum=spec-remove-tag,enum=spec-prepend-lines,enum=spec-append-lines,enum=spec-search-replace,enum=spec-remove-section,enum=spec-remove-subpackage,enum=patch-add,enum=patch-remove,enum=file-prepend-lines,enum=file-search-replace,enum=file-add,enum=file-remove,enum=file-rename,title=Overlay type,description=The type of overlay to apply"`
	// Human readable description of overlay; primarily present to document the need for the change.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Human readable description of overlay" fingerprint:"-"`

	// Scopes the overlay to files inside this source archive (a bare filename, not a path).
	// Only file-remove and file-search-replace honor it; when set, the overlay operates inside
	// the named archive instead of the loose sources tree.
	Archive string `toml:"archive,omitempty" json:"archive,omitempty" jsonschema:"title=Archive,description=The source archive to modify (e.g. pkg-1.0.tar.gz)"`
	// Overrides the archive's extraction root (rpmbuild's `%setup -n` equivalent). When unset, the
	// root is inferred: a single top-level directory is used, otherwise the archive root.
	ArchiveRoot string `toml:"archive-root,omitempty" json:"archiveRoot,omitempty" jsonschema:"title=Archive root,description=Top-level directory inside the archive to treat as the extraction root (mirrors %setup -n); inferred when unset"`
	// For overlays that apply to non-spec files, indicates the filename. For overlays that can
	// apply to multiple files, supports glob patterns (including globstar).
	Filename string `toml:"file,omitempty" json:"file,omitempty" jsonschema:"title=Filename,description=The name of the non-spec file to which this overlay applies, or a glob pattern matching multiple files"`
	// For overlays that apply to specs, indicates the name of the section to which it applies.
	// Optional for spec-prepend-lines, spec-append-lines, and spec-search-replace: when omitted,
	// the overlay targets the entire spec file (prepend at top, append at end, search-replace
	// across all sections).
	SectionName string `toml:"section,omitempty" json:"section,omitempty" jsonschema:"title=Section name,description=The name of the section to which this overlay applies. Optional for spec-prepend-lines/spec-append-lines/spec-search-replace; when omitted these overlays target the entire spec file."`
	// For overlays that apply to specs, indicates the name of the sub-package to which it applies.
	// A sub-package is always a sub-qualifier of a section, so this field cannot be combined
	// with an omitted SectionName on overlays that support whole-file targeting.
	PackageName string `toml:"package,omitempty" json:"package,omitempty" jsonschema:"title=Package name,description=The name of the sub-package to which this overlay applies. Cannot be combined with an omitted section on overlays that support whole-file targeting."`
	// For overlays that apply to spec tags, indicates the name of the tag.
	Tag string `toml:"tag,omitempty" json:"tag,omitempty" jsonschema:"title=Tag,description=For overlays that apply to spec tags, indicates the name of the tag"`
	// For overlays that apply to values in specs, an exact string value to match.
	Value string `toml:"value,omitempty" json:"value,omitempty" jsonschema:"title=Value,description=An exact string value to match in the spec"`
	// For overlays that use a regular expression to match text in the spec, the regular expression to match.
	Regex string `toml:"regex,omitempty" json:"regex,omitempty" jsonschema:"title=Regular expression,description=The regular expression to match in the spec"`
	// For overlays that replace text in a spec, the replacement text to use.
	Replacement string `toml:"replacement,omitempty" json:"replacement,omitempty" jsonschema:"title=Replacement text,description=The replacement text to use in the spec"`
	// For overlays that reference lines of text, the lines of text to use.
	Lines []string `toml:"lines,omitempty" json:"lines,omitempty" jsonschema:"title=Lines,description=The lines of text to use"`
	// For overlays that require a source file as input, indicates a path to that file; relative paths are relative to
	// the config file that defines the overlay.
	// Excluded from fingerprint because it contains an absolute path that varies by checkout
	// location. Overlay content is hashed separately by [fingerprint.ComputeIdentity].
	Source string `toml:"source,omitempty" json:"source,omitempty" jsonschema:"title=Source,description=For overlays that require a source file as input, indicates a path to that file; relative paths are relative to the config file that defines the overlay" fingerprint:"-"`

	// Metadata describes the intent and provenance of the overlay (category, related
	// commits, bug links, upstreamability, etc.). Optional. Populated either inline
	// in the component config file or by the [ComponentConfig.OverlayFiles] loader,
	// which stamps the per-file metadata onto every overlay declared in that file.
	// Excluded from the fingerprint because it is documentation only.
	Metadata *OverlayMetadata `toml:"metadata,omitempty" json:"metadata,omitempty" jsonschema:"title=Metadata,description=Optional documentation metadata describing the overlay's intent and provenance" fingerprint:"-"`
}

// EffectiveSourceName returns the checkout-independent identity of the overlay's
// source file. When [ComponentOverlay.Filename] is set it takes precedence;
// otherwise the basename of [ComponentOverlay.Source] is used (this matches the
// runtime behavior in the overlay application layer, e.g., patch-add derives its
// destination filename from the source basename).
//
// Returns empty string if the overlay has no source file.
func (c *ComponentOverlay) EffectiveSourceName() string {
	if c.Source == "" {
		return ""
	}

	if c.Filename != "" {
		return c.Filename
	}

	return filepath.Base(c.Source)
}

// SourceContentIdentity returns an opaque identity string for the overlay's source
// file, combining the effective destination filename and a SHA256 content hash.
// Returns empty string and nil error if the overlay has no source file.
// Used by [fingerprint.ComputeIdentity] so that fingerprint logic does not need
// overlay-specific knowledge.
func (c *ComponentOverlay) SourceContentIdentity(fs opctx.FS) (string, error) {
	name := c.EffectiveSourceName()
	if name == "" {
		return "", nil
	}

	contentHash, err := fileutils.ComputeFileHash(fs, fileutils.HashTypeSHA256, c.Source)
	if err != nil {
		return "", fmt.Errorf("hashing overlay source %#q:\n%w", c.Source, err)
	}

	return name + ":" + contentHash, nil
}

// WithAbsolutePaths returns a copy of the overlay with config-relative file paths converted to absolute
// file paths (relative to referenceDir, not the current working directory). Note that
// paths that are intentionally relative to the destination component sources are left
// relative.
func (c *ComponentOverlay) WithAbsolutePaths(referenceDir string) (result *ComponentOverlay) {
	// Deep copy the input to avoid unexpected sharing.
	//
	// NOTE: We use the panicking MustCopy() because copying should only fail if the input *type*
	// is invalid. Since we're always using the same type, we never expect to see a runtime error
	// here.
	result = deep.MustCopy(c)

	// Fix up paths.
	result.Source = makeAbsolute(referenceDir, result.Source)

	return result
}

// ModifiesSpec returns true if the overlay modifies a spec file. This includes hybrid
// overlays that modify both spec and source files (e.g., patch overlays), since those
// also require spec modifications.
func (c *ComponentOverlay) ModifiesSpec() bool {
	return c.Type == ComponentOverlayAddSpecTag ||
		c.Type == ComponentOverlayInsertSpecTag ||
		c.Type == ComponentOverlaySetSpecTag ||
		c.Type == ComponentOverlayUpdateSpecTag ||
		c.Type == ComponentOverlayRemoveSpecTag ||
		c.Type == ComponentOverlayPrependSpecLines ||
		c.Type == ComponentOverlayAppendSpecLines ||
		c.Type == ComponentOverlaySearchAndReplaceInSpec ||
		c.Type == ComponentOverlayRemoveSection ||
		c.Type == ComponentOverlayRemoveSubpackage ||
		c.Type == ComponentOverlayAddPatch ||
		c.Type == ComponentOverlayRemovePatch
}

// ModifiesArchive returns true if the overlay modifies files inside a source archive.
// These overlays require extraction and repacking of the archive. Only file-remove and
// file-search-replace support archive scoping, and only when their
// [ComponentOverlay.Archive] field is set.
func (c *ComponentOverlay) ModifiesArchive() bool {
	return c.Archive != "" &&
		(c.Type == ComponentOverlayRemoveFile || c.Type == ComponentOverlaySearchAndReplaceInFile)
}

// ModifiesNonSpecFiles returns true if the overlay modifies non-spec files. This includes
// hybrid overlays that modify both spec and source files (e.g., patch overlays), since
// those also require non-spec modifications. Archive-scoped overlays (see [ModifiesArchive])
// are excluded: they operate on files inside an archive, not loose files in the sources tree.
func (c *ComponentOverlay) ModifiesNonSpecFiles() bool {
	if c.ModifiesArchive() {
		return false
	}

	return c.Type == ComponentOverlayPrependLinesToFile ||
		c.Type == ComponentOverlaySearchAndReplaceInFile ||
		c.Type == ComponentOverlayAddFile ||
		c.Type == ComponentOverlayRemoveFile ||
		c.Type == ComponentOverlayRenameFile ||
		c.Type == ComponentOverlayAddPatch ||
		c.Type == ComponentOverlayRemovePatch
}

// ComponentOverlayType is the type of a component overlay.
type ComponentOverlayType string

const (
	// ComponentOverlayAddSpecTag is an overlay that adds a tag to the spec; fails if the tag already exists.
	ComponentOverlayAddSpecTag ComponentOverlayType = "spec-add-tag"
	// ComponentOverlayInsertSpecTag is an overlay that inserts a tag into the spec, placing it
	// after the last existing tag from the same family (e.g., Source9999 after the last Source* tag).
	// Falls back to after the last tag of any kind, then to appending at the section end.
	ComponentOverlayInsertSpecTag ComponentOverlayType = "spec-insert-tag"
	// ComponentOverlaySetSpecTag is an overlay that sets a tag to the spec. If the tag already exists, replaces
	// its existing value; otherwise, adds the tag.
	ComponentOverlaySetSpecTag ComponentOverlayType = "spec-set-tag"
	// ComponentOverlayUpdateSpecTag is an overlay that updates a tag in the spec; fails if the tag doesn't exist.
	ComponentOverlayUpdateSpecTag ComponentOverlayType = "spec-update-tag"
	// ComponentOverlayRemoveSpecTag is an overlay that removes a tag from the spec; fails if the tag doesn't exist.
	ComponentOverlayRemoveSpecTag ComponentOverlayType = "spec-remove-tag"
	// ComponentOverlayPrependSpecLines is an overlay that prepends lines to a section in a spec; fails if the section
	// doesn't exist.
	ComponentOverlayPrependSpecLines ComponentOverlayType = "spec-prepend-lines"
	// ComponentOverlayAppendSpecLines is an overlay that appends lines to a section in a spec; fails if the section
	// doesn't exist.
	ComponentOverlayAppendSpecLines ComponentOverlayType = "spec-append-lines"
	// ComponentOverlaySearchAndReplaceInSpec is an overlay that replaces text in a spec with other text.
	ComponentOverlaySearchAndReplaceInSpec ComponentOverlayType = "spec-search-replace"
	// ComponentOverlayRemoveSection is an overlay that removes an entire section from the spec;
	// fails if the section doesn't exist.
	ComponentOverlayRemoveSection ComponentOverlayType = "spec-remove-section"
	// ComponentOverlayRemoveSubpackage is an overlay that removes every section in the spec
	// that belongs to a given sub-package (e.g. its `%package`, `%description`, `%files`,
	// `%post`, `%postun`, etc. sections). Fails if the spec has no sections matching the
	// indicated sub-package.
	ComponentOverlayRemoveSubpackage ComponentOverlayType = "spec-remove-subpackage"
	// ComponentOverlayAddPatch is an overlay that adds a patch file and registers it in the spec.
	// It copies the source file into the component sources and adds a PatchN tag (or appends to
	// %%patchlist if one exists).
	ComponentOverlayAddPatch ComponentOverlayType = "patch-add"
	// ComponentOverlayRemovePatch is an overlay that removes a patch file and its corresponding
	// PatchN tag and/or %%patchlist entry from the spec.
	ComponentOverlayRemovePatch ComponentOverlayType = "patch-remove"
	// ComponentOverlayPrependLinesToFile is an overlay that prepends lines to a non-spec file.
	ComponentOverlayPrependLinesToFile ComponentOverlayType = "file-prepend-lines"
	// ComponentOverlaySearchAndReplaceInFile is an overlay that replaces text in a non-spec file.
	ComponentOverlaySearchAndReplaceInFile ComponentOverlayType = "file-search-replace"
	// ComponentOverlayAddFile is an overlay that adds a non-spec file.
	ComponentOverlayAddFile ComponentOverlayType = "file-add"
	// ComponentOverlayRemoveFile is an overlay that removes a non-spec file. When its
	// [ComponentOverlay.Archive] field is set, it removes file(s) from inside that source
	// archive instead of loose files in the sources tree.
	ComponentOverlayRemoveFile ComponentOverlayType = "file-remove"
	// ComponentOverlayRenameFile is an overlay that renames a non-spec file.
	ComponentOverlayRenameFile ComponentOverlayType = "file-rename"
)

// Validate checks that required fields are set based on the overlay type. This catches
// configuration errors at load time rather than at apply time.
func (c *ComponentOverlay) Validate() error {
	desc := c.Description
	if desc == "" {
		desc = "(no description)"
	}

	if err := c.validateRequiredFields(desc); err != nil {
		return err
	}

	if c.Metadata != nil {
		if err := c.Metadata.Validate(); err != nil {
			return fmt.Errorf("invalid metadata for overlay %q:\n%w", desc, err)
		}
	}

	return nil
}

func (c *ComponentOverlay) validateRequiredFields(desc string) error {
	// The archive field scopes an overlay to operate inside a source archive. It is only
	// accepted on file-remove and file-search-replace, and must be a bare filename (not a path).
	if c.Archive != "" {
		if c.Type != ComponentOverlayRemoveFile && c.Type != ComponentOverlaySearchAndReplaceInFile {
			return fmt.Errorf("overlay type %#q does not accept %#q field: %s", c.Type, "archive", desc)
		}

		if err := c.requireFileBasename("archive", c.Archive, desc); err != nil {
			return err
		}
	}

	// The archive-root override is only meaningful for archive-scoped overlays, and must be a
	// local relative path so it cannot escape the extraction directory.
	if c.ArchiveRoot != "" {
		if !c.ModifiesArchive() {
			return fmt.Errorf("overlay type %#q does not accept %#q field: %s", c.Type, "archive-root", desc)
		}

		if !filepath.IsLocal(c.ArchiveRoot) {
			return fmt.Errorf(
				"overlay type %#q requires %#q to be a local relative path (no %#q or absolute paths); found %#q",
				c.Type, "archive-root", "..", c.ArchiveRoot,
			)
		}
	}

	switch c.Type {
	case ComponentOverlayAddSpecTag, ComponentOverlayInsertSpecTag,
		ComponentOverlaySetSpecTag, ComponentOverlayUpdateSpecTag, ComponentOverlayRemoveSpecTag:
		return c.validateSpecTagFields(desc)
	case ComponentOverlayPrependSpecLines, ComponentOverlayAppendSpecLines:
		return c.validateSpecLineOverlay(desc)
	case ComponentOverlaySearchAndReplaceInSpec:
		return c.validateSpecSearchReplaceOverlay(desc)
	case ComponentOverlayPrependLinesToFile, ComponentOverlaySearchAndReplaceInFile:
		return c.validateFileOverlay(desc)
	case ComponentOverlayAddFile:
		return c.validateAddFileOverlay(desc)
	case ComponentOverlayRemoveFile:
		return c.validateRemoveFileOverlay(desc)
	case ComponentOverlayRenameFile:
		return c.validateRenameFileOverlay(desc)
	case ComponentOverlayRemoveSection:
		return c.validateRemoveSectionOverlay(desc)
	case ComponentOverlayRemoveSubpackage:
		return c.validateRemoveSubpackageOverlay(desc)
	case ComponentOverlayAddPatch, ComponentOverlayRemovePatch:
		return c.validatePatchOverlay(desc)
	default:
		return fmt.Errorf("unknown overlay type %#q: %#q", c.Type, desc)
	}
}

func (c *ComponentOverlay) validateSpecTagFields(desc string) error {
	if c.Tag == "" {
		return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, "tag", desc)
	}

	if c.Type != ComponentOverlayRemoveSpecTag && c.Value == "" {
		return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, "value", desc)
	}

	return nil
}

func (c *ComponentOverlay) validateSpecLineOverlay(desc string) error {
	if len(c.Lines) == 0 {
		return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, "lines", desc)
	}

	return c.requireSectionIfPackageSet(desc)
}

func (c *ComponentOverlay) validateSpecSearchReplaceOverlay(desc string) error {
	if c.Regex == "" {
		return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, "regex", desc)
	}

	if err := validateRegex(c.Regex, desc); err != nil {
		return err
	}

	return c.requireSectionIfPackageSet(desc)
}

func (c *ComponentOverlay) validateFileOverlay(desc string) error {
	if err := c.requireRelativePath(c.Filename, desc); err != nil {
		return err
	}

	if c.Type == ComponentOverlayPrependLinesToFile {
		if len(c.Lines) == 0 {
			return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, "lines", desc)
		}

		return nil
	}

	if c.Regex == "" {
		return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, "regex", desc)
	}

	return validateRegex(c.Regex, desc)
}

func (c *ComponentOverlay) validateAddFileOverlay(desc string) error {
	if err := c.requireRelativePath(c.Filename, desc); err != nil {
		return err
	}

	if c.Source == "" {
		return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, "source", desc)
	}

	return nil
}

func (c *ComponentOverlay) validateRemoveFileOverlay(desc string) error {
	return c.requireRelativePath(c.Filename, desc)
}

func (c *ComponentOverlay) validateRenameFileOverlay(desc string) error {
	if err := c.requireRelativePath(c.Filename, desc); err != nil {
		return err
	}

	return c.requireFileBasename("replacement", c.Replacement, desc)
}

func (c *ComponentOverlay) validateRemoveSectionOverlay(desc string) error {
	if c.SectionName == "" {
		return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, "section", desc)
	}

	return nil
}

func (c *ComponentOverlay) validateRemoveSubpackageOverlay(desc string) error {
	if c.PackageName == "" {
		return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, "package", desc)
	}

	if c.SectionName != "" {
		return fmt.Errorf("overlay type %#q does not accept %#q field: %s", c.Type, "section", desc)
	}

	return nil
}

func (c *ComponentOverlay) validatePatchOverlay(desc string) error {
	if c.Type == ComponentOverlayAddPatch {
		if c.Source == "" {
			return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, "source", desc)
		}

		// Filename is optional; if provided, it must be a relative path.
		if c.Filename != "" {
			return c.requireRelativePath(c.Filename, desc)
		}

		return nil
	}

	if err := c.requireRelativePath(c.Filename, desc); err != nil {
		return err
	}

	return validateGlobPattern(c.Filename, desc)
}

// requireSectionIfPackageSet checks that, for overlays that may target either a single
// section or the entire spec file (indicated by omitting `section`), a `package` is only
// specified when a `section` is also specified. A package is always a sub-qualifier of
// a section, so specifying one without the other is meaningless.
func (c *ComponentOverlay) requireSectionIfPackageSet(desc string) error {
	if c.SectionName == "" && c.PackageName != "" {
		return fmt.Errorf(
			"overlay type %#q requires %#q field when %#q is set: %s",
			c.Type, "section", "package", desc,
		)
	}

	return nil
}

func (c *ComponentOverlay) requireRelativePath(value, desc string) error {
	if value == "" {
		return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, "file", desc)
	}

	if filepath.IsAbs(value) {
		return fmt.Errorf(
			"overlay type %#q requires %#q to be a relative path; found %#q",
			c.Type, "file", value,
		)
	}

	return nil
}

func (c *ComponentOverlay) requireFileBasename(fieldName, value, desc string) error {
	if value == "" {
		return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, fieldName, desc)
	}

	if value != filepath.Base(value) {
		return fmt.Errorf(
			"overlay type %#q requires %#q to be a filename only (not a path); found %#q",
			c.Type, fieldName, value,
		)
	}

	return nil
}

// validateRegex checks if the provided regex pattern is valid.
func validateRegex(pattern, overlayDesc string) error {
	_, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid regex %#q in overlay %q:\n%w", pattern, overlayDesc, err)
	}

	return nil
}

// validateGlobPattern checks if the provided glob pattern is valid.
func validateGlobPattern(pattern, overlayDesc string) error {
	if !doublestar.ValidatePattern(pattern) {
		return fmt.Errorf("invalid glob pattern %#q in overlay %q", pattern, overlayDesc)
	}

	return nil
}
