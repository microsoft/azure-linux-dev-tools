// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// versionOpen is the high-bound sentinel for an open-ended range ("*"), meaning
// "this version and every later one".
const versionOpen = -1

// excludeTag is the fingerprint tag that marks a field as never measured.
const excludeTag = "-"

// keyPrefix is the fingerprint-tag prefix for an explicit emit-key override.
const keyPrefix = "key="

// versionRange is one closed (inclusive) membership range in a version set.
// A range covers [low, high]; high == versionOpen means open-ended ("*").
type versionRange struct {
	low        int
	high       int
	alwaysEmit bool
}

// versionSet is the parsed form of a field's fingerprint tag: the set of
// content versions that measure the field, whether each range always-emits, and
// an optional emit-key override.
type versionSet struct {
	// excluded is true when the tag is "-" (the field is never measured).
	excluded bool
	// emitKey is the explicit "key=" override, or "" to use the field's toml key.
	emitKey string
	// ranges are the membership ranges, sorted by low bound and non-overlapping.
	ranges []versionRange
}

// parseVersionSet parses a fingerprint struct-tag value against currentVersion
// (the highest content version that exists). It rejects malformed, overlapping,
// future-referencing (any bound above currentVersion), and duplicate-key= tags.
// An absent tag is the caller's responsibility: the empty string is rejected
// here because every fingerprinted field must carry an explicit decision.
func parseVersionSet(tag string, currentVersion int) (versionSet, error) {
	if tag == excludeTag {
		return versionSet{excluded: true}, nil
	}

	if tag == "" {
		return versionSet{}, errors.New("empty fingerprint tag: every fingerprinted field must carry an explicit decision")
	}

	members := strings.Split(tag, ",")

	set := versionSet{}

	// An optional key= override must be the first comma-separated element.
	if strings.HasPrefix(members[0], keyPrefix) {
		emitKey := strings.TrimPrefix(members[0], keyPrefix)
		if emitKey == "" {
			return versionSet{}, fmt.Errorf("empty key= override in fingerprint tag %#q", tag)
		}

		if !isValidEmitKey(emitKey) {
			return versionSet{}, fmt.Errorf(
				"invalid key= override %#q in fingerprint tag %#q: must be a bare identifier "+
					"(letters, digits, '-', '_', '.')", emitKey, tag)
		}

		set.emitKey = emitKey
		members = members[1:]
	}

	if len(members) == 0 {
		return versionSet{}, fmt.Errorf("fingerprint tag %#q has a key= override but no version ranges", tag)
	}

	for _, member := range members {
		if strings.HasPrefix(member, keyPrefix) {
			return versionSet{}, fmt.Errorf(
				"fingerprint tag %#q: a key= override must be the first tag element", tag)
		}

		rng, err := parseRange(member, currentVersion)
		if err != nil {
			return versionSet{}, fmt.Errorf("fingerprint tag %#q:\n%w", tag, err)
		}

		set.ranges = append(set.ranges, rng)
	}

	if err := sortAndValidateRanges(set.ranges); err != nil {
		return versionSet{}, fmt.Errorf("fingerprint tag %#q:\n%w", tag, err)
	}

	return set, nil
}

// isValidEmitKey reports whether s is a bare emit-key identifier - the character
// set of a frozen TOML key (letters, digits, '-', '_', '.'). The grammar's key=
// override takes an identifier, so a malformed value like "foo bar" is rejected
// up front rather than silently freezing an odd emit-key.
func isValidEmitKey(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
		default:
			return false
		}
	}

	return s != ""
}

// parseRange parses a single "[!]vLow[..(vHigh|*)]" member.
func parseRange(member string, currentVersion int) (versionRange, error) {
	rng := versionRange{}

	// rangeText keeps the original member (including any leading '!') for error
	// messages, so a malformed "!v3..v1" is reported as written, not as "v3..v1".
	rangeText := member

	if strings.HasPrefix(member, "!") {
		rng.alwaysEmit = true
		member = strings.TrimPrefix(member, "!")
	}

	low, high, found := strings.Cut(member, "..")

	lowVersion, err := parseVersion(low)
	if err != nil {
		return versionRange{}, err
	}

	rng.low = lowVersion

	switch {
	case !found:
		// "vN" is shorthand for the single-version range vN..vN.
		rng.high = lowVersion
	case high == "*":
		rng.high = versionOpen
	default:
		highVersion, highErr := parseVersion(high)
		if highErr != nil {
			return versionRange{}, highErr
		}

		if highVersion < lowVersion {
			return versionRange{}, fmt.Errorf("range %#q is inverted: high bound is below low bound", rangeText)
		}

		rng.high = highVersion
	}

	if rng.low > currentVersion {
		return versionRange{}, fmt.Errorf(
			"range %#q references v%d, which is beyond the current version v%d", rangeText, rng.low, currentVersion)
	}

	if rng.high != versionOpen && rng.high > currentVersion {
		return versionRange{}, fmt.Errorf(
			"range %#q references v%d, which is beyond the current version v%d", rangeText, rng.high, currentVersion)
	}

	return rng, nil
}

// parseVersion parses a "vN" token into its integer N (N >= 1).
func parseVersion(token string) (int, error) {
	if !strings.HasPrefix(token, "v") {
		return 0, fmt.Errorf("version %#q must start with 'v'", token)
	}

	digits := strings.TrimPrefix(token, "v")

	version, err := strconv.Atoi(digits)
	if err != nil {
		return 0, fmt.Errorf("version %#q has a non-numeric component", token)
	}

	if version < 1 {
		return 0, fmt.Errorf("version %#q must be v1 or greater", token)
	}

	return version, nil
}

// sortAndValidateRanges sorts the ranges by low bound and rejects any overlap,
// so a version can never match two ranges (which would alias two always-emit
// flags).
func sortAndValidateRanges(ranges []versionRange) error {
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].low < ranges[j].low
	})

	for i := 1; i < len(ranges); i++ {
		previous := ranges[i-1]
		current := ranges[i]

		if previous.high == versionOpen || current.low <= previous.high {
			return fmt.Errorf("overlapping ranges at v%d and v%d", previous.low, current.low)
		}
	}

	return nil
}

// measuredAt reports whether the field is measured at the given content version
// and, if so, whether that version always-emits the field (the '!' flag).
func (s versionSet) measuredAt(version int) (measured bool, alwaysEmit bool) {
	for _, rng := range s.ranges {
		if version >= rng.low && (rng.high == versionOpen || version <= rng.high) {
			return true, rng.alwaysEmit
		}
	}

	return false, false
}

// resolveEmitKey returns the stable emit-key for the field: the explicit key=
// override if present, otherwise the field's toml key. A field with neither a
// usable toml key nor a key= override is an error, because its bytes could not
// be pinned to a stable string.
func (s versionSet) resolveEmitKey(tomlKey string) (string, error) {
	if s.emitKey != "" {
		return s.emitKey, nil
	}

	if tomlKey == "" || tomlKey == excludeTag {
		return "", errors.New("field has no usable toml key and no key= override in its fingerprint tag")
	}

	return tomlKey, nil
}
