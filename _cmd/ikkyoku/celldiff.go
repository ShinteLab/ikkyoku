package main

import (
	"fmt"
	"image"
	"sort"
	"strings"

	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// マスごとの差の**測定**（2026-10-06。`TODO.md` 4「変わったマスだけ見て手を割り出す案」）。
//
// 追従で**読んだ 1 枚ごとに、前に読んだ 1 枚とマスごとに比べてログに出すだけ**。
// ⚠️ **追従の振る舞いには一切使わないこと**（測り終えるまでは判断材料ではない）。
// 録画には手を足したときの静止した 1 枚しか残らないので、**手や頭が被った 1 枚で
// 変わったマスがどう見えるか**はこれでないと測れない。
// ⚠️ **測り終えたら消すこと**（足すなら作りを決めてから書き直す）。
const (
	// cellDiffLevel は「変わった画素」と見なす明るさの差（0〜255）。
	cellDiffLevel = 24
	// cellDiffChanged は「変わったマス」と見なす、変わった画素の割合。
	// 録画の実測で、指した手のマスは最小 0.72、ほかのマスは最大 0.077 だった。
	cellDiffChanged = 0.3
	// cellDiffMargin はマスの縁から内側へ削る割合（罫線を避ける）。
	cellDiffMargin = 15
)

// noteCellDiff は読んだ 1 枚を前に読んだ 1 枚とマスごとに比べてログに出し、今の 1 枚を控える。
//
// マス割りは**今の 1 枚の認識結果**（`Debug.Cells` の矩形）を両方に当てる。
// 比べる相手が無い・大きさが違う・マス割りが無いときは控えるだけ。
func (s *CaptureService) noteCellDiff(img image.Image, d *recognize.Debug) {
	s.mu.Lock()
	prev := s.cellPrev
	s.cellPrev = img
	s.mu.Unlock()
	if prev == nil || d == nil || len(d.Cells) != 81 || prev.Bounds() != img.Bounds() {
		return
	}

	// ⚠️ **露出の揺れを打ち消すこと**（録画で 1 枚に 15 変わった組があり、
	// そのままだと盤じゅうのマスが「変わった」に化けた）。
	off := meanLuma(prev, d.Region) - meanLuma(img, d.Region)

	type cell struct {
		name   string
		ratio  float64
		hidden bool
	}
	cells := make([]cell, 0, 81)
	for _, c := range d.Cells {
		cells = append(cells, cell{
			name:   fmt.Sprintf("%d%c", 9-c.Col, 'a'+c.Row), // USI のマス（9筋が Col 0）
			ratio:  cellChanged(prev, img, c.Rect, off),
			hidden: c.Hidden,
		})
	}
	sort.SliceStable(cells, func(i, j int) bool { return cells[i].ratio > cells[j].ratio })

	var changed, top, hidden []string
	for i, c := range cells {
		if c.ratio >= cellDiffChanged {
			changed = append(changed, c.name)
		}
		if i < 6 {
			top = append(top, fmt.Sprintf("%s:%.2f", c.name, c.ratio))
		}
		if c.hidden {
			hidden = append(hidden, c.name)
		}
	}
	log.Info("マスごとの差（前に読んだ 1 枚と）",
		"変わったマス", len(changed), "マス", strings.Join(changed, ","),
		"上位", strings.Join(top, " "),
		"見えないマス", strings.Join(hidden, ","),
		"明るさの差", fmt.Sprintf("%.1f", off))
}

// cellChanged はマスの内側で、明るさの差（露出の差 off を引いたもの）が
// `cellDiffLevel` を超えた画素の割合。
func cellChanged(a, b image.Image, r image.Rectangle, off float64) float64 {
	mx, my := r.Dx()*cellDiffMargin/100, r.Dy()*cellDiffMargin/100
	in := image.Rect(r.Min.X+mx, r.Min.Y+my, r.Max.X-mx, r.Max.Y-my).Intersect(a.Bounds())
	over, n := 0, 0
	for y := in.Min.Y; y < in.Max.Y; y++ {
		for x := in.Min.X; x < in.Max.X; x++ {
			d := luma(a, x, y) - luma(b, x, y) - off
			if d > cellDiffLevel || d < -cellDiffLevel {
				over++
			}
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return float64(over) / float64(n)
}

// meanLuma は矩形の中の明るさの平均（1 画素おき）。
func meanLuma(img image.Image, r image.Rectangle) float64 {
	r = r.Intersect(img.Bounds())
	var sum float64
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y += 2 {
		for x := r.Min.X; x < r.Max.X; x += 2 {
			sum += luma(img, x, y)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func luma(img image.Image, x, y int) float64 {
	r, g, b, _ := img.At(x, y).RGBA()
	return (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 257
}
