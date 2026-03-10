// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package workdir

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/buildenvfactory"
	"github.com/microsoft/azure-linux-dev-tools/internal/buildenv"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// defaultNameForGlobalWorkDir is the default name used for the global work directory that is
// not component-specific.
const defaultNameForGlobalWorkDir = "_global"

type Factory struct {
	fs            opctx.FS
	baseDir       string
	timestampTime time.Time
}

func NewFactory(fs opctx.FS, baseDir string, timestampTime time.Time) (*Factory, error) {
	if baseDir == "" {
		return nil, errors.New("can't setup work dir; root work dir was not specified in config")
	}

	return &Factory{
		fs:            fs,
		baseDir:       baseDir,
		timestampTime: timestampTime,
	}, nil
}

// Creates a temporary work directory appropriate for the indicated component. The provided
// label is used to create a unique directory name under the component's work directory.
// If a work directory with that label has already been used, then a unique one will be
// generated with a random suffix.
func (f *Factory) Create(
	componentName string, label string,
) (dirPath string, err error) {
	dateBasedDirName := f.timestampTime.Format("2006-01-02.150405")

	componentNameToUse := defaultNameForGlobalWorkDir
	if componentName != "" {
		componentNameToUse = componentName
	}

	parentDirPath := filepath.Join(f.baseDir, componentNameToUse, dateBasedDirName)

	err = fileutils.MkdirAll(f.fs, parentDirPath)
	if err != nil {
		return "", fmt.Errorf("failed to create work dir '%s':\n%w", parentDirPath, err)
	}

	if !strings.HasSuffix(label, "-") {
		label += "-"
	}

	dirPath, err = fileutils.MkdirTemp(f.fs, parentDirPath, label)
	if err != nil {
		return "", fmt.Errorf("failed to create temp work dir under '%s':\n%w", parentDirPath, err)
	}

	return dirPath, nil
}

// MkComponentBuildEnvironment creates a new working build environment for the specified
// component.
func MkComponentBuildEnvironment(
	env *azldev.Env,
	factory *Factory,
	component *projectconfig.ComponentConfig,
	label string,
	configOpts map[string]string,
) (buildEnv buildenv.RPMAwareBuildEnv, err error) {
	// Create a work directory for us to place the environment under.
	workDir, err := factory.Create(component.Name, label)
	if err != nil {
		return nil, fmt.Errorf("failed to create work dir for component %q:\n%w", component.Name, err)
	}

	// We'll nest the build environment in a subdirectory under the work directory.
	buildEnvDir := filepath.Join(workDir, "buildenv")

	buildEnvFactory, err := buildenvfactory.NewMockRootFactoryForEnv(env)
	if err != nil {
		return nil, fmt.Errorf("failed to create mock root factory:\n%w", err)
	}

	// Create the build environment with an auto-generated name.
	buildEnv, err = buildEnvFactory.CreateRPMAwareEnv(
		buildenv.CreateOptions{
			Name:        uuid.New().String(),
			Dir:         buildEnvDir,
			UserCreated: false,
			Description: fmt.Sprintf("Build: %s (%s)", component.Name, label),
			ConfigOpts:  configOpts,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create build environment for component %q:\n%w", component.Name, err)
	}

	return buildEnv, nil
}
