// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package repocompare loads and compares RPM repository package inventories.
package repocompare

import (
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
	"github.com/ulikunitz/xz"
)

// Repository describes one physical RPM repository.
type Repository struct {
	ID               string
	Kind             projectconfig.SubrepoKind
	URL              string
	DisableSSLVerify bool
}

// Package identifies one package in RPM primary metadata.
type Package struct {
	Name    string
	Epoch   string
	Version string
	Release string
	Arch    string
	Kind    projectconfig.SubrepoKind
}

// Identity returns the stable key used to compare package inventories.
func (p Package) Identity() string {
	return strings.Join([]string{
		p.Name, normalizedEpoch(p.Epoch), p.Version, p.Release, p.Arch, string(p.Kind),
	}, "\x00")
}

// NEVRA returns the package's human-readable name, epoch, version, release, and architecture.
func (p Package) NEVRA() string {
	epoch := ""
	if normalizedEpoch(p.Epoch) != "0" {
		epoch = normalizedEpoch(p.Epoch) + ":"
	}

	return fmt.Sprintf("%s-%s%s-%s.%s", p.Name, epoch, p.Version, p.Release, p.Arch)
}

// NEVR returns the package's human-readable name, epoch, version, and release.
func (p Package) NEVR() string {
	epoch := ""
	if normalizedEpoch(p.Epoch) != "0" {
		epoch = normalizedEpoch(p.Epoch) + ":"
	}

	return fmt.Sprintf("%s-%s%s-%s", p.Name, epoch, p.Version, p.Release)
}

// EVR returns the package's epoch, version, and release without its architecture.
func (p Package) EVR() string {
	epoch := ""
	if normalizedEpoch(p.Epoch) != "0" {
		epoch = normalizedEpoch(p.Epoch) + ":"
	}

	return fmt.Sprintf("%s%s-%s", epoch, p.Version, p.Release)
}

// Fetcher retrieves repository metadata.
type Fetcher interface {
	// Fetch returns the bytes at rawURL.
	Fetch(ctx context.Context, rawURL string, disableSSLVerify bool) ([]byte, error)
}

// HTTPFetcher retrieves repository metadata over HTTP with bounded retries.
type HTTPFetcher struct {
	Attempts int
}

// Fetch implements [Fetcher].
func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL string, disableSSLVerify bool) ([]byte, error) {
	const requestTimeout = 10 * time.Minute

	attempts := f.Attempts
	if attempts < 1 {
		attempts = 1
	}

	var result []byte

	retryConfig := retry.DefaultConfig()
	retryConfig.MaxAttempts = attempts

	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("default HTTP transport is not an *http.Transport")
	}

	transport := defaultTransport.Clone()
	if disableSSLVerify {
		if transport.TLSClientConfig != nil {
			clone := transport.TLSClientConfig.Clone()
			clone.InsecureSkipVerify = true
			transport.TLSClientConfig = clone
		} else {
			transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit per-repo opt-out
		}
	}

	client := &http.Client{Transport: transport, Timeout: requestTimeout}

	err := retry.Do(ctx, retryConfig, func() error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return fmt.Errorf("creating request for %#q:\n%w", rawURL, err)
		}

		request.Header.Set("Accept-Encoding", "identity")

		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("fetching %#q:\n%w", rawURL, err)
		}
		defer response.Body.Close()

		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("fetching %#q returned status %#q", rawURL, response.Status)
		}

		result, err = io.ReadAll(response.Body)
		if err != nil {
			return fmt.Errorf("reading %#q:\n%w", rawURL, err)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("fetching repository metadata:\n%w", err)
	}

	return result, nil
}

type repoMD struct {
	Data []repoMDData `xml:"data"`
}

type repoMDData struct {
	Type     string      `xml:"type,attr"`
	Location xmlLocation `xml:"location"`
}

type xmlLocation struct {
	Href string `xml:"href,attr"`
}

type primaryPackage struct {
	Name    string `xml:"name"`
	Arch    string `xml:"arch"`
	Version struct {
		Epoch   string `xml:"epoch,attr"`
		Version string `xml:"ver,attr"`
		Release string `xml:"rel,attr"`
	} `xml:"version"`
}

type pendingRepository struct {
	repository Repository
	primary    repoMDData
}

// LoadRepositories snapshots each repository's repomd document before loading its primary metadata.
func LoadRepositories(
	ctx context.Context,
	fetcher Fetcher,
	repositories []Repository,
) ([]Package, error) {
	if fetcher == nil {
		return nil, errors.New("metadata fetcher cannot be nil")
	}

	pending := make([]pendingRepository, 0, len(repositories))
	for _, repository := range repositories {
		repomdURL, err := joinURL(repository.URL, "repodata/repomd.xml")
		if err != nil {
			return nil, fmt.Errorf("building repomd URL for repository %#q:\n%w", repository.ID, err)
		}

		data, err := fetcher.Fetch(ctx, repomdURL, repository.DisableSSLVerify)
		if err != nil {
			return nil, fmt.Errorf("loading repomd for repository %#q:\n%w", repository.ID, err)
		}

		var metadata repoMD
		if err := xml.Unmarshal(data, &metadata); err != nil {
			return nil, fmt.Errorf("parsing repomd for repository %#q:\n%w", repository.ID, err)
		}

		primary, err := findPrimary(metadata.Data)
		if err != nil {
			return nil, fmt.Errorf("repository %#q:\n%w", repository.ID, err)
		}

		pending = append(pending, pendingRepository{repository: repository, primary: primary})
	}

	var packages []Package

	for _, entry := range pending {
		primaryURL, err := joinURL(entry.repository.URL, entry.primary.Location.Href)
		if err != nil {
			return nil, fmt.Errorf("building primary metadata URL for repository %#q:\n%w", entry.repository.ID, err)
		}

		data, err := fetcher.Fetch(ctx, primaryURL, entry.repository.DisableSSLVerify)
		if err != nil {
			return nil, fmt.Errorf("loading primary metadata for repository %#q:\n%w", entry.repository.ID, err)
		}

		repositoryPackages, err := parsePrimary(data, entry.primary.Location.Href, entry.repository.Kind)
		if err != nil {
			return nil, fmt.Errorf("parsing primary metadata for repository %#q:\n%w", entry.repository.ID, err)
		}

		packages = append(packages, repositoryPackages...)
	}

	return packages, nil
}

func findPrimary(data []repoMDData) (repoMDData, error) {
	for _, entry := range data {
		if entry.Type == "primary" {
			if entry.Location.Href == "" {
				return repoMDData{}, errors.New("primary metadata has no location")
			}

			return entry, nil
		}
	}

	return repoMDData{}, errors.New("repomd contains no primary metadata")
}

func parsePrimary(data []byte, name string, kind projectconfig.SubrepoKind) ([]Package, error) {
	reader, closeReader, err := decompressedReader(bytes.NewReader(data), name)
	if err != nil {
		return nil, err
	}

	if closeReader != nil {
		defer closeReader()
	}

	decoder := xml.NewDecoder(reader)

	var packages []Package

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("reading primary XML:\n%w", err)
		}

		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "package" {
			continue
		}

		var raw primaryPackage
		if err := decoder.DecodeElement(&raw, &start); err != nil {
			return nil, fmt.Errorf("reading package element:\n%w", err)
		}

		packages = append(packages, Package{
			Name:    raw.Name,
			Epoch:   normalizedEpoch(raw.Version.Epoch),
			Version: raw.Version.Version,
			Release: raw.Version.Release,
			Arch:    raw.Arch,
			Kind:    kind,
		})
	}

	return packages, nil
}

func decompressedReader(reader io.Reader, name string) (io.Reader, func(), error) {
	switch {
	case strings.HasSuffix(name, ".gz"):
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("creating gzip reader:\n%w", err)
		}

		return gzipReader, func() { _ = gzipReader.Close() }, nil
	case strings.HasSuffix(name, ".zst"), strings.HasSuffix(name, ".zstd"):
		zstdReader, err := zstd.NewReader(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("creating zstd reader:\n%w", err)
		}

		return zstdReader, zstdReader.Close, nil
	case strings.HasSuffix(name, ".bz2"):
		return bzip2.NewReader(reader), nil, nil
	case strings.HasSuffix(name, ".xz"):
		xzReader, err := xz.NewReader(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("creating xz reader:\n%w", err)
		}

		return xzReader, nil, nil
	default:
		return reader, nil, nil
	}
}

func joinURL(baseURL, relative string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parsing base URL %#q:\n%w", baseURL, err)
	}

	parsed.Path = path.Join(parsed.Path, relative)

	return parsed.String(), nil
}

func normalizedEpoch(epoch string) string {
	if epoch == "" {
		return "0"
	}

	return epoch
}
