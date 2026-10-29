// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lisa

import (
	"errors"
	"fmt"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// parseLisaSource extracts and validates the LISA framework git source from a new-style
// [tests.X.lisa] subtable. A 'source' (git-url, ref) is required to run a test locally;
// tests without one are metadata-only and must be run through external LISA orchestration.
func parseLisaSource(lisa map[string]any, testName string) (*projectconfig.GitSourceConfig, error) {
	rawSource, hasSource := lisa["source"]
	if !hasSource {
		return nil, fmt.Errorf(
			"LISA test %#q cannot be run locally via 'azldev image test': it has no "+
				"[tests.%s.lisa.source] (git-url, ref) and must be run through the LISA "+
				"infrastructure; add a 'source' with a pinned commit SHA to enable local execution",
			testName, testName,
		)
	}

	sourceMap, ok := rawSource.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("test %#q lisa.source must be a table with 'git-url' and 'ref'", testName)
	}

	gitURL, _ := sourceMap["git-url"].(string)
	ref, _ := sourceMap["ref"].(string)

	source := &projectconfig.GitSourceConfig{GitURL: gitURL, Ref: ref}

	if err := source.Validate(fmt.Sprintf("test %#q lisa.source", testName)); err != nil {
		return nil, fmt.Errorf("test %#q lisa.source: %w", testName, err)
	}

	return source, nil
}

// parseLisaCriteriaFromDefinition converts a new-style [tests.X.lisa] subtable's selectors
// (criteria table/list, or top-level name/testcase-name/testcase-names) into the runbook
// criteria blocks used to generate a LISA runbook. Config-load-time validation already
// guarantees at least one selector is present and that criteria entries only use allowed
// keys, so this focuses on type conversion.
func parseLisaCriteriaFromDefinition(lisa map[string]any, testName string) ([]lisaCriteria, error) {
	var entries []map[string]any

	if rawCriteria, ok := lisa["criteria"]; ok {
		var err error

		entries, err = normalizeLisaCriteriaEntries(rawCriteria)
		if err != nil {
			return nil, fmt.Errorf("test %#q lisa.criteria: %w", testName, err)
		}
	}

	if len(entries) == 0 {
		topLevel := map[string]any{}

		for _, key := range []string{"name", "testcase-name", "testcase-names"} {
			if value, ok := lisa[key]; ok {
				topLevel[key] = value
			}
		}

		if len(topLevel) > 0 {
			entries = []map[string]any{topLevel}
		}
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf(
			"test %#q has no LISA selector (lisa.criteria, lisa.name, lisa.testcase-name, or "+
				"lisa.testcase-names)", testName,
		)
	}

	criteria := make([]lisaCriteria, 0, len(entries))

	for _, entry := range entries {
		converted, err := convertLisaCriteriaEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("test %#q lisa.criteria: %w", testName, err)
		}

		criteria = append(criteria, converted)
	}

	return criteria, nil
}

// normalizeLisaCriteriaEntries accepts either a single criteria table or a list of criteria
// tables, returning a uniform list of maps.
func normalizeLisaCriteriaEntries(raw any) ([]map[string]any, error) {
	switch value := raw.(type) {
	case map[string]any:
		return []map[string]any{value}, nil
	case []any:
		entries := make([]map[string]any, 0, len(value))

		for entryIndex, item := range value {
			entryMap, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("entry %d must be a table", entryIndex)
			}

			entries = append(entries, entryMap)
		}

		return entries, nil
	default:
		return nil, errors.New("must be a table or list of tables")
	}
}

// convertLisaCriteriaEntry converts a single criteria (or top-level selector) map into a
// lisaCriteria runbook block. 'testcase-name'/'testcase-names' are azldev conveniences for
// LISA's underlying 'name' filter and are joined with '|' when multiple are given; an
// explicit 'name' takes precedence if also present.
func convertLisaCriteriaEntry(entry map[string]any) (lisaCriteria, error) {
	criteria := lisaCriteria{}

	if name, ok := entry["name"].(string); ok {
		criteria.Name = name
	}

	if criteria.Name == "" {
		if name, ok := entry["testcase-name"].(string); ok {
			criteria.Name = name
		}
	}

	if criteria.Name == "" {
		if rawNames, ok := entry["testcase-names"]; ok {
			names, err := toStringSlice(rawNames)
			if err != nil {
				return lisaCriteria{}, fmt.Errorf("testcase-names: %w", err)
			}

			criteria.Name = strings.Join(names, "|")
		}
	}

	if area, ok := entry["area"].(string); ok {
		criteria.Area = area
	}

	if category, ok := entry["category"].(string); ok {
		criteria.Category = category
	}

	if rawPriority, ok := entry["priority"]; ok {
		priority, err := convertLisaPriority(rawPriority)
		if err != nil {
			return lisaCriteria{}, fmt.Errorf("priority: %w", err)
		}

		criteria.Priority = priority
	}

	if rawTags, ok := entry["tags"]; ok {
		tags, err := toStringSlice(rawTags)
		if err != nil {
			return lisaCriteria{}, fmt.Errorf("tags: %w", err)
		}

		criteria.Tags = tags
	}

	return criteria, nil
}

// convertLisaPriority converts a TOML-decoded priority value (a single integer or a list of
// integers) into either an int or []int for embedding into the generated runbook YAML.
func convertLisaPriority(value any) (any, error) {
	if n, ok := toInt(value); ok {
		return n, nil
	}

	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an integer or list of integers, got %T", value)
	}

	result := make([]int, 0, len(items))

	for _, item := range items {
		n, ok := toInt(item)
		if !ok {
			return nil, fmt.Errorf("must be a list of integers, got element of type %T", item)
		}

		result = append(result, n)
	}

	return result, nil
}

// toInt converts a TOML-decoded numeric value (int, int64, or float64) into an int.
func toInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}

		return int(typed), true
	default:
		return 0, false
	}
}

// toStringSlice converts a TOML-decoded list value ([]any of strings) into a []string.
func toStringSlice(value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("must be a list of strings, got %T", value)
	}

	result := make([]string, 0, len(items))

	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("must be a list of strings, got element of type %T", item)
		}

		result = append(result, s)
	}

	return result, nil
}

// toOptionalStringSlice converts an optional TOML-decoded list value into a []string,
// returning nil (with no error) when the value is absent.
func toOptionalStringSlice(value any, fieldName string) ([]string, error) {
	if value == nil {
		return nil, nil
	}

	list, err := toStringSlice(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fieldName, err)
	}

	return list, nil
}
