// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

// CountCommitsTouchingFile returns the number of commits that touched relPath
// in the git repository rooted at repoDir, plus the timestamp of the most
// recent such commit (zero when no commits found). When since is non-zero,
// commits older than that are excluded.
//
// Shells out to 'git log -- <path>' because go-git's PathFilter walks the
// entire commit graph in-process and is prohibitively slow on large repos
// (see the commentary on gitLogFileMetadata in
// internal/app/azldev/core/sources/synthistory.go).
func CountCommitsTouchingFile(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	repoDir, relPath string,
	since time.Time,
) (count int, latest time.Time, err error) {
	args := []string{"log", "--format=%at"}

	if !since.IsZero() {
		args = append(args, "--since="+since.Format(time.RFC3339))
	}

	args = append(args, "--", relPath)

	output, err := RunInDir(ctx, cmdFactory, repoDir, args...)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("listing commits for %#q:\n%w", relPath, err)
	}

	if output == "" {
		return 0, time.Time{}, nil
	}

	lines := strings.Split(output, "\n")

	// 'git log' emits newest-first; the first line is the latest commit.
	unixSeconds, err := strconv.ParseInt(strings.TrimSpace(lines[0]), 10, 64)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("parsing commit timestamp %#q:\n%w", lines[0], err)
	}

	return len(lines), time.Unix(unixSeconds, 0), nil
}
