// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
)

const (
	tmtJUnitPhase    = "azldev-junit"
	tmtPipeWaitDelay = 2 * time.Second
)

func executeComponentTMTTests(
	env *azldev.Env, workDir string, tests []projectconfig.ResolvedTest, settings tmtRunSettings,
) ([]azldev.TestRunResult, error) {
	workDir, err := componentTMTWorkDir(env, workDir)
	if err != nil {
		return nil, err
	}

	root := filepath.Join(workDir, "<dry-run>")
	if !env.DryRun() {
		if err := env.FS().MkdirAll(workDir, fileperms.PrivateDir); err != nil {
			return nil, fmt.Errorf("create TMT work directory:\n%w", err)
		}

		root, err = fileutils.MkdirTemp(env.FS(), workDir, "azldev-test-")
		if err != nil {
			return nil, fmt.Errorf("create test artifact directory:\n%w", err)
		}

		slog.Info("Retaining test artifacts", "path", root)
		settings.SetupLog = filepath.Join(root, "setup.log")
	}

	results := make([]azldev.TestRunResult, 0, len(tests))
	for index, test := range tests {
		result := azldev.TestRunResult{Test: test.Name, JUnit: []string{}}
		if !env.DryRun() {
			result.ArtifactDir = root
		}

		results = append(results, result)

		if index == 0 {
			_, settings.TMTProgramPath, err = prepareTMTEnvironment(
				env, workDir, settings.Provision, settings.SourceDir != "", settings.SetupLog,
			)
			if err != nil {
				return results, err
			}
		}

		settings.WorkDir = filepath.Join(root, fmt.Sprintf("%03d-%s", index+1, test.Name))

		runErr := runOneTMTTest(env, test, settings, &results[index])
		if !env.DryRun() {
			results[index].JUnit, err = collectTMTJUnit(env.FS(), root, filepath.Join(settings.WorkDir, "native"))
		}

		if err := errors.Join(runErr, err); err != nil {
			return results, fmt.Errorf("TMT test %#q:\n%w", test.Name, err)
		}
	}

	return results, nil
}

func collectTMTJUnit(fs opctx.FS, root string, native string) ([]string, error) {
	reports := []string{}

	info, err := lstatIfPossible(fs, native)
	if errors.Is(err, os.ErrNotExist) {
		return reports, nil
	}

	if err != nil {
		return reports, fmt.Errorf("inspect TMT run directory:\n%w", err)
	}

	if !info.IsDir() {
		return reports, fmt.Errorf("TMT run path %#q is not a directory", native)
	}

	paths, err := fileutils.Glob(afero.NewBasePathFs(fs, native),
		"**/report/"+tmtJUnitPhase+"/junit.xml", doublestar.WithNoFollow(), doublestar.WithFailOnIOErrors())
	if err != nil {
		return reports, fmt.Errorf("find TMT JUnit reports:\n%w", err)
	}

	var failures []error

	for _, path := range paths {
		rel, err := retainedTMTReport(fs, root, filepath.Join(native, path))
		if err != nil {
			failures = append(failures, err)
		} else {
			reports = append(reports, rel)
		}
	}

	return reports, errors.Join(failures...)
}

func retainedTMTReport(fs opctx.FS, root string, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return "", fmt.Errorf("report %#q escapes artifact directory %#q", path, root)
	}

	current := root
	for _, part := range append([]string{"."}, strings.Split(rel, string(filepath.Separator))...) {
		current = filepath.Join(current, part)

		info, err := lstatIfPossible(fs, current)
		if err != nil {
			return "", fmt.Errorf("inspect report %#q:\n%w", current, err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("report %#q contains a symlink", path)
		}

		if current == path && (!info.Mode().IsRegular() || info.Size() == 0) {
			return "", fmt.Errorf("report %#q is not a nonempty regular file", path)
		}
	}

	return filepath.ToSlash(rel), nil
}

// stdout and stderr may copy concurrently into the same transcript.
type testTranscript struct {
	mu     sync.Mutex
	writer io.Writer
	err    error
}

func (w *testTranscript) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	count, err := w.writer.Write(data)
	if err == nil && count != len(data) {
		err = io.ErrShortWrite
	}

	w.err = errors.Join(w.err, err)

	return count, err
}

//nolint:wrapcheck // The deferred wrapper adds command/transcript context.
func runCapturedTMTCommand(
	env *azldev.Env, command *exec.Cmd, logPath string, captureOutput bool,
) (output string, exitCode *int, err error) {
	if env.DryRun() {
		slog.Info("Would run", "command", command.Args)

		return "", nil, nil
	}

	// Descendants may retain the pipes after cancellation kills their parent.
	command.WaitDelay = tmtPipeWaitDelay
	transcript := io.Discard

	if logPath != "" {
		file, openErr := env.FS().OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileperms.PrivateFile)
		if openErr != nil {
			return "", nil, fmt.Errorf("open command transcript %#q:\n%w", logPath, openErr)
		}

		defer func() { err = errors.Join(err, file.Close()) }()

		transcript = file
	}

	log := &testTranscript{writer: transcript}

	defer func() {
		err = errors.Join(err, log.err)
		if err != nil {
			err = fmt.Errorf("command %q (transcript %#q):\n%w", command.Args, logPath, err)
		}
	}()

	if _, err = fmt.Fprintf(log, "$ %q\n", command.Args); err != nil {
		return "", nil, err
	}

	stdout := env.ReportFile()
	if env.DefaultReportFormat() == azldev.ReportFormatJSON {
		stdout = os.Stderr
	}

	command.Stderr = io.MultiWriter(log, os.Stderr)
	if !captureOutput {
		command.Stdout = io.MultiWriter(log, stdout)
	}

	wrapped, err := env.Command(command)
	if err != nil {
		return "", nil, err
	}

	if captureOutput {
		output, err = wrapped.RunAndGetOutput(env)
		_, writeErr := io.WriteString(log, output)
		err = errors.Join(err, writeErr)
	} else {
		err = wrapped.Run(env)
	}

	if command.ProcessState != nil && command.ProcessState.Exited() {
		code := command.ProcessState.ExitCode()
		exitCode = &code
	}

	return output, exitCode, err
}
