//go:build !embedmodel

package main

import "io/fs"

// 通常ビルド（`wails3 dev` / `go build`）には認識器を焼き込まない。
// **開発中に 10MB 級のデータを毎回リンクしない**ためで、`model/` が空でもビルドが通る。
func embeddedModel() fs.FS { return nil }
