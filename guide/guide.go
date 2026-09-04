// Package guide はガイド枠の寸法と、盤に合わせるときの幾何を持つ。
//
// **ウィンドウにも Wails にも依存しない。** HWND を触ったり画面を撮ったりするのは
// `_cmd/ikkyoku` の仕事で、ここにあるのは**数の計算だけ**。
//
// GUI から切り離してあるのは、**符号を 1 つ間違えると枠が逆へ飛ぶのに、
// 実機で押すまで気づけない**ため（テストは guide_test.go）。
package guide

import (
	"image"
	"math"

	"github.com/ShinteLab/ikkyoku"
)

const (
	// BorderPx はガイド枠の線の太さ(CSS px)。
	BorderPx = 2
	// ToolbarHeightPx は枠ウィンドウ上部のツールバーの高さ(CSS px)。
	// Frameless なので、これが OS のタイトルバーの代わりになる。
	ToolbarHeightPx = 32
)

// FramelessBottomPaddingPx は Wails が Frameless ウィンドウの下端に入れる余白(物理px)。
//
// Wails は WM_NCCALCSIZE で `rgrc.Bottom += 1` と `setPadding(edge.Rect{Bottom: 1})` を
// 行っている(リサイズ時のちらつき回避)。結果として **クライアント領域の最下 1 行には
// WebView が描画されず**、ガイド枠の下辺は GetClientRect の下端より 1px 上に来る。
// これを引かないと、撮った画像の最下行に赤いガイド枠が 1px 写り込む(実測で確認済み)。
//
// DPI ではなく物理ピクセル単位の固定値なので、スケール変換はしない。
// なお最大化・全画面のときは Wails 側がこの余白を入れないため 1px 余分に内側を撮る
// ことになるが、盤が 1px 欠けるだけで実害は無い(枠が写り込む方を避ける)。
const FramelessBottomPaddingPx = 1

// ClickThroughInsetPx は素通しにする範囲を、撮る領域から内側へ詰める幅(CSS px)。
//
// ⚠️ **撮る領域をそのまま素通しにしないこと。** Wails のリサイズ判定は
// 「クライアント領域の端 5px(角は +10px)」で、**ガイド枠(2px)より内側まで食い込む**。
// そこを素通しにすると、**左右と下の縁から枠をリサイズできなくなる**。
// 詰めたぶんは「押しても後ろへ抜けない細い縁」になるだけで、実害が無い。
const ClickThroughInsetPx = 10

// MinBoardPx は自動フィットで受け入れる盤の最小の一辺(物理px)。
//
// 9 マスに割ると 1 マス 10px。これ以下の矩形に枠を合わせると、盤ではない何かを
// 掴んでいたときに枠が潰れて操作できなくなる。信頼度の判定(MinRegionConfidence)を
// 通ったあとの最後の歯止め。
const MinBoardPx = 90

// MarginCellRatio は自動フィットで盤の外側に残す余白(マス 1 つの何割か)。
// MinMarginPx はその下限(物理px)。
//
// **盤にぴったり合わせると、次に撮った画像が認識しづらくなる。** 盤の外枠の線が
// 画像の端に来てしまい、検出(DetectBoard)が格子として掴めなくなるため。撮り溜めた
// PNG を「検出した矩形ぴったり」と「余白つき」で切り出して Recognize に流すと、
// ぴったり側だけが落ちる(実測: 0.63←0.90 / 0.79←0.99 / 0.86←0.98 / 0.88←1.00 / 0.91←1.00。
// 悪いものは検出そのものが失敗する)。**フィットの目的は認識を良くすることなので、
// ここで余白を取らないと機能として本末転倒になる。**
//
// 余白はマスの大きさに比例させる(盤の見かけの大きさは中継によって 2 倍以上違う)。
//
// **2026-08-11 に 0.1 → 0.3 へ広げた。** 合わせた枠で撮った画像を suteme が
// 認識できず、少し広げて送ると拾う、という事象が実際に出た。suteme 側の見解は
// 「**外枠線を含めたうえで、さらに外側へ 0.3 マス程度**(その解像度で 15〜20px)、
// 最低でも 8px」。以前の 0.1(マス 70〜100px で 7〜10px)は通ることもあるが、
// **枠自体が数px 揺れる前提だと余裕が足りない**。
// **大きくしすぎないこと**(余白に写った中継の UI が盤の格子と競合しうる)。
const (
	MarginCellRatio = 0.3
	MinMarginPx     = 8
)

// MinShrinkRatio は自動フィットで許す縮小の下限(今のキャプチャ領域に対する一辺の比)。
//
// **9x9 のグリッド検出には「半分の周期」で信頼度 1.00 が出る当たり方がある。**
// マス 2 つぶんを 1 マスとみなすと格子線が 1 本おきに一致し、盤の内側は
// どこを切り取っても色が均一なので、信頼度(ValidateBoard)は 1.00 のまま
// **盤の 1/4 の領域**が返る。撮り溜めた 62 枚のうち 3 枚で実際に起きた
// (例: 651x700 の盤に対して (10,312) 309x334 で信頼度 1.00)。
//
// この当たり方は**縦横の両方がちょうど半分**になるのが特徴なので、両辺ともこの比を
// 下回る候補は採らない。片辺だけ小さいのは「枠の縦横比が盤と違う」という普通の状態で、
// これは弾かない。信頼度では区別が付かないため、大きさで見るしかない。
//
// **検出そのものを直すのは suteme の仕事。** ここでやっているのは
// 「アプリとして、ユーザーが手で合わせた枠をどこまで信じて動かすか」の線引き。
const MinShrinkRatio = 0.6

// Layout は枠ウィンドウの描画寸法(CSS px)をフロントに渡すための型。
//
// **寸法の唯一のソースは Go 側。** フロントは起動時にこれを受け取って CSS 変数へ
// 流し込む。**フロントに既定値を書かないこと** —— ずれると「見えている枠」と
// 「実際に撮れる領域」が食い違い、枠が写り込む。
type Layout struct {
	BorderPx  int `json:"borderPx"`
	ToolbarPx int `json:"toolbarPx"`
}

// CurrentLayout は今の寸法を返す。
func CurrentLayout() Layout {
	return Layout{BorderPx: BorderPx, ToolbarPx: ToolbarHeightPx}
}

// Window はウィンドウの位置と大きさ(DIP)。
//
// ⚠️ **物理ピクセルではない。** Wails の `Position()` / `Size()` の座標系で、
// 物理ピクセルへ直すには scale を掛ける(`ScaleUp`)。
type Window struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// ScaleUp は CSS px を物理ピクセルへ直す。
//
// ⚠️ **切り上げること。** 丸めで 1px ずれたとき、内側に食い込む(盤が 1px 欠ける)のは
// 実害が無いが、**外側にはみ出すと赤いガイド枠が写り込む**。
func ScaleUp(cssPx int, scale float64) int {
	return int(math.Ceil(float64(cssPx) * scale))
}

// Margin は盤の外側に残す余白(物理px)を返す。
func Margin(board image.Rectangle) int {
	cell := float64(board.Dx()) / 9
	if h := float64(board.Dy()) / 9; h < cell {
		cell = h // 縦横で違う場合は狭いほうに合わせる(余白が過剰にならないように)
	}
	m := int(math.Round(cell * MarginCellRatio))
	if m < MinMarginPx {
		m = MinMarginPx
	}
	return m
}

// WithMargin は盤の矩形を余白のぶんだけ広げる。
//
// **画像の外にはみ出しても切り詰めない。** 枠は今より大きくなってよく、はみ出した
// ぶんには画面の続きが写るだけ。ここで image.Bounds() に丸めると、盤が枠の端に
// 接している(= まさに余白が要る)ときに限って余白が消える。
func WithMargin(board image.Rectangle) image.Rectangle {
	return board.Inset(-Margin(board))
}

// Slop は「もう合っている」とみなすずれ(物理px。Geometry に渡す)。
//
// 検出結果は毎回 5〜10px 揺れるので、そこまで直しにいくと押すたびに枠が動く。
//
// ⚠️ **余白そのものを渡さないこと**(2026-08-11 まではそうしていた)。余白と同じ
// 幅のずれを許すと、**ずれた向きの余白がちょうど 0 になるところまで見逃す**ことに
// なり、余白を広げた意味が無くなる。余白の半分なら、どちらへ揺れても
// 余白の半分は必ず残る。
//
// 下限は余白の下限の半分(4px)。揺れの実測(5〜10px)よりは小さいが、そこまで
// 小さい盤は 1 マスが 27px 未満で、そもそもこの機能の想定の外側。
func Slop(board image.Rectangle) int {
	return Margin(board) / 2
}

// TooSmall は見つけた盤が「半分の周期」の誤検出とみなせるかを返す。
//
// **縦横の両方が MinShrinkRatio を下回るときだけ true。** 片辺だけ小さいのは
// 「枠の縦横比が盤と違う」という普通の状態なので弾かない(MinShrinkRatio のコメント)。
//
// ⚠️ **ここでログを出さないこと。** 出すかどうかは呼び出し側(GUI)の判断で、
// この関数は「採ってよいか」だけを答える。
func TooSmall(board image.Rectangle, region ikkyoku.Region) bool {
	return float64(board.Dx()) < float64(region.Width)*MinShrinkRatio &&
		float64(board.Dy()) < float64(region.Height)*MinShrinkRatio
}

// InsideClickThrough はその点が「素通しにしてよい範囲」に入っているか。
//
// 撮る範囲そのものではなく、**内側へ ClickThroughInsetPx だけ詰めた範囲**。
// 詰めないと Wails のリサイズ判定（クライアント領域の端 5px）と重なり、
// **左右と下の縁から枠をリサイズできなくなる**。
func InsideClickThrough(region ikkyoku.Region, scale float64, x, y int) bool {
	inset := ScaleUp(ClickThroughInsetPx, scale)
	return x >= region.X+inset && x < region.X+region.Width-inset &&
		y >= region.Y+inset && y < region.Y+region.Height-inset
}

// Geometry は「盤がスクリーンのどこにあるか」から、枠ウィンドウの新しい
// 位置・サイズ(DIP)を求める。moved が false なら動かす必要は無い。
//
// board は**スクリーン座標・物理ピクセル**の矩形(撮った画像の座標系ではない。
// 画面全体から探すので、画像の原点とキャプチャ領域の原点が一致しないため)。
// region は今のキャプチャ領域で、これもスクリーン座標・物理ピクセル。
//
// キャプチャ領域はガイド枠の内側そのものなので、**region と board のずれをそのまま
// ウィンドウに足せば**枠の内側が盤に重なる。ツールバーやガイド枠の太さは
// 位置とサイズの両方に同じだけ乗っているので、差分にすると消える(足し引き不要)。
//
// scale は CSS px → 物理 px の係数で、ウィンドウの座標系(DIP)へ割り戻すのに使う。
// slop は「もう合っている」とみなすずれ(物理px。呼び出し側は Slop の値を渡す。
// **余白と同じ値ではない** —— 理由は Slop のコメント)。
//
// **サイズは外側に倒す**(ScaleUp と同じ理由の裏返し。丸めで縮むと盤の端が欠ける。
// 1px 広いぶんには盤の外周が少し余分に写るだけで実害が無い)。
func Geometry(cur Window, region ikkyoku.Region, board image.Rectangle, slop int, scale float64) (Window, bool) {
	dx := board.Min.X - region.X
	dy := board.Min.Y - region.Y
	dw := board.Dx() - region.Width
	dh := board.Dy() - region.Height
	if abs(dx) <= slop && abs(dy) <= slop && abs(dw) <= slop && abs(dh) <= slop {
		return cur, false
	}
	return Window{
		X:      cur.X + int(math.Round(float64(dx)/scale)),
		Y:      cur.Y + int(math.Round(float64(dy)/scale)),
		Width:  cur.Width + int(math.Ceil(float64(dw)/scale)),
		Height: cur.Height + int(math.Ceil(float64(dh)/scale)),
	}, true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
