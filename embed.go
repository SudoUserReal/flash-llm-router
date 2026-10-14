package main

import "embed"

// web/dist 为前端构建产物。构建镜像时由 Docker 多阶段构建注入；
// 开发时若未构建前端，此处为占位文件。
//
//go:embed all:web/dist
var webDistFS embed.FS
