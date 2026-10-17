#!/bin/bash
# 阶段 9：文件管理与手动证书。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "== 单元测试 =="
go test ./internal/files ./internal/ssl ./internal/httpapi
go test ./...
go vet ./...
echo UNIT_OK

echo "== 无 tag 与 plugin_all 构建 =="
CGO_ENABLED=0 go build -o /tmp/lnmp-s9-none ./cmd/lnmp-panel
CGO_ENABLED=0 go build -tags plugin_all -o /tmp/lnmp-s9-all ./cmd/lnmp-panel
echo BUILD_OK

echo STAGE9_FILES_OK
