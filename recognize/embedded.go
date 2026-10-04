package recognize

import (
	"errors"
	"io/fs"
	"sync"
)

// exe に焼き込んだ認識器（3 段の 3 段目。2026-10-04 に置き場所を変えた）。
//
// ⚠️ **焼き込むのはこのパッケージではない。** `go:embed` は `_cmd/ikkyoku` が持ち
// （`_cmd/ikkyoku/model/` を `-tags embedmodel` のときだけ埋め込む）、ここへは
// `SetEmbedded` で中身（fs.FS）を渡すだけ。以前は `recognize/model/` を埋め込んでいたが、
// **焼き込むモデルはアプリ（exe）の持ち物**で、置き場所もアプリの横（wails3 で
// ビルドする場所）が自然なので移した。`recognize` は埋め込まれたものを読むだけ。
//
// 中身は**置いた配布モデルと同じ形**（suteme の配布用の書き出し。predictor.go）で、
// 読む口も同じ（LoadPackFS）。
//
// ⚠️ **既定の運用（学習データから読む）を置き換えるものではない。** 学習データは
// 育て続けるものなので、開発中は suteme のリポジトリを直接指すのが正しい。焼き込みが
// 要るのは**配る**とき —— 配布先に suteme のリポジトリは無いので、**exe 1 つで動く形**にする。

var (
	embeddedMu sync.Mutex
	embedded   fs.FS
)

// SetEmbedded は exe に焼き込んだ配布セットを渡す（`_cmd/ikkyoku` の main が起動時に 1 回）。
// nil なら焼き込みは無い（タグなしのビルド）。
func SetEmbedded(fsys fs.FS) {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	embedded = fsys
}

func embeddedFS() fs.FS {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	return embedded
}

// EmbeddedAvailable は焼き込んだ認識器を持つビルドか（学習データのファイルが埋まっているか）。
func EmbeddedAvailable() bool {
	fsys := embeddedFS()
	return fsys != nil && len(packDataFiles(fsys)) > 0
}

// EmbeddedSource は焼き込んだものの説明（書き出しの日時・件数）。**観測用**で、画面とログに出す。
func EmbeddedSource() string {
	fsys := embeddedFS()
	if fsys == nil {
		return ""
	}
	s := &Set{}
	info := readExportInfo(fsys)
	s.Date, s.Samples = info.Date, info.Samples
	return s.Label()
}

// LoadEmbedded は焼き込んだ配布セットから 1 組を読む（`Use` で差し替える）。
func LoadEmbedded() (*Set, error) {
	fsys := embeddedFS()
	if fsys == nil {
		return nil, errors.New("ikkyoku/recognize: このビルドには認識器が焼き込まれていません")
	}
	s, err := LoadPackFS(fsys, "embed")
	if err != nil {
		return nil, err
	}
	s.Source = "焼き込み"
	if l := s.Label(); l != "" {
		s.Source += "（" + l + "）"
	}
	return s, nil
}
