package ikkyoku

// 2 つのマスに写った駒の**見た目がどれくらい似ているか**（追従の成・不成の証拠。2026-10-07）。
//
// 動いた駒の「動く前のマス」と「動いた先のマス」を比べると、成っていなければ同じ字がそのまま
// 写り、成っていれば字が変わる（飛 → 龍）。**学習していない成駒でも、変わったことは分かる**
// —— 中継では成駒の絵を前もって用意できないので、読むのではなく比べる。
//
// ⚠️ **ここは測るだけ。** 将棋の知識（どの駒か・成れるか）も、「どれくらい違えば成りか」の
// しきい値も持たない（呼ぶ側の `app.StudyService` が持つ）。

import (
	"image"
	"math"
)

// PieceTrim はマスの縁から削る割合（各辺）。罫線と、隣のマスへのはみ出しを避ける。
const PieceTrim = 0.12

// PieceShift はずらして探す幅の、マスの幅に対する割合（63px のマスで ±4px）。
// 駒はマスの真ん中にきっちり置かれるとは限らない（ゲーム画面は動かした駒が少しずれて止まる）。
const PieceShift = 0.06

// PieceSimilarity は画像 a の矩形 ra と、画像 b の矩形 rb に写ったものの**見た目の近さ**を返す（2026-10-07）。
//
// 明るさの正規化相互相関（NCC。-1〜1。同じ絵なら 1）。両方の矩形の内側（各辺 `PieceTrim` を削る）の
// 小さいほうの大きさで真ん中を切り出し、b 側だけを ±`PieceShift`（マスの幅に対する割合）ずらして
// いちばん高い値を採る。明るさの平均と幅を揃えてから比べるので、露出の揺れでは下がらない。
//
// ok が false なのは測れなかったとき（矩形が画像からはみ出す・小さすぎる・どちらかが一様で
// 相関が定義できない）。⚠️ **そのとき値は 0 で、呼ぶ側は「証拠が無い」として扱うこと**（似ていない、ではない）。
func PieceSimilarity(a image.Image, ra image.Rectangle, b image.Image, rb image.Rectangle) (float64, bool) {
	if a == nil || b == nil {
		return 0, false
	}
	ia, ib := pieceInner(ra), pieceInner(rb)
	w, h := min(ia.Dx(), ib.Dx()), min(ia.Dy(), ib.Dy())
	if w < 4 || h < 4 {
		return 0, false
	}
	ca := pieceCenter(ia, w, h)
	cb := pieceCenter(ib, w, h)
	s := int(math.Round(float64(min(ra.Dx(), rb.Dx())) * PieceShift))
	if s < 1 {
		s = 1
	}
	if !ca.In(a.Bounds()) {
		return 0, false
	}
	// b 側はずらす幅のぶん広く読む（画像からはみ出すところは使わない）。
	wide := image.Rect(cb.Min.X-s, cb.Min.Y-s, cb.Max.X+s, cb.Max.Y+s).Intersect(b.Bounds())
	if !cb.In(b.Bounds()) {
		return 0, false
	}

	// ⚠️ **1 画素ずつ `At()` を呼ばないこと**（`diff.go` と同じ理由）。先に明るさの配列へ落とす。
	pa := pieceGray(a, ca)
	pb := pieceGray(b, wide)
	n := float64(w * h)
	var sum float64
	for _, v := range pa {
		sum += v
	}
	mean := sum / n
	var va float64
	for i := range pa {
		pa[i] -= mean
		va += pa[i] * pa[i]
	}
	if va < n*1e-6 {
		return 0, false // a が一様（相関が定義できない）
	}

	best, found := -1.0, false
	ww := wide.Dx()
	for dy := -s; dy <= s; dy++ {
		for dx := -s; dx <= s; dx++ {
			x0, y0 := cb.Min.X+dx-wide.Min.X, cb.Min.Y+dy-wide.Min.Y
			if x0 < 0 || y0 < 0 || x0+w > ww || y0+h > wide.Dy() {
				continue
			}
			var sb, sbb, sab float64
			for y := 0; y < h; y++ {
				row := pb[(y0+y)*ww+x0 : (y0+y)*ww+x0+w]
				ar := pa[y*w : y*w+w]
				for x, v := range row {
					sb += v
					sbb += v * v
					sab += ar[x] * v
				}
			}
			vb := sbb - sb*sb/n
			if vb < n*1e-6 {
				continue // このずらし方では b が一様
			}
			// a は平均を引いてあるので、b の平均を引かなくても共分散は sab のまま。
			r := sab / math.Sqrt(va*vb)
			if r > best {
				best, found = r, true
			}
		}
	}
	if !found {
		return 0, false
	}
	return best, true
}

// pieceInner は矩形の各辺を `PieceTrim` だけ削った内側。
func pieceInner(r image.Rectangle) image.Rectangle {
	dx, dy := int(float64(r.Dx())*PieceTrim), int(float64(r.Dy())*PieceTrim)
	return image.Rect(r.Min.X+dx, r.Min.Y+dy, r.Max.X-dx, r.Max.Y-dy)
}

// pieceCenter は r の真ん中にある w×h の矩形。
func pieceCenter(r image.Rectangle, w, h int) image.Rectangle {
	x, y := r.Min.X+(r.Dx()-w)/2, r.Min.Y+(r.Dy()-h)/2
	return image.Rect(x, y, x+w, y+h)
}

// pieceGray は r の明るさを行優先の配列にする。
func pieceGray(img image.Image, r image.Rectangle) []float64 {
	l := lumaAt(img)
	out := make([]float64, 0, r.Dx()*r.Dy())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			out = append(out, float64(l(x, y)))
		}
	}
	return out
}
