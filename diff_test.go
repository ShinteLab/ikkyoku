package ikkyoku

// ⚠️ **ここが歯止めにしているのは 3 つ。**
//   - 動いていないフレームが「変化なし」と出ること（**ここが崩れるとふるいが
//     素通しになり、認識 2.1 秒を毎周払う**）
//   - **1 マスぶんの変化が拾えること**（拾えないと指し手を見落とす。
//     ふるいで落とした手は**二度と戻ってこない**ので、こちらのほうが重い）
//   - **矩形の外は見ないこと**（消費時間の秒読みで毎周発火しないこと）

import (
	"image"
	"image/color"
	"testing"
)

// testFrame は盤らしい絵を作る（一様な地色 + 格子）。
func testFrame(t *testing.T) *image.RGBA {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 170, B: 120, A: 255})
		}
	}
	return img
}

// board は testFrame の中で「盤」と見なす矩形（9x9 マス・1 マス 20px）。
var board = image.Rect(100, 60, 280, 240)

func TestFrameDiffSameFrame(t *testing.T) {
	a, b := testFrame(t), testFrame(t)
	ratio, ok := FrameDiff(a, b, board)
	if !ok {
		t.Fatalf("比べられませんでした")
	}
	if ratio != 0 {
		t.Fatalf("同じ絵なのに %.4f 変化したことになっています", ratio)
	}
}

// ⚠️ **圧縮のノイズで発火しないこと。** 中継は動画なので、静止していても
// 1〜2 は常に揺れている。
func TestFrameDiffIgnoresNoise(t *testing.T) {
	a, b := testFrame(t), testFrame(t)
	for y := b.Bounds().Min.Y; y < b.Bounds().Max.Y; y++ {
		for x := b.Bounds().Min.X; x < b.Bounds().Max.X; x++ {
			c := b.RGBAAt(x, y)
			c.R += uint8((x + y) % 3) // 0〜2 の揺れ
			b.SetRGBA(x, y, c)
		}
	}
	ratio, ok := FrameDiff(a, b, board)
	if !ok {
		t.Fatalf("比べられませんでした")
	}
	if ratio != 0 {
		t.Fatalf("ノイズで %.4f 変化したことになっています", ratio)
	}
}

// ⚠️ **1 マスぶんでも拾えること。** 実際の指し手は 2 マス（動いた先と元）が
// 変わるので、1 マスで拾えれば足りる。
func TestFrameDiffOneCell(t *testing.T) {
	a, b := testFrame(t), testFrame(t)
	// 1 マス（20x20）を駒の字のつもりで暗くする。
	cell := image.Rect(160, 120, 180, 140)
	for y := cell.Min.Y; y < cell.Max.Y; y++ {
		for x := cell.Min.X; x < cell.Max.X; x++ {
			b.SetRGBA(x, y, color.RGBA{R: 30, G: 20, B: 10, A: 255})
		}
	}
	ratio, ok := FrameDiff(a, b, board)
	if !ok {
		t.Fatalf("比べられませんでした")
	}
	// 1 マスは盤の 1/81 ≈ 1.2%。**半分も拾えれば十分**だが、
	// ここが 0 に近いなら間引きが粗すぎる。
	if ratio < 0.005 {
		t.Fatalf("1 マス変えたのに %.4f しか出ていません（間引きが粗すぎます）", ratio)
	}
}

// ⚠️ **矩形の外は見ないこと。** 見てしまうと、消費時間の秒読みやテロップで
// **毎周「変化あり」になってふるいが意味を失う。**
func TestFrameDiffIgnoresOutsideRect(t *testing.T) {
	a, b := testFrame(t), testFrame(t)
	// 盤の外（秒読みの表示のつもり）を派手に変える。
	for y := 10; y < 40; y++ {
		for x := 10; x < 90; x++ {
			b.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	ratio, ok := FrameDiff(a, b, board)
	if !ok {
		t.Fatalf("比べられませんでした")
	}
	if ratio != 0 {
		t.Fatalf("盤の外の変化を %.4f 拾っています", ratio)
	}
}

// ⚠️ **比べられないときは false。** 呼ぶ側はそこで「認識する」ほうへ倒す。
func TestFrameDiffNotComparable(t *testing.T) {
	a := testFrame(t)
	small := image.NewRGBA(image.Rect(0, 0, 10, 10))
	if _, ok := FrameDiff(a, small, board); ok {
		t.Fatalf("大きさが違うのに比べられたことになっています")
	}
	if _, ok := FrameDiff(a, testFrame(t), image.Rectangle{}); ok {
		t.Fatalf("空の矩形で比べられたことになっています")
	}
	if _, ok := FrameDiff(nil, a, board); ok {
		t.Fatalf("画像が無いのに比べられたことになっています")
	}
}

// cellsOf は board を 9x9 に割ったマスの矩形（行優先）。
func cellsOf(r image.Rectangle) []image.Rectangle {
	out := make([]image.Rectangle, 0, 81)
	w, h := r.Dx()/9, r.Dy()/9
	for row := 0; row < 9; row++ {
		for col := 0; col < 9; col++ {
			x, y := r.Min.X+col*w, r.Min.Y+row*h
			out = append(out, image.Rect(x, y, x+w, y+h))
		}
	}
	return out
}

// ⚠️ **CellDiff の歯止めは 2 つ**（2026-10-07。追従の速い経路）:
//   - **駒を描いたマスだけが変わったと出ること**（ほかのマスは 0）
//   - **盤全体が明るくなっただけでは、どのマスも変わらないこと**（中継の露出の揺れ）
func TestCellDiffFindsOnlyTheChangedCell(t *testing.T) {
	a, b := testFrame(t), testFrame(t)
	cells := cellsOf(board)
	// 3 行目・5 列目のマスに「駒」（暗い四角）を描く。
	c := cells[2*9+4]
	for y := c.Min.Y + 3; y < c.Max.Y-3; y++ {
		for x := c.Min.X + 3; x < c.Max.X-3; x++ {
			b.Set(x, y, color.RGBA{R: 60, G: 40, B: 20, A: 255})
		}
	}
	got, ok := CellDiff(a, b, cells, board)
	if !ok {
		t.Fatalf("比べられませんでした")
	}
	for i, v := range got {
		if i == 2*9+4 {
			if v < 0.5 {
				t.Errorf("駒を描いたマスの変化が %.2f しかありません", v)
			}
			continue
		}
		if v != 0 {
			t.Errorf("マス %d が %.2f 変わったことになっています", i, v)
		}
	}
}

func TestCellDiffIgnoresExposure(t *testing.T) {
	a, b := testFrame(t), testFrame(t)
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			b.Set(x, y, color.RGBA{R: 230, G: 200, B: 150, A: 255}) // 全体が明るくなった
		}
	}
	got, ok := CellDiff(a, b, cellsOf(board), board)
	if !ok {
		t.Fatalf("比べられませんでした")
	}
	for i, v := range got {
		if v != 0 {
			t.Fatalf("明るくなっただけなのに、マス %d が %.2f 変わったことになっています", i, v)
		}
	}
}
