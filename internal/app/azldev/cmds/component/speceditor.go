// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	specEditorLegacy       = "legacy"
	specEditorExperimental = "experimental"
)

// componentCommandOptions are shared by component subcommands that edit specs.
type componentCommandOptions struct {
	SpecEditor spec.EditorMode
}

func (o componentCommandOptions) specEditorMode() spec.EditorMode {
	if o.SpecEditor == "" {
		return spec.EditorLegacy
	}

	return o.SpecEditor
}

type specEditorFlagValue struct {
	mode spec.EditorMode
}

var _ pflag.Value = (*specEditorFlagValue)(nil)

func newSpecEditorFlagValue() *specEditorFlagValue {
	return &specEditorFlagValue{mode: spec.EditorLegacy}
}

func (v *specEditorFlagValue) String() string {
	if v.mode == spec.EditorStructural {
		return specEditorExperimental
	}

	return specEditorLegacy
}

func (v *specEditorFlagValue) Set(value string) error {
	switch value {
	case specEditorLegacy:
		v.mode = spec.EditorLegacy
	case specEditorExperimental:
		v.mode = spec.EditorStructural
	default:
		return fmt.Errorf(
			"unsupported RPM spec editor %#q; expected %#q or %#q",
			value, specEditorLegacy, specEditorExperimental,
		)
	}

	return nil
}

func (v *specEditorFlagValue) Type() string {
	return "editor"
}

func addSpecEditorOption(cmd *cobra.Command) {
	cmd.PersistentFlags().Var(
		newSpecEditorFlagValue(),
		"spec-editor",
		"Select the RPM spec editor (legacy or experimental)",
	)
}

func specEditorFromCommand(cmd *cobra.Command) spec.EditorMode {
	flag := cmd.Flags().Lookup("spec-editor")
	if flag == nil {
		return spec.EditorLegacy
	}

	value, ok := flag.Value.(*specEditorFlagValue)
	if !ok {
		return spec.EditorLegacy
	}

	return value.mode
}
