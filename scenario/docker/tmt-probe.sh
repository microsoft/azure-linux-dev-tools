#!/bin/sh
set -eu

input="$1"
output="$2"

tr '[:lower:]' '[:upper:]' <"$input" >"$output"
