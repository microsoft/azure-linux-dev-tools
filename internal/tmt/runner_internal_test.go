// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package tmt

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unreadableArtifactFS struct {
	opctx.FS
	blocked string
	err     error
}

func (fs unreadableArtifactFS) Open(path string) (afero.File, error) {
	if path == fs.blocked {
		return nil, &os.PathError{Op: "open", Path: path, Err: fs.err}
	}

	return fs.FS.Open(path)
}

func (fs unreadableArtifactFS) Stat(path string) (os.FileInfo, error) {
	if strings.HasPrefix(path, fs.blocked+string(filepath.Separator)) {
		return nil, &os.PathError{Op: "stat", Path: path, Err: fs.err}
	}

	return fs.FS.Stat(path)
}

func TestCollectTMTJUnitUnreadableDirectories(t *testing.T) {
	const (
		root   = "/owned"
		native = "/owned/native"
	)
	// Directory names here are legitimate plan hierarchy, not lifecycle steps.
	goodReport := "native/execute/data/report/nested/report/azldev-junit/junit.xml"
	otherReport := "native/other/report/azldev-junit/junit.xml"

	for _, testCase := range []struct {
		name, blocked string
		readErr       error
		want          []string
	}{
		{"private test data", "other/execute/data/private", os.ErrPermission, []string{goodReport, otherReport}},
		{
			"private payload named report", "other/execute/data/report", os.ErrPermission,
			[]string{goodReport, otherReport},
		},
		{"private report directory", "other/report", os.ErrPermission, []string{goodReport}},
		{"private reporter directory", "other/report/azldev-junit", os.ErrPermission, []string{goodReport}},
		{"private run directory", "", os.ErrPermission, []string{}},
		{"payload IO failure", "other/execute/data/private", io.ErrUnexpectedEOF, []string{goodReport, otherReport}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := testutils.NewTestEnv(t)

			var warnings strings.Builder

			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&warnings, nil)))
			t.Cleanup(func() { slog.SetDefault(prev) })

			for _, path := range []string{goodReport, otherReport} {
				require.NoError(t, fileutils.WriteFile(env.TestFS, filepath.Join(root, path),
					[]byte("<testsuites/>"), fileperms.PrivateFile))
			}

			blocked := filepath.Join(native, testCase.blocked)
			require.NoError(t, env.TestFS.MkdirAll(blocked, fileperms.PrivateDir))

			reports := collectTMTJUnit(unreadableArtifactFS{
				FS: env.TestFS, blocked: blocked, err: testCase.readErr,
			}, root, native)

			assert.Equal(t, testCase.want, reports)
			assert.Contains(t, warnings.String(), "level=WARN")
			assert.Contains(t, warnings.String(), blocked)
			assert.Contains(t, warnings.String(), testCase.readErr.Error())
		})
	}
}

type reportSymlinkFS struct {
	opctx.FS
	link string
}

type reportSymlinkInfo struct{ os.FileInfo }

func (reportSymlinkInfo) Mode() os.FileMode { return os.ModeSymlink | fileperms.PublicFile }

func (fs reportSymlinkFS) LstatIfPossible(path string) (os.FileInfo, bool, error) {
	info, err := fs.Stat(path)
	if err == nil && path == fs.link {
		info = reportSymlinkInfo{info}
	}

	return info, true, err
}

func TestRetainedTMTReportBoundaries(t *testing.T) {
	env := testutils.NewTestEnv(t)
	require.NoError(t, fileutils.WriteFile(
		env.TestFS, "/owned/native/junit.xml", []byte("<testsuites/>"), fileperms.PrivateFile,
	))

	for _, link := range []string{"/owned", "/owned/native", "/owned/native/junit.xml"} {
		_, err := retainedTMTReport(reportSymlinkFS{FS: env.TestFS, link: link}, "/owned", "/owned/native/junit.xml")
		require.ErrorContains(t, err, "symlink")
	}

	for _, path := range []string{"/outside", "/owned/../outside", "/owned/native", "/owned"} {
		_, err := retainedTMTReport(env.TestFS, "/owned", path)
		require.Error(t, err)
	}

	rel, err := retainedTMTReport(env.TestFS, "/owned", "/owned/native/junit.xml")
	require.NoError(t, err)
	assert.Equal(t, "native/junit.xml", rel)
}
