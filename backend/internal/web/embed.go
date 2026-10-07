// Package web：内嵌前端构建产物。
package web

import "embed"

// Dist 为 frontend 构建输出（scripts/build.sh 复制到此处）。
//
//go:embed all:dist
var Dist embed.FS
