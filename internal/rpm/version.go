// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package rpm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	rpmversion "github.com/knqyf263/go-rpm-version"
)

// Version is a wrapper around the RPM version implementation from github.com/knqyf263/go-rpm-version,
// preventing creation of invalid versions.
type Version struct {
	version rpmversion.Version
}

// NewVersion creates a new Version from a string and returns an error if the version format is invalid.
func NewVersion(fullVersionString string) (*Version, error) {
	// The underlying library doesn't return errors on invalid formats,
	// so we need to validate the version ourselves.
	err := verifyFullVersionString(fullVersionString)
	if err != nil {
		return nil, fmt.Errorf("invalid RPM version format:\n%w", err)
	}

	return &Version{
		version: rpmversion.NewVersion(fullVersionString),
	}, nil
}

// NewVersionFromEVR creates a new Version from epoch, version, and release strings.
func NewVersionFromEVR(epoch, version, release string) (*Version, error) {
	// NOTE: It's a bit odd to turn this back into a string, just for it to get parsed again, but
	// the underlying library we're using doesn't have a constructor for epoch, version, and release
	// directly.
	versionStr := fmt.Sprintf("%s-%s", version, release)
	if epoch != "" {
		versionStr = epoch + ":" + versionStr
	}

	return NewVersion(versionStr)
}

// Compare compares this version with another version.
// Returns -1, 0, or 1 if this version is less than, equal to, or greater than the other version.
func (v *Version) Compare(other *Version) int {
	return v.version.Compare(other.version)
}

// Epoch returns the epoch part of the RPM version.
func (v *Version) Epoch() int {
	return v.version.Epoch()
}

// Equal returns true if this version is equal to the other version.
func (v *Version) Equal(other *Version) bool {
	return v.version.Equal(other.version)
}

// GreaterThan returns true if this version is greater than the other version.
func (v *Version) GreaterThan(other *Version) bool {
	return v.version.GreaterThan(other.version)
}

// LessThan returns true if this version is less than the other version.
func (v *Version) LessThan(other *Version) bool {
	return v.version.LessThan(other.version)
}

// Release returns the release part of the RPM version.
func (v *Version) Release() string {
	return v.version.Release()
}

// String returns the string representation of the version.
func (v *Version) String() string {
	return v.version.String()
}

// Version returns the version part of the RPM version.
func (v *Version) Version() string {
	return v.version.Version()
}

// MarshalJSON serializes the version to JSON, thereby implementing [json.Marshaler].
func (v *Version) MarshalJSON() ([]byte, error) {
	properties := map[string]interface{}{
		"Epoch":   v.Epoch(),
		"Version": v.Version(),
		"Release": v.Release(),
	}

	bytes, err := json.Marshal(properties)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal version to JSON:\n%w", err)
	}

	return bytes, nil
}

func verifyEpochString(epochString string, fullVersionString string) error {
	if epochString == "" {
		return fmt.Errorf("RPM epoch in version %#q is empty despite a colon being present", fullVersionString)
	}

	if epochNumber, err := strconv.Atoi(epochString); err != nil || epochNumber < 0 {
		return fmt.Errorf("RPM epoch %#q in version %#q is not a valid positive integer", epochString, fullVersionString)
	}

	return nil
}

func verifyReleaseString(releaseString string, fullVersionString string) error {
	if releaseString == "" {
		return fmt.Errorf("RPM release string in version %#q is empty after the hyphen", fullVersionString)
	}

	return nil
}

func verifyVersionString(versionString string, fullVersionString string) error {
	if versionString == "" {
		return fmt.Errorf("RPM version string in version %#q is empty before the hyphen", fullVersionString)
	}

	return nil
}

// verifyFullVersionString checks if the version string is valid.
// Using explicit checks instead of regex to provide more detailed error messages.
func verifyFullVersionString(fullVersionString string) error {
	const (
		maxColonSplitParts  = 2
		maxHyphenSplitParts = 2
	)

	if fullVersionString == "" {
		return errors.New("RPM version string is empty")
	}

	invalidChars := []string{" ", "/", "\\", "!", "@"}
	for _, char := range invalidChars {
		if strings.Contains(fullVersionString, char) {
			return fmt.Errorf("RPM version %#q contains invalid character '%s'", fullVersionString, char)
		}
	}

	colonSplit := strings.Split(fullVersionString, ":")
	if len(colonSplit) > maxColonSplitParts {
		return fmt.Errorf("RPM version %#q has more than one colon", fullVersionString)
	}

	versionString := fullVersionString

	if len(colonSplit) == maxColonSplitParts {
		err := verifyEpochString(colonSplit[0], fullVersionString)
		if err != nil {
			return err
		}

		versionString = colonSplit[1]
	}

	hyphenSplit := strings.Split(versionString, "-")
	if len(hyphenSplit) > maxHyphenSplitParts {
		return fmt.Errorf("RPM version %#q has more than one hyphen", fullVersionString)
	}

	err := verifyVersionString(hyphenSplit[0], versionString)
	if err != nil {
		return err
	}

	if len(hyphenSplit) == maxHyphenSplitParts {
		err := verifyReleaseString(hyphenSplit[1], fullVersionString)
		if err != nil {
			return err
		}
	}

	return nil
}
