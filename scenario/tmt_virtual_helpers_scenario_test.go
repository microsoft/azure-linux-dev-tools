// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadTMTVirtualInputs(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	imagePath := filepath.Join(tempDir, "vm.qcow2")
	rpmPath := filepath.Join(tempDir, "candidate.rpm")
	require.NoError(t, os.WriteFile(imagePath, []byte("image"), fileperms.PrivateFile))
	require.NoError(t, os.WriteFile(rpmPath, []byte("rpm"), fileperms.PrivateFile))

	tests := map[string]struct {
		env       map[string]string
		optedIn   bool
		errMatch  string
		wantInput tmtVirtualInputs
	}{
		"disabled": {
			env:     map[string]string{},
			optedIn: false,
		},
		"complete": {
			env: map[string]string{
				tmtImageEnv:         imagePath,
				tmtRPMEnv:           rpmPath,
				tmtImageRevisionEnv: "revision",
			},
			optedIn: true,
			wantInput: tmtVirtualInputs{
				imagePath:     imagePath,
				rpmPath:       rpmPath,
				imageRevision: "revision",
			},
		},
		"partial": {
			env: map[string]string{
				tmtImageEnv: imagePath,
			},
			optedIn:  true,
			errMatch: tmtRPMEnv,
		},
		"relative path": {
			env: map[string]string{
				tmtImageEnv:         "vm.qcow2",
				tmtRPMEnv:           rpmPath,
				tmtImageRevisionEnv: "revision",
			},
			optedIn:  true,
			errMatch: "absolute path",
		},
		"missing file": {
			env: map[string]string{
				tmtImageEnv:         filepath.Join(tempDir, "missing.qcow2"),
				tmtRPMEnv:           rpmPath,
				tmtImageRevisionEnv: "revision",
			},
			optedIn:  true,
			errMatch: "does not exist",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			getenv := func(key string) string {
				return test.env[key]
			}

			inputs, optedIn, err := loadTMTVirtualInputs(getenv)

			assert.Equal(t, test.optedIn, optedIn)
			if test.errMatch != "" {
				require.ErrorContains(t, err, test.errMatch)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.wantInput, inputs)
		})
	}
}
