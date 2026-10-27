#!/usr/bin/env bash
# FirePanel 一键构建：前端 dist → 嵌入 → 交叉编译单二进制。
# 用法: ./scripts/build.sh [linux|windows] [输出目录] [amd64|arm64]
# 例:   ./scripts/build.sh linux dist arm64
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GOOS_T="${1:-linux}"
OUT_DIR="${2:-$ROOT/dist}"
GOARCH_T="${3:-amd64}"
VERSION="${FIREPANEL_VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}"

case "$GOOS_T" in
  linux|windows) ;;
  *) echo "!! 不支持的目标系统: $GOOS_T（linux|windows）"; exit 1 ;;
esac
case "$GOARCH_T" in
  amd64|arm64) ;;
  *) echo "!! 不支持的架构: $GOARCH_T（amd64|arm64）"; exit 1 ;;
esac

echo "==> 构建前端 (Svelte)"
cd "$ROOT/frontend"
if [ ! -d node_modules ]; then
  npm ci --no-audit --no-fund
fi
npm run build >/dev/null

echo "==> 同步前端产物到 embed 目录"
mkdir -p "$ROOT/backend/internal/web/dist"
# 保留 .gitkeep（让干净检出也能通过 go:embed 编译），只替换构建产物
find "$ROOT/backend/internal/web/dist" -mindepth 1 ! -name '.gitkeep' -delete
cp -r "$ROOT/frontend/dist/." "$ROOT/backend/internal/web/dist/"

echo "==> 编译后端 ($GOOS_T/$GOARCH_T, $VERSION)"
cd "$ROOT/backend"
BIN_NAME="firepanel"
if [ "$GOOS_T" = "windows" ]; then BIN_NAME="firepanel.exe"; fi
mkdir -p "$OUT_DIR"
CGO_ENABLED=0 GOOS="$GOOS_T" GOARCH="$GOARCH_T" go build -trimpath \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$OUT_DIR/$BIN_NAME" ./cmd/firepanel

echo "==> 完成: $OUT_DIR/$BIN_NAME"
ls -lh "$OUT_DIR/$BIN_NAME"
