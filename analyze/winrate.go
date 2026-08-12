package analyze

import "math"

// DefaultPonanzaConstant は評価値 → 勝率の変換に使うポナンザ定数の既定値。
//
// **1500 は「評価値 1500 でおよそ 73%」という当たり方をする値**で、
// 将棋 UI で広く使われている。エンジンによって評価値の尺度は違う
// （⚠️ **自作 `engine` の PST は手作り・未調整なので特にそう**）ので、
// **設定で変えられるようにしてある**（`ikkyoku.Config.PonanzaConstant`）。
const DefaultPonanzaConstant = 1500.0

// PonanzaConstantOr は指定された定数を、指定が無ければ既定値を返す。
//
// ⚠️ **「0 なら既定」の解決をここ 1 か所に置くこと。** 呼び出し側（設定タブの
// 表示・解析の指定）がそれぞれ書くと、既定を変えたときに食い違う。
func PonanzaConstantOr(k float64) float64 {
	if k <= 0 {
		return DefaultPonanzaConstant
	}
	return k
}

// WinRate は評価値を**先手の勝率**（0.0〜1.0）に直す。
//
//	1 / (1 + exp(-評価値 / ポナンザ定数))
//
// ⚠️ **評価値と同じで先手視点。** `Score` は既に先手視点へ揃えてあるので、
// ここでも符号をいじらない（**表示側で反転しないこと**）。
//
// **詰みは 1.0 / 0.0 に振り切る。** 詰みスコアの生値をそのままシグモイドに
// 通すと、詰みの手数で勝率が変わって見える（詰んでいることに変わりはない）。
func WinRate(s Score, k float64) float64 {
	if s.Mate > 0 {
		return 1
	}
	if s.Mate < 0 {
		return 0
	}
	return 1 / (1 + math.Exp(-float64(s.CP)/PonanzaConstantOr(k)))
}
