#!/usr/bin/env bash

# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.

# Wrapper for the azldev MCP server with inotify-based auto-restart.
#
# - Uses out/bin/azldev if it exists, otherwise falls back to 'go run'.
# - Watches for binary changes via inotifywait and restarts automatically.
# - Falls back to plain exec (no auto-restart) if inotifywait is unavailable.
#
# A wrapper is needed instead of using vscode's "dev" flag directly because it triggers every time the binary
# is built, and this causes the MCP server's output console to grab focus and interrupt the developer's workflow.
#
# Install inotify-tools for auto-restart:
#   sudo dnf install inotify-tools    (Fedora/RHEL)
#   sudo apt install inotify-tools    (Debian/Ubuntu)

set -uo pipefail

BINARY="./out/bin/azldev"
BINARY_DIR="$(dirname "$BINARY")"
SERVER_PID=""
WATCH_PID=""

# Save original stdin to fd 3. Bash redirects background processes' stdin to
# /dev/null, so we need to explicitly restore it for the MCP server which
# communicates over stdio.
exec 3<&0

cleanup() {
    [[ -n "$SERVER_PID" ]] && kill "$SERVER_PID" 2>/dev/null
    [[ -n "$WATCH_PID" ]] && kill "$WATCH_PID" 2>/dev/null
    wait 2>/dev/null
}
trap cleanup EXIT SIGTERM SIGINT

# Start the server in the background and save its PID.
start_server() {
    if [[ -x "$BINARY" ]]; then
        echo "Starting azldev MCP server from $BINARY..." >&2
        "$BINARY" advanced mcp 0<&3 &
    else
        echo "Binary not found; running azldev MCP server via 'go run'..." >&2
        go run ./cmd/azldev advanced mcp 0<&3 &
    fi
    SERVER_PID=$!
}

# Completely replace the current process with the server (no auto-restart).
replace_server() {
    if [[ -x "$BINARY" ]]; then
        echo "Starting azldev MCP server from $BINARY..." >&2
        exec "$BINARY" advanced mcp 0<&3
    else
        echo "Binary not found; running azldev MCP server via 'go run'..." >&2
        exec go run ./cmd/azldev advanced mcp 0<&3
    fi
     # No return expected since exec replaces the shell
}

# No inotifywait → run directly without auto-restart.
if ! command -v inotifywait &>/dev/null; then
    echo "inotifywait not found; running without auto-restart. Install inotify-tools for auto-restart." >&2
    replace_server
fi

while true; do
    start_server

    # Watch the directory for the binary being created/replaced.
    # go build writes to a temp file then renames, so watching the file
    # directly misses the event (inode changes).
    (
        while true; do
            while [[ ! -d "$BINARY_DIR" ]]; do sleep 2; done
            inotifywait -qq -e close_write -e moved_to -e create "$BINARY_DIR" 2>/dev/null
            # Only restart if the actual binary was affected.
            if [[ -x "$BINARY" ]]; then
                break
            fi
        done
    ) &
    WATCH_PID=$!

    # Wait for either the server to exit or the file watcher to trigger.
    wait -n "$SERVER_PID" "$WATCH_PID" 2>/dev/null || true

    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        # Server exited on its own → clean up watcher and exit.
        kill "$WATCH_PID" 2>/dev/null || true
        wait "$WATCH_PID" 2>/dev/null || true
        echo "azldev MCP server exited" >&2
        exit 1
    fi

    # Binary changed → kill server and restart after debounce.
    kill "$WATCH_PID" 2>/dev/null || true
    wait "$WATCH_PID" 2>/dev/null || true

    # Debounce: wait for file activity to settle (go build does multiple ops).
    sleep 1

    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    echo "azldev MCP server restarted (binary changed)" >&2
done
