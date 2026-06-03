// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package tarball provides deterministic tar archive extraction and repacking.
//
// Repacking is designed for reproducible builds: file ordering is lexicographic,
// timestamps are pinned to Unix epoch, and owner/group metadata is zeroed out.
// This matches the `tar --sort=name --mtime=@0 --owner=0 --group=0` convention
// used by source modification scripts.
package tarball

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
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
	// CompressionBzip2 indicates bzip2 compression (.tar.bz2).
	CompressionBzip2
	// CompressionXZ indicates xz compression (.tar.xz).
	CompressionXZ
	// CompressionZstd indicates zstandard compression (.tar.zst).
	CompressionZstd
)

// DetectCompression determines the compression type from the archive filename.
func DetectCompression(filename string) (Compression, error) {
	lower := strings.ToLower(filename)

	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return CompressionGzip, nil
	case strings.HasSuffix(lower, ".tar.bz2"):
		return CompressionBzip2, nil
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

// Extract reads a tar archive from the filesystem, decompresses it, and extracts
// all entries to destDir. Supported entry types are regular files, directories,
// and symlinks. Path traversal entries are rejected.
func Extract(fs opctx.FS, archivePath, destDir string, comp Compression) (err error) {
	file, err := fs.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening archive %#q:\n%w", archivePath, err)
	}
	defer defers.HandleDeferError(file.Close, &err)

	decompressed, err := newDecompressor(file, comp)
	if err != nil {
		return err
	}

	if closer, ok := decompressed.(io.Closer); ok {
		if closer != io.Closer(file) {
			defer defers.HandleDeferError(closer.Close, &err)
		}
	}

	tarReader := tar.NewReader(decompressed)

	for {
		header, readErr := tarReader.Next()
		if readErr == io.EOF {
			break
		}

		if readErr != nil {
			return fmt.Errorf("reading tar header:\n%w", readErr)
		}

		if err := extractEntry(destDir, header, tarReader); err != nil {
			return err
		}
	}

	return nil
}

// RepackDeterministic creates a new tar archive from the contents of sourceDir
// and writes it to archivePath, replacing any existing file.
//
// The output is deterministic:
//   - File ordering is lexicographic (via [filepath.WalkDir]).
//   - Timestamps are pinned to Unix epoch (1970-01-01 00:00:00 UTC).
//   - Owner/group IDs and names are zeroed out.
//   - Gzip output uses best compression with no OS or filename metadata.
func RepackDeterministic(fs opctx.FS, archivePath, sourceDir string, comp Compression) (err error) {
	file, err := fs.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileperms.PublicFile)
	if err != nil {
		return fmt.Errorf("opening archive for writing %#q:\n%w", archivePath, err)
	}
	defer defers.HandleDeferError(file.Close, &err)

	compressedWriter, err := newCompressor(file, comp)
	if err != nil {
		return err
	}

	if closer, ok := compressedWriter.(io.Closer); ok {
		if closer != io.Closer(file) {
			defer defers.HandleDeferError(closer.Close, &err)
		}
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

// ResolveExtractRoot determines the root directory of extracted tarball content.
func ResolveExtractRoot(workDir string) (string, error) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return "", fmt.Errorf("reading extracted directory:\n%w", err)
	}

	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(workDir, entries[0].Name()), nil
	}

	return workDir, nil
}

func deterministicEpoch() time.Time {
	return time.Unix(0, 0).UTC()
}

func extractEntry(destDir string, header *tar.Header, tarReader *tar.Reader) error {
	cleanName := filepath.Clean(header.Name)
	targetPath := filepath.Join(destDir, cleanName)
	cleanTarget := filepath.Clean(targetPath)
	cleanDest := filepath.Clean(destDir)

	if !strings.HasPrefix(cleanTarget, cleanDest+string(os.PathSeparator)) && cleanTarget != cleanDest {
		return fmt.Errorf("tar entry %#q escapes destination directory", header.Name)
	}

	switch header.Typeflag {
	case tar.TypeDir:
		if err := os.MkdirAll(targetPath, fileperms.PublicDir); err != nil {
			return fmt.Errorf("creating directory %#q:\n%w", targetPath, err)
		}
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(targetPath), fileperms.PublicDir); err != nil {
			return fmt.Errorf("creating parent for %#q:\n%w", targetPath, err)
		}

		if err := extractRegularFile(targetPath, header, tarReader); err != nil {
			return err
		}
	case tar.TypeSymlink:
		if err := os.MkdirAll(filepath.Dir(targetPath), fileperms.PublicDir); err != nil {
			return fmt.Errorf("creating parent for symlink %#q:\n%w", targetPath, err)
		}

		if err := os.Symlink(header.Linkname, targetPath); err != nil {
			return fmt.Errorf("creating symlink %#q:\n%w", targetPath, err)
		}
	default:
		slog.Debug("Skipping unsupported tar entry type", "name", header.Name, "type", header.Typeflag)
	}

	return nil
}

func extractRegularFile(targetPath string, header *tar.Header, tarReader *tar.Reader) (err error) {
	mode := os.FileMode(header.Mode) & os.ModePerm //nolint:gosec // Truncation to permission bits is intentional.

	outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("creating file %#q:\n%w", targetPath, err)
	}
	defer defers.HandleDeferError(outFile.Close, &err)

	if _, err := io.Copy(outFile, tarReader); err != nil {
		return fmt.Errorf("writing file %#q:\n%w", targetPath, err)
	}

	return nil
}

func writeEntryDeterministic(
	tarWriter *tar.Writer, path, rel string, entry os.DirEntry, epoch time.Time,
) error {
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("stat %#q:\n%w", path, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		linkTarget, linkErr := os.Readlink(path)
		if linkErr != nil {
			return fmt.Errorf("reading symlink %#q:\n%w", path, linkErr)
		}

		header := &tar.Header{
			Typeflag: tar.TypeSymlink,
			Name:     rel,
			Linkname: linkTarget,
			ModTime:  epoch,
			Format:   tar.FormatGNU,
		}

		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("writing symlink header for %#q:\n%w", path, err)
		}

		return nil
	}

	header, headerErr := tar.FileInfoHeader(info, "")
	if headerErr != nil {
		return fmt.Errorf("creating tar header for %#q:\n%w", path, headerErr)
	}

	header.Name = rel
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
	defer sourceFile.Close()

	if _, copyErr := io.Copy(tarWriter, sourceFile); copyErr != nil {
		return fmt.Errorf("writing %#q to archive:\n%w", path, copyErr)
	}

	return nil
}

func newDecompressor(reader io.Reader, comp Compression) (io.Reader, error) {
	switch comp {
	case CompressionNone:
		return reader, nil
	case CompressionGzip:
		gzReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("creating gzip reader:\n%w", err)
		}

		return gzReader, nil
	case CompressionBzip2:
		return bzip2.NewReader(reader), nil
	case CompressionXZ:
		xzReader, err := xz.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("creating xz reader:\n%w", err)
		}

		return xzReader, nil
	case CompressionZstd:
		zstdReader, err := zstd.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("creating zstd reader:\n%w", err)
		}

		return zstdReader.IOReadCloser(), nil
	default:
		return nil, fmt.Errorf("unsupported compression type %d", comp)
	}
}

func newCompressor(writer io.Writer, comp Compression) (io.Writer, error) {
	switch comp {
	case CompressionNone:
		return writer, nil
	case CompressionGzip:
		gzWriter, gzErr := gzip.NewWriterLevel(writer, gzip.BestCompression)
		if gzErr != nil {
			return nil, fmt.Errorf("creating gzip writer:\n%w", gzErr)
		}

		gzWriter.OS = 0xff

		return gzWriter, nil
	case CompressionBzip2:
		slog.Warn("bzip2 compression not supported for repacking; output will be gzip-compressed")

		gzWriter, gzErr := gzip.NewWriterLevel(writer, gzip.BestCompression)
		if gzErr != nil {
			return nil, fmt.Errorf("creating gzip writer for bzip2 fallback:\n%w", gzErr)
		}

		gzWriter.OS = 0xff

		return gzWriter, nil
	case CompressionXZ:
		xzWriter, err := xz.NewWriter(writer)
		if err != nil {
			return nil, fmt.Errorf("creating xz writer:\n%w", err)
		}

		return xzWriter, nil
	case CompressionZstd:
		zstdWriter, err := zstd.NewWriter(writer)
		if err != nil {
			return nil, fmt.Errorf("creating zstd writer:\n%w", err)
		}

		return zstdWriter, nil
	default:
		return nil, fmt.Errorf("unsupported compression type %d for writing", comp)
	}
}
