// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cttools

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// LoadConfig loads a distro config starting from the given top-level TOML file path.
// It recursively resolves `include` directives (relative glob paths), deep-merges
// all included files, and returns the final merged raw map.
func LoadConfig(topLevelPath string) (*DistroConfig, error) {
	absPath, err := filepath.Abs(topLevelPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for %#q:\n%w", topLevelPath, err)
	}

	visited := make(map[string]bool)

	merged, err := loadAndMerge(absPath, visited)
	if err != nil {
		return nil, err
	}

	// Remove the include key from the merged map before marshalling to typed struct.
	delete(merged, "include")

	// Re-serialize the merged map to TOML, then unmarshal into the typed struct.
	buf, err := toml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged config:\n%w", err)
	}

	var config DistroConfig
	if err := toml.Unmarshal(buf, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal merged config into typed struct:\n%w", err)
	}

	return &config, nil
}

// loadAndMerge loads a single TOML file, processes its include directives,
// and returns the deep-merged result as a raw map.
func loadAndMerge(absPath string, visited map[string]bool) (map[string]any, error) {
	if visited[absPath] {
		return nil, fmt.Errorf("circular include detected for %#q", absPath)
	}

	visited[absPath] = true

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %#q:\n%w", absPath, err)
	}

	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse TOML %#q:\n%w", absPath, err)
	}

	// Extract and process includes.
	includes, err := extractIncludes(raw, absPath)
	if err != nil {
		return nil, err
	}

	// Start with the current file's data (without include key).
	result := make(map[string]any)
	deepMergeMaps(result, raw)
	delete(result, "include")

	// Load each included file and merge into result.
	dir := filepath.Dir(absPath)

	for _, pattern := range includes {
		globPath := filepath.Join(dir, pattern)

		matches, err := filepath.Glob(globPath)
		if err != nil {
			return nil, fmt.Errorf("failed to glob %#q (from include in %#q):\n%w", globPath, absPath, err)
		}

		for _, match := range matches {
			matchAbs, err := filepath.Abs(match)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve absolute path for %#q:\n%w", match, err)
			}

			// Skip self-includes (e.g., when a glob like "./*.toml" matches the current file).
			if matchAbs == absPath {
				continue
			}

			child, err := loadAndMerge(matchAbs, visited)
			if err != nil {
				return nil, fmt.Errorf("error loading include %#q from %#q:\n%w", match, absPath, err)
			}

			deepMergeMaps(result, child)
		}
	}

	return result, nil
}

// extractIncludes reads the "include" key from a raw TOML map and returns it as a string slice.
func extractIncludes(raw map[string]any, filePath string) ([]string, error) {
	includeVal, hasInclude := raw["include"]
	if !hasInclude {
		return nil, nil
	}

	includeSlice, isSlice := includeVal.([]any)
	if !isSlice {
		return nil, fmt.Errorf("'include' in %#q must be an array of strings", filePath)
	}

	result := make([]string, 0, len(includeSlice))

	for _, v := range includeSlice {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("'include' entry in %#q must be a string, got %T", filePath, v)
		}

		result = append(result, s)
	}

	return result, nil
}

// deepMergeMaps merges src into dst recursively. For map values, sub-maps are merged recursively.
// For slice values, slices are concatenated. For all other types, src overwrites dst.
func deepMergeMaps(dst, src map[string]any) {
	for key, srcVal := range src {
		dstVal, exists := dst[key]
		if !exists {
			dst[key] = srcVal

			continue
		}

		// If both are maps, merge recursively.
		srcMap, srcIsMap := srcVal.(map[string]any)
		dstMap, dstIsMap := dstVal.(map[string]any)

		if srcIsMap && dstIsMap {
			deepMergeMaps(dstMap, srcMap)

			continue
		}

		// If both are slices, concatenate.
		srcSlice, srcIsSlice := srcVal.([]any)
		dstSlice, dstIsSlice := dstVal.([]any)

		if srcIsSlice && dstIsSlice {
			dst[key] = append(dstSlice, srcSlice...)

			continue
		}

		// Otherwise, src overwrites dst.
		dst[key] = srcVal
	}
}
