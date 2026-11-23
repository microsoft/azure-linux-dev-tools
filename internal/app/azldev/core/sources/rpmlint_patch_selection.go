// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/spec"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
)

//go:embed rpmlint_patch_selection.py
var rpmlintPatchSelectionScript string

func (p *sourcePreparerImpl) basicPatchSelector(ctx context.Context, componentName string) spec.BasicPatchSelector {
	if p.rpmlintCtx == nil || p.dryRunnable.DryRun() {
		return nil
	}

	return func(lines []string) (spec.PatchSelection, error) {
		return selectRPMLintPatches(ctx, p.rpmlintCtx, componentName, lines)
	}
}

func selectRPMLintPatches(
	ctx context.Context, runner opctx.Ctx, componentName string, lines []string,
) (spec.PatchSelection, error) {
	if err := prereqs.RequireExecutable(runner, python3Binary, nil); err != nil {
		if errors.Is(err, prereqs.ErrMissingExecutable) {
			slog.Warn("Python unavailable; using built-in patch selection", "component", componentName, "error", err)

			return spec.PatchSelection{}, nil
		}

		return spec.PatchSelection{}, fmt.Errorf("checking Python prerequisite for rpmlint:\n%w", err)
	}

	input, err := json.Marshal(lines)
	if err != nil {
		return spec.PatchSelection{}, fmt.Errorf("encoding patch selection input:\n%w", err)
	}

	var stderr bytes.Buffer

	rawCmd := exec.CommandContext(ctx, python3Binary, "-I", "-B", "-c", rpmlintPatchSelectionScript)
	rawCmd.Stdin = bytes.NewReader(input)
	rawCmd.Stderr = &stderr

	cmd, err := runner.Command(rawCmd)
	if err != nil {
		return spec.PatchSelection{}, fmt.Errorf("preparing rpmlint patch selection:\n%w", err)
	}

	cmd.SetDescription("Checking basic patch selections with rpmlint")

	output, err := cmd.RunAndGetOutput(ctx)
	if err != nil {
		return spec.PatchSelection{}, fmt.Errorf(
			"running rpmlint patch selection (%#q):\n%w", strings.TrimSpace(stderr.String()), err)
	}

	var result struct {
		Unavailable bool  `json:"unavailable"`
		All         *bool `json:"all"`
		Numbers     []int `json:"numbers"`
	}

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return spec.PatchSelection{}, fmt.Errorf("decoding rpmlint patch selection:\n%w", err)
	}

	if result.Unavailable {
		slog.Warn("rpmlint is not installed for the host Python; using built-in patch selection", "component", componentName)

		return spec.PatchSelection{}, nil
	}

	if result.All == nil || result.Numbers == nil {
		return spec.PatchSelection{}, errors.New("incomplete rpmlint patch selection result")
	}

	return spec.PatchSelection{All: *result.All, Numbers: result.Numbers, Available: true}, nil
}
