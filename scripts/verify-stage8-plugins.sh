#!/bin/bash
# 阶段 8：无 tag 与 plugin_all 都能编译；单元测试（含穿越/启停 404）；plugin_all 下 filexfer 上传下载。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "== 无 tag 构建 =="
CGO_ENABLED=0 go build -o "$TMP/lnmp-none" ./cmd/lnmp-panel
echo "NONE_BUILD_OK"

echo "== plugin_all 构建 =="
CGO_ENABLED=0 go build -tags plugin_all -o "$TMP/lnmp-all" ./cmd/lnmp-panel
echo "ALL_BUILD_OK"

echo "== 无 tag 测试 =="
go test ./...
go vet ./...
echo "NONE_TEST_OK"

echo "== plugin_all 测试 =="
go test -tags plugin_all ./...
echo "ALL_TEST_OK"

echo STAGE8_PLUGIN_OK
