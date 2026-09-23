// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/externalcmd"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

const (
	// RPMDevBumpspecBinary is the executable used to make one spec release bump.
	RPMDevBumpspecBinary = "rpmdev-bumpspec"

	rpmdevPackagerBinary = "rpmdev-packager"
	rpmBinary            = "rpm"
	rpmspecBinary        = "rpmspec"
	python3Binary        = "python3"

	rpmautospecNoBumpOutput = "RPMAutoSpec usage detected, not changing the spec file."

	rpmdevBumpspecBinDirName = ".rpmdev-bumpspec-bin"
	rpmdevBumpspecRPMWrapper = "rpm"

	rpmdevBumpspecEVRSeparator   = "\x1f"
	rpmdevBumpspecEVRQueryFormat = "%{NAME}\x1f%{EPOCHNUM}\x1f%{VERSION}\x1f%{RELEASE}\n"
	rpmdevBumpspecEVRFieldCount  = 4
	rpmLabelCompareProgram       = "import rpm, sys\n" +
		"print(rpm.labelCompare(tuple(sys.argv[1:4]), tuple(sys.argv[4:7])))\n"
)

// ErrRPMDevBumpspecNoMutation is returned when rpmdev-bumpspec exits successfully without
// producing a newer source package EVR.
var ErrRPMDevBumpspecNoMutation = errors.New("rpmdev-bumpspec did not produce a newer source package EVR")

// RPMDevBumpspecSourceEVR is the source package identity evaluated by the host RPM implementation.
type RPMDevBumpspecSourceEVR struct {
	Name    string
	Epoch   string
	Version string
	Release string
}

// RPMDevBumpspecRequest describes one deterministic host rpmdev-bumpspec operation. HomeDir must
// be a request-local, initially empty directory whose lifecycle and cleanup are caller-owned.
type RPMDevBumpspecRequest struct {
	SpecPath   string
	Comment    string
	Identity   string
	Datestamp  string
	HomeDir    string
	Build      projectconfig.ComponentBuildConfig
	TargetArch string
}

// RunRPMDevBumpspec executes one deterministic host rpmdev-bumpspec operation. rpmdevtools remains
// an external host dependency because its GPL-licensed release behavior, including fallback forms,
// is intentionally not reimplemented by azldev.
//
//nolint:cyclop // The transaction explicitly preserves every actionable failure boundary.
func RunRPMDevBumpspec(ctx opctx.Ctx, request RPMDevBumpspecRequest) (err error) {
	if err := validateRPMDevBumpspecRequest(request); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("rpmdev-bumpspec operation cancelled before execution:\n%w", err)
	}

	if err := requireRPMDevBumpspecRuntime(ctx); err != nil {
		return err
	}

	if ctx.DryRun() {
		return runRPMDevBumpspecCommand(ctx, request)
	}

	beforeBytes, err := fileutils.ReadFile(ctx.FS(), request.SpecPath)
	if err != nil {
		return fmt.Errorf("failed to save spec %#q before rpmdev-bumpspec:\n%w", request.SpecPath, err)
	}

	defer func() {
		if err == nil {
			return
		}

		if restoreErr := fileutils.WriteFile(
			ctx.FS(), request.SpecPath, beforeBytes, fileperms.PublicFile,
		); restoreErr != nil {
			err = fmt.Errorf("rpmdev-bumpspec failed and restoring original spec %#q also failed:\n%w",
				request.SpecPath, errors.Join(err, restoreErr))
		}
	}()

	if err := prepareRPMDevBumpspecHome(ctx, request); err != nil {
		return err
	}

	beforeEVR, err := evaluateRPMDevBumpspecSourceEVR(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to evaluate source package EVR before rpmdev-bumpspec for spec %#q:\n%w",
			request.SpecPath, err)
	}

	if err := runRPMDevBumpspecCommand(ctx, request); err != nil {
		return err
	}

	afterEVR, err := evaluateRPMDevBumpspecSourceEVR(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to evaluate source package EVR after rpmdev-bumpspec for spec %#q:\n%w",
			request.SpecPath, err)
	}

	if beforeEVR.Name != afterEVR.Name || beforeEVR.Epoch != afterEVR.Epoch || beforeEVR.Version != afterEVR.Version {
		return fmt.Errorf("rpmdev-bumpspec unexpectedly changed source package identity for spec %#q "+
			"(before: %#v, after: %#v):\n%w", request.SpecPath, beforeEVR, afterEVR, ErrRPMDevBumpspecNoMutation)
	}

	if err := requireRPMDevBumpspecReleaseIncrease(ctx, request, beforeEVR, afterEVR); err != nil {
		return err
	}

	return nil
}

func requireRPMDevBumpspecRuntime(ctx opctx.Ctx) error {
	for _, binary := range rpmdevBumpspecBinaries() {
		if !ctx.CommandInSearchPath(binary) {
			return fmt.Errorf(
				"required runtime executable %#q was not found on PATH; install a behaviorally compatible "+
					"'rpmdevtools' runtime (tested: 9.6 / rpmdev-bumpspec 1.0.13):\n%w",
				binary, externalcmd.ErrMissingExecutable,
			)
		}
	}

	return nil
}

func runRPMDevBumpspecCommand(ctx opctx.Ctx, request RPMDevBumpspecRequest) error {
	rawCmd := exec.CommandContext(ctx, RPMDevBumpspecBinary,
		"-c", request.Comment,
		"-u", request.Identity,
		"-d", request.Datestamp,
		request.SpecPath,
	)
	rawCmd.Env = rpmdevBumpspecEnv(ctx, request)

	var stdout, stderr bytes.Buffer

	rawCmd.Stdout = &stdout
	rawCmd.Stderr = &stderr

	cmd, err := ctx.Command(rawCmd)
	if err != nil {
		return fmt.Errorf("failed to create rpmdev-bumpspec command:\n%w", err)
	}

	if err := cmd.Run(ctx); err != nil {
		return fmt.Errorf(
			"rpmdev-bumpspec failed for spec %#q (stdout: %#q, stderr: %#q):\n%w",
			request.SpecPath, stdout.String(), stderr.String(), err,
		)
	}

	if reportsRPMAutoSpecNoBump(stdout.String(), stderr.String()) {
		return fmt.Errorf(
			"rpmdev-bumpspec reported no release bump for spec %#q (stdout: %#q, stderr: %#q):\n%w",
			request.SpecPath, stdout.String(), stderr.String(), ErrRPMDevBumpspecNoMutation,
		)
	}

	return nil
}

func prepareRPMDevBumpspecHome(ctx opctx.Ctx, request RPMDevBumpspecRequest) error {
	if err := fileutils.MkdirAll(ctx.FS(), request.HomeDir); err != nil {
		return fmt.Errorf("failed to create isolated HOME %#q:\n%w", request.HomeDir, err)
	}

	isHomeEmpty, err := fileutils.IsDirEmpty(ctx.FS(), request.HomeDir)
	if err != nil {
		return fmt.Errorf("failed to inspect isolated HOME %#q:\n%w", request.HomeDir, err)
	}

	if !isHomeEmpty {
		return fmt.Errorf("isolated HOME %#q must be empty before running rpmdev-bumpspec", request.HomeDir)
	}

	rpmWrapperPath := filepath.Join(request.HomeDir, rpmdevBumpspecBinDirName, rpmdevBumpspecRPMWrapper)
	if err := fileutils.MkdirAll(ctx.FS(), filepath.Dir(rpmWrapperPath)); err != nil {
		return fmt.Errorf("failed to create isolated RPM wrapper directory %#q:\n%w", filepath.Dir(rpmWrapperPath), err)
	}

	if err := fileutils.WriteFile(ctx.FS(), rpmWrapperPath, []byte(rpmdevBumpspecWrapper(request)),
		fileperms.PublicExecutable); err != nil {
		return fmt.Errorf("failed to create isolated RPM wrapper %#q:\n%w", rpmWrapperPath, err)
	}

	return nil
}

func rpmdevBumpspecWrapper(request RPMDevBumpspecRequest) string {
	args := append([]string{"rpm"}, rpmdevBumpspecContextArgs(request)...)

	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = shellQuote(arg)
	}

	return "#!/bin/sh\n" +
		"PATH=\"$RPMDEV_BUMPSPEC_ORIGINAL_PATH\"\n" +
		"export PATH\n" +
		"exec " + strings.Join(quoted, " ") + " \"$@\"\n"
}

func rpmdevBumpspecContextArgs(request RPMDevBumpspecRequest) []string {
	args := []string{
		"--define", "_sourcedir " + filepath.Dir(request.SpecPath),
		"--define", "_specdir " + filepath.Dir(request.SpecPath),
	}
	if request.TargetArch != "" {
		args = append(args, "--target="+request.TargetArch)
	}

	macros := buildMacrosMap(request.Build)

	macroNames := make([]string, 0, len(macros))
	for name := range macros {
		macroNames = append(macroNames, name)
	}

	sort.Strings(macroNames)

	for _, name := range macroNames {
		args = append(args, "--define", name+" "+macros[name])
	}

	return args
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func evaluateRPMDevBumpspecSourceEVR(ctx opctx.Ctx, request RPMDevBumpspecRequest) (RPMDevBumpspecSourceEVR, error) {
	args := append(rpmdevBumpspecContextArgs(request),
		"-q", "--srpm",
		"--define", "dist %{nil}",
		"--qf", rpmdevBumpspecEVRQueryFormat,
		request.SpecPath,
	)
	rawCmd := exec.CommandContext(ctx, rpmspecBinary, args...)
	rawCmd.Env = rpmdevBumpspecEnv(ctx, request)

	var stdout, stderr bytes.Buffer

	rawCmd.Stdout = &stdout
	rawCmd.Stderr = &stderr

	cmd, err := ctx.Command(rawCmd)
	if err != nil {
		return RPMDevBumpspecSourceEVR{}, fmt.Errorf("failed to create host rpmspec source-EVR query:\n%w", err)
	}

	if err := cmd.Run(ctx); err != nil {
		return RPMDevBumpspecSourceEVR{}, fmt.Errorf(
			"host rpmspec source-EVR query failed for spec %#q (stdout: %#q, stderr: %#q):\n%w",
			request.SpecPath, stdout.String(), stderr.String(), err)
	}

	result, err := parseRPMDevBumpspecSourceEVR(stdout.String())
	if err != nil {
		return RPMDevBumpspecSourceEVR{}, fmt.Errorf("invalid host rpmspec source-EVR query output for spec %#q "+
			"(stdout: %#q, stderr: %#q):\n%w", request.SpecPath, stdout.String(), stderr.String(), err)
	}

	return result, nil
}

func parseRPMDevBumpspecSourceEVR(output string) (RPMDevBumpspecSourceEVR, error) {
	if !strings.HasSuffix(output, "\n") || strings.Count(output, "\n") != 1 || strings.Contains(output, "\r") {
		return RPMDevBumpspecSourceEVR{}, fmt.Errorf(
			"expected exactly one terminal newline in source EVR output, got %#q", output)
	}

	fields := strings.Split(strings.TrimSuffix(output, "\n"), rpmdevBumpspecEVRSeparator)
	if len(fields) != rpmdevBumpspecEVRFieldCount {
		return RPMDevBumpspecSourceEVR{}, fmt.Errorf("expected exactly four source EVR fields, got %#q", output)
	}

	for _, field := range fields {
		if field == "" {
			return RPMDevBumpspecSourceEVR{}, fmt.Errorf("source EVR output contains an empty field: %#q", output)
		}
	}

	if fields[1] == "(none)" {
		fields[1] = "0"
	}

	return RPMDevBumpspecSourceEVR{Name: fields[0], Epoch: fields[1], Version: fields[2], Release: fields[3]}, nil
}

func requireRPMDevBumpspecReleaseIncrease(
	ctx opctx.Ctx, request RPMDevBumpspecRequest, before, after RPMDevBumpspecSourceEVR,
) error {
	rawCmd := exec.CommandContext(ctx, python3Binary, "-c", rpmLabelCompareProgram,
		before.Epoch, before.Version, before.Release, after.Epoch, after.Version, after.Release)
	rawCmd.Env = rpmdevBumpspecEnv(ctx, request)

	var stdout, stderr bytes.Buffer

	rawCmd.Stdout = &stdout
	rawCmd.Stderr = &stderr

	cmd, err := ctx.Command(rawCmd)
	if err != nil {
		return fmt.Errorf("failed to create python RPM release comparison:\n%w", err)
	}

	if err := cmd.Run(ctx); err != nil {
		return fmt.Errorf("host RPM release comparison failed for spec %#q "+
			"(before: %#v, after: %#v, stdout: %#q, stderr: %#q):\n%w",
			request.SpecPath, before, after, stdout.String(), stderr.String(), errors.Join(ErrRPMDevBumpspecNoMutation, err))
	}

	switch comparison := stdout.String(); comparison {
	case "-1\n":
		return nil
	case "0\n", "1\n":
		return fmt.Errorf("host RPM release comparison rejected non-increasing release for spec %#q "+
			"(before: %#v, after: %#v, comparison: %#q, stdout: %#q, stderr: %#q):\n%w",
			request.SpecPath, before, after, comparison, stdout.String(), stderr.String(), ErrRPMDevBumpspecNoMutation)
	default:
		err := fmt.Errorf("expected exactly '-1\\n', '0\\n', or '1\\n', got %#q", comparison)

		return fmt.Errorf("host RPM release comparison produced invalid output for spec %#q "+
			"(before: %#v, after: %#v, stdout: %#q, stderr: %#q):\n%w",
			request.SpecPath, before, after, stdout.String(), stderr.String(), errors.Join(ErrRPMDevBumpspecNoMutation, err))
	}
}

func rpmdevBumpspecEnv(ctx opctx.Ctx, request RPMDevBumpspecRequest) []string {
	originalPath := ctx.OSEnv().Getenv("PATH")

	return []string{
		"PATH=" + filepath.Join(request.HomeDir, rpmdevBumpspecBinDirName) + string(os.PathListSeparator) + originalPath,
		"HOME=" + request.HomeDir,
		"LC_ALL=C",
		"TZ=UTC",
		"RPMDEV_BUMPSPEC_ORIGINAL_PATH=" + originalPath,
	}
}

func reportsRPMAutoSpecNoBump(stdout, stderr string) bool {
	return strings.Contains(stdout, rpmautospecNoBumpOutput) || strings.Contains(stderr, rpmautospecNoBumpOutput)
}

func rpmdevBumpspecBinaries() []string {
	return []string{RPMDevBumpspecBinary, rpmdevPackagerBinary, rpmBinary, rpmspecBinary, python3Binary}
}

func validateRPMDevBumpspecRequest(request RPMDevBumpspecRequest) error {
	if request.SpecPath == "" || !filepath.IsAbs(request.SpecPath) {
		return fmt.Errorf("rpmdev-bumpspec spec path %#q must be absolute", request.SpecPath)
	}

	if request.HomeDir == "" || !filepath.IsAbs(request.HomeDir) {
		return fmt.Errorf("rpmdev-bumpspec HOME path %#q must be absolute", request.HomeDir)
	}

	if strings.ContainsRune(request.HomeDir, os.PathListSeparator) {
		return fmt.Errorf("rpmdev-bumpspec HOME path %#q must not contain PATH list separator %#q",
			request.HomeDir, string(os.PathListSeparator))
	}

	for _, field := range []struct{ name, value string }{
		{"comment", request.Comment}, {"identity", request.Identity}, {"datestamp", request.Datestamp},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("rpmdev-bumpspec '%s' must not be empty", field.name)
		}
	}

	return nil
}
