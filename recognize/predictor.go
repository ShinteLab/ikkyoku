package recognize

import (
	"fmt"

	"github.com/ShinteLab/suteme"
)

// UsePredictorFrom は dir に置かれた学習データから駒種推論器を読み込み、
// 以後の FromImage がそれを使うようにする。
//
// suteme は既定でカレントディレクトリ → 実行ファイルのディレクトリの順に
// `training_data_v2.json` / `model_v2.json` を探すが、**GUI アプリの実行ファイルの隣に
// 3.5MB の学習データを置く運用は取らない。** データは育て続けるものなので、コピーを
// 置くと更新のたびにコピーし直す必要があり、古いデータで認識する事故が起きる。
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
