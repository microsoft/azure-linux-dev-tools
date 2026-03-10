// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package version

import (
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/spf13/cobra"
	"go.szostok.io/version"
)

// Called once when the app is initialized; registers the version command.
func OnAppInit(app *azldev.App) {
	cmd := NewVersionCmd()
	app.AddTopLevelCommand(cmd)
}

// VersionInfo represents the version information structure.
type VersionInfo struct {
	Version    string `json:"version"`
	GitCommit  string `json:"gitCommit"`
	GoVersion  string `json:"goVersion"`
	Platform   string `json:"platform"`
	Compiler   string `json:"compiler"`
	BuildDate  string `json:"buildDate"`
	CommitDate string `json:"commitDate"`
	DirtyBuild bool   `json:"dirtyBuild"`
}

// NewVersionCmd creates a custom version command that respects the global -O flag.
func NewVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "version",
		Aliases: []string{"ver"},
		Short:   "Print the CLI version",
		GroupID: azldev.CommandGroupMeta,
		Example: `
azldev version
azldev version -O json`,
		RunE: azldev.RunFuncWithoutRequiredConfigWithExtraArgs(func(env *azldev.Env, args []string) (interface{}, error) {
			return GetVersionInfo(), nil
		}),
	}

	return cmd
}

// GetVersionInfo gathers version information using the same approach as go.szostok.io/version/extension.
func GetVersionInfo() *VersionInfo {
	info := version.Get()

	return &VersionInfo{
		Version:    info.Version,
		GitCommit:  info.GitCommit,
		GoVersion:  info.GoVersion,
		Platform:   info.Platform,
		Compiler:   info.Compiler,
		BuildDate:  info.BuildDate,
		CommitDate: info.CommitDate,
		DirtyBuild: info.DirtyBuild,
	}
}
