package recognize

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"

	"github.com/ShinteLab/suteme"
)

// 認識器を**アプリのバイナリに焼き込む**ための口（ビルドタグ `embedmodel`）。
//
// ⚠️ **既定の運用（`SutemeDataDir` から読む）を置き換えるものではない。**
// 学習データは育て続けるものなので、開発中は suteme のリポジトリを直接指すのが正しい
// （predictor.go の `UsePredictorFrom` の注記）。焼き込みが要るのは**配る**ときだけ
// —— 配布先に suteme のリポジトリは無く、「exe の隣に .bin を 2 つ置いてください」は
// 説明のいる運用になる。**exe 1 つで動く形**を選べるようにするのがこの口。
//
// **データはこのリポジトリに置いていない**（`recognize/model/` は .gitignore）。
// 20MB 級のバイナリを育てるたびにコミットすることになるため。配布ビルドの前に
// suteme の `dist/` から取ってくる（`_cmd/ikkyoku/Taskfile.yml` の `model:copy`）:
//
//	task model:copy      # ../../../suteme/dist → recognize/model/*.gz
//	wails3 build -tags embedmodel
//
// タグを付けずにビルドしたバイナリには**データが入らない**（`EmbeddedAvailable` が
// false）。設定「認識器の読み込み元」は、その場合 dir 固定として扱う。
// 分岐は `_cmd/ikkyoku/captureservice.go` の `loadRecognizer`。

// EmbeddedAvailable は焼き込んだ認識器を持つビルドか。
func EmbeddedAvailable() bool {
	return len(embeddedPredictorGZ) > 0
}

// EmbeddedSource は焼き込んだデータの出所（`model/source.txt` の中身）。
// **観測用**で、どの版を焼いたのかを画面とログに出すためだけに使う
// （焼き込むと元のファイル名が残らない。suteme の embed.go の `name` と同じ役目）。
func EmbeddedSource() string {
	// ⚠️ **BOM を落とすこと。** 書き出しは Windows PowerShell（`build/copy-model.ps1`）で、
	// あちらの `Set-Content -Encoding utf8` は BOM を付ける。付いたまま画面に出すと
	// 行頭に見えない文字が乗る。
	return strings.TrimSpace(strings.TrimPrefix(embeddedSource, "\ufeff"))
}

// UsePredictorEmbedded は焼き込んだ学習データから駒種推論器を作り、
// 以後の FromImage がそれを使うようにする。
func UsePredictorEmbedded() error {
	if !EmbeddedAvailable() {
		return fmt.Errorf("ikkyoku/recognize: このビルドには認識器が焼き込まれていません")
	}
	r, err := gunzip(embeddedPredictorGZ)
	if err != nil {
		return fmt.Errorf("ikkyoku/recognize: 焼き込んだ学習データが読めません: %w", err)
	}
	p, err := suteme.PredictorFrom(r, embeddedLabel("predictor"))
	if err != nil {
		return fmt.Errorf("ikkyoku/recognize: %w", err)
	}
	suteme.SetPredictor(p)
	return nil
}

// UseStripJudgeEmbedded は焼き込んだ帯の教師データから、盤の縁の帯の判定器を作る。
// 戻り値はサンプル数。**これが無いと盤の位置が 1マス滑る**（UseStripJudgeFrom 参照）。
//
// ⚠️ **エラーのときは何も設定しない。** `suteme.SetStripJudge(nil)` は
// 「判定器を使わない」の意味で、既定探索へ戻るのではない（UseStripJudgeFrom と同じ）。
func UseStripJudgeEmbedded() (int, error) {
	if len(embeddedStripGZ) == 0 {
		return 0, fmt.Errorf("ikkyoku/recognize: このビルドには帯の教師データが焼き込まれていません")
	}
	r, err := gunzip(embeddedStripGZ)
	if err != nil {
		return 0, fmt.Errorf("ikkyoku/recognize: 焼き込んだ帯の教師データが読めません: %w", err)
	}
	j, err := suteme.StripJudgeFrom(r, embeddedLabel("strip"))
	if err != nil {
		return 0, fmt.Errorf("ikkyoku/recognize: %w", err)
	}
	suteme.SetStripJudge(j)
	return j.Samples(), nil
}

// embeddedLabel は suteme に名乗るラベル（`Result.Debug` に出る）。
func embeddedLabel(kind string) string {
	if s := EmbeddedSource(); s != "" {
		return "embed:" + kind + ":" + firstLine(s)
	}
	return "embed:" + kind
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// gunzip は焼き込んだ gzip を展開する Reader を返す。
//
// **gzip で持っているのは exe を太らせすぎないため**（実測: 生 23.2MB → 9.9MB）。
// suteme の口が io.Reader を取る（`PredictorFrom` / `StripJudgeFrom`）ので、
// 展開したバイト列を作らずにそのまま渡せる。
func gunzip(b []byte) (io.Reader, error) {
	return gzip.NewReader(bytes.NewReader(b))
}
