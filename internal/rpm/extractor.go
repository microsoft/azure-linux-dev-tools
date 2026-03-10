// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:generate go tool -modfile=../../tools/mockgen/go.mod mockgen -source=extractor.go -destination=rpm_test/extractor_mocks.go -package=rpm_test --copyright_file=../../.license-preamble

package rpm

import (
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cavaliergopher/cpio"
	rpmlib "github.com/cavaliergopher/rpm"
	"github.com/klauspost/compress/zstd"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/defers"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/ulikunitz/xz"
	"github.com/ulikunitz/xz/lzma"
)

// RPMExtractor interface defines methods for extracting RPM packages.
type RPMExtractor interface {
	// Extract extracts the contents of an RPM to the specified destination directory
	Extract(rpmStream io.Reader, destDir string) error
}

// RPMExtractorImpl implements [RPMExtractor].
type RPMExtractorImpl struct {
	fs       opctx.FS
	fsLinker afero.Linker
}

// Ensure RPMExtractorImpl implements [RPMExtractor].
var _ RPMExtractor = (*RPMExtractorImpl)(nil)

// NewRPMExtractorImpl creates a new RPMExtractor instance with the provided file system.
func NewRPMExtractorImpl(fs opctx.FS) (RPMExtractor, error) {
	if fs == nil {
		return nil, errors.New("file system cannot be nil")
	}

	linker, _ := fs.(afero.Linker)

	return &RPMExtractorImpl{
		fs:       fs,
		fsLinker: linker,
	}, nil
}

// Extract extracts the contents of an RPM file to the specified destination directory
// If destDir is empty, it will extract to the current directory.
func (e *RPMExtractorImpl) Extract(rpmStream io.Reader, destinationPath string) (err error) {
	if destinationPath == "" {
		return errors.New("destination path cannot be empty")
	}

	if rpmStream == nil {
		return errors.New("RPM file stream cannot be nil")
	}

	slog.Debug("Extracting RPM file",
		"destination", destinationPath,
	)

	// Get package information from the RPM.
	// When [rpmlib.Read] is done, the stream is at the beginning of the payload,
	// thus we can pass it directly to [getDecompressor].
	pkg, err := rpmlib.Read(rpmStream)
	if err != nil {
		return fmt.Errorf("failed to read RPM headers:\n%w", err)
	}

	payloadFormat := pkg.PayloadFormat()
	if payloadFormat != "cpio" {
		return fmt.Errorf("unsupported payload format %#q", payloadFormat)
	}

	payloadReader, err := getDecompressor(pkg.PayloadCompression(), rpmStream)
	if err != nil {
		return fmt.Errorf("failed to create decompression reader:\n%w", err)
	}
	defer defers.HandleDeferError(payloadReader.Close, &err)

	err = e.extractArchiveContents(payloadReader, destinationPath)
	if err != nil {
		return fmt.Errorf("failed to extract RPM contents:\n%w", err)
	}

	return nil
}

func (e *RPMExtractorImpl) extractArchiveContents(payloadReader io.Reader, destinationPath string) error {
	cpioReader := cpio.NewReader(payloadReader)

	for {
		hdr, err := cpioReader.Next()
		if errors.Is(err, io.EOF) {
			break // No more files
		}

		if err != nil {
			return fmt.Errorf("failed reading cpio header:\n%w", err)
		}

		if !e.isSupportedFileType(hdr) {
			slog.Warn("Skipping unsupported file type in RPM",
				"file", hdr.Name)

			continue
		}

		slog.Debug("Extracting file from RPM",
			"file", hdr.Name,
			"size", hdr.Size,
			"mode", hdr.FileInfo().Mode())

		targetPath := filepath.Join(destinationPath, hdr.Name)
		if hdr.Mode.IsDir() {
			err = e.fs.MkdirAll(targetPath, hdr.FileInfo().Mode())
		} else {
			err = e.extractNonDir(hdr, targetPath, cpioReader)
		}

		if err != nil {
			return fmt.Errorf("failed to extract %#q from RPM:\n%w", hdr.Name, err)
		}
	}

	return nil
}

func (e *RPMExtractorImpl) extractNonDir(hdr *cpio.Header, targetPath string, cpioReader *cpio.Reader) error {
	dirName := filepath.Dir(targetPath)

	err := fileutils.MkdirAll(e.fs, dirName)
	if err != nil {
		return fmt.Errorf("failed to create target file directory %#q:\n%w", dirName, err)
	}

	if hdr.Mode.IsRegular() {
		err := e.extractRegularFile(hdr, targetPath, cpioReader)
		if err != nil {
			return fmt.Errorf("failed to extract regular file %#q:\n%w", hdr.Name, err)
		}
	} else {
		err := e.fsLinker.SymlinkIfPossible(hdr.Linkname, targetPath)
		if err != nil {
			return fmt.Errorf("failed to create symlink %#q for %#q:\n%w", hdr.Linkname, targetPath, err)
		}
	}

	return nil
}

func (e *RPMExtractorImpl) extractRegularFile(
	hdr *cpio.Header, targetPath string, cpioReader *cpio.Reader,
) (err error) {
	outFile, err := e.fs.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, hdr.FileInfo().Mode())
	if err != nil {
		return fmt.Errorf("failed to create file %#q:\n%w", targetPath, err)
	}
	defer defers.HandleDeferError(outFile.Close, &err)

	if _, err := io.Copy(outFile, cpioReader); err != nil {
		return fmt.Errorf("failed to write file %#q:\n%w", targetPath, err)
	}

	return nil
}

func getDecompressor(compression string, fileReader io.Reader) (io.ReadCloser, error) {
	switch compression {
	case "bzip2", "bz2":
		return io.NopCloser(bzip2.NewReader(fileReader)), nil
	case "gzip", "gz":
		gzipReader, err := gzip.NewReader(fileReader)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader:\n%w", err)
		}

		return gzipReader, nil
	case "lzma":
		lzmaReader, err := lzma.NewReader(fileReader)
		if err != nil {
			return nil, fmt.Errorf("failed to create lzma reader:\n%w", err)
		}

		return io.NopCloser(lzmaReader), nil
	case "xz":
		xzReader, err := xz.NewReader(fileReader)
		if err != nil {
			return nil, fmt.Errorf("failed to create xz reader:\n%w", err)
		}

		return io.NopCloser(xzReader), nil
	case "zstd":
		zstdReader, err := zstd.NewReader(fileReader)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd reader:\n%w", err)
		}

		return zstdReader.IOReadCloser(), nil
	}

	return nil, fmt.Errorf("unsupported compression %#q", compression)
}

// isSupportedFileType checks if the file type is supported for extraction.
// Currently we support regular files and directories.
// Symbolic links are also supported if the filesystem supports linking.
func (e *RPMExtractorImpl) isSupportedFileType(hdr *cpio.Header) bool {
	linksSupported := e.fsLinker != nil
	fileIsLink := hdr.FileInfo().Mode()&os.ModeSymlink != 0

	return hdr.Mode.IsRegular() ||
		hdr.Mode.IsDir() ||
		(linksSupported && fileIsLink)
}
