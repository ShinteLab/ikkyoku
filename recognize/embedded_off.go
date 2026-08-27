//go:build !embedmodel

package recognize

// 通常ビルドには認識器を焼き込まない。
//
// **開発中に 20MB 級のデータを毎回リンクしない**ためで、`go build` / `wails3 dev` が
// `recognize/model/` の中身に依存しないのもここが分かれているおかげ。

var (
	embeddedPredictorGZ []byte
	embeddedStripGZ     []byte
	embeddedSource      string
)
