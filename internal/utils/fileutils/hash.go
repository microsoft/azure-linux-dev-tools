// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils

import (
	"crypto/md5" //nolint:gosec
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

// HashType represents a type of hash used for source file verification.
type HashType string

const (
	// HashTypeMD5 represents the MD5 hash algorithm.
	// Note: MD5 is cryptographically weak and should only be used for legacy compatibility.
	HashTypeMD5 HashType = "md5"

	// HashTypeSHA256 represents the SHA-256 hash algorithm.
	HashTypeSHA256 HashType = "sha256"

	// HashTypeSHA512 represents the SHA-512 hash algorithm.
	HashTypeSHA512 HashType = "sha512"
)

// Computes the hash of the file located at filePath using the specified hashType; returns the hash in hex string form.
func ComputeFileHash(fs opctx.FS, hashType HashType, filePath string) (string, error) {
	hasher, err := getHasher(hashType)
	if err != nil {
		return "", err
	}

	file, err := fs.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file %#q:\n%w", filePath, err)
	}

	defer file.Close()

	_, err = io.Copy(hasher, file)
	if err != nil {
		return "", fmt.Errorf("failed to compute hash of file %#q:\n%w", filePath, err)
	}

	hash := hex.EncodeToString(hasher.Sum(nil))

	return hash, nil
}

// ValidateFileHash validates that the file stored at filePath has contents matching
// the expectedHash using the specified hashType algorithm.
func ValidateFileHash(
	dryRunnable opctx.DryRunnable,
	fs opctx.FS,
	hashType HashType,
	filePath string,
	expectedHash string,
) error {
	if dryRunnable.DryRun() {
		slog.Info("Dry run; would validate hash", "path", filePath, "hash", expectedHash)

		return nil
	}

	// Compute the *actual* hash of the downloaded file.
	actualHash, err := ComputeFileHash(fs, hashType, filePath)
	if err != nil {
		return fmt.Errorf("failed to compute hash of downloaded file %s:\n%w", filePath, err)
	}

	// Verify hash (case-insensitive since hex can be upper or lowercase)
	if strings.EqualFold(actualHash, expectedHash) {
		slog.Debug("Downloaded file verified", "filename", filepath.Base(filePath), "hash", actualHash)
	} else {
		return fmt.Errorf(
			"hash mismatch for %s; downloaded file has hash %s but expected %s",
			filepath.Base(filePath), actualHash, expectedHash)
	}

	return nil
}

// getHasher returns the appropriate hash.Hash implementation for the given algorithm name.
func getHasher(hashType HashType) (hash.Hash, error) {
	switch strings.ToUpper(string(hashType)) {
	case "MD5":
		//nolint:gosec // MD5 is required for legacy Fedora sources file format compatibility
		return md5.New(), nil
	case "SHA256":
		return sha256.New(), nil
	case "SHA512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("unsupported hash algorithm: %s", hashType)
	}
}
