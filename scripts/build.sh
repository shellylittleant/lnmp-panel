#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
if [[ -d web && -f web/package.json ]]; then
  (cd web && npm ci && npm run build)
fi
mkdir -p bin
CGO_ENABLED=0 go build -o bin/lnmp-panel ./cmd/lnmp-panel
echo "已生成 $ROOT/bin/lnmp-panel"
