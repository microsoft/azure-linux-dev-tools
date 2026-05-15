// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders/fedorasource"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// SourceOriginType describes how a source file was obtained during source preparation.
type SourceOriginType string

const (
	// SourceOriginLookaside indicates the file was downloaded from a lookaside cache.
	// The [SourceProvenance.URL] field contains the exact lookaside URL used.
	SourceOriginLookaside SourceOriginType = "lookaside-url"

	// SourceOriginURL indicates the file was downloaded from an explicitly configured
	// origin URL (the [projectconfig.SourceFileReference.Origin] field).
	SourceOriginURL SourceOriginType = "configured-origin-url"
)

// SourceProvenance records where a single downloaded source file came from.
type SourceProvenance struct {
	// Filename is the name of the downloaded file.
	Filename string `json:"filename" table:"Filename"`

	// OriginType describes how the file was obtained.
	OriginType SourceOriginType `json:"originType" table:"Origin"`

	// URL is the actual download URL that was used to retrieve the file.
	URL string `json:"url" table:"URL"`

	// HashType is the hash algorithm used to validate the download (e.g., "sha512").
	HashType fileutils.HashType `json:"hashType,omitempty" table:"-"`

	// Hash is the hex-encoded hash value used to validate the download.
	Hash string `json:"hash,omitempty" table:"-"`
}

// ConvertDownloadsToProvenance converts [fedorasource.SourceDownload] entries
// (returned by lookaside extraction) into [SourceProvenance] entries with the
// [SourceOriginLookaside] origin type.
func ConvertDownloadsToProvenance(downloads []fedorasource.SourceDownload) []SourceProvenance {
	if len(downloads) == 0 {
		return nil
	}

	prov := make([]SourceProvenance, len(downloads))
	for i, download := range downloads {
		prov[i] = SourceProvenance{
			Filename:   download.Filename,
			OriginType: SourceOriginLookaside,
			URL:        download.URL,
			HashType:   download.HashType,
			Hash:       download.Hash,
		}
	}

	return prov
}
