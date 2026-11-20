// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"fmt"
	"log/slog"

	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	// specEditorLegacy remains supported temporarily for backward compatibility.
	// It is deprecated and will be removed in a future release.
	specEditorLegacy       = "legacy"
	specEditorStructural   = "structural"
	specEditorExperimental = "experimental"
)

// componentCommandOptions are shared by component subcommands that edit specs.
type componentCommandOptions struct {
	SpecEditor spec.EditorMode
}

func (o componentCommandOptions) specEditorMode() spec.EditorMode {
	if o.SpecEditor == "" {
		return spec.EditorStructural
	}

	return o.SpecEditor
}

type specEditorFlagValue struct {
	mode spec.EditorMode
}

var _ pflag.Value = (*specEditorFlagValue)(nil)

func newSpecEditorFlagValue() *specEditorFlagValue {
	return &specEditorFlagValue{mode: spec.EditorStructural}
}

func (v *specEditorFlagValue) String() string {
	if v.mode == spec.EditorStructural {
		return specEditorStructural
	}

	return specEditorLegacy
}

func (v *specEditorFlagValue) Set(value string) error {
	switch value {
	case specEditorLegacy:
		v.mode = spec.EditorLegacy
		slog.Warn("The RPM spec 'legacy' editor is deprecated and will be removed in a future release; use 'structural' instead")
	case specEditorStructural, specEditorExperimental:
		v.mode = spec.EditorStructural
	default:
		return fmt.Errorf(
			"unsupported RPM spec editor %#q; expected %#q or %#q",
			value, specEditorLegacy, specEditorStructural,
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
		"Select the RPM spec editor (legacy or structural)",
	)
}

func specEditorFromCommand(cmd *cobra.Command) spec.EditorMode {
	flag := cmd.Flags().Lookup("spec-editor")
	if flag == nil {
		return spec.EditorStructural
	}

	value, ok := flag.Value.(*specEditorFlagValue)
	if !ok {
		return spec.EditorStructural
	}

	return value.mode
}
