# How To: Set Up AI Coding Agents

This guide shows how to use `azldev docs agent` to teach AI coding agents (such as
GitHub Copilot) how to work with azldev in your repository.

## Background

azldev can emit two kinds of agent-facing files:

- **Agent Skills** under `.agents/skills/` — on-demand capabilities,
  in the tool-neutral [Agent Skills](https://agentskills.io/) format, that agents
  load when a task involves azldev.
- **Path-specific instructions** under `.github/instructions/` that apply automatically
  to azldev configuration and generated output.

Every emitted Markdown file identifies itself as generated. Keep repository-specific
policy and editorial guidance in separate files rather than editing emitted files.
The central `.agents/skills/.azldev-generated.json` file records the azldev version and
whether full skills were inlined. With the `github` layout, it is written under
`.github/skills/` instead.

The authoritative skill content ships inside the azldev binary. By default the
emitted `SKILL.md` is a light wrapper that points agents at a read-only MCP tool
(`docs-agent-show`) so they always load the guidance that matches the installed
azldev version.

## Emit the Agent Files

Run the command from the root of the target repository:

```bash
azldev docs agent install
```

Or point it at another repository:

```bash
azldev docs agent install -o ../other-repo
```

Use `--dry-run` / `-n` to preview which files would be written without changing
anything:

```bash
azldev -n docs agent install
```

## Wire Up the MCP Server (Recommended)

The emitted wrapper files reference the read-only `docs-agent-show` MCP tool. To
make that tool available to agents, register the azldev MCP server with your agent
tooling. The server is started with:

```bash
azldev advanced mcp
```

With the server configured, an agent can call `docs-agent-show` (a read-only tool
that most agent frameworks can auto-approve) to load the full azldev skill on
demand.

### Troubleshoot MCP discovery timeouts

If an MCP client pauses for 60 seconds while discovering azldev tools, update
azldev to a build using `mcp-go` v1.1.1 or later, then restart the MCP server in
the client. The older v1.0.0 dependency can block its stdio reader when a client
opens a change-notification subscription using MCP protocol `2026-07-28`,
preventing subsequent tool-list requests from being processed.

## Emit Without the MCP Server

If the target environment will not run the azldev MCP server, inline the full skill
into `SKILL.md` instead of the light wrapper:

```bash
azldev docs agent install --full
```

## Preview the Skill

Run without `--skill` to list the available skills:

```bash
azldev docs agent show
```

To print a full skill document to stdout — the same content the MCP tool serves — run:

```bash
azldev docs agent show --skill azldev
```
