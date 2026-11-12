// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/sources"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/prereqs"
)

type hostRenderProcessor struct {
	env *azldev.Env
}

func (p hostRenderProcessor) GenerateChangelog(ctx context.Context, dir, filename string) (string, error) {
	return p.run(ctx, dir, "rpmautospec", "generate-changelog", filename)
}

func (p hostRenderProcessor) CalculateBaseRelease(ctx context.Context, dir, filename string) (string, error) {
	return p.run(ctx, dir, "rpmautospec", "calculate-release", "--number-only", filename)
}

func (p hostRenderProcessor) CalculateRelease(ctx context.Context, dir, filename string) (string, error) {
	return p.run(ctx, dir, "rpmautospec", "calculate-release", "--complete-release", filename)
}

func (p hostRenderProcessor) ExpandRelease(ctx context.Context, dir, filename string) (string, error) {
	out, err := p.run(ctx, dir, "rpmspec",
		"-q", "--srpm", "--queryformat", "release=%{RELEASE}\n",
		"--define", "_sourcedir "+dir, "--define", "_specdir "+dir, filename)
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(out, "\n") {
		if release, found := strings.CutPrefix(strings.TrimSpace(line), "release="); found && release != "" {
			return release, nil
		}
	}

	return "", fmt.Errorf("no Release field in committed spec query output %#q", out)
}

func (p hostRenderProcessor) run(ctx context.Context, dir, binary string, args ...string) (string, error) {
	if err := prereqs.RequireExecutable(p.env, binary, nil); err != nil {
		return "", fmt.Errorf("lockless rendering requires host program %#q:\n%w", binary, err)
	}

	raw := exec.CommandContext(ctx, binary, args...)
	raw.Dir = dir

	var stderr bytes.Buffer

	raw.Stderr = &stderr

	cmd, err := p.env.Command(raw)
	if err != nil {
		return "", fmt.Errorf("creating host %#q command:\n%w", binary, err)
	}

	out, err := cmd.RunAndGetOutput(ctx)
	if err != nil {
		return "", fmt.Errorf("running host %#q (%s):\n%w", binary, stderr.String(), err)
	}

	return out, nil
}

func processRenderSources(
	env *azldev.Env, processor *sources.MockProcessor, stagingDir string, prepared []*preparedComponent,
) map[string]*sources.ComponentMockResult {
	var locked []*preparedComponent

	results := make(map[string]*sources.ComponentMockResult, len(prepared))
	for _, prep := range prepared {
		if !prep.lockless {
			locked = append(locked, prep)

			continue
		}

		name := prep.comp.GetName()

		results[name] = &sources.ComponentMockResult{Name: name}
	}

	for name, result := range batchMockProcess(env, processor, stagingDir, locked) {
		results[name] = result
	}

	return results
}
