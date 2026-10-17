#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
# Tool versions are pinned in docs/API.md and CI; no runtime code generation.
buf generate
buf build --as-file-descriptor-set -o internal/rpc/descriptor.bin
go run ./cmd/generate-rpc
