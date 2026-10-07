#!/usr/bin/env bash
# FirePanel 一键构建：前端 dist → 嵌入 → 交叉编译单二进制。
# 用法: ./scripts/build.sh [linux|windows] [输出目录]
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GOOS_T="${1:-linux}"
OUT_DIR="${2:-$ROOT/dist}"

echo "==> 构建前端 (Svelte)"
cd "$ROOT/frontend"
npm run build >/dev/null

echo "==> 同步前端产物到 embed 目录"
rm -rf "$ROOT/backend/internal/web/dist"
cp -r "$ROOT/frontend/dist" "$ROOT/backend/internal/web/dist"

echo "==> 编译后端 ($GOOS_T)"
cd "$ROOT/backend"
BIN_NAME="firepanel"
if [ "$GOOS_T" = "windows" ]; then BIN_NAME="firepanel.exe"; fi
mkdir -p "$OUT_DIR"
CGO_ENABLED=0 GOOS="$GOOS_T" GOARCH=amd64 go build -trimpath -ldflags "-s -w" \
  -o "$OUT_DIR/$BIN_NAME" ./cmd/firepanel

echo "==> 完成: $OUT_DIR/$BIN_NAME"
ls -lh "$OUT_DIR/$BIN_NAME"
