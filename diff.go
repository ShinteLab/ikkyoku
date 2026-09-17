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
// 回すふるいとしては重い。64x64 = 4,096 点なら**1 マスあたり 約 49 点**で、
// 1 枚の駒が動けば必ず数十点に出る。
const DiffSamples = 64

// DiffLevel は「その点が変わった」と見なす明るさの差（0〜255）。
//
// ⚠️ **0 にしないこと** —— 中継は動画なので、静止画でも圧縮のノイズで
// 1〜2 は常に揺れている。**ノイズで毎周「変化あり」になるとふるいが効かない。**
const DiffLevel = 24

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
			d := int(luma(a, x, y)) - int(luma(b, x, y))
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

// luma は 1 点の明るさ（0〜255）。
//
// **色は見ない。** 駒が動いたかを見るのに色相まで要らないし、中継は
// 照明も色調整も変わるので**明るさのほうが素直**。
func luma(img image.Image, x, y int) uint8 {
	r, g, b, _ := img.At(x, y).RGBA()
	// RGBA() は 16bit なので 8bit へ落とす（係数は ITU-R BT.601）。
	v := (299*int(r>>8) + 587*int(g>>8) + 114*int(b>>8)) / 1000
	if v > 255 {
		v = 255
	}
	return uint8(v)
}
