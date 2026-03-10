# Build tool go modules

This directory contains a subdirectory for each golang-based build tool that this project depends on. Each subdirectory contains its own `go.mod` that manages a tool as a `go tool`-invokable tool.

We use separate modules (one per tool) to manage and isolate tools' dependencies.

## MCP Server (magemcp)

The `magemcp` subdirectory contains an MCP (Model Context Protocol) server that exposes Mage build targets as tools for AI coding assistants. This allows AI agents to build, test, and check code quality in this repository.

Available tools:

- `mage_build` - Build Go binaries
- `mage_unit` - Run unit tests
- `mage_generate` - Run code generation
- `mage_check_all` - Run all quality checks (linting, formatting, etc.)
- `mage_fix_all` - Auto-fix code issues (modifies files)
- `mage_scenario` - Run scenario tests (slow)
- `mage_scenario_update` - Update test snapshots (modifies test files)
- `mage_all` - Run all build and test targets (slow)

The server operates in the project root directory and executes `go run magefile.go <target>` commands.

It is configured in `.vscode/mcp.json` to work with VS Code's AI coding assistants.

## Adding a tool

To add a new go tool required for building this project, follow these steps:

- Under the `tools/` directory, create a new subdirectory with the name of the tool, e.g.:

  ```console
  mkdir -p tools/my-go-tool
  ```

- Initialize a go module under that directory, e.g.:

  ```console
  cd tools/my-go-tool
  go mod init github.com/microsoft/azure-linux-dev-tools/tools/my-go-tool
  ```

- While still `cd`'d to that directory, use `go get` to add that tool to the module using its source URI, e.g.:

  ```console
  go get -tool github.com/some-org/my-go-tool@v0.23.1
  ```

- Update `.github/dependabot.yml` to list the new tool under the `dependabot-gomod-tools-upgrades` group; follow the pattern of the other tools already listed there.

- If you have a `go.work` file in the root of this repository (not checked in), make sure to add an entry for the new module (e.g., `./tools/my-go-tool`).

## Calling the tool from within a mage target

To call this tool from within a mage target, make sure to use `mageutil.GetToolAbsPath()` to first fetch a resolved path to the tool.

## Invoking the tool as part of `go:generate`

To use one of these tools via the `go:generate` mechanism, you'll need to use syntax something like the following:

```golang
//go:generate go tool -modfile=../../../tools/stringer/go.mod stringer -type=Foo -output=foo_stringer.go
```

The path should be a relative path from the directory containing this `.go` file to the tool-specific go module directory under `/tools`. We'd like this to be simpler in the future, but this is the best option we've found.

## Dependency updates

Dependencies for tools in this directory are managed by dependabot with a restrictive policy:

- Top level tools will take minor version updates
- Second order dependencies will only have security updates applied

This ensures tools maintain their intended dependency versions while still receiving important security fixes.
