package ikkyoku

// 駒の見た目の近さ（`PieceSimilarity`）の歯止め（2026-10-07）。
//
// ⚠️ **ここが守っているのは 4 つ。**
//   - **同じ絵なら 1 に近いこと**（成っていない駒を「変わった」と言わない）
//   - **少しずれて止まっても高いままなこと**（ゲーム画面は動かした駒がマスの真ん中に来ない）
//   - **字が変われば低いこと**（飛 → 龍。成りの証拠そのもの）
//   - **一様なマスで NaN を返さず、「測れない」と言うこと**（呼ぶ側は証拠が無いとして扱う）

import (
	"image"
	"image/color"
	"math"
	"testing"
)

var pieceCell = image.Rect(20, 30, 83, 100) // 63×70（実機のゲーム画面のマスに近い）

func pieceImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 210, G: 180, B: 120, A: 255})
		}
	}
	return img
}

// drawGlyph は cell の中に字の代わりの模様を描く（kind で形が変わる。dx/dy でずらす）。
func drawGlyph(img *image.RGBA, cell image.Rectangle, kind, dx, dy int) {
	ink := color.RGBA{R: 20, G: 15, B: 10, A: 255}
	cx, cy := cell.Min.X+cell.Dx()/2+dx, cell.Min.Y+cell.Dy()/2+dy
	set := func(x, y int) {
		if image.Pt(x, y).In(cell) {
			img.SetRGBA(x, y, ink)
		}
	}
	for t := -18; t <= 18; t++ {
		for k := -2; k <= 2; k++ {
			switch kind {
			case 0: // 「十」と横棒（飛のつもり）
				set(cx+t, cy+k)
				set(cx+k, cy+t)
				set(cx+t, cy-12+k)
			case 1: // 斜めの「×」と縦棒（龍のつもり）
				set(cx+t, cy+t+k)
				set(cx+t, cy-t+k)
				set(cx+14+k, cy+t)
			}
		}
	}
}

func TestPieceSimilaritySame(t *testing.T) {
	a, b := pieceImage(), pieceImage()
	drawGlyph(a, pieceCell, 0, 0, 0)
	other := pieceCell.Add(image.Pt(90, 0))
	drawGlyph(b, other, 0, 0, 0)
	got, ok := PieceSimilarity(a, pieceCell, b, other)
	if !ok || got < 0.99 {
		t.Fatalf("同じ絵なのに %.3f（%v）", got, ok)
	}
}

func TestPieceSimilarityShifted(t *testing.T) {
	a, b := pieceImage(), pieceImage()
	drawGlyph(a, pieceCell, 0, 0, 0)
	drawGlyph(b, pieceCell, 0, 3, -2)
	got, ok := PieceSimilarity(a, pieceCell, b, pieceCell)
	if !ok || got < 0.95 {
		t.Fatalf("3px ずれただけなのに %.3f（%v）", got, ok)
	}
}

func TestPieceSimilarityDifferentGlyph(t *testing.T) {
	a, b := pieceImage(), pieceImage()
	drawGlyph(a, pieceCell, 0, 0, 0)
	drawGlyph(b, pieceCell, 1, 0, 0)
	got, ok := PieceSimilarity(a, pieceCell, b, pieceCell)
	if !ok || got > 0.6 {
		t.Fatalf("字が違うのに %.3f（%v）", got, ok)
	}
}

func TestPieceSimilarityFlat(t *testing.T) {
	a, b := pieceImage(), pieceImage()
	drawGlyph(b, pieceCell, 0, 0, 0)
	for _, tc := range []struct {
		name string
		a, b image.Image
	}{{"両方一様", a, a}, {"片方一様", a, b}, {"反対の片方一様", b, a}} {
		got, ok := PieceSimilarity(tc.a, pieceCell, tc.b, pieceCell)
		if ok || math.IsNaN(got) {
			t.Fatalf("%s: 測れないはずなのに %.3f（%v）", tc.name, got, ok)
		}
	}
	// 画像からはみ出す矩形・小さすぎる矩形も測らない。
	if _, ok := PieceSimilarity(b, image.Rect(180, 180, 260, 260), b, pieceCell); ok {
		t.Fatal("はみ出した矩形を測りました")
	}
	if _, ok := PieceSimilarity(b, image.Rect(0, 0, 3, 3), b, pieceCell); ok {
		t.Fatal("小さすぎる矩形を測りました")
	}
}
