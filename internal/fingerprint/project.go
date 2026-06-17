// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// currentContentVersion is the highest content version that exists. The reset
// establishes v1, so projectV1 is the current (and only) projection and the tag
// parser rejects any field that references a future version.
const currentContentVersion = 1

// FingerprintedStructTypes returns every struct type whose fields participate in
// the component fingerprint. It is the single source of truth that both the
// mandatory-tag decision test (`projectconfig.TestAllFingerprintedFieldsHaveDecision`)
// and the emission probe (`measuredLeafEmitKeys`) walk - keeping it in one
// exported place is what stops those two walks from drifting silently (a struct
// dropped from one but not the other would weaken coverage with no failure).
//
// It is hand-maintained until the projection generator (which would derive it
// from projectV1 directly) lands. When you add a new struct type to the
// fingerprinted graph, add it here - and nowhere else.
func FingerprintedStructTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeFor[projectconfig.ComponentConfig](),
		reflect.TypeFor[projectconfig.ComponentBuildConfig](),
		reflect.TypeFor[projectconfig.CheckConfig](),
		reflect.TypeFor[projectconfig.PackageConfig](),
		reflect.TypeFor[projectconfig.ComponentOverlay](),
		reflect.TypeFor[projectconfig.SpecSource](),
		reflect.TypeFor[projectconfig.DistroReference](),
		reflect.TypeFor[projectconfig.SourceFileReference](),
		reflect.TypeFor[projectconfig.ReleaseConfig](),
		reflect.TypeFor[projectconfig.ComponentRenderConfig](),
	}
}

// ValidateFieldTag checks one fingerprinted struct field's tag at the current
// content version. It enforces the mandatory-decision rule (an absent tag is an
// error - every fingerprinted field must declare a version-set or exclusion),
// rejects an unparseable / future-referencing tag, and requires a resolvable
// emit-key for a measured field.
//
// It also gates the documented composite-'!' placeholder: a nested struct, map,
// or slice-of-struct tagged with an always-emit range is not implemented at v1,
// so it is rejected here rather than silently guessing an encoding.
func ValidateFieldTag(field reflect.StructField) error {
	set, err := parseVersionSet(field.Tag.Get(fingerprintTagName), currentContentVersion)
	if err != nil {
		return fmt.Errorf("field %#q:\n%w", field.Name, err)
	}

	if set.excluded {
		return nil
	}

	if _, err := set.resolveEmitKey(tomlKeyOf(field)); err != nil {
		return fmt.Errorf("field %#q:\n%w", field.Name, err)
	}

	if !isScalarLeaf(reflect.New(field.Type).Elem()) {
		for _, rng := range set.ranges {
			if rng.alwaysEmit {
				return fmt.Errorf(
					"field %#q: composite-'!' (always-emit nested struct, map, or slice-of-struct) "+
						"is not implemented at v1",
					field.Name)
			}
		}
	}

	return nil
}

// tomlKeyOf returns the field's bare toml key (the part before the first comma),
// or "" when there is no toml tag.
func tomlKeyOf(field reflect.StructField) string {
	key, _, _ := strings.Cut(field.Tag.Get("toml"), ",")

	return key
}
