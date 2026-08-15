// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package rpm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComposeRpmspecCmdlineAppliesUndefinesLast(t *testing.T) {
	querier := &SpecQuerier{buildOptions: BuildOptions{
		With:      []string{"asan"},
		Without:   []string{"docs"},
		Defines:   map[string]string{"dist": ".azl4"},
		Undefines: []string{"_with_asan", "dist"},
	}}

	assert.Equal(t, []string{
		"rpmspec",
		"-q",
		"--srpm",
		"-D", "_sourcedir /spec",
		"-D", "_specdir /spec",
		"-D", "with_check 0",
		"--queryformat",
		"name=%{name}\nepoch=%{epoch}\nversion=%{version}\nrelease=%{release}\n[source=%{SOURCE}\n][patch=%{PATCH}\n]",
		"--with", "asan",
		"--without", "docs",
		"-D", "dist .azl4",
		"--undefine", "_with_asan",
		"--undefine", "dist",
		"/spec/component.spec",
	}, querier.composeRpmspecCmdline("/spec/component.spec"))
}
