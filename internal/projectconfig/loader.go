// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/pelletier/go-toml/v2"
)

var (
	// ErrDuplicateComponents is returned when duplicate conflicting component definitions are found.
	ErrDuplicateComponents = errors.New("duplicate component")
	// ErrDuplicateComponentGroups is returned when duplicate conflicting component group definitions are found.
	ErrDuplicateComponentGroups = errors.New("duplicate component group")
	// ErrDuplicateImages is returned when duplicate conflicting image definitions are found.
	ErrDuplicateImages = errors.New("duplicate image")
	// ErrDuplicatePackageGroups is returned when duplicate conflicting package group definitions are found.
	ErrDuplicatePackageGroups = errors.New("duplicate package group")
)

// Loads and resolves the project configuration files located at the given path. Referenced include files
// are recursively loaded and appropriately merged. If multiple file paths are provided, they are each
// fully loaded and merged in specified order, with later files overriding earlier ones.
func loadAndResolveProjectConfig(
	fs opctx.FS, permissiveConfigParsing bool, configFilePaths ...string,
) (*ProjectConfig, error) {
	resolvedCfg := &ProjectConfig{
		ComponentGroups:   make(map[string]ComponentGroupConfig),
		Components:        make(map[string]ComponentConfig),
		Images:            make(map[string]ImageConfig),
		Distros:           make(map[string]DistroDefinition),
		GroupsByComponent: make(map[string][]string),
		PackageGroups:     make(map[string]PackageGroupConfig),
		TestSuites:        make(map[string]TestSuiteConfig),
	}

	for _, configFilePath := range configFilePaths {
		// Load the project config file and all transitive includes.
		err := loadAndMergeConfigWithIncludes(resolvedCfg, fs, configFilePath, permissiveConfigParsing)
		if err != nil {
			return nil, err
		}
	}

	// Validate the resulting configuration.
	err := resolvedCfg.Validate()
	if err != nil {
		return nil, err
	}

	return resolvedCfg, nil
}

func loadAndMergeConfigWithIncludes(
	configToUpdate *ProjectConfig, fs opctx.FS, filePath string,
	permissiveConfigParsing bool,
) error {
	// Load the project config file and all transitive includes.
	loadedCfgs, err := loadProjectConfigWithIncludes(fs, filePath, permissiveConfigParsing)
	if err != nil {
		return err
	}

	// Go through all the loaded configs and merge them into the resolved config.
	err = mergeConfigFiles(configToUpdate, loadedCfgs)
	if err != nil {
		return err
	}

	return nil
}

func mergeConfigFiles(resolvedCfg *ProjectConfig, loadedCfgs []*ConfigFile) error {
	for _, loadedCfg := range loadedCfgs {
		err := mergeConfigFile(resolvedCfg, loadedCfg)
		if err != nil {
			return err
		}
	}

	return nil
}

func mergeConfigFile(resolvedCfg *ProjectConfig, loadedCfg *ConfigFile) error {
	if loadedCfg.Project != nil {
		err := resolvedCfg.Project.MergeUpdatesFrom(loadedCfg.Project.WithAbsolutePaths(loadedCfg.dir))
		if err != nil {
			return err
		}
	}

	if err := mergeDistros(resolvedCfg, loadedCfg); err != nil {
		return err
	}

	if err := mergeComponentGroups(resolvedCfg, loadedCfg); err != nil {
		return err
	}

	if err := mergeComponents(resolvedCfg, loadedCfg); err != nil {
		return err
	}

	if err := mergeImages(resolvedCfg, loadedCfg); err != nil {
		return err
	}

	if err := mergeTools(resolvedCfg, loadedCfg); err != nil {
		return err
	}

	if err := mergeDefaultComponentConfig(resolvedCfg, loadedCfg); err != nil {
		return err
	}

	if err := mergeDefaultPackageConfig(resolvedCfg, loadedCfg); err != nil {
		return err
	}

	if err := mergePackageGroups(resolvedCfg, loadedCfg); err != nil {
		return err
	}

	if err := mergeTestSuites(resolvedCfg, loadedCfg); err != nil {
		return err
	}

	return nil
}

// mergeDistros merges distro definitions from a loaded config file into the
// resolved config. Distros support additive merging: if a distro already exists,
// its fields are updated from the new definition.
func mergeDistros(resolvedCfg *ProjectConfig, loadedCfg *ConfigFile) error {
	for distroName, distro := range loadedCfg.Distros {
		resolvedDistro := distro.WithResolvedConfigs()
		resolvedDistro = resolvedDistro.WithAbsolutePaths(loadedCfg.dir)

		if existing, ok := resolvedCfg.Distros[distroName]; ok {
			err := existing.MergeUpdatesFrom(&resolvedDistro)
			if err != nil {
				return fmt.Errorf("failed to merge distro %#q:\n%w", distroName, err)
			}

			resolvedCfg.Distros[distroName] = existing
		} else {
			resolvedCfg.Distros[distroName] = resolvedDistro
		}
	}

	return nil
}

// mergeComponentGroups merges component group definitions from a loaded config
// file into the resolved config. Duplicate group names are not allowed. For each
// group, the reverse index ([ProjectConfig.GroupsByComponent]) is also updated.
func mergeComponentGroups(resolvedCfg *ProjectConfig, loadedCfg *ConfigFile) error {
	for groupName, group := range loadedCfg.ComponentGroups {
		if _, ok := resolvedCfg.ComponentGroups[groupName]; ok {
			return fmt.Errorf("%w: %s", ErrDuplicateComponentGroups, groupName)
		}

		resolvedCfg.ComponentGroups[groupName] = group.WithAbsolutePaths(loadedCfg.dir)

		// Keep a mapping from component names to component groups so
		// we can easily find all groups that a given component is a member of.
		for _, member := range group.Components {
			resolvedCfg.GroupsByComponent[member] = append(
				resolvedCfg.GroupsByComponent[member], groupName)
		}
	}

	return nil
}

// mergeComponents merges component definitions from a loaded config file into
// the resolved config. Duplicate component names are not allowed.
func mergeComponents(resolvedCfg *ProjectConfig, loadedCfg *ConfigFile) error {
	for componentName, component := range loadedCfg.Components {
		if _, ok := resolvedCfg.Components[componentName]; ok {
			return fmt.Errorf("%w: %s", ErrDuplicateComponents, componentName)
		}

		// Fill out fields not explicitly serialized.
		component.Name = componentName
		component.SourceConfigFile = loadedCfg

		resolvedCfg.Components[componentName] = *(component.WithAbsolutePaths(loadedCfg.dir))
	}

	return nil
}

// mergeImages merges image definitions from a loaded config file into the
// resolved config. Duplicate image names are not allowed.
func mergeImages(resolvedCfg *ProjectConfig, loadedCfg *ConfigFile) error {
	for imageName, image := range loadedCfg.Images {
		if _, ok := resolvedCfg.Images[imageName]; ok {
			return fmt.Errorf("%w: %s", ErrDuplicateImages, imageName)
		}

		// Fill out fields not explicitly serialized.
		image.Name = imageName
		image.SourceConfigFile = loadedCfg

		resolvedCfg.Images[imageName] = *(image.WithAbsolutePaths(loadedCfg.dir))
	}

	return nil
}

// mergeTools merges tools configuration from a loaded config file into the
// resolved config.
func mergeTools(resolvedCfg *ProjectConfig, loadedCfg *ConfigFile) error {
	if loadedCfg.Tools != nil {
		err := resolvedCfg.Tools.MergeUpdatesFrom(loadedCfg.Tools)
		if err != nil {
			return err
		}
	}

	return nil
}

// mergeDefaultPackageConfig merges the project-level default package config from a loaded
// config file into the resolved config.
func mergeDefaultPackageConfig(resolvedCfg *ProjectConfig, loadedCfg *ConfigFile) error {
	if loadedCfg.DefaultPackageConfig != nil {
		if err := resolvedCfg.DefaultPackageConfig.MergeUpdatesFrom(loadedCfg.DefaultPackageConfig); err != nil {
			return fmt.Errorf("failed to merge project default package config:\n%w", err)
		}
	}

	return nil
}

// mergeDefaultComponentConfig merges the project-level default component config from a loaded
// config file into the resolved config.
func mergeDefaultComponentConfig(resolvedCfg *ProjectConfig, loadedCfg *ConfigFile) error {
	if loadedCfg.DefaultComponentConfig != nil {
		absConfig := loadedCfg.DefaultComponentConfig.WithAbsolutePaths(loadedCfg.dir)
		if err := resolvedCfg.DefaultComponentConfig.MergeUpdatesFrom(absConfig); err != nil {
			return fmt.Errorf("failed to merge project default component config:\n%w", err)
		}
	}

	return nil
}

// mergePackageGroups merges package group definitions from a loaded config file into
// the resolved config. Duplicate package group names are not allowed.
func mergePackageGroups(resolvedCfg *ProjectConfig, loadedCfg *ConfigFile) error {
	for groupName, group := range loadedCfg.PackageGroups {
		if _, ok := resolvedCfg.PackageGroups[groupName]; ok {
			return fmt.Errorf("%w: %#q", ErrDuplicatePackageGroups, groupName)
		}

		resolvedCfg.PackageGroups[groupName] = group
	}

	return nil
}

// mergeTestSuites merges test suite definitions from a loaded config file into the
// resolved config. Duplicate test suite names are not allowed.
func mergeTestSuites(resolvedCfg *ProjectConfig, loadedCfg *ConfigFile) error {
	for testName, test := range loadedCfg.TestSuites {
		if _, ok := resolvedCfg.TestSuites[testName]; ok {
			return fmt.Errorf("%w: test suite %#q", ErrDuplicateTestSuites, testName)
		}

		// Fill out fields not explicitly serialized.
		test.Name = testName
		test.SourceConfigFile = loadedCfg

		resolvedCfg.TestSuites[testName] = test
	}

	return nil
}

func loadProjectConfigWithIncludes(
	fs opctx.FS, filePath string, permissiveConfigParsing bool,
) ([]*ConfigFile, error) {
	// Load the immediate config file.
	cfg, err := loadProjectConfigFile(fs, filePath, permissiveConfigParsing)
	if err != nil {
		return nil, err
	}

	allCfgs := []*ConfigFile{cfg}

	// Iterate through specified include patterns, and load any matched includes.
	for _, includePattern := range cfg.Includes {
		absIncludePattern := makeAbsolute(cfg.dir, includePattern)

		matches, err := fileutils.Glob(fs, absIncludePattern,
			doublestar.WithFailOnIOErrors(), doublestar.WithFilesOnly(),
		)
		if err != nil {
			err = fmt.Errorf("failed to expand include pattern '%s' in config file '%s':\n%w", includePattern, filePath, err)

			return nil, err
		}

		if len(matches) == 0 && !containsPattern(includePattern) {
			err = fmt.Errorf(
				"failed to find include file '%s' referenced in config file '%s':\n%w",
				includePattern, filePath, os.ErrNotExist,
			)

			return nil, err
		}

		for _, includePath := range matches {
			absIncludePath := makeAbsolute(cfg.dir, includePath)

			includeCfgs, err := loadProjectConfigWithIncludes(
				fs, absIncludePath, permissiveConfigParsing,
			)
			if err != nil {
				return nil, err
			}

			allCfgs = append(allCfgs, includeCfgs...)
		}
	}

	return allCfgs, nil
}

func loadProjectConfigFile(
	fs opctx.FS, filePath string, permissiveConfigParsing bool,
) (*ConfigFile, error) {
	slog.Debug("Loading project config", "filePath", filePath)

	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for '%s':\n%w", filePath, err)
	}

	projectFile, err := fs.Open(absFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open project config file at '%s':\n%w", absFilePath, err)
	}

	decoder := toml.NewDecoder(projectFile)

	if !permissiveConfigParsing {
		decoder.DisallowUnknownFields()
	}

	cfg := &ConfigFile{}

	err = decoder.Decode(&cfg)
	if err != nil {
		var stringableErr fmt.Stringer

		// See if we can convert the error to a stringable error; we've found that the stringified result has more
		// details.
		if errors.As(err, &stringableErr) {
			// Directly display error to stderr so the multi-line output is readable; otherwise, if we rely on
			// slog or propagate it back up only in an error, then the multi-line error will be squashed into a
			// single line and hard to read.
			fmt.Fprintf(os.Stderr, "Parse error: %s\n%s\n\n", filePath, stringableErr.String())

			return nil, fmt.Errorf("project config file %q contains invalid data:\n%w", filePath, err)
		}

		return nil, fmt.Errorf("failed to load project config from %s:\n%w", filePath, err)
	}

	// Keep track of where this came from.
	cfg.sourcePath = absFilePath
	cfg.dir = filepath.Dir(absFilePath)

	// Make sure that the read data is internally consistent.
	err = cfg.Validate()
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

func makeAbsolute(referenceDir, filePath string) string {
	if filePath == "" {
		return ""
	}

	if filepath.IsAbs(filePath) {
		return filePath
	}

	return path.Join(referenceDir, filePath)
}

func containsPattern(glob string) bool {
	return strings.ContainsAny(glob, "*?[")
}
