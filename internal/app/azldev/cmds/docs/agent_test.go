// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package docs_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/agentskill"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/docs"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAgentTestRoot builds a minimal root command with a couple of stub top-level commands so that
// the injected command list is deterministic.
func newAgentTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "azldev"}
	root.AddCommand(&cobra.Command{Use: "component", Short: "components"})
	root.AddCommand(&cobra.Command{Use: "docs", Short: "docs"})

	return root
}

// primarySkillFile returns the repo-relative SKILL.md path for the built-in skill under the default
// layout.
func primarySkillFile(t *testing.T) string {
	t.Helper()

	skill, err := agentskill.FindSkill(agentskill.SkillName)
	require.NoError(t, err)

	return agentskill.DefaultLayout().SkillFile(skill)
}

// writtenPaths returns the set of paths reported as written by an install.
func writtenPaths(results []docs.InstalledAgentFile) map[string]bool {
	written := make(map[string]bool, len(results))
	for _, result := range results {
		if result.Written {
			written[result.Path] = true
		}
	}

	return written
}

func TestInstallAgentFiles(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	const outputDir = "/target"

	results, err := docs.InstallAgentFiles(testEnv.Env, newAgentTestRoot(), &docs.AgentInstallOptions{
		OutputDir: outputDir,
	})
	require.NoError(t, err)
	// One skill file per registered skill, one instruction file per registered instruction,
	// plus the VS Code and Copilot MCP server configs.
	require.Len(t, results, len(agentskill.Skills())+len(agentskill.Instructions())+2)

	skillPath := filepath.Join(outputDir, filepath.FromSlash(primarySkillFile(t)))

	azldevInstruction := agentskill.Instructions()[0]
	instructionsPath := filepath.Join(outputDir,
		filepath.FromSlash(agentskill.InstructionFile(azldevInstruction)))

	written := writtenPaths(results)
	assert.True(t, written[skillPath], "expected the azldev skill to be written")
	assert.True(t, written[instructionsPath], "expected the instructions file to be written")
	assert.True(t, written[filepath.Join(outputDir, ".vscode/mcp.json")],
		"expected the VS Code MCP config to be written")
	assert.True(t, written[filepath.Join(outputDir, ".mcp.json")],
		"expected the Copilot MCP config to be written")

	// The files must actually exist on the (in-memory) filesystem with wrapper content.
	skill, err := fileutils.ReadFile(testEnv.TestFS, skillPath)
	require.NoError(t, err)
	assert.Contains(t, string(skill), "name: "+agentskill.SkillName)
	assert.Contains(t, string(skill), agentskill.ShowSkillToolName)

	instructions, err := fileutils.ReadFile(testEnv.TestFS, instructionsPath)
	require.NoError(t, err)
	assert.Contains(t, string(instructions), agentskill.ConfigGlob)
}

func TestInstallAgentFilesWritesMCPConfig(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	const outputDir = "/target"

	_, err := docs.InstallAgentFiles(testEnv.Env, newAgentTestRoot(), &docs.AgentInstallOptions{OutputDir: outputDir})
	require.NoError(t, err)

	raw, err := fileutils.ReadFile(testEnv.TestFS, filepath.Join(outputDir, ".vscode/mcp.json"))
	require.NoError(t, err)

	var config struct {
		Servers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"servers"`
	}
	require.NoError(t, json.Unmarshal(raw, &config))

	azldevSrv, ok := config.Servers["azldev"]
	require.True(t, ok, "expected an azldev MCP server entry")
	assert.Equal(t, "stdio", azldevSrv.Type)
	assert.Equal(t, "azldev", azldevSrv.Command)
	assert.Equal(t, []string{"advanced", "mcp"}, azldevSrv.Args)
}

func TestInstallAgentFilesCopilotMCPConfigPreservesOtherServers(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	const outputDir = "/target"

	mcpPath := filepath.Join(outputDir, ".mcp.json")
	require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, outputDir))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, mcpPath,
		[]byte(`{"mcpServers":{"fedora-distgit":{"command":"python3"}}}`), fileperms.PublicFile))

	_, err := docs.InstallAgentFiles(testEnv.Env, newAgentTestRoot(), &docs.AgentInstallOptions{OutputDir: outputDir})
	require.NoError(t, err)

	raw, err := fileutils.ReadFile(testEnv.TestFS, mcpPath)
	require.NoError(t, err)

	var config struct {
		Servers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Tools   []string `json:"tools"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(raw, &config))
	assert.Contains(t, config.Servers, "fedora-distgit", "existing server must be preserved")

	azldevSrv, ok := config.Servers["azldev"]
	require.True(t, ok, "expected an azldev MCP server entry")
	assert.Equal(t, "stdio", azldevSrv.Type)
	assert.Equal(t, "azldev", azldevSrv.Command)
	assert.Equal(t, []string{"advanced", "mcp"}, azldevSrv.Args)
	assert.Equal(t, []string{"*"}, azldevSrv.Tools)
}

func TestInstallAgentFilesMCPConfigIdempotent(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	const outputDir = "/target"

	opts := &docs.AgentInstallOptions{OutputDir: outputDir}
	mcpPaths := []string{
		filepath.Join(outputDir, ".vscode/mcp.json"),
		filepath.Join(outputDir, ".mcp.json"),
	}

	_, err := docs.InstallAgentFiles(testEnv.Env, newAgentTestRoot(), opts)
	require.NoError(t, err)

	first := make(map[string][]byte, len(mcpPaths))
	for _, mcpPath := range mcpPaths {
		first[mcpPath], err = fileutils.ReadFile(testEnv.TestFS, mcpPath)
		require.NoError(t, err)
	}

	_, err = docs.InstallAgentFiles(testEnv.Env, newAgentTestRoot(), opts)
	require.NoError(t, err)

	for _, mcpPath := range mcpPaths {
		second, err := fileutils.ReadFile(testEnv.TestFS, mcpPath)
		require.NoError(t, err)
		// Re-running install must leave MCP config byte-identical so a CI check sees an empty diff.
		assert.Equal(t, string(first[mcpPath]), string(second), "%s changed", mcpPath)
	}
}

func TestInstallAgentFilesMCPConfigPreservesOtherServers(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	const outputDir = "/target"

	mcpPath := filepath.Join(outputDir, ".vscode/mcp.json")
	require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, filepath.Join(outputDir, ".vscode")))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, mcpPath,
		[]byte(`{"servers":{"other":{"command":"foo"}}}`), fileperms.PublicFile))

	_, err := docs.InstallAgentFiles(testEnv.Env, newAgentTestRoot(), &docs.AgentInstallOptions{OutputDir: outputDir})
	require.NoError(t, err)

	raw, err := fileutils.ReadFile(testEnv.TestFS, mcpPath)
	require.NoError(t, err)

	var config struct {
		Servers map[string]json.RawMessage `json:"servers"`
	}
	require.NoError(t, json.Unmarshal(raw, &config))
	assert.Contains(t, config.Servers, "other", "existing server must be preserved")
	assert.Contains(t, config.Servers, "azldev", "azldev server must be added")
}

func TestInstallAgentFilesFull(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	const outputDir = "/target"

	_, err := docs.InstallAgentFiles(testEnv.Env, newAgentTestRoot(), &docs.AgentInstallOptions{
		OutputDir: outputDir,
		Full:      true,
	})
	require.NoError(t, err)

	skillPath := filepath.Join(outputDir, filepath.FromSlash(primarySkillFile(t)))

	skill, err := fileutils.ReadFile(testEnv.TestFS, skillPath)
	require.NoError(t, err)
	// In full mode the complete skill body is inlined.
	assert.Contains(t, string(skill), "overlay system")
}

func TestInstallAgentFilesGitHubLayout(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	const outputDir = "/target"

	_, err := docs.InstallAgentFiles(testEnv.Env, newAgentTestRoot(), &docs.AgentInstallOptions{
		OutputDir: outputDir,
		Layout:    "github",
	})
	require.NoError(t, err)

	skillPath := filepath.Join(outputDir, filepath.FromSlash(".github/skills/azldev/SKILL.md"))

	exists, err := fileutils.Exists(testEnv.TestFS, skillPath)
	require.NoError(t, err)
	assert.True(t, exists, "expected skill at the github layout path")
}

func TestInstallAgentFilesUnknownLayout(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	_, err := docs.InstallAgentFiles(testEnv.Env, newAgentTestRoot(), &docs.AgentInstallOptions{
		OutputDir: "/target",
		Layout:    "bogus",
	})
	require.Error(t, err)
}
