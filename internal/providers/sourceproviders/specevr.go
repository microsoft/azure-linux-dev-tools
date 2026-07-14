// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
)

// SpecEVR bundles the Name/Epoch/Version/Release tag values extracted from
// the base package of an RPM spec file.
//
// The values are captured verbatim from the spec's tag lines; macros such as
// %{?dist} are NOT expanded. Callers that need a fully expanded form are
// responsible for macro substitution themselves.
type SpecEVR struct {
	Name    string
	Epoch   string
	Version string
	Release string
}

// ParseSpecEVR parses NEVR tags from the base package of the spec content
// read from reader.
//
// The parse is a lightweight tag-line scan (no rpmspec expansion). Returns
// the captured tag values with macros preserved verbatim. It is not an error
// for individual tags to be missing; missing tags simply yield empty strings
// in the corresponding [SpecEVR] fields.
func ParseSpecEVR(reader io.Reader) (SpecEVR, error) {
	if reader == nil {
		return SpecEVR{}, errors.New("reader cannot be nil")
	}

	parsed, err := spec.OpenSpec(reader)
	if err != nil {
		return SpecEVR{}, fmt.Errorf("parsing spec:\n%w", err)
	}

	var result SpecEVR

	// VisitTagsPackage("") iterates tags in the base (unnamed) package. That
	// is where Name/Epoch/Version/Release live for a well-formed spec.
	visitErr := parsed.VisitTagsPackage("", func(tagLine *spec.TagLine, _ *spec.Context) error {
		switch strings.ToLower(tagLine.Tag) {
		case "name":
			if result.Name == "" {
				result.Name = strings.TrimSpace(tagLine.Value)
			}
		case "epoch":
			if result.Epoch == "" {
				result.Epoch = strings.TrimSpace(tagLine.Value)
			}
		case "version":
			if result.Version == "" {
				result.Version = strings.TrimSpace(tagLine.Value)
			}
		case "release":
			if result.Release == "" {
				result.Release = strings.TrimSpace(tagLine.Value)
			}
		}

		return nil
	})
	if visitErr != nil {
		return SpecEVR{}, fmt.Errorf("scanning spec tags:\n%w", visitErr)
	}

	return result, nil
}

// ParseSpecEVRFromFile is a convenience wrapper around [ParseSpecEVR] that
// opens the given spec file from filesystem.
func ParseSpecEVRFromFile(filesystem opctx.FS, path string) (result SpecEVR, err error) {
	if path == "" {
		return SpecEVR{}, errors.New("spec path cannot be empty")
	}

	file, openErr := filesystem.Open(path)
	if openErr != nil {
		return SpecEVR{}, fmt.Errorf("opening spec %#q:\n%w", path, openErr)
	}

	defer defers.HandleDeferError(file.Close, &err)

	return ParseSpecEVR(file)
}
