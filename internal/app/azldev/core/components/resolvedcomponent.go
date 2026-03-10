// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package components

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/specs"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
)

// resolvedComponent is a package-private implementation of the [Component] interface that
// captures an internal reference to the [azldev.Env] environment it belongs to. This
// environment is used by implementation of this struct, such as to use mock.
type resolvedComponent struct {
	env    *azldev.Env
	config projectconfig.ComponentConfig
}

func (c *resolvedComponent) GetName() string {
	return c.config.Name
}

func (c *resolvedComponent) GetConfig() *projectconfig.ComponentConfig {
	return &c.config
}

func (c *resolvedComponent) GetSpec() specs.ComponentSpec {
	return specs.NewSpec(c.env, c.config)
}

func (c *resolvedComponent) GetDetails() (info *ComponentDetails, err error) {
	specInfo, err := c.GetSpec().Parse()
	if err != nil {
		return nil, fmt.Errorf("failed to parse spec for component %q:\n%w", c.GetName(), err)
	}

	info = &ComponentDetails{
		ComponentSpecDetails: *specInfo,
		Config:               c.config,
	}

	return info, nil
}
