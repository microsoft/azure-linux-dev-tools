// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
)

// CommitMetadata holds full metadata for a commit in the project repository.
type CommitMetadata struct {
	Hash        string
	Author      string
	AuthorEmail string
	Timestamp   int64
	Message     string
}

// FingerprintChange records a project commit that changed a component's lock file
// fingerprint. [UpstreamCommit] is the value of the 'upstream-commit' field in the
// lock file at the point of the change.
type FingerprintChange struct {
	CommitMetadata

	// UpstreamCommit is the upstream dist-git commit hash recorded in the lock
	// file at the time the fingerprint changed.
	UpstreamCommit string
}

// interleavedEntry represents a single commit in the rebuilt dist-git history.
// Exactly one of upstreamCommit or syntheticChange is non-nil.
type interleavedEntry struct {
	upstreamCommit  *object.Commit
	syntheticChange *FingerprintChange
}

// FindFingerprintChanges walks the git log of the project repository for commits
// that changed the given lock file and returns metadata for each commit where the
// 'input-fingerprint' field changed. Results are sorted chronologically (oldest
// first).
func FindFingerprintChanges(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	projectRepo *gogit.Repository,
	projectRepoDir string,
	lockFileRelPath string,
) ([]FingerprintChange, error) {
	// Get commit metadata (newest-first) for all commits that touched the lock file.
	metas, err := gitLogFileMetadata(ctx, cmdFactory, projectRepoDir, lockFileRelPath)
	if err != nil {
		return nil, err
	}

	if len(metas) == 0 {
		return nil, nil
	}

	// Pair each commit's metadata with its lock file contents.
	type entry struct {
		lock lockfile.ComponentLock
		meta CommitMetadata
	}

	var entries []entry //nolint:prealloc // size not known ahead of time.

	for _, meta := range metas {
		lock, err := showLockFileAtCommit(projectRepo, meta.Hash, lockFileRelPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read lock file at commit %#q:\n%w", meta.Hash, err)
		}

		entries = append(entries, entry{lock: lock, meta: meta})
	}

	if len(entries) == 0 {
		return nil, nil
	}

	// Entries are newest-first (from git log order). Reverse to chronological.
	slices.Reverse(entries)

	// Walk chronologically and detect fingerprint changes.
	var changes []FingerprintChange

	prevFingerprint := ""

	for _, change := range entries {
		if change.lock.InputFingerprint != prevFingerprint {
			changes = append(changes, FingerprintChange{
				CommitMetadata: change.meta,
				UpstreamCommit: change.lock.UpstreamCommit,
			})
		}

		prevFingerprint = change.lock.InputFingerprint
	}

	return changes, nil
}

// CommitInterleavedHistory rebuilds the dist-git history by interleaving
// synthetic commits with the existing upstream commits. Synthetic commits
// referencing an older upstream commit are placed directly after that commit;
// those referencing the latest upstream commit are appended on top. The very
// last synthetic commit carries the overlay file changes; all others are empty.
//
// When importCommit is non-empty, only upstream commits from importCommit
// onward are considered for interleaving.
func CommitInterleavedHistory(
	repo *gogit.Repository,
	changes []FingerprintChange,
	importCommit string,
) error {
	// No changes means no synthetic commits to create, so skip the whole process.
	if len(changes) == 0 {
		return errors.New("no fingerprint changes to commit")
	}

	// The latest fingerprint change's UpstreamCommit is the commit we're
	// pinned to — use it as the upper bound for the upstream walk instead
	// of HEAD, which may be ahead (e.g., at the branch tip).
	upstreamCommit := changes[len(changes)-1].UpstreamCommit

	// Collect upstream commits BEFORE staging, so the temporary commit
	// created by stageAndCaptureOverlayTree is not included.
	upstreamCommits, err := collectUpstreamCommits(repo, importCommit, upstreamCommit)
	if err != nil {
		return err
	}

	// Stage overlay changes and capture the resulting tree hash.
	overlayTreeHash, err := stageAndCaptureOverlayTree(repo)
	if err != nil {
		return err
	}

	// Build the full interleaved sequence of upstream and synthetic commits.
	sequence := buildInterleavedSequence(upstreamCommits, changes)

	return replayInterleavedHistory(repo, sequence, overlayTreeHash)
}

// stageAndCaptureOverlayTree stages all working tree changes and creates a
// temporary commit to capture the resulting tree hash. The tree hash is used
// later to set the content of the final synthetic commit.
func stageAndCaptureOverlayTree(repo *gogit.Repository) (plumbing.Hash, error) {
	worktree, err := repo.Worktree()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to get worktree:\n%w", err)
	}

	if err := worktree.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to stage changes:\n%w", err)
	}

	tempHash, err := worktree.Commit("temp: capture overlay tree", &gogit.CommitOptions{
		AllowEmptyCommits: true,
		Author:            &object.Signature{Name: "azldev", When: time.Unix(0, 0).UTC()},
	})
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to create temporary commit:\n%w", err)
	}

	tempCommit, err := repo.CommitObject(tempHash)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to read temporary commit:\n%w", err)
	}

	return tempCommit.TreeHash, nil
}

// buildInterleavedSequence produces the full commit sequence for the rebuilt
// history. Upstream commits appear in chronological order; synthetic commits
// that reference an older upstream are inserted directly after it. Synthetic
// commits referencing the latest upstream are appended at the end.
// Changes with no upstream commit (local components) are always placed on top.
// Orphaned commits whose non-empty upstream is not found in the dist-git
// history are dropped with a warning.
func buildInterleavedSequence(
	upstreamCommits []*object.Commit,
	changes []FingerprintChange,
) []interleavedEntry {
	latestUpstream := changes[len(changes)-1].UpstreamCommit

	var interleaved, top []FingerprintChange

	for idx := range changes {
		switch changes[idx].UpstreamCommit {
		case "":
			// Local component changes have no upstream commit reference.
			// Always place them on top of the history.
			top = append(top, changes[idx])
		case latestUpstream:
			top = append(top, changes[idx])
		default:
			interleaved = append(interleaved, changes[idx])
		}
	}

	// Build a lookup from upstream-commit hash → synthetic commits.
	interleavedByUpstream := make(map[string][]FingerprintChange)

	for i := range interleaved {
		hash := interleaved[i].UpstreamCommit
		interleavedByUpstream[hash] = append(interleavedByUpstream[hash], interleaved[i])
	}

	// Walk upstream commits, inserting synthetics after their referenced commit.
	sequence := make([]interleavedEntry, 0, len(upstreamCommits)+len(changes))

	for i := range upstreamCommits {
		sequence = append(sequence, interleavedEntry{upstreamCommit: upstreamCommits[i]})

		hash := upstreamCommits[i].Hash.String()
		if synthetics, ok := interleavedByUpstream[hash]; ok {
			for j := range synthetics {
				synth := synthetics[j]
				sequence = append(sequence, interleavedEntry{syntheticChange: &synth})
			}

			delete(interleavedByUpstream, hash)
		}
	}

	// Remaining interleaved changes reference upstream commits not found in
	// the dist-git history — drop them with a warning. Will be useful for when we switch branches.
	for hash, orphaned := range interleavedByUpstream {
		slog.Warn("Upstream commit referenced by fingerprint change not found in dist-git history; "+
			"dropping",
			"upstreamCommit", hash,
			"count", len(orphaned))
	}

	// Append "top" synthetic commits at the end.
	for i := range top {
		topChange := top[i]
		sequence = append(sequence, interleavedEntry{syntheticChange: &topChange})
	}

	return sequence
}

// replayInterleavedHistory walks the interleaved sequence and creates new
// commit objects with correct tree hashes and parent chains. The first upstream
// commit (import-commit) is kept as-is; subsequent upstream commits are
// recreated with updated parents. Synthetic commits are empty except for the
// very last one, which carries the overlay tree.
func replayInterleavedHistory(
	repo *gogit.Repository,
	sequence []interleavedEntry,
	overlayTreeHash plumbing.Hash,
) error {
	syntheticCount := countSyntheticEntries(sequence)

	var (
		lastHash     plumbing.Hash
		lastTreeHash plumbing.Hash
		syntheticIdx int
	)

	for idx, entry := range sequence {
		if idx == 0 && entry.upstreamCommit != nil {
			lastHash = entry.upstreamCommit.Hash
			lastTreeHash = entry.upstreamCommit.TreeHash

			continue
		}

		if entry.upstreamCommit != nil {
			hash, err := replayUpstreamCommit(repo, entry.upstreamCommit, lastHash)
			if err != nil {
				return err
			}

			lastHash = hash
			lastTreeHash = entry.upstreamCommit.TreeHash

			continue
		}

		syntheticIdx++

		isLast := syntheticIdx == syntheticCount

		treeHash := lastTreeHash
		if isLast {
			treeHash = overlayTreeHash
		}

		hash, err := createSyntheticCommit(repo, entry.syntheticChange, treeHash, lastHash,
			syntheticIdx, syntheticCount)
		if err != nil {
			return err
		}

		lastHash = hash
		lastTreeHash = treeHash
	}

	if err := updateHead(repo, lastHash); err != nil {
		return err
	}

	slog.Info("Interleaved synthetic history complete",
		"syntheticCommits", syntheticCount,
		"totalCommits", len(sequence))

	return nil
}

// replayUpstreamCommit recreates an upstream commit with a new parent, preserving
// tree content, author, committer, and message. Returns an error if the commit
// is a merge commit (multiple parents), since the replay assumes linear history.
func replayUpstreamCommit(
	repo *gogit.Repository,
	commit *object.Commit,
	parentHash plumbing.Hash,
) (plumbing.Hash, error) {
	if len(commit.ParentHashes) > 1 {
		return plumbing.ZeroHash, fmt.Errorf("upstream commit %s is a merge commit; linear history expected",
			commit.Hash)
	}

	hash, err := createCommitObject(repo, commit.TreeHash, parentHash,
		commit.Author, commit.Committer, commit.Message)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to replay upstream commit:\n%w", err)
	}

	return hash, nil
}

// createSyntheticCommit creates a synthetic commit from a [FingerprintChange],
// logging progress information.
func createSyntheticCommit(
	repo *gogit.Repository,
	change *FingerprintChange,
	treeHash, parentHash plumbing.Hash,
	syntheticIdx, syntheticCount int,
) (plumbing.Hash, error) {
	author := object.Signature{
		Name:  change.Author,
		Email: change.AuthorEmail,
		When:  unixToTime(change.Timestamp),
	}

	message := fmt.Sprintf("%s\n\nProject commit: %s", change.Message, change.Hash)

	slog.Info("Creating synthetic commit",
		"commit", syntheticIdx,
		"total", syntheticCount,
		"projectHash", change.Hash,
		"upstreamCommit", change.UpstreamCommit,
		"isLast", syntheticIdx == syntheticCount,
	)

	hash, err := createCommitObject(repo, treeHash, parentHash, author, author, message)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to create synthetic commit %d:\n%w", syntheticIdx, err)
	}

	return hash, nil
}

// countSyntheticEntries returns the number of synthetic entries in the sequence.
func countSyntheticEntries(sequence []interleavedEntry) int {
	count := 0

	for _, entry := range sequence {
		if entry.syntheticChange != nil {
			count++
		}
	}

	return count
}

// createCommitObject creates a new commit in the repository's object store with
// the given tree, parent, author, committer, and message.
func createCommitObject(
	repo *gogit.Repository,
	treeHash, parentHash plumbing.Hash,
	author, committer object.Signature,
	message string,
) (plumbing.Hash, error) {
	commit := &object.Commit{
		Author:       author,
		Committer:    committer,
		Message:      message,
		TreeHash:     treeHash,
		ParentHashes: []plumbing.Hash{parentHash},
	}

	obj := repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to encode commit:\n%w", err)
	}

	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to store commit:\n%w", err)
	}

	return hash, nil
}

// updateHead updates the HEAD reference (or the branch it points to) to the
// given commit hash.
func updateHead(repo *gogit.Repository, commitHash plumbing.Hash) error {
	head, err := repo.Storer.Reference(plumbing.HEAD)
	if err != nil {
		return fmt.Errorf("failed to read HEAD reference:\n%w", err)
	}

	// Resolve symbolic ref (e.g., HEAD → refs/heads/main).
	name := plumbing.HEAD
	if head.Type() != plumbing.HashReference {
		name = head.Target()
	}

	ref := plumbing.NewHashReference(name, commitHash)
	if err := repo.Storer.SetReference(ref); err != nil {
		return fmt.Errorf("failed to update HEAD to %s:\n%w", commitHash, err)
	}

	return nil
}

// buildSyntheticCommits resolves the project repository from the component's
// config file, walks the lock file's git history for fingerprint changes, and
// returns the matching [FingerprintChange] entries sorted chronologically.
// Returns an error if the lock file exists but has no fingerprint changes.
// The second return value is the import-commit hash from the lock file, used
// to scope the upstream commit walk in [CommitInterleavedHistory].
func buildSyntheticCommits(
	ctx context.Context,
	cmdFactory opctx.CmdFactory,
	config *projectconfig.ComponentConfig,
	lockFileRelPath string,
) (changes []FingerprintChange, importCommit string, err error) {
	if config.SourceConfigFile == nil || config.SourceConfigFile.SourcePath() == "" {
		slog.Debug("Cannot resolve config file for synthetic commits; skipping",
			"lockFile", lockFileRelPath)

		return nil, "", nil
	}

	configFilePath := config.SourceConfigFile.SourcePath()

	// Open the project repository by walking up from the config file directory.
	projectRepo, err := gogit.PlainOpenWithOptions(
		filepath.Dir(configFilePath),
		&gogit.PlainOpenOptions{DetectDotGit: true},
	)
	if err != nil {
		return nil, "", fmt.Errorf("failed to find project repository for config file %#q:\n%w",
			configFilePath, err)
	}

	// Read the current lock file at HEAD to get the import-commit boundary.
	head, err := projectRepo.Head()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get HEAD:\n%w", err)
	}

	// Read the lock file at HEAD. Distinguish "path does not exist in the
	// commit tree" (file-not-found) from real failures such as TOML parse
	// errors or unexpected git object errors — only the former is tolerated.
	headLock, lockFileErr := showLockFileAtCommit(projectRepo, head.Hash().String(), lockFileRelPath)
	if lockFileErr != nil {
		// Real errors (parse failures, git object errors, etc.) are always fatal.
		if !errors.Is(lockFileErr, object.ErrFileNotFound) {
			return nil, "", fmt.Errorf("failed to read lock file %#q at HEAD:\n%w",
				lockFileRelPath, lockFileErr)
		}

		// File genuinely missing — the component has no overlays, so the
		// dist-git is just the upstream as-is and no synthetic commits
		// are needed.
		//nolint:godox // tracked by TODO(lockfiles) tag.
		// TODO(lockfiles): remove env var gate and make this a hard error unconditionally.
		if os.Getenv("AZLDEV_ENABLE_LOCK_VALIDATION") == "1" {
			return nil, "", fmt.Errorf("lock file %#q not found at HEAD:\n%w",
				lockFileRelPath, lockFileErr)
		}

		slog.Debug("No lock file found at HEAD; skipping synthetic history",
			"lockFile", lockFileRelPath, "error", lockFileErr)

		return nil, "", nil
	}

	importCommit = headLock.ImportCommit

	// Resolve the worktree root for git CLI operations that still need a
	// directory path (gitLogFileMetadata uses git log with path filtering).
	worktree, err := projectRepo.Worktree()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get project worktree:\n%w", err)
	}

	projectRepoDir := worktree.Filesystem.Root()

	fpChanges, err := FindFingerprintChanges(ctx, cmdFactory, projectRepo, projectRepoDir, lockFileRelPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to find fingerprint changes for lock file %#q:\n%w",
			lockFileRelPath, err)
	}

	if len(fpChanges) == 0 {
		return nil, "", fmt.Errorf(
			"lock file %#q exists but has no fingerprint changes; "+
				"this indicates a corrupt or empty lock file history",
			lockFileRelPath)
	}

	slog.Info("Found fingerprint changes",
		"lockFile", lockFileRelPath,
		"changeCount", len(fpChanges))

	return fpChanges, importCommit, nil
}

// collectUpstreamCommits returns commits in the repository in chronological
// order (oldest first), bounded by importCommit (inclusive start) and
// upstreamCommit (inclusive end). The walk stops as soon as the import-commit
// is reached to avoid traversing the entire history.
func collectUpstreamCommits(
	repo *gogit.Repository, importCommit, upstreamCommit string,
) ([]*object.Commit, error) {
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD reference:\n%w", err)
	}

	iter, err := repo.Log(&gogit.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate commit log:\n%w", err)
	}

	// Walk newest-first. Collect commits until we pass the upstream-commit
	// boundary, then keep collecting until we reach the import-commit.
	var (
		commits       []*object.Commit
		foundUpstream bool
		foundImport   bool
		collecting    = upstreamCommit == "" // if no upper bound, collect from start.
	)

	err = iter.ForEach(func(commit *object.Commit) error {
		hash := commit.Hash.String()

		// Start collecting once we see the upstream-commit (newest boundary).
		if !collecting && hash == upstreamCommit {
			collecting = true
		}

		if collecting {
			commits = append(commits, commit)
		}

		if hash == upstreamCommit {
			foundUpstream = true
		}

		// Stop once we reach the import-commit (oldest boundary).
		if importCommit != "" && hash == importCommit {
			foundImport = true

			return storer.ErrStop
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk commit log:\n%w", err)
	}

	if upstreamCommit != "" && !foundUpstream {
		return nil, fmt.Errorf(
			"upstream-commit %#q not found in dist-git history; "+
				"the lock file may reference a commit from a different branch",
			upstreamCommit)
	}

	if importCommit != "" && !foundImport {
		return nil, fmt.Errorf(
			"import-commit %#q not found in dist-git history; "+
				"the repository may be a shallow clone or the commit may have been rebased away",
			importCommit)
	}

	// Walk was newest-first; reverse to chronological.
	slices.Reverse(commits)

	return commits, nil
}

// unixToTime converts a Unix timestamp to a [time.Time] in UTC.
func unixToTime(unix int64) time.Time {
	return time.Unix(unix, 0).UTC()
}

// --- git CLI helpers ---

// gitLogFileMetadata returns commit metadata (newest-first) for all commits
// that touched the given file path in the repository at repoDir. Fields within
// each record are separated by NUL (\x00); records are separated by SOH (\x01).
func gitLogFileMetadata(
	ctx context.Context, cmdFactory opctx.CmdFactory, repoDir, filePath string,
) ([]CommitMetadata, error) {
	output, err := git.RunInDir(ctx, cmdFactory, repoDir,
		"log", "--format=%H%x00%an%x00%ae%x00%at%x00%s%x01", "--", filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to list commits for %#q:\n%w", filePath, err)
	}

	if output == "" {
		return nil, nil
	}

	blocks := strings.Split(output, "\x01")

	var metas []CommitMetadata //nolint:prealloc // trailing empty block after split.

	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		meta, err := ParseCommitMetadata(block)
		if err != nil {
			return nil, fmt.Errorf("failed to parse commit metadata:\n%w", err)
		}

		metas = append(metas, meta)
	}

	return metas, nil
}

// showLockFileAtCommit reads the lock file content at a specific commit hash
// using go-git and parses it into a [lockfile.ComponentLock].
func showLockFileAtCommit(
	repo *gogit.Repository,
	commitHash, lockFileRelPath string,
) (lockfile.ComponentLock, error) {
	hash := plumbing.NewHash(commitHash)

	commitObj, err := repo.CommitObject(hash)
	if err != nil {
		return lockfile.ComponentLock{}, fmt.Errorf("failed to resolve commit %#q:\n%w", commitHash, err)
	}

	tree, err := commitObj.Tree()
	if err != nil {
		return lockfile.ComponentLock{}, fmt.Errorf("failed to get tree for commit %#q:\n%w", commitHash, err)
	}

	file, err := tree.File(lockFileRelPath)
	if err != nil {
		return lockfile.ComponentLock{}, fmt.Errorf("failed to read %#q at commit %#q:\n%w",
			lockFileRelPath, commitHash, err)
	}

	content, err := file.Contents()
	if err != nil {
		return lockfile.ComponentLock{}, fmt.Errorf("failed to read contents of %#q at commit %#q:\n%w",
			lockFileRelPath, commitHash, err)
	}

	lock, err := lockfile.Parse([]byte(content))
	if err != nil {
		return lockfile.ComponentLock{}, fmt.Errorf("failed to parse lock file %#q at commit %#q:\n%w",
			lockFileRelPath, commitHash, err)
	}

	return *lock, nil
}

// commitMetadataFieldCount is the number of NUL-separated fields expected in
// a single commit record produced by 'git log --format=%H%x00%an%x00%ae%x00%at%x00%s'.
const commitMetadataFieldCount = 5

// ParseCommitMetadata parses a single NUL-delimited commit record produced by
// 'git log --format=%H%x00%an%x00%ae%x00%at%x00%s'.
func ParseCommitMetadata(output string) (CommitMetadata, error) {
	fields := strings.SplitN(output, "\x00", commitMetadataFieldCount)

	if len(fields) < commitMetadataFieldCount {
		return CommitMetadata{}, fmt.Errorf(
			"unexpected git log output (expected %d fields, got %d):\n%v",
			commitMetadataFieldCount, len(fields), output)
	}

	var timestamp int64
	if _, err := fmt.Sscanf(fields[3], "%d", &timestamp); err != nil {
		return CommitMetadata{}, fmt.Errorf("failed to parse timestamp %#q:\n%w", fields[3], err)
	}

	return CommitMetadata{
		Hash:        fields[0],
		Author:      fields[1],
		AuthorEmail: fields[2],
		Timestamp:   timestamp,
		Message:     fields[4],
	}, nil
}
