// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package docs

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/agentskill"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/cobra"
	"go.szostok.io/version"
)

// AgentInstallOptions holds the options for the 'docs agent install' command.
type AgentInstallOptions struct {
	OutputDir string
	Full      bool
	Layout    string
}

// InstalledAgentFile describes a single agent file written (or, in dry-run mode, that would be
// written) by the 'docs agent install' command.
type InstalledAgentFile struct {
	Path    string `json:"path"`
	Written bool   `json:"written"`
}

// Called once when the app is initialized; registers the 'agent' command tree under 'docs'.
func agentOnAppInit(_ *azldev.App, parentCmd *cobra.Command) {
	parentCmd.AddCommand(newAgentCmd())
}

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Emit AI agent skill and instruction files",
		Long: `Emit files that teach AI coding agents how to use azldev.

The 'install' subcommand writes an Agent Skill and a path-specific instructions
file into a target repository. The 'show' subcommand prints the full azldev skill
to stdout and is exposed as a read-only MCP tool, which the emitted wrapper files
reference so that agents always load the guidance that ships with the binary.`,
	}

	cmd.AddCommand(newAgentInstallCmd())
	cmd.AddCommand(newAgentShowCmd())

	return cmd
}

func newAgentInstallCmd() *cobra.Command {
	var options AgentInstallOptions

	cmd := &cobra.Command{
		Use:     "install",
		Aliases: []string{"write", "emit"},
		Short:   "Write azldev agent skill and instruction files into a repository",
		Long: `Write AI agent files describing how to use azldev into a target repository.

Creates (or overwrites) the generated agent substrate, relative to the output directory:

.agents/skills/<name>/SKILL.md                one Agent Skill per built-in skill
.github/instructions/<name>.instructions.md   path-specific instruction wrappers
.mcp.json                                     registers the azldev MCP server for Copilot
.vscode/mcp.json                              registers the azldev MCP server

Everything azldev writes is generated deterministically, so re-running install is a
no-op once committed — a CI check can run install and assert an empty diff. The
MCP entries are upserted, preserving any other servers already configured.

By default the emitted SKILL.md is a light wrapper that points agents at the
read-only 'docs-agent-show' MCP tool for the full, always-current skill. Pass
--full to inline the complete skill instead, for environments without the azldev
MCP server.

Directory paths in the emitted content (such as the lock and rendered-spec
directories) are resolved from the loaded azldev.toml, falling back to azldev's
built-in defaults when no configuration is found. The bindings reflect the project
azldev runs in, so pair --output-dir with -C pointing at the target repository when
scaffolding a different repo.`,
		Example: `  # Write agent files into the current repository
  azldev docs agent install

  # Write into a specific repository, inlining the full skill
  azldev docs agent install -o ../other-repo --full`,
	}

	cmd.RunE = azldev.RunFuncWithoutRequiredConfig(
		func(env *azldev.Env) (interface{}, error) {
			return InstallAgentFiles(env, cmd.Root(), &options)
		})

	cmd.Flags().StringVarP(&options.OutputDir, "output-dir", "o", ".",
		"target repository directory to write agent files into")
	cmd.Flags().BoolVar(&options.Full, "full", false,
		"inline the full skill into SKILL.md instead of a light MCP wrapper")
	cmd.Flags().StringVar(&options.Layout, "layout", "agents",
		"skill layout: 'agents' (.agents/skills) or 'github' (.github/skills)")

	_ = cmd.MarkFlagDirname("output-dir")

	// Not exposed as an MCP tool: 'install' writes files into the target repository. Only the
	// read-only 'show' subcommand is exposed to agents; they can regenerate files via the CLI.

	return cmd
}

func newAgentShowCmd() *cobra.Command {
	var skillName string

	completeSkillNames := cobra.FixedCompletions(skillNames(), cobra.ShellCompDirectiveNoFileComp)

	cmd := &cobra.Command{
		Use:     "show",
		Aliases: []string{"skill", "print"},
		Short:   "Print an azldev agent skill to stdout",
		Long: `Print an azldev Agent Skill document to stdout.

This is the authoritative skill content embedded in the binary. It is exposed as a
read-only MCP tool so that agents can load it on demand; the wrapper files written
by 'azldev docs agent install' reference this tool.

Name the skill with --skill (shell completion lists the choices). Run with no
skill to list the available skills.`,
		Example: `  # List the available skills
  azldev docs agent show

  # Print a skill (--skill tab-completes)
  azldev docs agent show --skill azldev-overlays`,
		Args: cobra.NoArgs,
	}

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		name, list, err := resolveShowSkill(skillName)
		if err != nil {
			return err
		}

		if name == "" {
			// No skill named and several available: list them instead of failing.
			for _, n := range list {
				fmt.Println(n)
			}

			return nil
		}

		env, err := azldev.GetEnvFromCommand(cmd)
		if err != nil {
			return fmt.Errorf("failed to get command environment:\n%w", err)
		}

		doc, err := agentskill.SkillDocument(name, agentSkillParams(env, cmd.Root()))
		if err != nil {
			return fmt.Errorf("failed to render azldev skill:\n%w", err)
		}

		fmt.Println(doc)

		return nil
	}

	cmd.Flags().StringVar(&skillName, "skill", "", "skill to print")
	_ = cmd.RegisterFlagCompletionFunc("skill", completeSkillNames)

	azldev.ExportAsReadOnlyMCPTool(cmd)

	return cmd
}

// InstallAgentFiles renders and writes (or, in dry-run mode, reports) the azldev agent files into
// the output directory named by options.
func InstallAgentFiles(
	env *azldev.Env, rootCmd *cobra.Command, options *AgentInstallOptions,
) ([]InstalledAgentFile, error) {
	layout, err := resolveLayout(options.Layout)
	if err != nil {
		return nil, err
	}

	files, err := agentskill.Files(layout, agentSkillParams(env, rootCmd), options.Full)
	if err != nil {
		return nil, fmt.Errorf("failed to render azldev agent files:\n%w", err)
	}

	fs := env.FS()
	results := make([]InstalledAgentFile, 0, len(files))

	for _, file := range files {
		destPath := filepath.Join(options.OutputDir, filepath.FromSlash(file.RelPath))

		if env.DryRun() {
			results = append(results, InstalledAgentFile{Path: destPath, Written: false})

			continue
		}

		err := fileutils.MkdirAll(fs, filepath.Dir(destPath))
		if err != nil {
			return nil, fmt.Errorf("failed to create directory for %#q:\n%w", destPath, err)
		}

		err = fileutils.WriteFile(fs, destPath, []byte(file.Content), fileperms.PublicFile)
		if err != nil {
			return nil, fmt.Errorf("failed to write agent file %#q:\n%w", destPath, err)
		}

		results = append(results, InstalledAgentFile{Path: destPath, Written: true})
	}

	vscodeMCPResult, err := emitMCPConfig(
		env, options.OutputDir, vscodeMCPConfigRelPath, "servers", false,
	)
	if err != nil {
		return nil, err
	}

	results = append(results, vscodeMCPResult)

	copilotMCPResult, err := emitMCPConfig(
		env, options.OutputDir, copilotMCPConfigRelPath, "mcpServers", true,
	)
	if err != nil {
		return nil, err
	}

	results = append(results, copilotMCPResult)

	return results, nil
}

const (
	// vscodeMCPConfigRelPath is the repo-relative path of the VS Code MCP server configuration.
	vscodeMCPConfigRelPath = ".vscode/mcp.json"
	// copilotMCPConfigRelPath is the repo-relative path of the Copilot MCP server configuration.
	copilotMCPConfigRelPath = ".mcp.json"
)

// emitMCPConfig upserts the azldev MCP server into one target-repo MCP configuration file,
// preserving any other servers already configured and writing canonical (key-sorted, indented)
// JSON so that re-running install is a no-op once the file is committed.
func emitMCPConfig(
	env *azldev.Env, outputDir string, relPath string, serversKey string, exposeAllTools bool,
) (InstalledAgentFile, error) {
	fs := env.FS()
	destPath := filepath.Join(outputDir, filepath.FromSlash(relPath))

	config := map[string]any{}

	exists, err := fileutils.Exists(fs, destPath)
	if err != nil {
		return InstalledAgentFile{}, fmt.Errorf("failed to stat %#q:\n%w", destPath, err)
	}

	if exists {
		raw, err := fileutils.ReadFile(fs, destPath)
		if err != nil {
			return InstalledAgentFile{}, fmt.Errorf("failed to read %#q:\n%w", destPath, err)
		}

		// ponytail: plain JSON only. A JSONC file with comments fails here with a clear error
		// rather than being silently rewritten and losing the comments.
		if err := json.Unmarshal(raw, &config); err != nil {
			return InstalledAgentFile{}, fmt.Errorf(
				"existing %#q is not valid JSON (remove comments so azldev can manage it):\n%w", destPath, err)
		}
	}

	servers, _ := config[serversKey].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}

	azldevServer := map[string]any{
		"type":    "stdio",
		"command": "azldev",
		"args":    []any{"advanced", "mcp"},
	}
	if exposeAllTools {
		azldevServer["tools"] = []any{"*"}
	}

	servers["azldev"] = azldevServer
	config[serversKey] = servers

	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return InstalledAgentFile{}, fmt.Errorf("failed to marshal MCP config:\n%w", err)
	}

	content = append(content, '\n')

	if env.DryRun() {
		return InstalledAgentFile{Path: destPath, Written: false}, nil
	}

	if err := fileutils.MkdirAll(fs, filepath.Dir(destPath)); err != nil {
		return InstalledAgentFile{}, fmt.Errorf("failed to create directory for %#q:\n%w", destPath, err)
	}

	if err := fileutils.WriteFile(fs, destPath, content, fileperms.PublicFile); err != nil {
		return InstalledAgentFile{}, fmt.Errorf("failed to write %#q:\n%w", destPath, err)
	}

	return InstalledAgentFile{Path: destPath, Written: true}, nil
}

// agentSkillParams gathers the dynamic values injected into the emitted and served agent content,
// including the target-repo bindings resolved from the loaded project configuration.
func agentSkillParams(env *azldev.Env, rootCmd *cobra.Command) agentskill.Params {
	return agentskill.Params{
		Version:          version.Get().Version,
		TopLevelCommands: topLevelCommands(rootCmd),
		Bindings:         resolveBindings(env),
	}
}

// resolveBindings resolves the target-repo skill bindings from the loaded project configuration,
// degrading gracefully to azldev's built-in defaults for any value the configuration does not
// provide (including when there is no 'azldev.toml' at all). The bindings reflect the project
// azldev was invoked in; when writing into a different repository with '--output-dir', run azldev
// with '-C' pointing at that repository so the emitted paths match it.
func resolveBindings(env *azldev.Env) agentskill.Bindings {
	bindings := agentskill.Bindings{
		LockDir:          projectconfig.DefaultLockDir,
		RenderedSpecsDir: projectconfig.DefaultRenderedSpecsDir,
		WorkDir:          projectconfig.DefaultWorkDir,
	}

	if env == nil || env.Config() == nil {
		return bindings
	}

	cfg := env.Config()
	projectDir := env.ProjectDir()

	if dir := repoRelativeDir(projectDir, cfg.Project.LockDir); dir != "" {
		bindings.LockDir = dir
	}

	if dir := repoRelativeDir(projectDir, cfg.Project.RenderedSpecsDir); dir != "" {
		bindings.RenderedSpecsDir = dir
	}

	if dir := repoRelativeDir(projectDir, cfg.Project.WorkDir); dir != "" {
		bindings.WorkDir = dir
	}

	return bindings
}

// repoRelativeDir converts an absolute project path to a clean, forward-slash, repository-relative
// directory. It returns "" when the path is empty or resolves to the project root or outside the
// project tree, so the caller keeps its default.
func repoRelativeDir(projectDir, absPath string) string {
	if projectDir == "" || absPath == "" {
		return ""
	}

	rel, err := filepath.Rel(projectDir, absPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}

	return filepath.ToSlash(rel)
}

// resolveLayout builds an [agentskill.Layout] from the command-line layout name.
func resolveLayout(layoutName string) (agentskill.Layout, error) {
	layout := agentskill.DefaultLayout()

	switch layoutName {
	case "", "agents":
		// Default: skills under '.agents/skills'.
	case "github":
		layout.SkillsDir = ".github/skills"
	default:
		return agentskill.Layout{}, fmt.Errorf(
			"unknown layout %#q; expected 'agents' or 'github'", layoutName)
	}

	return layout, nil
}

// resolveShowSkill decides what 'docs agent show' should do for the requested skill.
// It returns the skill name to print; or, when no skill is named, an empty name plus
// the list of skill names for the caller to display.
// An unknown name is an error that names the valid choices.
func resolveShowSkill(requested string) (name string, list []string, err error) {
	names := skillNames()

	if requested == "" {
		return "", names, nil
	}

	if _, findErr := agentskill.FindSkill(requested); findErr != nil {
		return "", nil, fmt.Errorf("unknown skill %q; choose one of: %s",
			requested, strings.Join(names, ", "))
	}

	return requested, nil, nil
}

// skillNames returns the registered skill names in emission order.
func skillNames() []string {
	skills := agentskill.Skills()
	names := make([]string, len(skills))

	for i, skill := range skills {
		names[i] = skill.Name
	}

	return names
}

// topLevelCommands returns the non-hidden, available top-level commands with their summaries,
// sorted by name.
func topLevelCommands(rootCmd *cobra.Command) []agentskill.Command {
	cmds := make([]agentskill.Command, 0, len(rootCmd.Commands()))

	for _, child := range rootCmd.Commands() {
		if child.Hidden || !child.IsAvailableCommand() {
			continue
		}

		cmds = append(cmds, agentskill.Command{Name: child.Name(), Short: child.Short})
	}

	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })

	return cmds
}
