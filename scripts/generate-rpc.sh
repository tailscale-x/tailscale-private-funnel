#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
# Tool versions are pinned in docs/API.md and CI; no runtime code generation.
buf generate
buf build --as-file-descriptor-set -o internal/rpc/descriptor.bin
# Stable JSON ordering removes map-order noise from generator output.
python3 - <<'JSON'
import json
from pathlib import Path
for path in Path("internal/web/assets/api").glob("*openapi.json"):
    path.write_text(json.dumps(json.loads(path.read_text()), sort_keys=True, indent=2) + "\n")
JSON
go run ./cmd/generate-rpc
