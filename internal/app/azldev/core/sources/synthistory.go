// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

var (
	// ErrNoGitRepository is returned when no enclosing git repository can be found.
	ErrNoGitRepository = errors.New("no git repository found")

	// ErrNoOverlaysToCommit is returned when there are no overlay groups to commit.
	ErrNoOverlaysToCommit = errors.New("no overlays to commit")

	// ErrLineRangeOverlayMismatch is returned when the number of located overlay line ranges
	// does not match the number of overlays on the component.
	ErrLineRangeOverlayMismatch = errors.New("line range count does not match overlay count")

	// sectionHeaderRegexp matches any TOML table or array-of-tables header line.
	sectionHeaderRegexp = regexp.MustCompile(`^\s*\[{1,2}[^\]]+\]{1,2}\s*$`)
)

// BlameEntry represents a single line's blame information from a git repository.
type BlameEntry struct {
	// CommitHash is the hash of the commit that last modified this line.
	CommitHash string
	// Author is the name of the author who last modified this line.
	Author string
	// Timestamp is when the line was last modified.
	Timestamp int64
	// Line is the 1-based line number.
	Line int
	// Content is the text content of the line.
	Content string
}

// CommitMetadata holds full metadata for a commit in the project repository.
type CommitMetadata struct {
	Hash        string
	Author      string
	AuthorEmail string
	Timestamp   int64
	Message     string
}

// OverlayCommitGroup groups overlays that originate from the same git commit in the project
// configuration repository. During synthetic history generation, all overlays in a group are
// applied together and recorded as a single commit.
type OverlayCommitGroup struct {
	// Commit holds metadata from the originating commit in the project repository.
	Commit CommitMetadata
	// Overlays contains the overlay definitions to apply as part of this synthetic commit.
	Overlays []projectconfig.ComponentOverlay
}

// OverlayApplyFunc is a callback that applies a batch of overlays to the component sources.
// It is called once per [OverlayCommitGroup] during synthetic history generation.
type OverlayApplyFunc func(overlays []projectconfig.ComponentOverlay) error

// ConfigBlameResult holds the per-line blame entries for a configuration file.
type ConfigBlameResult struct {
	// Entries contains one [BlameEntry] per line in the blamed file.
	Entries []BlameEntry
}

// OverlayLineRange tracks the line range of a single [[components.X.overlays]] block
// in a TOML config file.
type OverlayLineRange struct {
	StartLine int // 1-based, inclusive (the [[...]] header line)
	EndLine   int // 1-based, inclusive
	Index     int // positional index in the component's overlays slice
}

// BlameFile performs git blame on the specified file within the provided go-git repository.
// The filePath must be relative to the repository root.
func BlameFile(repo *gogit.Repository, filePath string) (*ConfigBlameResult, error) {
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD reference:\n%w", err)
	}

	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD commit:\n%w", err)
	}

	blameResult, err := gogit.Blame(commit, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to blame file %#q:\n%w", filePath, err)
	}

	entries := make([]BlameEntry, len(blameResult.Lines))
	for i, line := range blameResult.Lines {
		entries[i] = BlameEntry{
			CommitHash: line.Hash.String(),
			Author:     line.AuthorName,
			Timestamp:  line.Date.Unix(),
			Line:       i + 1,
			Content:    line.Text,
		}
	}

	return &ConfigBlameResult{Entries: entries}, nil
}

// FindOverlayLineRanges parses raw TOML content to locate the line ranges of all overlay
// definitions for the named component. It supports two TOML styles:
//
//  1. Array-of-tables: [[components.<name>.overlays]] blocks.
//  2. Inline array:    overlays = [ { ... }, { ... } ] under a [components.<name>] section.
//
// The returned ranges are ordered by their position in the file, matching the
// serialization order of the component's overlay slice.
func FindOverlayLineRanges(configContent string, componentName string) []OverlayLineRange {
	lines := strings.Split(configContent, "\n")

	ranges := findArrayOfTablesOverlays(lines, componentName)
	if len(ranges) > 0 {
		return ranges
	}

	return findInlineArrayOverlays(lines, componentName)
}

// findArrayOfTablesOverlays locates overlays declared as [[components.<name>.overlays]] blocks.
func findArrayOfTablesOverlays(lines []string, componentName string) []OverlayLineRange {
	expectedHeaders := []string{
		fmt.Sprintf("[[components.%s.overlays]]", componentName),
		fmt.Sprintf(`[[components."%s".overlays]]`, componentName),
	}

	var ranges []OverlayLineRange

	overlayIndex := 0

	for lineIdx := 0; lineIdx < len(lines); lineIdx++ {
		trimmed := strings.TrimSpace(lines[lineIdx])

		if !slices.Contains(expectedHeaders, trimmed) {
			continue
		}

		startLine := lineIdx + 1 // convert to 1-based

		// Find the end of this overlay block: the line before the next section header, or EOF.
		endLineExclusive := len(lines)
		for j := lineIdx + 1; j < len(lines); j++ {
			if sectionHeaderRegexp.MatchString(lines[j]) {
				endLineExclusive = j

				break
			}
		}

		for endLineExclusive > lineIdx+1 && strings.TrimSpace(lines[endLineExclusive-1]) == "" {
			endLineExclusive--
		}

		ranges = append(ranges, OverlayLineRange{
			StartLine: startLine,
			EndLine:   endLineExclusive,
			Index:     overlayIndex,
		})

		overlayIndex++
		lineIdx = endLineExclusive - 1 // advance past this block (loop increments)
	}

	return ranges
}

// findInlineArrayOverlays locates overlays declared as an inline array under a
// [components.<name>] section (e.g. overlays = [ { type = "patch-add", ... }, ... ]).
func findInlineArrayOverlays(lines []string, componentName string) []OverlayLineRange {
	sectionHeaders := []string{
		fmt.Sprintf("[components.%s]", componentName),
		fmt.Sprintf(`[components."%s"]`, componentName),
	}

	// Locate the section header for this component.
	sectionStart := -1

	for i, line := range lines {
		if slices.Contains(sectionHeaders, strings.TrimSpace(line)) {
			sectionStart = i

			break
		}
	}

	if sectionStart < 0 {
		return nil
	}

	// Scan forward from the section header to find "overlays = [", stopping at the next
	// section header.
	overlaysStart := -1

	for lineIdx := sectionStart + 1; lineIdx < len(lines); lineIdx++ {
		if sectionHeaderRegexp.MatchString(lines[lineIdx]) {
			break
		}

		trimmed := strings.TrimSpace(lines[lineIdx])
		if strings.HasPrefix(trimmed, "overlays") && strings.Contains(trimmed, "=") && strings.Contains(trimmed, "[") {
			overlaysStart = lineIdx

			break
		}
	}

	if overlaysStart < 0 {
		return nil
	}

	return parseInlineOverlayEntries(lines, overlaysStart)
}

// parseInlineOverlayEntries parses individual { ... } entries from an inline overlay array
// starting at the line containing "overlays = [". Each top-level brace pair is one overlay.
func parseInlineOverlayEntries(lines []string, overlaysStart int) []OverlayLineRange {
	var ranges []OverlayLineRange

	overlayIndex := 0
	braceDepth := 0
	entryStartLine := -1

	for lineIdx := overlaysStart; lineIdx < len(lines); lineIdx++ {
		line := lines[lineIdx]

		for _, ch := range line {
			switch ch {
			case '{':
				if braceDepth == 0 {
					entryStartLine = lineIdx + 1 // 1-based
				}

				braceDepth++
			case '}':
				braceDepth--

				if braceDepth == 0 && entryStartLine > 0 {
					ranges = append(ranges, OverlayLineRange{
						StartLine: entryStartLine,
						EndLine:   lineIdx + 1, // 1-based
						Index:     overlayIndex,
					})

					overlayIndex++
					entryStartLine = -1
				}
			}
		}

		// Stop scanning when we hit the closing ']' of the array (outside any braces).
		trimmed := strings.TrimSpace(line)
		if braceDepth == 0 && lineIdx > overlaysStart && (trimmed == "]" || strings.HasSuffix(trimmed, "]")) {
			break
		}
	}

	return ranges
}

// MapOverlaysToCommits groups overlays by their originating commit hash using blame data
// and overlay line ranges. It retrieves full commit metadata (author email, message) from
// the project repository for each unique commit. Groups are returned sorted chronologically.
func MapOverlaysToCommits(
	repo *gogit.Repository,
	overlays []projectconfig.ComponentOverlay,
	lineRanges []OverlayLineRange,
	blame *ConfigBlameResult,
) ([]OverlayCommitGroup, error) {
	if len(overlays) == 0 {
		return nil, nil
	}

	if blame == nil {
		return nil, errors.New("blame result cannot be nil")
	}

	if len(lineRanges) != len(overlays) {
		return nil, fmt.Errorf(
			"%w: found %d line ranges but component has %d overlays",
			ErrLineRangeOverlayMismatch, len(lineRanges), len(overlays),
		)
	}

	// Map each overlay to a blame commit hash derived from the full TOML block range
	// (StartLine..EndLine)
	commitOverlays := make(map[string][]projectconfig.ComponentOverlay)

	for _, lineRange := range lineRanges {
		if lineRange.StartLine < 1 ||
			lineRange.EndLine < 1 ||
			lineRange.StartLine > lineRange.EndLine ||
			lineRange.StartLine > len(blame.Entries) {
			return nil, fmt.Errorf(
				"overlay at index %d has line range [%d, %d], but blame has only %d lines",
				lineRange.Index, lineRange.StartLine, lineRange.EndLine, len(blame.Entries),
			)
		}

		// Clamp EndLine to the blame length. TOML blocks at EOF may extend past the
		// last blamed line when the file has a trailing newline that git blame omits.
		endLine := min(lineRange.EndLine, len(blame.Entries))

		// Attribute the overlay to the most recent commit that touched any line in
		// the block.
		var selectedHash string

		var latestTimestamp int64

		for i := lineRange.StartLine; i <= endLine; i++ {
			entry := blame.Entries[i-1]
			if entry.Timestamp > latestTimestamp {
				latestTimestamp = entry.Timestamp
				selectedHash = entry.CommitHash
			}
		}

		commitOverlays[selectedHash] = append(commitOverlays[selectedHash], overlays[lineRange.Index])
	}

	// Build groups with full commit metadata from the project repository.
	commitCache := make(map[string]*CommitMetadata)

	groups := make([]OverlayCommitGroup, 0, len(commitOverlays))

	for hash, overlayList := range commitOverlays {
		meta, err := resolveCommitMetadata(repo, hash, commitCache)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve commit metadata for %#q:\n%w", hash, err)
		}

		groups = append(groups, OverlayCommitGroup{
			Commit:   *meta,
			Overlays: overlayList,
		})
	}

	// Sort groups chronologically so synthetic commits preserve temporal ordering.
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Commit.Timestamp < groups[j].Commit.Timestamp
	})

	return groups, nil
}

// CommitSyntheticHistory creates synthetic commits in the provided git repository, one per
// [OverlayCommitGroup]. For each group the applyFn callback is invoked to mutate the working
// tree, then all changes are staged and committed with the group's metadata.
func CommitSyntheticHistory(
	repo *gogit.Repository,
	groups []OverlayCommitGroup,
	applyFn OverlayApplyFunc,
) error {
	if len(groups) == 0 {
		return ErrNoOverlaysToCommit
	}

	if applyFn == nil {
		return errors.New("applyFn callback is required")
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree:\n%w", err)
	}

	for groupIdx, group := range groups {
		slog.Info("Creating synthetic commit",
			"commit", groupIdx+1,
			"total", len(groups),
			"originalHash", group.Commit.Hash,
			"overlayCount", len(group.Overlays),
		)

		// Apply the overlay batch to the working tree.
		if err := applyFn(group.Overlays); err != nil {
			return fmt.Errorf("failed to apply overlays for synthetic commit %d (original %s):\n%w",
				groupIdx+1, group.Commit.Hash, err)
		}

		// Stage all changes (modified, added, and deleted files).
		if err := worktree.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
			return fmt.Errorf("failed to stage changes for synthetic commit %d:\n%w", groupIdx+1, err)
		}

		// Create the synthetic commit preserving author attribution from the project repo.
		message := fmt.Sprintf("[azldev] %s\n\nOriginal commit: %s",
			group.Commit.Message, group.Commit.Hash)

		_, err := worktree.Commit(message, &gogit.CommitOptions{
			Author: &object.Signature{
				Name:  group.Commit.Author,
				Email: group.Commit.AuthorEmail,
				When:  unixToTime(group.Commit.Timestamp),
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create synthetic commit %d:\n%w", groupIdx+1, err)
		}
	}

	slog.Info("Synthetic history generation complete",
		"commitsCreated", len(groups))

	return nil
}

// buildOverlayGroups resolves the project repository from the component's config file, blames
// the config to attribute lines to commits, and maps overlays to [OverlayCommitGroup] values
// sorted chronologically. Returns nil groups when overlay line ranges cannot be located.
func buildOverlayGroups(
	fs opctx.FS, config *projectconfig.ComponentConfig, componentName string,
) ([]OverlayCommitGroup, error) {
	configFilePath, err := resolveConfigFilePath(config, componentName)
	if err != nil {
		return nil, err
	}

	projectRepo, relConfigPath, err := openProjectRepo(configFilePath)
	if err != nil {
		return nil, err
	}

	blame, err := BlameFile(projectRepo, relConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to blame config file %#q:\n%w", relConfigPath, err)
	}

	configContent, err := fileutils.ReadFile(fs, configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %#q:\n%w", configFilePath, err)
	}

	lineRanges := FindOverlayLineRanges(string(configContent), config.Name)
	if len(lineRanges) == 0 {
		slog.Warn("Could not locate overlay definitions in config file; "+
			"falling back to standard overlay processing",
			"component", componentName, "configFile", configFilePath)

		return nil, nil
	}

	return MapOverlaysToCommits(projectRepo, config.Overlays, lineRanges, blame)
}

// resolveConfigFilePath extracts and validates the source config file path from the component config.
func resolveConfigFilePath(config *projectconfig.ComponentConfig, componentName string) (string, error) {
	configFile := config.SourceConfigFile
	if configFile == nil {
		return "", fmt.Errorf("component %#q has no source config file reference", componentName)
	}

	configFilePath := configFile.SourcePath()
	if configFilePath == "" {
		return "", fmt.Errorf("component %#q source config file has no path", componentName)
	}

	return configFilePath, nil
}

// openProjectRepo finds the git repository root containing configFilePath, opens it, and
// returns the repository handle along with the config file path relative to the repo root.
func openProjectRepo(configFilePath string) (*gogit.Repository, string, error) {
	projectRepoPath, err := findRepoRoot(filepath.Dir(configFilePath))
	if err != nil {
		return nil, "", fmt.Errorf("failed to find project repository for config file %#q:\n%w",
			configFilePath, err)
	}

	projectRepo, err := gogit.PlainOpen(projectRepoPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open project repository at %#q:\n%w", projectRepoPath, err)
	}

	relConfigPath, err := filepath.Rel(projectRepoPath, configFilePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to compute relative config path:\n%w", err)
	}

	return projectRepo, relConfigPath, nil
}

// resolveCommitMetadata retrieves full commit metadata from the repository, using a cache
// to avoid redundant lookups for the same commit hash.
func resolveCommitMetadata(
	repo *gogit.Repository,
	hash string,
	cache map[string]*CommitMetadata,
) (*CommitMetadata, error) {
	if meta, ok := cache[hash]; ok {
		return meta, nil
	}

	commitHash := plumbing.NewHash(hash)

	commit, err := repo.CommitObject(commitHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit %#q:\n%w", hash, err)
	}

	meta := &CommitMetadata{
		Hash:        hash,
		Author:      commit.Author.Name,
		AuthorEmail: commit.Author.Email,
		Timestamp:   commit.Author.When.Unix(),
		Message:     strings.TrimSpace(commit.Message),
	}

	cache[hash] = meta

	return meta, nil
}

// findRepoRoot walks up the directory tree from startDir to find a directory containing
// a .git directory or file (for worktrees).
func findRepoRoot(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path for %#q:\n%w", startDir, err)
	}

	for {
		gitPath := filepath.Join(dir, ".git")

		if info, statErr := os.Stat(gitPath); statErr == nil {
			// Accept both .git directories and .git files (for git worktrees).
			if info.IsDir() || info.Mode().IsRegular() {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%w: searched from %#q to filesystem root", ErrNoGitRepository, startDir)
		}

		dir = parent
	}
}

// unixToTime converts a Unix timestamp to a [time.Time] in UTC.
func unixToTime(unix int64) time.Time {
	return time.Unix(unix, 0).UTC()
}
