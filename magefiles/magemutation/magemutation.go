// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package magemutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/magesrc"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
)

var ErrMutation = errors.New("mutation testing failed")

// timeoutCoefficient multiplies the baseline test duration to derive each
// mutant's test timeout. Gremlins' default is too small for this repo's fast
// suites: each mutant must recompile before running, and that build alone can
// exceed the default timeout, so nearly every mutant is wrongly reported as
// TIMED OUT (which gremlins counts toward efficacy, inflating it to a bogus
// 100%). A generous coefficient leaves room for the per-mutant build+test.
const timeoutCoefficient = "20"

// actionableStatuses limits the per-mutant console output to the mutants worth
// acting on: LIVED (a real test gap) and NOT_COVERED (code with no test at all).
// KILLED and other statuses are omitted from the console to keep it readable;
// the JSON report (see runGremlins) always contains every mutant. Letters map to
// gremlins statuses: l=LIVED, c=NOT_COVERED.
const actionableStatuses = "lc"

// gremlinsFlakeSignature is the panic gremlins prints when its per-worker copy of
// the module tree fails mid-walk (e.g. a concurrent git/IDE operation removes a
// file under .git). gremlins discards the real error and panics with this
// placeholder, so we match on it to retry the run once. See:
// https://github.com/go-gremlins/gremlins/blob/v0.6.0/internal/engine/executor.go
const gremlinsFlakeSignature = "panic: error, this is temporary"

// excludeFiles are filepath regexps for files that should never be mutated:
//   - mockgen-generated mocks,
//   - scenario test infrastructure: its tests are gated behind the 'scenario'
//     build tag and so never run under the plain 'go test' that gremlins uses,
//     which would otherwise report every scenario mutant as NOT COVERED,
//   - the build system itself.
func excludeFiles() []string {
	return []string{
		`_mocks\.go$`,
		`(^|/)scenario/`,
		`(^|/)magefiles/`,
	}
}

// Mutation runs gremlins mutation testing scoped to the given package path
// (e.g. './internal/rpm'). Pass './' to run the whole repository, which takes a
// few minutes; scoping to a package gives quicker feedback. The console lists
// only the mutants worth acting on, and a full JSON report is written to the
// build output directory.
func Mutation(path string) error {
	if path == "" {
		return fmt.Errorf("%w: a package path is required, e.g. 'mage mutation ./internal/rpm' "+
			"(or './' for the whole repo)", ErrMutation)
	}

	return runGremlins(fmt.Sprintf("on '%s'", path), path)
}

// MutationDiff runs gremlins mutation testing only on the lines changed
// relative to the given git ref (e.g. 'main'). This is the fastest way to
// check whether a branch's changes are covered by tests.
func MutationDiff(ref string) error {
	if ref == "" {
		return fmt.Errorf("%w: a git ref is required, e.g. 'mage mutationDiff main'", ErrMutation)
	}

	return runGremlins(fmt.Sprintf("against the diff vs '%s'", ref), "--diff", ref)
}

// runGremlins invokes the gremlins tool with this repo's standard options.
// description is a human-readable phrase describing the scope, and extraArgs
// are appended after the standard flags (e.g. a package path or diff ref).
func runGremlins(description string, extraArgs ...string) error {
	// Generated sources must exist for the target packages to compile, and the
	// JSON report is written under the build output directory.
	mg.SerialDeps(magesrc.Generate, mageutil.CreateOutDir)

	cmdAbsPath, err := mageutil.GetToolAbsPath(mageutil.GremlinsTool)
	if err != nil {
		return mageutil.PrintAndReturnError("Failed to find gremlins tool.", ErrMutation, err)
	}

	reportPath := filepath.Join(mageutil.OutDir(), "mutation-report.json")

	mageutil.MagePrintf(mageutil.MsgStart, "Running mutation testing %s...\n", description)

	args := []string{
		"unleash",
		"--timeout-coefficient", timeoutCoefficient,
		// Console shows only mutants needing attention; the JSON report has them all.
		"--output-statuses", actionableStatuses,
		"--output", reportPath,
	}
	for _, pattern := range excludeFiles() {
		args = append(args, "--exclude-files", pattern)
	}

	args = append(args, extraArgs...)

	if err := runWithFlakeRetry(cmdAbsPath, args...); err != nil {
		return mageutil.PrintAndReturnError("Mutation testing failed.", ErrMutation, err)
	}

	mageutil.MagePrintf(mageutil.MsgSuccess, "Mutation testing complete. Full JSON report: %s\n", reportPath)

	return nil
}

// runWithFlakeRetry runs gremlins, streaming its output, and retries once if it
// fails with the known transient workdir-copy panic (see gremlinsFlakeSignature).
// Any other failure is returned immediately.
func runWithFlakeRetry(cmdAbsPath string, args ...string) error {
	const maxAttempts = 2 // initial run + one retry on the transient copy panic.

	var err error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var output string

		output, err = runAndCapture(cmdAbsPath, args...)
		if err == nil {
			return nil
		}

		if attempt < maxAttempts && strings.Contains(output, gremlinsFlakeSignature) {
			mageutil.MagePrintln(mageutil.MsgWarning,
				"gremlins hit a transient workdir-copy failure (likely concurrent .git activity); retrying once...")

			continue
		}

		break
	}

	return err
}

// runAndCapture runs the command, streaming its combined output to stdout while
// also capturing it so the caller can detect a retryable failure.
func runAndCapture(cmdAbsPath string, args ...string) (string, error) {
	var buf bytes.Buffer

	cmd := exec.CommandContext(context.Background(), cmdAbsPath, args...)
	writer := io.MultiWriter(os.Stdout, &buf)
	cmd.Stdout = writer
	cmd.Stderr = writer

	err := cmd.Run()

	return buf.String(), err
}
