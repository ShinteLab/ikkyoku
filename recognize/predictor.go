package recognize

import (
	"fmt"
	"path/filepath"

	"github.com/ShinteLab/suteme"
)

// ⚠️ **`SutemeDataDir` から読むものは 2 つある。** 駒種推論器（UsePredictorFrom）と
// 盤の縁の帯の判定器（UseStripJudgeFrom）で、**どちらも suteme のリポジトリ直下に
// 育っていくファイル**。片方だけ配線すると「駒種は最新の学習データ、盤の位置合わせは
// 学習前」というちぐはぐな状態になる（実際にそうなっていた。下記）。
// 入口を増やすときは loadRecognizer（_cmd/ikkyoku/captureservice.go）も合わせること。

// UsePredictorFrom は dir に置かれた学習データから駒種推論器を読み込み、
// 以後の FromImage がそれを使うようにする。
//
// suteme は既定でカレントディレクトリ → 実行ファイルのディレクトリの順に
// 学習データ / モデルを探すが、**GUI アプリの実行ファイルの隣に学習データを置く運用は
// 取らない。** データは育て続けるものなので、コピーを置くと更新のたびにコピーし直す
// 必要があり、古いデータで認識する事故が起きる。
// 置き場所は ikkyoku.Config の SutemeDataDir で指す。
//
// **どの学習データを使うかはアプリの判断で、認識のアルゴリズムではない。**
// suteme が LoadPredictor / SetPredictor を公開しているのはそのための継ぎ目。
func UsePredictorFrom(dir string) error {
	p, err := suteme.LoadPredictor(dir)
	if err != nil {
		return fmt.Errorf("ikkyoku/recognize: %w", err)
	}
	suteme.SetPredictor(p)
	return nil
}

// UseDefaultPredictor は suteme 既定の探索に戻す。
// キャッシュも捨てられるので、再読み込みの入口としても使える。
func UseDefaultPredictor() {
	suteme.SetPredictor(nil)
}

// UseStripJudgeFrom は dir に置かれた帯の教師データから、盤の縁の帯の判定器を
// 読み込む。戻り値はサンプル数。
//
// **これが無いと盤の位置が 1マス滑ったまま信頼度 1.00 で返ることがある。**
// 盤の外枠線が画像の外に出ている（＝ガイド枠が盤の縁ぴったり、あるいは内側）と、
// 検出した窓が 1マス滑っても格子線には乗るので suteme.ValidateBoard では見分けが
// 付かない。suteme がこれを直す唯一の手立てが帯の判定器（unslipByJudge）で、
// **judge が無ければ何もしない**（滑ったまま通る）。
//
// 実測（2026-08-22 の中継キャプチャ 1203x961。盤の下端が画像の外）:
//
//	判定器なし  (223, 32)-(974,875)  信頼度 1.00  ← 1マス上へ滑っている
//	判定器あり  (223,125)-(974,968)  信頼度 1.00  ← 正しい
//
// 同じ画像を suteme-training（カレントが suteme のリポジトリ＝判定器を拾える）で
// 開くと盤が合う、という食い違いはこれが原因だった。
//
// ⚠️ **suteme.SetStripJudge(nil) は「判定器を使わない」の意味**で、
// SetPredictor(nil) のように既定探索へ戻るのではない。読めなかったときに nil を
// 渡すと自動探索まで止まるので、**エラーのときは何も設定せずに返す。**
func UseStripJudgeFrom(dir string) (int, error) {
	path := filepath.Join(dir, suteme.DefaultStripFile)
	s, err := suteme.LoadStripData(path)
	if err != nil {
		return 0, fmt.Errorf("ikkyoku/recognize: %w", err)
	}
	j := suteme.NewStripJudge(s)
	if j == nil {
		return 0, fmt.Errorf("ikkyoku/recognize: 帯の教師データが空です: %s", path)
	}
	suteme.SetStripJudge(j)
	return j.Samples(), nil
}

// UseDefaultStripJudge は suteme 既定の探索に戻す。
// **SetStripJudge(nil) ではない**（あちらは「使わない」）。
func UseDefaultStripJudge() {
	suteme.ResetStripJudge()
}
