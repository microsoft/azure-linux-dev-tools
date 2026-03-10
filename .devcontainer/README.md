# Dev Container Configuration

This directory contains the VS Code dev container configuration for the `azldev` project.

## Overview

The dev container uses the `azl_dev_3.0` image defined in `Dockerfile.AZL-3.0`. The image includes:

- Go development tools,
- Mage build system,
- golangci-lint,
- required development dependencies for VS Code.

## Getting Started

1. Make sure Docker is installed and running on your machine.
1. Open this project in VS Code.
1. When prompted, click "Reopen in Container" or use the Command Palette:
   - Press `Ctrl+Shift+P` (or `Cmd+Shift+P` on Mac)
   - Type "Dev Containers: Reopen in Container"
   - Select the command
1. VS Code will start the dev container.
1. Once ready, you can run the following commands:

```bash
# Verify Go installation
go version

# Verify Mage installation
mage --version

# Run the build
mage build

# Run unit tests
mage unit

# Run all checks
mage check all
```

## Extensions Included

The dev container automatically installs these VS Code extensions:

- Go (golang.go) - Go language support
- JSON Language Features - JSON editing support
- markdownlint - Markdown linting
- YAML Language Support - YAML editing support

## Customization

You can modify `.devcontainer/devcontainer.json` to:

- Add more VS Code extensions
- Change environment variables
- Add port forwarding
- Modify container settings
