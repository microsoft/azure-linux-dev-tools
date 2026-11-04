// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
)

//nolint:gochecknoglobals // Replaced by isolated command tests to observe constructed source preparers.
var newSourcePreparer = sources.NewPreparer

func newComponentSourcePreparer(
	env *azldev.Env, component components.Component, manager sourceproviders.SourceManager,
	mockConfigOpts map[string]string, options ...sources.PreparerOption,
) (sources.SourcePreparer, error) {
	vendorOpts, err := goVendorPreparerOptions(env, component, mockConfigOpts)
	if err != nil {
		return nil, err
	}

	return newSourcePreparer(manager, env.FS(), env, env, append(options, vendorOpts...)...)
}
