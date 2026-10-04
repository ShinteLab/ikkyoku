package recognize

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// 認識器を**アプリのバイナリに焼き込む**ための口（ビルドタグ `embedmodel`）。
//
// ⚠️ **既定の運用（`SutemeDataDir` から読む）を置き換えるものではない。**
// 学習データは育て続けるものなので、開発中は suteme のリポジトリを直接指すのが正しい
// （predictor.go の冒頭）。焼き込みが要るのは**配る**ときだけ
// —— 配布先に suteme のリポジトリは無く、「exe の隣に .bin を 2 つ置いてください」は
// 説明のいる運用になる。**exe 1 つで動く形**を選べるようにするのがこの口。
//
// **データはこのリポジトリに置いていない**（`recognize/model/` は .gitignore）。
// 10MB 級のバイナリを育てるたびにコミットすることになるため。`recognize/model/` が
// 「手元のモデル」で、ビルドはそれをそのまま焼き込む（`_cmd/ikkyoku/Taskfile.yml`）:
//
//	task model:update    # 手元を suteme の学習データから作り直す
//	task build:embed     # 手元をそのまま焼き込む（MODEL_DIR=<フォルダ> で入れ替えてから）
//
// タグを付けずにビルドしたバイナリには**データが入らない**（`EmbeddedAvailable` が
// false）。そのときは 3 段のうち焼き込みの段が無いだけ（predictor.go の冒頭）。
// 分岐は `_cmd/ikkyoku/captureservice.go` の `loadRecognizer`。

// EmbeddedAvailable は焼き込んだ認識器を持つビルドか。
func EmbeddedAvailable() bool {
	return len(embeddedPredictorGZ) > 0
}

// EmbeddedSource は焼き込んだデータの出所（`model/source.txt` の中身）。
// **観測用**で、どの版を焼いたのかを画面とログに出すためだけに使う
// （焼き込むと元のファイル名が残らない。suteme の embed.go の `name` と同じ役目）。
func EmbeddedSource() string {
	return cleanSource(embeddedSource)
}

// LoadEmbedded は焼き込んだ配布セットから 1 組を読む（`Use` で差し替える）。
// **ダウンロードして置く配布モデル（LoadPack）と同じ形**なので、組み立ては共用。
func LoadEmbedded() (*Set, error) {
	if !EmbeddedAvailable() {
		return nil, fmt.Errorf("ikkyoku/recognize: このビルドには認識器が焼き込まれていません")
	}
	s, err := loadPack(embeddedPredictorGZ, embeddedStripGZ, EmbeddedSource(), "embed")
	if err != nil {
		return nil, err
	}
	if s.Source == "" {
		s.Source = "焼き込み"
	}
	return s, nil
}

// gunzip は焼き込んだ gzip を展開する Reader を返す。
//
// **gzip で持っているのは exe を太らせすぎないため**（実測: 生 23.2MB → 9.9MB）。
// suteme の口が io.Reader を取る（`PredictorFrom` / `StripJudgeFrom`）ので、
// 展開したバイト列を作らずにそのまま渡せる。
func gunzip(b []byte) (io.Reader, error) {
	return gzip.NewReader(bytes.NewReader(b))
}
