// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario || e2e

package scenario_tests

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	tmtImageEnv         = "AZL_TMT_IMAGE"
	tmtRPMEnv           = "AZL_TMT_RPM"
	tmtImageRevisionEnv = "AZL_TMT_IMAGE_REVISION"
	tmtEnableEnv        = "TESTING_ENABLE_TMT_VIRTUAL_E2E"
)

type tmtVirtualInputs struct {
	imagePath     string
	rpmPath       string
	imageRevision string
}

func loadTMTVirtualInputs(getenv func(string) string) (inputs tmtVirtualInputs, optedIn bool, err error) {
	inputs = tmtVirtualInputs{
		imagePath:     getenv(tmtImageEnv),
		rpmPath:       getenv(tmtRPMEnv),
		imageRevision: getenv(tmtImageRevisionEnv),
	}

	if inputs.imagePath == "" && inputs.rpmPath == "" {
		return inputs, false, nil
	}

	missing := ""
	switch {
	case inputs.imagePath == "":
		missing = tmtImageEnv
	case inputs.rpmPath == "":
		missing = tmtRPMEnv
	case inputs.imageRevision == "":
		missing = tmtImageRevisionEnv
	}
	if missing != "" {
		return inputs, true, fmt.Errorf("%s is required when the virtual TMT e2e test is enabled", missing)
	}

	for name, path := range map[string]string{
		tmtImageEnv: inputs.imagePath,
		tmtRPMEnv:   inputs.rpmPath,
	} {
		if !filepath.IsAbs(path) {
			return inputs, true, fmt.Errorf("%s must be an absolute path: %#q", name, path)
		}

		info, statErr := os.Stat(path)
		if statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				return inputs, true, fmt.Errorf("%s does not exist: %#q", name, path)
			}

			return inputs, true, fmt.Errorf("check %s path %#q:\n%w", name, path, statErr)
		}
		if !info.Mode().IsRegular() {
			return inputs, true, fmt.Errorf("%s must name a regular file: %#q", name, path)
		}
	}

	return inputs, true, nil
}
