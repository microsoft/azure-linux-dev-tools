// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

//go:embed query_process.py
var queryProcessScript []byte

// SpecQueryInput describes a single rendered spec to query in the mock chroot.
// SpecRelPath is the path of the .spec relative to the specs directory bind
// mounted into the chroot (e.g. "c/curl/curl.spec").
type SpecQueryInput struct {
	Name        string
	SpecRelPath string
	With        []string
	Without     []string
	Defines     map[string]string
}

// SpecQueryResult holds the batch-query result for one spec.
//
// Exactly one of Info, ExcludedFromArch, or Error indicates the outcome:
//   - Info is populated (and Error is nil, ExcludedFromArch is false) when the
//     spec was successfully queried for the requested arch.
//   - ExcludedFromArch is true when rpmspec refused to evaluate the spec for
//     the requested arch (ExclusiveArch/ExcludeArch policy). This is not an
//     error; the component simply isn't built for that arch.
//   - Error is non-nil for any other failure (rpmspec parse error, missing
//     spec, timeout, etc).
type SpecQueryResult struct {
	Name             string
	Info             *rpm.SpecInfo
	ExcludedFromArch bool
	Error            error
}

// validateSpecQueryInputs rejects empty names, path-traversal in spec
// relative paths, absolute spec paths, and duplicate component names.
func validateSpecQueryInputs(inputs []SpecQueryInput) error {
	seen := make(map[string]bool, len(inputs))

	for _, input := range inputs {
		if err := fileutils.ValidateFilename(input.Name); err != nil {
			return fmt.Errorf("invalid component name %#q:\n%w", input.Name, err)
		}

		if err := fileutils.ValidateRelPath(input.SpecRelPath); err != nil {
			return fmt.Errorf("invalid spec path %#q for component %#q:\n%w",
				input.SpecRelPath, input.Name, err)
		}

		if seen[input.Name] {
			return fmt.Errorf("duplicate component name %#q", input.Name)
		}

		seen[input.Name] = true
	}

	return nil
}

// specQueryInputJSON is the JSON-serializable form of [SpecQueryInput]
// written into inputs.json for the embedded Python helper.
type specQueryInputJSON struct {
	Name                   string            `json:"name"`
	SpecRelPath            string            `json:"specRelPath"`
	SrpmQueryFormat        string            `json:"srpmQueryFormat"`
	SubpackagesQueryFormat string            `json:"subpackagesQueryFormat"`
	With                   []string          `json:"with,omitempty"`
	Without                []string          `json:"without,omitempty"`
	Defines                map[string]string `json:"defines,omitempty"`
}

// specQueryResultJSON mirrors the per-component JSON shape written by
// query_process.py.
type specQueryResultJSON struct {
	Name             string  `json:"name"`
	SrpmOut          string  `json:"srpmOut"`
	BinOut           string  `json:"binOut"`
	Error            *string `json:"error"`
	ExcludedFromArch bool    `json:"excludedFromArch,omitempty"`
}

// BatchQuerySpecsOptions parameterizes a [MockProcessor.BatchQuerySpecs]
// invocation.
//
// SpecsDir is the host directory containing the rendered specs tree (i.e.
// the project's rendered-specs-dir). Each input's SpecRelPath is resolved
// relative to SpecsDir. ScratchDir is a small host-side scratch directory
// used to ferry the script + inputs.json + results.json in and out of the
// chroot; it must be writable by the user the chroot runs as (mock's
// chrootuid defaults to os.getuid()).
//
// Arch sets the rpmspec build target (e.g. "x86_64", "aarch64") via
// --target=<arch>. When empty, rpmspec uses its built-in default (the host
// arch). Specs that ExclusiveArch/ExcludeArch-exclude the target arch are
// surfaced via [SpecQueryResult.ExcludedFromArch] rather than as errors.
type BatchQuerySpecsOptions struct {
	SpecsDir   string
	ScratchDir string
	Arch       string
	FS         opctx.FS
	MaxWorkers int
}

// BatchQuerySpecs runs `rpmspec` against multiple rendered spec files inside
// the shared mock chroot, parallelizing the per-spec invocations via an
// embedded Python helper. Returns one [SpecQueryResult] per input, in input
// order.
func (p *MockProcessor) BatchQuerySpecs(
	ctx context.Context, events opctx.EventListener,
	inputs []SpecQueryInput, opts BatchQuerySpecsOptions,
) ([]SpecQueryResult, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	if err := validateSpecQueryInputs(inputs); err != nil {
		return nil, err
	}

	jsonInputs := make([]specQueryInputJSON, len(inputs))
	for idx, input := range inputs {
		jsonInputs[idx] = specQueryInputJSON{
			Name:                   input.Name,
			SpecRelPath:            input.SpecRelPath,
			SrpmQueryFormat:        rpm.SrpmQueryFormat,
			SubpackagesQueryFormat: rpm.SubpackagesQueryFormat,
			With:                   input.With,
			Without:                input.Without,
			Defines:                input.Defines,
		}
	}

	inputsBytes, err := json.Marshal(jsonInputs)
	if err != nil {
		return nil, fmt.Errorf("marshaling spec query inputs:\n%w", err)
	}

	slog.Info("Batch-querying rendered specs in mock chroot", "count", len(inputs))

	const (
		chrootScratchPath = "/tmp/query"
		chrootSpecsPath   = "/tmp/specs"
	)

	workers := strconv.Itoa(max(1, opts.MaxWorkers))

	rawResults, err := p.runBatchScript(ctx, events, runBatchScriptOptions{
		Mounts: []batchBindMount{
			{Host: opts.ScratchDir, InChroot: chrootScratchPath},
			{Host: opts.SpecsDir, InChroot: chrootSpecsPath},
		},
		ScratchHost:     opts.ScratchDir,
		ScratchInChroot: chrootScratchPath,
		ScriptName:      "query_process.py",
		ScriptBytes:     queryProcessScript,
		InputsJSON:      inputsBytes,
		ResultsName:     "results.json",
		ScriptArgs:      []string{chrootScratchPath, chrootSpecsPath, workers, opts.Arch},
		ProgressLabel:   "Querying specs in mock chroot",
		ProgressTotal:   int64(len(inputs)),
		FS:              opts.FS,
	})
	if err != nil {
		return nil, err
	}

	return parseSpecQueryBatchJSON(rawResults, inputs)
}

// parseSpecQueryBatchJSON parses the JSON array produced by query_process.py
// into [SpecQueryResult] values. Per-component rpmspec failures are surfaced
// as a non-nil Error on the result; parse failures of an otherwise-successful
// rpmspec invocation are likewise surfaced per component.
func parseSpecQueryBatchJSON(raw []byte, inputs []SpecQueryInput) ([]SpecQueryResult, error) {
	var jsonResults []specQueryResultJSON
	if err := json.Unmarshal(raw, &jsonResults); err != nil {
		return nil, fmt.Errorf("parsing spec query batch results JSON:\n%w", err)
	}

	resultMap := make(map[string]*specQueryResultJSON, len(jsonResults))
	for idx := range jsonResults {
		resultMap[jsonResults[idx].Name] = &jsonResults[idx]
	}

	results := make([]SpecQueryResult, len(inputs))

	for idx, input := range inputs {
		results[idx].Name = input.Name

		compResult, ok := resultMap[input.Name]
		if !ok {
			results[idx].Error = fmt.Errorf("no result returned for %#q", input.Name)

			continue
		}

		if compResult.Error != nil {
			results[idx].Error = fmt.Errorf("%s", *compResult.Error)

			continue
		}

		if compResult.ExcludedFromArch {
			results[idx].ExcludedFromArch = true

			continue
		}

		info, parseErr := rpm.ParseSrpmQueryOutput(input.SpecRelPath, compResult.SrpmOut)
		if parseErr != nil {
			results[idx].Error = fmt.Errorf("parsing rpmspec --srpm output:\n%w", parseErr)

			continue
		}

		info.Subpackages = rpm.ParseSubpackagesOutput(compResult.BinOut)
		results[idx].Info = info
	}

	return results, nil
}
