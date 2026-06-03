// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package tarball provides on-disk tar extraction and deterministic archive
// creation.
//
// [Extract] materializes entries into a destination directory via [os.Root],
// which confines every file, directory, and symlink operation to that
// directory — any entry name or symlink target that would escape destDir is
// rejected by the runtime.
//
// Archive creation is designed for reproducible builds: file ordering is
// lexicographic, timestamps are pinned to Unix epoch, and owner/group metadata
// is zeroed out. This matches the
// `tar --sort=name --mtime=@0 --owner=0 --group=0` convention used by source
// modification scripts.
package tarball

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/ulikunitz/xz"
)

// Compression identifies the compression format of a tarball.
type Compression int

const (
	// CompressionNone indicates an uncompressed .tar archive.
	CompressionNone Compression = iota
	// CompressionGzip indicates gzip compression (.tar.gz or .tgz).
	CompressionGzip
	// CompressionXZ indicates xz compression (.tar.xz).
	CompressionXZ
	// CompressionZstd indicates zstandard compression (.tar.zst).
	CompressionZstd
)

// maxEntryBytes caps the decompressed size of any single regular-file entry
// extracted by [Extract]. This prevents a decompression-bomb archive from
// filling the destination filesystem. 10 GiB is well above any reasonable
// source tarball entry but small enough to refuse pathological inputs.
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
	case strings.HasSuffix(lower, ".tar.xz"):
		return CompressionXZ, nil
	case strings.HasSuffix(lower, ".tar.zst"):
		return CompressionZstd, nil
	case strings.HasSuffix(lower, ".tar"):
		return CompressionNone, nil
	default:
		return CompressionNone, fmt.Errorf("unsupported archive format %#q", filename)
	}
}

// Extract reads a tar archive, decompresses it, and extracts all entries into
// destDir. Supported entry types are regular files, directories, and symlinks;
// other entry types are skipped. Extraction is confined to destDir via [os.Root]:
// any entry name or symlink target that would escape destDir is rejected by
// the runtime.
func Extract(archivePath, destDir string, comp Compression) (err error) {
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

	decompressed, closer, err := newDecompressor(file, comp)
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
			return nil
		}

		if readErr != nil {
			return fmt.Errorf("reading tar entry from %#q:\n%w", archivePath, readErr)
		}

		if err := extractEntry(root, header, tarReader); err != nil {
			return fmt.Errorf("extracting %#q from %#q:\n%w", header.Name, archivePath, err)
		}
	}
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

func extractEntry(root *os.Root, header *tar.Header, tarReader io.Reader) error {
	name := header.Name

	if header.Typeflag == tar.TypeDir {
		if err := root.MkdirAll(name, fileperms.PublicDir); err != nil {
			return fmt.Errorf("creating directory %#q:\n%w", name, err)
		}

		return nil
	}

	if err := root.MkdirAll(filepath.Dir(name), fileperms.PublicDir); err != nil {
		return fmt.Errorf("creating parent for %#q:\n%w", name, err)
	}

	switch header.Typeflag {
	case tar.TypeSymlink:
		// os.Root validates that the link's own path stays inside the root, but
		// it stores the target verbatim. Reject non-local targets so that tools
		// which later walk the extracted tree without os.Root cannot be led
		// outside destDir.
		if !filepath.IsLocal(header.Linkname) {
			return fmt.Errorf("tar symlink %#q has non-local target %#q", name, header.Linkname)
		}

		if err := root.Symlink(header.Linkname, name); err != nil {
			return fmt.Errorf("creating symlink %#q -> %#q:\n%w", name, header.Linkname, err)
		}

		return nil
	case tar.TypeReg:
		return extractRegularFile(root, header, tarReader)
	default:
		slog.Debug("Skipping unsupported tar entry type", "name", name, "typeflag", header.Typeflag)

		return nil
	}
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

	// Cap copy at maxEntryBytes+1 so we can still detect (and reject) entries
	// whose declared size lied. The +1 byte is bounded and is cleaned up by
	// the deferred Remove above.
	written, copyErr := io.CopyN(outFile, src, maxEntryBytes+1)
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		return fmt.Errorf("writing file %#q:\n%w", name, copyErr)
	}

	if written > maxEntryBytes {
		return fmt.Errorf("tar entry %#q exceeds max size of %d bytes", name, maxEntryBytes)
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

	if _, copyErr := io.Copy(tarWriter, sourceFile); copyErr != nil {
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
