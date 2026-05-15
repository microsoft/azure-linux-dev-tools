// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import "github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"

// ProvenanceReport is the output of a source preparation run, listing
// every file that was downloaded and where it came from.
type ProvenanceReport struct {
	// ComponentName is the name of the component whose sources were prepared.
	ComponentName string `json:"componentName" table:"Component"`

	// Sources lists the provenance of each downloaded source file.
	Sources []sourceproviders.SourceProvenance `json:"sources" table:"-"`
}
