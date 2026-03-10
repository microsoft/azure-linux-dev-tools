// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeFileHash_BadPath(t *testing.T) {
	ctx := testctx.NewCtx()

	_, err := fileutils.ComputeFileHash(ctx.FS(), fileutils.HashTypeSHA256, "/non/existent")
	require.Error(t, err)
}

func TestComputeFileHash(t *testing.T) {
	const (
		testPath       = "somefile.txt"
		expectedSHA256 = "054edec1d0211f624fed0cbca9d4f9400b0e491c43742af2c5b0abebf0c990d8"
		expectedSHA512 = "4ec54b09e2b209ddb9a678522bb451740c513f488cb27a088363071857174514" +
			"1920036aebdb78c0b4cd783a4a6eecc937a40c6104e427512d709a634b412f60"
	)

	testData := []byte{0, 1, 2, 3}

	ctx := testctx.NewCtx()

	err := fileutils.WriteFile(ctx.FS(), testPath, testData, fileperms.PrivateFile)
	require.NoError(t, err)

	tests := []struct {
		name         string
		hashType     fileutils.HashType
		expectedHash string
	}{
		{
			name:         "SHA256",
			hashType:     fileutils.HashTypeSHA256,
			expectedHash: expectedSHA256,
		},
		{
			name:         "SHA512",
			hashType:     fileutils.HashTypeSHA512,
			expectedHash: expectedSHA512,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualHash, err := fileutils.ComputeFileHash(ctx.FS(), tt.hashType, testPath)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedHash, actualHash)
		})
	}
}

func TestValidateFileHash(t *testing.T) {
	const (
		testPath       = "somefile.txt"
		expectedSHA256 = "054edec1d0211f624fed0cbca9d4f9400b0e491c43742af2c5b0abebf0c990d8"
		expectedSHA512 = "4ec54b09e2b209ddb9a678522bb451740c513f488cb27a088363071857174514" +
			"1920036aebdb78c0b4cd783a4a6eecc937a40c6104e427512d709a634b412f60"
		expectedMD5  = "37b59afd592725f9305e484a5d7f5168"
		bogusHash    = "0000000000000000000000000000000000000000000000000000000000000000"
		bogusMD5Hash = "00000000000000000000000000000000"
	)

	testData := []byte{0, 1, 2, 3}

	ctx := testctx.NewCtx()

	err := fileutils.WriteFile(ctx.FS(), testPath, testData, fileperms.PrivateFile)
	require.NoError(t, err)

	tests := []struct {
		name         string
		hashType     fileutils.HashType
		expectedHash string
		wantError    bool
	}{
		{
			name:         "SHA256 valid",
			hashType:     fileutils.HashType("SHA256"),
			expectedHash: expectedSHA256,
			wantError:    false,
		},
		{
			name:         "sha256 lowercase valid",
			hashType:     fileutils.HashType("sha256"),
			expectedHash: expectedSHA256,
			wantError:    false,
		},
		{
			name:         "SHA512 valid",
			hashType:     fileutils.HashType("SHA512"),
			expectedHash: expectedSHA512,
			wantError:    false,
		},
		{
			name:         "sha512 lowercase valid",
			hashType:     fileutils.HashType("sha512"),
			expectedHash: expectedSHA512,
			wantError:    false,
		},
		{
			name:         "SHA256 invalid hash",
			hashType:     fileutils.HashType("SHA256"),
			expectedHash: bogusHash,
			wantError:    true,
		},
		{
			name:         "MD5 valid",
			hashType:     fileutils.HashType("MD5"),
			expectedHash: expectedMD5,
			wantError:    false,
		},
		{
			name:         "md5 lowercase valid",
			hashType:     fileutils.HashType("md5"),
			expectedHash: expectedMD5,
			wantError:    false,
		},
		{
			name:         "MD5 invalid hash",
			hashType:     fileutils.HashType("MD5"),
			expectedHash: bogusMD5Hash,
			wantError:    true,
		},
		{
			name:         "unsupported algorithm",
			hashType:     fileutils.HashType("BLAKE2"),
			expectedHash: bogusHash,
			wantError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fileutils.ValidateFileHash(ctx, ctx.FS(), tt.hashType, testPath, tt.expectedHash)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
