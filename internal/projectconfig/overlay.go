// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/brunoga/deep"
)

// ComponentOverlay represents an overlay that may be applied to a component's spec and/or its sources.
type ComponentOverlay struct {
	// The type of overlay to apply.
	Type ComponentOverlayType `toml:"type" json:"type" validate:"required" jsonschema:"enum=spec-add-tag,enum=spec-insert-tag,enum=spec-set-tag,enum=spec-update-tag,enum=spec-remove-tag,enum=spec-prepend-lines,enum=spec-append-lines,enum=spec-search-replace,enum=spec-remove-section,enum=patch-add,enum=patch-remove,enum=file-prepend-lines,enum=file-search-replace,enum=file-add,enum=file-remove,enum=file-rename,title=Overlay type,description=The type of overlay to apply"`
	// Human readable description of overlay; primarily present to document the need for the change.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Human readable description of overlay"`

	// For overlays that apply to non-spec files, indicates the filename. For overlays that can
	// apply to multiple files, supports glob patterns (including globstar).
	Filename string `toml:"file,omitempty" json:"file,omitempty" jsonschema:"title=Filename,description=The name of the non-spec file to which this overlay applies, or a glob pattern matching multiple files"`
	// For overlays that apply to specs, indicates the name of the section to which it applies.
	SectionName string `toml:"section,omitempty" json:"section,omitempty" jsonschema:"title=Section name,description=The name of the section to which this overlay applies"`
	// For overlays that apply to specs, indicates the name of the sub-package to which it applies.
	PackageName string `toml:"package,omitempty" json:"package,omitempty" jsonschema:"title=Package name,description=The name of the sub-package to which this overlay applies"`
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
	Source string `toml:"source,omitempty" json:"source,omitempty" jsonschema:"title=Source,description=For overlays that require a source file as input, indicates a path to that file; relative paths are relative to the config file that defines the overlay"`
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
		c.Type == ComponentOverlayAddPatch ||
		c.Type == ComponentOverlayRemovePatch
}

// ModifiesNonSpecFiles returns true if the overlay modifies non-spec files. This includes
// hybrid overlays that modify both spec and source files (e.g., patch overlays), since
// those also require non-spec modifications.
func (c *ComponentOverlay) ModifiesNonSpecFiles() bool {
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
	// ComponentOverlayRemoveFile is an overlay that removes a non-spec file.
	ComponentOverlayRemoveFile ComponentOverlayType = "file-remove"
	// ComponentOverlayRenameFile is an overlay that renames a non-spec file.
	ComponentOverlayRenameFile ComponentOverlayType = "file-rename"
)

// Validate checks that required fields are set based on the overlay type. This catches
// configuration errors at load time rather than at apply time.
//
//nolint:cyclop,gocognit,gocyclo,funlen // complexity is inherent to the number of overlay types.
func (c *ComponentOverlay) Validate() error {
	desc := c.Description
	if desc == "" {
		desc = "(no description)"
	}

	missingField := func(fieldName string) error {
		return fmt.Errorf("overlay type %#q requires %#q field: %s", c.Type, fieldName, desc)
	}

	requireRelativePath := func(fieldName, value string) error {
		if value == "" {
			return missingField(fieldName)
		}

		if filepath.IsAbs(value) {
			return fmt.Errorf(
				"overlay type %#q requires %#q to be a relative path; found %#q",
				c.Type, fieldName, value,
			)
		}

		return nil
	}

	requireFileBasename := func(fieldName, value string) error {
		if value == "" {
			return missingField(fieldName)
		}

		if value != filepath.Base(value) {
			return fmt.Errorf(
				"overlay type %#q requires %#q to be a filename only (not a path); found %#q",
				c.Type, fieldName, value,
			)
		}

		return nil
	}

	switch c.Type {
	case ComponentOverlayAddSpecTag, ComponentOverlayInsertSpecTag,
		ComponentOverlaySetSpecTag, ComponentOverlayUpdateSpecTag:
		if c.Tag == "" {
			return missingField("tag")
		}

		if c.Value == "" {
			return missingField("value")
		}
	case ComponentOverlayRemoveSpecTag:
		if c.Tag == "" {
			return missingField("tag")
		}
	case ComponentOverlayPrependSpecLines, ComponentOverlayAppendSpecLines:
		if len(c.Lines) == 0 {
			return missingField("lines")
		}
	case ComponentOverlaySearchAndReplaceInSpec:
		if c.Regex == "" {
			return missingField("regex")
		}

		if err := validateRegex(c.Regex, desc); err != nil {
			return err
		}
	case ComponentOverlayPrependLinesToFile:
		if err := requireRelativePath("file", c.Filename); err != nil {
			return err
		}

		if len(c.Lines) == 0 {
			return missingField("lines")
		}
	case ComponentOverlaySearchAndReplaceInFile:
		if err := requireRelativePath("file", c.Filename); err != nil {
			return err
		}

		if c.Regex == "" {
			return missingField("regex")
		}

		if err := validateRegex(c.Regex, desc); err != nil {
			return err
		}
	case ComponentOverlayAddFile:
		if err := requireRelativePath("file", c.Filename); err != nil {
			return err
		}

		if c.Source == "" {
			return missingField("source")
		}
	case ComponentOverlayRemoveFile:
		if err := requireRelativePath("file", c.Filename); err != nil {
			return err
		}
	case ComponentOverlayRenameFile:
		if err := requireRelativePath("file", c.Filename); err != nil {
			return err
		}

		if err := requireFileBasename("replacement", c.Replacement); err != nil {
			return err
		}
	case ComponentOverlayRemoveSection:
		if c.SectionName == "" {
			return missingField("section")
		}
	case ComponentOverlayAddPatch:
		if c.Source == "" {
			return missingField("source")
		}

		// Filename is optional; if provided, it must be a relative path.
		if c.Filename != "" {
			if err := requireRelativePath("file", c.Filename); err != nil {
				return err
			}
		}
	case ComponentOverlayRemovePatch:
		if err := requireRelativePath("file", c.Filename); err != nil {
			return err
		}

		if err := validateGlobPattern(c.Filename, desc); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown overlay type %#q: %#q", c.Type, desc)
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
