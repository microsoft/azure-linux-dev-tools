// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
)

//nolint:gochecknoglobals // Replaced by isolated command tests to observe constructed source preparers.
var newSourcePreparer = sources.NewPreparer

func newComponentSourcePreparer(
	env *azldev.Env, comp components.Component, manager sourceproviders.SourceManager,
	mockOpts map[string]string, options ...sources.PreparerOption,
) (sources.SourcePreparer, error) {
	vendor, err := vendorPreparerOptions(env, comp, mockOpts)
	if err != nil {
		return nil, err
	}

	preparer, err := newSourcePreparer(manager, env.FS(), env, env, append(options, vendor...)...)
	if err != nil {
		return nil, fmt.Errorf("creating component source preparer:\n%w", err)
	}

	return preparer, nil
}
