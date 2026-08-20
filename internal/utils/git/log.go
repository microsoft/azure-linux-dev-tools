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
// recent such commit (zero when no commits found).
//
// The returned timestamp is on the committer-date axis: we format with %ct so
// it matches the order 'git log' walks (newest committer-date first).
//
// Shells out to 'git log -- <path>' because go-git's PathFilter walks the
// entire commit graph in-process and is prohibitively slow on large repos
// (see the commentary on gitLogFileMetadata in
// internal/app/azldev/core/sources/synthistory.go).
func CountCommitsTouchingFile(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	repoDir, relPath string,
) (count int, latest time.Time, err error) {
	args := []string{"log", "--format=%ct"}

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

	// Normalize to UTC so the timestamp serializes identically regardless of
	// the host's local timezone (time.Unix defaults to Location: Local, which
	// would otherwise make JSON output non-reproducible across machines).
	return len(lines), time.Unix(unixSeconds, 0).UTC(), nil
}
