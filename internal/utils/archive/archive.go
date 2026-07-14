// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package archive provides on-disk tar extraction and deterministic archive
// creation.
//
// [Extract] materializes entries into a destination directory via [os.Root],
// which confines every file, directory, and symlink operation to that
// directory — any entry path that would escape destDir is rejected by the
// runtime. Symlink *targets* (the contents of a symlink, not its own path)
// are not policed by [os.Root]; this package additionally rejects any symlink
// whose target is non-local via [filepath.IsLocal].
//
// Only regular files, directories, and symlinks are extracted. Other entry
// types (hardlinks, devices, FIFOs, etc.) are skipped by default, or cause a
// failure when [WithErrorOnUnsupportedEntry] is set — callers that repack the
// tree must set it so such entries aren't silently dropped.
//
// Archive creation is designed for reproducible builds: file ordering is
// lexicographic, timestamps are pinned to Unix epoch, and owner/group metadata
// is zeroed out. This matches the
// `tar --sort=name --mtime=@0 --owner=0 --group=0` convention used by source
// modification scripts.
package archive

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/ulikunitz/xz"
)

// Compression identifies the compression format of an archive.
type Compression int

const (
	// CompressionNone indicates an uncompressed .tar archive.
	CompressionNone Compression = iota
	// CompressionGzip indicates gzip compression (.tar.gz or .tgz).
	CompressionGzip
	// CompressionXZ indicates xz compression (.tar.xz or .txz).
	CompressionXZ
	// CompressionZstd indicates zstandard compression (.tar.zst or .tzst).
	CompressionZstd
)

// compressionMagicLen is the number of leading bytes [sniffCompression] needs
// to recognize every supported compressed format (xz has the longest, 6-byte,
// signature).
const compressionMagicLen = 6

// maxEntryBytes caps the decompressed size of any single regular-file entry
// extracted by [Extract]. This prevents a decompression-bomb archive from
// filling the destination filesystem. 10 GiB is well above any reasonable
// source archive entry but small enough to refuse pathological inputs.
//
// Declared as var rather than const so internal tests can override it
// without having to construct a >10 GiB fixture.
//
//nolint:gochecknoglobals // see comment above
var maxEntryBytes int64 = 10 << 30

// DetectCompression determines the compression type from the archive filename.
func DetectCompression(filename string) (Compression, error) {
	lower := strings.ToLower(filename)

	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return CompressionGzip, nil
	case strings.HasSuffix(lower, ".tar.xz") || strings.HasSuffix(lower, ".txz"):
		return CompressionXZ, nil
	case strings.HasSuffix(lower, ".tar.zst") || strings.HasSuffix(lower, ".tzst"):
		return CompressionZstd, nil
	case strings.HasSuffix(lower, ".tar"):
		return CompressionNone, nil
	default:
		return CompressionNone, fmt.Errorf("unsupported archive format %#q", filename)
	}
}

// IsArchiveName reports whether filename has a recognized archive extension.
// It is a convenience predicate over [DetectCompression] for classifying a path
// as an archive without needing the specific compression type.
func IsArchiveName(filename string) bool {
	_, err := DetectCompression(filename)

	return err == nil
}

// ExtractAuto is a convenience wrapper that infers the compression from
// archivePath's extension via [DetectCompression] and then calls [Extract].
// Most callers should prefer this over the explicit-compression [Extract],
// which exists for cases where the compression cannot be derived from the
// filename.
func ExtractAuto(archivePath, destDir string, opts ...ExtractOption) error {
	comp, err := DetectCompression(archivePath)
	if err != nil {
		return fmt.Errorf("detecting compression for %#q:\n%w", archivePath, err)
	}

	return Extract(archivePath, destDir, comp, opts...)
}

// ExtractOption configures the behavior of [Extract] and [ExtractAuto].
type ExtractOption func(*extractConfig)

// extractConfig holds the resolved options for an extraction.
type extractConfig struct {
	errorOnUnsupportedEntry bool
	directoryModes          map[string]os.FileMode
}

// WithErrorOnUnsupportedEntry makes [Extract] fail on tar entries it cannot
// materialize (anything but a regular file, directory, or symlink). Without it
// such entries are skipped; callers that repack the tree should set it so the
// entries aren't silently dropped from the rebuilt archive.
func WithErrorOnUnsupportedEntry() ExtractOption {
	return func(c *extractConfig) {
		c.errorOnUnsupportedEntry = true
	}
}

// Extract reads a tar archive, decompresses it, and extracts all entries into
// destDir. Entry paths are confined to destDir via [os.Root]: any path that
// would escape destDir is rejected by the runtime. Symlink targets are
// validated separately by this package — see the package doc for details.
//
// Only regular files, directories, and symlinks are supported. Other entry
// types are skipped by default, or fail when [WithErrorOnUnsupportedEntry] is
// set (required by callers that repack the tree).
func Extract(archivePath, destDir string, comp Compression, opts ...ExtractOption) (err error) {
	cfg := extractConfig{directoryModes: make(map[string]os.FileMode)}
	for _, opt := range opts {
		opt(&cfg)
	}

	if comp < CompressionNone || comp > CompressionZstd {
		return fmt.Errorf("unsupported compression type %d", comp)
	}

	if err := os.MkdirAll(destDir, fileperms.PublicDir); err != nil {
		return fmt.Errorf("creating destination %#q:\n%w", destDir, err)
	}

	root, err := os.OpenRoot(destDir)
	if err != nil {
		return fmt.Errorf("opening destination root %#q:\n%w", destDir, err)
	}
	defer defers.HandleDeferError(root.Close, &err)

	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening archive %#q:\n%w", archivePath, err)
	}
	defer defers.HandleDeferError(file.Close, &err)

	// Prefer the actual compression detected from the leading magic bytes over
	// the caller-supplied (extension-derived) value: upstream archives are
	// sometimes mislabeled, e.g. an uncompressed tar published as ".txz".
	bufReader := bufio.NewReader(file)
	magic, _ := bufReader.Peek(compressionMagicLen)

	decompressed, closer, err := newDecompressor(bufReader, sniffCompression(magic, comp))
	if err != nil {
		return err
	}

	if closer != nil {
		defer defers.HandleDeferError(closer.Close, &err)
	}

	tarReader := tar.NewReader(decompressed)

	for {
		header, readErr := tarReader.Next()
		if errors.Is(readErr, io.EOF) {
			if err := restoreDirectoryModes(root, cfg.directoryModes); err != nil {
				return fmt.Errorf("restoring extracted directory permissions:\n%w", err)
			}

			return nil
		}

		if readErr != nil {
			return fmt.Errorf("reading tar entry from %#q:\n%w", archivePath, readErr)
		}

		if err := extractEntry(root, header, tarReader, cfg); err != nil {
			return fmt.Errorf("extracting %#q from %#q:\n%w", header.Name, archivePath, err)
		}
	}
}

// sniffCompression returns the compression format implied by the leading magic
// bytes, falling back to fallback when no known signature matches (an
// uncompressed tar carries no leading magic). All supported compressed formats
// have a fixed-position header, so this is authoritative over the filename
// extension.
func sniffCompression(magic []byte, fallback Compression) Compression {
	switch {
	case bytes.HasPrefix(magic, []byte{0xFD, '7', 'z', 'X', 'Z', 0x00}):
		return CompressionXZ
	case bytes.HasPrefix(magic, []byte{0x1F, 0x8B}):
		return CompressionGzip
	case bytes.HasPrefix(magic, []byte{0x28, 0xB5, 0x2F, 0xFD}):
		return CompressionZstd
	case len(magic) == 0:
		// Unreadable/empty peek: defer to the extension-derived hint.
		return fallback
	default:
		// No compression signature: an uncompressed tar (whatever the
		// extension claims). A real tar's "ustar" magic sits at byte 257.
		return CompressionNone
	}
}

// SniffCompressionFromFile determines an existing archive's compression by
// inspecting its leading magic bytes — authoritative over the filename
// extension.
func SniffCompressionFromFile(archivePath string, fallback Compression) (comp Compression, err error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return fallback, fmt.Errorf("opening %#q for compression sniffing:\n%w", archivePath, err)
	}
	defer defers.HandleDeferError(file.Close, &err)

	magic := make([]byte, compressionMagicLen)

	bytesRead, readErr := io.ReadFull(file, magic)
	// Short files are fine — a tiny tar may be under compressionMagicLen bytes.
	// Only a genuine read error (not EOF/short read) is fatal.
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return fallback, fmt.Errorf("reading %#q header:\n%w", archivePath, readErr)
	}

	return sniffCompression(magic[:bytesRead], fallback), nil
}

// newDecompressor wraps reader in the chosen decompressor. For
// [CompressionNone] the returned closer is nil; otherwise it is the
// decompressor itself.
func newDecompressor(reader io.Reader, comp Compression) (io.Reader, io.Closer, error) {
	switch comp {
	case CompressionNone:
		return reader, nil, nil
	case CompressionGzip:
		gzReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("creating gzip reader:\n%w", err)
		}

		return gzReader, gzReader, nil
	case CompressionXZ:
		xzReader, err := xz.NewReader(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("creating xz reader:\n%w", err)
		}

		return xzReader, nil, nil
	case CompressionZstd:
		zstdReader, err := zstd.NewReader(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("creating zstd reader:\n%w", err)
		}

		return zstdReader, readerCloser{zstdReader.Close}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported compression type %d", comp)
	}
}

// readerCloser adapts a no-error Close (such as [zstd.Decoder.Close]) to
// [io.Closer].
type readerCloser struct {
	close func()
}

func (r readerCloser) Close() error {
	r.close()

	return nil
}

// CreateDeterministicArchive creates a new tar archive from the contents of sourceDir
// and writes it to archivePath on the OS filesystem, replacing any existing file.
//
// The output is deterministic:
//   - File ordering is lexicographic (via [filepath.WalkDir]).
//   - Timestamps are pinned to Unix epoch (1970-01-01 00:00:00 UTC).
//   - Owner/group IDs and names are zeroed out.
//   - Gzip output uses best compression with no OS or filename metadata.
//
// Symlink targets are recorded verbatim, including absolute or
// directory-escaping targets. [Extract] rejects non-local symlink targets
// (via [filepath.IsLocal]), so an archive produced here is not guaranteed to
// be extractable by [Extract] when the source tree contains such symlinks.
func CreateDeterministicArchive(archivePath, sourceDir string, comp Compression) (err error) {
	file, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("opening archive for writing %#q:\n%w", archivePath, err)
	}
	defer defers.HandleDeferError(file.Close, &err)

	compressedWriter, compressedCloser, err := newCompressor(file, comp)
	if err != nil {
		return err
	}

	if compressedCloser != nil {
		defer defers.HandleDeferError(compressedCloser.Close, &err)
	}

	tarWriter := tar.NewWriter(compressedWriter)
	defer defers.HandleDeferError(tarWriter.Close, &err)

	epoch := deterministicEpoch()

	walkErr := filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, dirErr error) error {
		if dirErr != nil {
			return dirErr
		}

		rel, relErr := filepath.Rel(sourceDir, path)
		if relErr != nil {
			return fmt.Errorf("computing relative path for %#q:\n%w", path, relErr)
		}

		if rel == "." {
			return nil
		}

		return writeEntryDeterministic(tarWriter, path, rel, entry, epoch)
	})
	if walkErr != nil {
		return fmt.Errorf("walking directory for repacking:\n%w", walkErr)
	}

	return nil
}

func deterministicEpoch() time.Time {
	return time.Unix(0, 0).UTC()
}

func extractEntry(root *os.Root, header *tar.Header, tarReader io.Reader, cfg extractConfig) error {
	name := header.Name

	if header.Typeflag == tar.TypeXGlobalHeader || header.Typeflag == tar.TypeXHeader {
		return nil
	}

	if header.Typeflag == tar.TypeDir {
		// Create the directory with a traversable temporary mode. Its archived mode
		// is restored after all entries have been extracted, so a restrictive mode
		// cannot prevent later child entries from being materialized.
		directoryName := filepath.Clean(name)
		directoryMode := os.FileMode(header.Mode) & os.ModePerm //nolint:gosec // mask tar mode to permission bits

		if err := root.MkdirAll(directoryName, fileperms.PublicDir); err != nil {
			return fmt.Errorf("creating directory %#q:\n%w", name, err)
		}

		cfg.directoryModes[directoryName] = directoryMode

		return nil
	}

	if err := root.MkdirAll(filepath.Dir(name), fileperms.PublicDir); err != nil {
		return fmt.Errorf("creating parent for %#q:\n%w", name, err)
	}

	switch header.Typeflag {
	case tar.TypeSymlink:
		// Reject symlink targets that escape destDir. Absolute targets are
		// rejected outright; relative targets containing ".." are resolved from
		// the symlink's parent and allowed if the result stays within the root.
		if filepath.IsAbs(header.Linkname) {
			return fmt.Errorf("tar symlink %#q has absolute target %#q", name, header.Linkname)
		}

		resolved := filepath.Clean(filepath.Join(filepath.Dir(name), header.Linkname)) //nolint:gosec
		if !filepath.IsLocal(resolved) {
			return fmt.Errorf("tar symlink %#q resolves to non-local path %#q (target %#q)", name, resolved, header.Linkname)
		}

		if err := root.Symlink(header.Linkname, name); err != nil {
			return fmt.Errorf("creating symlink %#q -> %#q:\n%w", name, header.Linkname, err)
		}

		return nil
	case tar.TypeReg:
		return extractRegularFile(root, header, tarReader)
	default:
		// Unsupported entry type (hardlink, device, FIFO, ...): fail when the
		// caller is strict (repacking would drop it), otherwise skip and log.
		if cfg.errorOnUnsupportedEntry {
			return fmt.Errorf(
				"tar entry %#q has unsupported type (typeflag %d); only regular files, directories, and symlinks are supported",
				name, header.Typeflag)
		}

		slog.Debug("Skipping unsupported tar entry type", "name", name, "typeflag", header.Typeflag)

		return nil
	}
}

// restoreDirectoryModes applies archive directory modes after all content has
// been extracted. Deepest paths are restored first so a restrictive parent mode
// cannot prevent reaching an explicit child directory.
func restoreDirectoryModes(root *os.Root, directoryModes map[string]os.FileMode) error {
	paths := make([]string, 0, len(directoryModes))
	for path := range directoryModes {
		paths = append(paths, path)
	}

	sort.Slice(paths, func(i, j int) bool {
		return len(paths[i]) > len(paths[j])
	})

	for _, path := range paths {
		if err := root.Chmod(path, directoryModes[path]); err != nil {
			return fmt.Errorf("setting permissions on directory %#q:\n%w", path, err)
		}
	}

	return nil
}

func extractRegularFile(root *os.Root, header *tar.Header, src io.Reader) (err error) {
	name := header.Name

	// gosec G115: header.Mode is the tar permission bits; mask to ModePerm
	// (the bottom 9 bits) before passing to OpenFile.
	mode := os.FileMode(header.Mode) & os.ModePerm //nolint:gosec

	// Reject up front if the declared header size is over the limit so we never
	// open a destination file for a known-bad entry.
	if header.Size > maxEntryBytes {
		return fmt.Errorf("tar entry %#q declares size %d bytes, exceeds max of %d", name, header.Size, maxEntryBytes)
	}

	outFile, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("creating file %#q:\n%w", name, err)
	}
	defer defers.HandleDeferError(outFile.Close, &err)

	// Remove any partially written output on error so callers don't see a
	// truncated file masquerading as a successful extraction.
	defer func() {
		if err != nil {
			_ = root.Remove(name)
		}
	}()

	// Copy exactly header.Size bytes. CopyN returns a non-nil error (including
	// io.EOF on a truncated archive) whenever it cannot satisfy the full count,
	// so any error here means the entry is short or unreadable.
	if _, copyErr := io.CopyN(outFile, src, header.Size); copyErr != nil {
		return fmt.Errorf("writing file %#q:\n%w", name, copyErr)
	}

	return nil
}

func writeEntryDeterministic(
	tarWriter *tar.Writer, path, rel string, entry os.DirEntry, epoch time.Time,
) (err error) {
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("stat %#q:\n%w", path, err)
	}

	var linkTarget string
	if info.Mode()&os.ModeSymlink != 0 {
		linkTarget, err = os.Readlink(path)
		if err != nil {
			return fmt.Errorf("reading symlink %#q:\n%w", path, err)
		}
	}

	header, err := tar.FileInfoHeader(info, linkTarget)
	if err != nil {
		return fmt.Errorf("creating tar header for %#q:\n%w", path, err)
	}

	header.Name = filepath.ToSlash(rel)
	if info.IsDir() {
		// tar convention: directory entry names end with '/'. tar.FileInfoHeader
		// applies this, but our rel-based override above drops it.
		header.Name += "/"
	}

	header.Format = tar.FormatGNU
	header.ModTime = epoch
	header.AccessTime = time.Time{}
	header.ChangeTime = time.Time{}
	header.Uid = 0
	header.Gid = 0
	header.Uname = ""
	header.Gname = ""

	if err := tarWriter.WriteHeader(header); err != nil {
		return fmt.Errorf("writing tar header for %#q:\n%w", path, err)
	}

	if !info.Mode().IsRegular() {
		return nil
	}

	sourceFile, openErr := os.Open(path)
	if openErr != nil {
		return fmt.Errorf("opening %#q for repack:\n%w", path, openErr)
	}
	defer defers.HandleDeferError(sourceFile.Close, &err)

	// Copy exactly header.Size bytes so the archive entry matches the header.
	// Using io.Copy would write whatever the file contains at read time, which
	// may exceed header.Size if the file grew between stat and open, causing
	// tar.Writer to error mid-stream with a partially written entry.
	if _, copyErr := io.CopyN(tarWriter, sourceFile, header.Size); copyErr != nil {
		return fmt.Errorf("writing %#q to archive:\n%w", path, copyErr)
	}

	return nil
}

// newCompressor wraps writer in the chosen compression. For [CompressionNone]
// the returned closer is nil (no wrapper to flush or close); otherwise it is
// the compressor itself, which must be closed before the underlying writer to
// flush trailing bytes.
func newCompressor(writer io.Writer, comp Compression) (io.Writer, io.Closer, error) {
	switch comp {
	case CompressionNone:
		return writer, nil, nil
	case CompressionGzip:
		gzWriter, gzErr := gzip.NewWriterLevel(writer, gzip.BestCompression)
		if gzErr != nil {
			return nil, nil, fmt.Errorf("creating gzip writer:\n%w", gzErr)
		}

		// Pin every gzip header field that the writer would otherwise populate
		// from the environment or input file, so two runs over identical inputs
		// produce byte-identical output. ModTime matches the tar header epoch
		// for consistency; OS is "unknown" (RFC 1952 §2.3.1) so output is
		// independent of the host OS.
		const gzipOSUnknown byte = 0xff

		gzWriter.Header = gzip.Header{
			Name:    "",
			Comment: "",
			Extra:   nil,
			ModTime: deterministicEpoch(),
			OS:      gzipOSUnknown,
		}

		return gzWriter, gzWriter, nil
	case CompressionXZ:
		xzWriter, err := xz.NewWriter(writer)
		if err != nil {
			return nil, nil, fmt.Errorf("creating xz writer:\n%w", err)
		}

		return xzWriter, xzWriter, nil
	case CompressionZstd:
		zstdWriter, err := zstd.NewWriter(writer)
		if err != nil {
			return nil, nil, fmt.Errorf("creating zstd writer:\n%w", err)
		}

		return zstdWriter, zstdWriter, nil
	default:
		return nil, nil, fmt.Errorf("unsupported compression type %d for writing", comp)
	}
}
