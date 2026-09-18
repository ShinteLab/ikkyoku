package ikkyoku

// 撮った 2 枚がどれくらい違うか（中継の追従のふるい。2026-09-18）。
//
// **認識 1 枚が 2.1 秒**なので、追従の速さを決めているのは「1 枚をどれだけ速く
// 読めるか」ではなく「**何枚読まずに済ませられるか**」。長考中も CM 中も
// 画素は動いていないので、**前の 1 枚と比べて変わっていなければ認識を呼ばない。**
// 差分は数ミリ秒で終わる（推論の 1/1000 以下）。
//
// ⚠️ **ここは測るだけ。** 「どれくらい違えば変化と見なすか」も「変化したら何を
// するか」も呼ぶ側（`_cmd/ikkyoku` の追従）の判断で、**この層は割合を返すだけ**
// （ルートパッケージは状態を持たない、の線引き）。
//
// ⚠️ **盤の矩形を渡すこと。** 中継のフレームには**消費時間の秒読みやテロップ**が
// 必ず入っていて、画面全体で比べると**毎周「変化あり」になってふるいが素通しになる。**

import "image"

// DiffSamples は 1 辺あたりに見る点の数（格子状に間引いて見る）。
//
// ⚠️ **全画素を見ないこと。** 盤が 600px 四方なら 36 万画素あり、1 秒に何度も
// 回すふるいとしては重い。
//
// ⚠️ **粗くもしないこと**（2026-09-18 に 64 から上げた。**実機で踏んだ**）。
// **64x64 では駒が動いても「変わっていない」と言った。** 盤 600px なら
// 点の間隔が 9px で、**駒の字の線（数 px）にほとんど当たらない** ——
// CG の盤は**駒の面と盤の地色がどちらも似た明るさ**なので、
// **字に当たらなければ差が出ない。**
//
// 160x160 = 25,600 点なら**1 マスあたり 約 300 点**。
// `*image.RGBA` は Pix を直に読むので、これでも 1ms 前後で終わる。
const DiffSamples = 160

// DiffLevel は「その点が変わった」と見なす明るさの差（0〜255）。
//
// ⚠️ **0 にしないこと** —— 中継は動画なので、静止画でも圧縮のノイズで
// 1〜2 は常に揺れている。**ノイズで毎周「変化あり」になるとふるいが効かない。**
//
// ⚠️ **大きくもしないこと**（2026-09-18 に 24 から下げた。**実機で踏んだ**）。
// **駒と盤の地色は似ている** —— 24 では「駒が乗ったか空いたか」を
// 明るさの差として拾えず、**指したのに「変わっていません」になった。**
// **ノイズを落とす仕事は、点ごとの差ではなく「変わった点の割合」に持たせる。**
const DiffLevel = 10

// FrameDiff は 2 枚の画像の rect の中で、**明るさが変わった点の割合**を返す。
//
// ok が false なのは比べられなかったとき（大きさが違う・矩形が空）。
// ⚠️ **そのときは「変化なし」に倒さないこと** —— 呼ぶ側は**認識する**ほうへ
// 倒す（見落とすより、1 枚余分に読むほうが軽い）。
func FrameDiff(a, b image.Image, rect image.Rectangle) (float64, bool) {
	if a == nil || b == nil {
		return 0, false
	}
	// ⚠️ **大きさが違ったら比べない** —— 枠を動かした直後がこれで、
	// 座標が同じ意味を持たなくなっている。
	if !a.Bounds().Eq(b.Bounds()) {
		return 0, false
	}
	r := rect.Intersect(a.Bounds())
	if r.Dx() <= 1 || r.Dy() <= 1 {
		return 0, false
	}

	// ⚠️ **1 点ずつ `At()` を呼ばないこと** —— `color.Color` に入れるたびに
	// 確保が走り、**1 周 5 万回**では効いてくる（撮るたびに回るので）。
	la, lb := lumaAt(a), lumaAt(b)

	nx, ny := DiffSamples, DiffSamples
	if r.Dx() < nx {
		nx = r.Dx()
	}
	if r.Dy() < ny {
		ny = r.Dy()
	}
	changed, total := 0, 0
	for j := 0; j < ny; j++ {
		y := r.Min.Y + r.Dy()*j/ny
		for i := 0; i < nx; i++ {
			x := r.Min.X + r.Dx()*i/nx
			d := int(la(x, y)) - int(lb(x, y))
			if d < 0 {
				d = -d
			}
			if d >= DiffLevel {
				changed++
			}
			total++
		}
	}
	if total == 0 {
		return 0, false
	}
	return float64(changed) / float64(total), true
}

// lumaAt は 1 点の明るさ（0〜255）を返す関数を作る。
//
// **色は見ない。** 駒が動いたかを見るのに色相まで要らないし、中継は
// 照明も色調整も変わるので**明るさのほうが素直**。
//
// 画面キャプチャは `*image.RGBA` なので、そちらは **Pix を直に読む**。
func lumaAt(img image.Image) func(x, y int) uint8 {
	if p, ok := img.(*image.RGBA); ok {
		return func(x, y int) uint8 {
			i := p.PixOffset(x, y)
			return luma(p.Pix[i], p.Pix[i+1], p.Pix[i+2])
		}
	}
	return func(x, y int) uint8 {
		r, g, b, _ := img.At(x, y).RGBA()
		// RGBA() は 16bit なので 8bit へ落とす。
		return luma(uint8(r>>8), uint8(g>>8), uint8(b>>8))
	}
}

// luma は明るさ（係数は ITU-R BT.601）。
func luma(r, g, b uint8) uint8 {
	return uint8((299*int(r) + 587*int(g) + 114*int(b)) / 1000)
}
