// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/rpmprovider/rpmprovider_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/providers/sourceproviders"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/rpm_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git/git_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/retry"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// --- ResolveLocalSourceIdentity tests ---

func TestResolveLocalSourceIdentity_EmptyDir(t *testing.T) {
	_, err := sourceproviders.ResolveLocalSourceIdentity(afero.NewMemMapFs(), "")
	require.Error(t, err)
}

func TestResolveLocalSourceIdentity_NoFiles(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	require.NoError(t, filesystem.MkdirAll("/specs", fileperms.PublicDir))

	_, err := sourceproviders.ResolveLocalSourceIdentity(filesystem, "/specs")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contains no files")
}

func TestResolveLocalSourceIdentity_Deterministic(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	require.NoError(t, fileutils.WriteFile(filesystem, "/specs/test.spec",
		[]byte("Name: test\nVersion: 1.0"), fileperms.PublicFile))

	identity1, err := sourceproviders.ResolveLocalSourceIdentity(filesystem, "/specs")
	require.NoError(t, err)

	identity2, err := sourceproviders.ResolveLocalSourceIdentity(filesystem, "/specs")
	require.NoError(t, err)

	assert.Equal(t, identity1, identity2)
	assert.NotEmpty(t, identity1)
	assert.Contains(t, identity1, "sha256:", "identity should have sha256: prefix")
}

func TestResolveLocalSourceIdentity_ContentChange(t *testing.T) {
	fs1 := afero.NewMemMapFs()
	require.NoError(t, fileutils.WriteFile(fs1, "/specs/test.spec", []byte("Version: 1.0"), fileperms.PublicFile))

	fs2 := afero.NewMemMapFs()
	require.NoError(t, fileutils.WriteFile(fs2, "/specs/test.spec", []byte("Version: 2.0"), fileperms.PublicFile))

	identity1, err := sourceproviders.ResolveLocalSourceIdentity(fs1, "/specs")
	require.NoError(t, err)

	identity2, err := sourceproviders.ResolveLocalSourceIdentity(fs2, "/specs")
	require.NoError(t, err)

	assert.NotEqual(t, identity1, identity2)
}

func TestResolveLocalSourceIdentity_SidecarFileChangesIdentity(t *testing.T) {
	fsSpecOnly := afero.NewMemMapFs()
	require.NoError(t, fileutils.WriteFile(fsSpecOnly, "/specs/test.spec", []byte("spec"), fileperms.PublicFile))

	fsWithPatch := afero.NewMemMapFs()
	require.NoError(t, fileutils.WriteFile(fsWithPatch, "/specs/test.spec", []byte("spec"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(fsWithPatch, "/specs/fix.patch", []byte("patch"), fileperms.PublicFile))

	identity1, err := sourceproviders.ResolveLocalSourceIdentity(fsSpecOnly, "/specs")
	require.NoError(t, err)

	identity2, err := sourceproviders.ResolveLocalSourceIdentity(fsWithPatch, "/specs")
	require.NoError(t, err)

	assert.NotEqual(t, identity1, identity2, "adding a sidecar file must change identity")
}

// --- FedoraSourcesProviderImpl.ResolveIdentity tests ---

func TestFedoraProvider_ResolveIdentity(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockGitProvider := git_test.NewMockGitProvider(ctrl)

	provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
		afero.NewMemMapFs(),
		newNoOpDryRunnable(),
		mockGitProvider,
		newNoOpDownloader(),
		testResolvedDistro(),
		retry.Disabled(),
	)
	require.NoError(t, err)

	t.Run("resolves commit via clone", func(t *testing.T) {
		expectedCommit := "abc123def456"

		// Expect: metadata-only clone, then GetCurrentCommit.
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			Return(nil)
		mockGitProvider.EXPECT().
			GetCurrentCommit(gomock.Any(), gomock.Any()).
			Return(expectedCommit, nil)

		comp := newMockComp(ctrl, testPackageName)
		identity, resolveErr := provider.ResolveIdentity(t.Context(), comp)
		require.NoError(t, resolveErr)
		assert.Equal(t, expectedCommit, identity)
	})

	t.Run("returns error on clone failure", func(t *testing.T) {
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(), gomock.Any()).
			Return(errors.New("network error"))

		comp := newMockComp(ctrl, testPackageName)
		_, resolveErr := provider.ResolveIdentity(t.Context(), comp)
		require.Error(t, resolveErr)
		assert.Contains(t, resolveErr.Error(), testPackageName)
	})

	t.Run("returns pinned commit without network call", func(t *testing.T) {
		pinnedCommit := "deadbeef12345678"
		comp := newMockCompWithConfig(ctrl, testPackageName, &projectconfig.ComponentConfig{
			Name: testPackageName,
			Spec: projectconfig.SpecSource{
				SourceType:     projectconfig.SpecSourceTypeUpstream,
				UpstreamCommit: pinnedCommit,
			},
		})

		// No LsRemoteHead expectation — the pinned commit should be returned directly.
		identity, resolveErr := provider.ResolveIdentity(t.Context(), comp)
		require.NoError(t, resolveErr)
		assert.Equal(t, pinnedCommit, identity)
	})
}

func TestFedoraProvider_ResolveIdentity_Snapshot(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockGitProvider := git_test.NewMockGitProvider(ctrl)

	snapshotTimeStr := "2025-06-15T00:00:00Z"
	snapshotTime, _ := time.Parse(time.RFC3339, snapshotTimeStr)

	provider, err := sourceproviders.NewFedoraSourcesProviderImpl(
		afero.NewMemMapFs(),
		newNoOpDryRunnable(),
		mockGitProvider,
		newNoOpDownloader(),
		testResolvedDistroWithSnapshot(snapshotTimeStr),
		retry.Disabled(),
	)
	require.NoError(t, err)

	t.Run("resolves commit via clone for snapshot", func(t *testing.T) {
		expectedCommit := "snapshot123abc"

		// Expect: full single-branch clone, then rev-list --before.
		mockGitProvider.EXPECT().
			Clone(gomock.Any(), repoURL, gomock.Any(),
				gomock.Any()). // branch option
			Return(nil)
		mockGitProvider.EXPECT().
			GetCommitHashBeforeDate(gomock.Any(), gomock.Any(), snapshotTime).
			Return(expectedCommit, nil)

		comp := newMockComp(ctrl, testPackageName)
		identity, resolveErr := provider.ResolveIdentity(t.Context(), comp)
		require.NoError(t, resolveErr)
		assert.Equal(t, expectedCommit, identity)
	})

	t.Run("pinned commit takes priority over snapshot", func(t *testing.T) {
		pinnedCommit := "pinned999"
		comp := newMockCompWithConfig(ctrl, testPackageName, &projectconfig.ComponentConfig{
			Name: testPackageName,
			Spec: projectconfig.SpecSource{
				SourceType:     projectconfig.SpecSourceTypeUpstream,
				UpstreamCommit: pinnedCommit,
			},
		})

		// No Clone/Deepen/GetCommitHashBeforeDate expectations — pinned commit is returned directly.
		identity, resolveErr := provider.ResolveIdentity(t.Context(), comp)
		require.NoError(t, resolveErr)
		assert.Equal(t, pinnedCommit, identity)
	})
}

// --- RPMContentsProviderImpl.ResolveIdentity tests ---

func TestRPMProvider_ResolveIdentity(t *testing.T) {
	ctrl := gomock.NewController(t)

	t.Run("hashes downloaded RPM", func(t *testing.T) {
		rpmContent := "test-rpm-file-content"
		mockRPMProvider := rpmprovider_test.NewMockRPMProvider(ctrl)
		mockRPMProvider.EXPECT().
			GetRPM(gomock.Any(), "test-pkg", nil).
			Return(io.NopCloser(strings.NewReader(rpmContent)), nil)

		provider, provErr := sourceproviders.NewRPMContentsProviderImpl(
			rpm_test.NewMockRPMExtractor(ctrl), mockRPMProvider)
		require.NoError(t, provErr)

		comp := newMockComp(ctrl, "test-pkg")
		identity, resolveErr := provider.ResolveIdentity(t.Context(), comp)
		require.NoError(t, resolveErr)
		assert.Equal(t, "sha256:"+sha256Hex(rpmContent), identity)
	})

	t.Run("returns error on RPM download failure", func(t *testing.T) {
		mockRPMProvider := rpmprovider_test.NewMockRPMProvider(ctrl)
		mockRPMProvider.EXPECT().
			GetRPM(gomock.Any(), "test-pkg", nil).
			Return(nil, errors.New("download failed"))

		provider, provErr := sourceproviders.NewRPMContentsProviderImpl(
			rpm_test.NewMockRPMExtractor(ctrl), mockRPMProvider)
		require.NoError(t, provErr)

		comp := newMockComp(ctrl, "test-pkg")
		_, resolveErr := provider.ResolveIdentity(t.Context(), comp)
		require.Error(t, resolveErr)
		assert.Contains(t, resolveErr.Error(), "test-pkg")
	})
}

// --- Helpers ---

// newMockComp creates a mock component with the given name and an empty upstream config.
func newMockComp(ctrl *gomock.Controller, name string) *components_testutils.MockComponent {
	return newMockCompWithConfig(ctrl, name, &projectconfig.ComponentConfig{
		Name: name,
		Spec: projectconfig.SpecSource{},
	})
}

// newMockCompWithConfig creates a mock component with the given name and a custom config.
func newMockCompWithConfig(
	ctrl *gomock.Controller, name string, config *projectconfig.ComponentConfig,
) *components_testutils.MockComponent {
	comp := components_testutils.NewMockComponent(ctrl)
	comp.EXPECT().GetName().AnyTimes().Return(name)
	comp.EXPECT().GetConfig().AnyTimes().Return(config)

	return comp
}

func sha256Hex(content string) string {
	hasher := sha256.New()
	fmt.Fprint(hasher, content)

	return hex.EncodeToString(hasher.Sum(nil))
}

// newNoOpDryRunnable returns a mock that reports dry-run as false.
func newNoOpDryRunnable() *opctxNoOpDryRunnable {
	return &opctxNoOpDryRunnable{}
}

type opctxNoOpDryRunnable struct{}

func (d *opctxNoOpDryRunnable) DryRun() bool { return false }

// newNoOpDownloader returns a stub FedoraSourceDownloader that does nothing.
func newNoOpDownloader() *noOpDownloader {
	return &noOpDownloader{}
}

type noOpDownloader struct{}

func (d *noOpDownloader) ExtractSourcesFromRepo(
	_ context.Context, _, _, _ string, _ []string,
) error {
	return nil
}
