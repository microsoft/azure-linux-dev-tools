// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/gowebpki/jcs"
)

// canonicalDigest serializes the fingerprint document to RFC 8785 (JCS) canonical
// JSON and returns its sha256 as a "sha256:<hex>" string. The document's object
// keys provide domain separation between the config projection and the non-config
// inputs, so no manual length-prefixing is needed; JCS sorts keys and pins number
// and string formatting, so the bytes are stable across runs and Go versions.
func canonicalDigest(document map[string]any) (string, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("marshaling fingerprint document:\n%w", err)
	}

	canonical, err := jcs.Transform(raw)
	if err != nil {
		return "", fmt.Errorf("canonicalizing fingerprint document:\n%w", err)
	}

	sum := sha256.Sum256(canonical)

	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
