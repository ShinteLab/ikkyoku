//go:build embedmodel

package main

import (
	"embed"
	"io/fs"
)

// 配布ビルド（`-tags embedmodel`。`task build:embed`）でだけ、認識器を exe に焼き込む（2026-10-04）。
//
// **焼き込むのは `_cmd/ikkyoku/model/`（手元のモデル）。** 中身は suteme の配布用の書き出し
// （`go run ./_cmd/suteme-training -export -gzip -out <ここ>`）で、ikkyoku は読むだけ
// （recognize.LoadPackFS）。⚠️ **git に入れない**（`.gitignore` の `model/`。学習し直すたびに
// 10MB 級をコミットすることになる）。
//
// ⚠️ **空ならビルドが通らない**（go:embed がファイルを見つけられない）。それが狙いで、
// 空の認識器で配れてしまうより止まったほうがよい。`task build:embed` は先に入れ方を出して止まる。
//
//go:embed model
var modelFS embed.FS

// embeddedModel は焼き込んだ配布セット（model/ の中身）を返す。
func embeddedModel() fs.FS {
	sub, err := fs.Sub(modelFS, "model")
	if err != nil {
		return nil
	}
	return sub
}
