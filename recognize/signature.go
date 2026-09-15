package recognize

// 「今映っているのは、追いかけている盤か」を見る層（2026-09-15）。
//
// ⚠️ **中継には大盤（解説用の盤）が映る。** そちらは**将棋の局面としては
// 矛盾しない**ので、盤面だけを見ていては弾けない —— 解説が本譜から 1 手の変化を
// 並べていたら、**その手をそのまま棋譜に足してしまう**（実機で出た話）。
//
// **だから「盤面が繋がるか」ではなく「同じ盤が映っているか」を別に見る。**
//
// ⚠️ **ここは「記述子を取り出して比べる」だけ。** どの盤を追っているか（マスタ）は
// **呼び出し側が持つ** —— 画像を認識する層に「前の画像」を持たせない（設計原則1）。
//
// ⚠️ **認識器を書く場所ではない**（それは `suteme`）。使っているのは
// `suteme` が既に返している観測値（盤の矩形と地色）だけ。

import (
	"fmt"
	"image"
)

// 同じ盤と見なす許容。⚠️ **実測で決めた数ではない**（2026-09-15 時点）。
// **実際の中継で当たり具合を見て調整すること。**
const (
	// SizeTolerance は盤の大きさの許容（比）。
	//
	// 大盤に切り替わると**画面に占める大きさが変わる**ので、ここが一番効く。
	// ⚠️ **狭くしすぎないこと** —— 本物の盤でも寄りで多少変わる。
	SizeTolerance = 0.18
	// ShiftToleranceCells は盤の中心のずれの許容（**マス何個ぶんか**）。
	//
	// ⚠️ **画素で持たないこと** —— 盤の大きさは中継によって違うので、
	// 画素で書くと**小さく映る中継では緩すぎ、大きく映る中継では厳しすぎる**。
	ShiftToleranceCells = 0.8
	// ColorTolerance は盤の地色の許容（0〜255）。
	//
	// 大盤は照明も素材も違うので差が出やすい。⚠️ **これだけで判断しないこと** ——
	// 同じ中継でもカメラの露出で動く。
	ColorTolerance = 34
)

// Signature は「どの盤が映っているか」の見た目の記述子。
//
// ⚠️ **局面（駒の並び）は入れない。** 手が進めば変わってしまうので、
// **同じ盤かどうかの判断には使えない。**
type Signature struct {
	// Frame は撮った画像の大きさ（記述子の基準）。
	Frame image.Rectangle
	// Board は盤と判定した矩形（Frame の中の座標）。
	Board image.Rectangle
	// Color は盤の地色（輝度の中央値）。
	Color uint8
}

// SignatureOf は認識の観測値から記述子を取り出す（取れなければ false）。
func SignatureOf(d *Debug) (Signature, bool) {
	if d == nil || d.Region.Dx() <= 0 || d.Region.Dy() <= 0 {
		return Signature{}, false
	}
	return Signature{Frame: d.ImageBounds, Board: d.Region, Color: d.BoardColor}, true
}

// Matches は同じ盤が映っているかを返す（違うなら理由も返す）。
//
// ⚠️ **判断できないときは「同じ」に倒すこと**（設計原則3）。記述子が取れないのは
// **盤が映っていないとき**で、それは別の層（一致度）が既に落としている。
// ここで重ねて落とすと、**理由が 2 か所から出て画面が分からなくなる。**
func (s Signature) Matches(o Signature) (bool, string) {
	if s.Board.Dx() <= 0 || o.Board.Dx() <= 0 {
		return true, ""
	}
	// **大きさ**（一番効く。大盤は画面に占める割合が違う）。
	if r := ratio(s.Board.Dx(), o.Board.Dx()); r > SizeTolerance {
		return false, fmt.Sprintf("盤の大きさが違います（%.0f%% 違い）", r*100)
	}
	if r := ratio(s.Board.Dy(), o.Board.Dy()); r > SizeTolerance {
		return false, fmt.Sprintf("盤の高さが違います（%.0f%% 違い）", r*100)
	}
	// **位置**（マス何個ぶんずれたか。⚠️ 画素で測らない）。
	cell := float64(s.Board.Dx()) / 9
	if cell <= 0 {
		return true, ""
	}
	dx := float64(center(s.Board).X-center(o.Board).X) / cell
	dy := float64(center(s.Board).Y-center(o.Board).Y) / cell
	if abs(dx) > ShiftToleranceCells || abs(dy) > ShiftToleranceCells {
		return false, fmt.Sprintf("盤の位置が違います（%.1f マスぶん）", max(abs(dx), abs(dy)))
	}
	// **地色**（最後の一押し。これだけで判断しない）。
	if d := int(s.Color) - int(o.Color); abs(float64(d)) > ColorTolerance {
		return false, "盤の色が違います"
	}
	return true, ""
}

// ratio は 2 つの長さの違いを比で返す（大きいほうを分母にする）。
func ratio(a, b int) float64 {
	if a <= 0 || b <= 0 {
		return 1
	}
	if a > b {
		a, b = b, a
	}
	return 1 - float64(a)/float64(b)
}

func center(r image.Rectangle) image.Point {
	return image.Pt((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
