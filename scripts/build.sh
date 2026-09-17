#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
if [[ -d web && -f web/package.json ]]; then
  (cd web && npm ci && npm run build)
fi
mkdir -p bin
# 发布二进制带全部官方插件；无 tag 构建仍须能通过（见 verify-stage8-plugins.sh）
CGO_ENABLED=0 go build -tags plugin_all -o bin/lnmp-panel ./cmd/lnmp-panel
echo "已生成 $ROOT/bin/lnmp-panel（plugin_all）"
