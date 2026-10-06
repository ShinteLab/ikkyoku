package main

// 速い経路の撮る側（`fastCells`）の歯止め（2026-10-07）。
//
// ⚠️ **ここが守っているのは 3 つ。**
//   - **比べる相手（`SetCellBase`）が無ければ使わないこと**（81 マスを読む）
//   - **ふるいが「変化して止まった」と言った 1 枚でしか使わないこと**（保険の 1 枚は答え合わせの役）
//   - **変わったマスの番号が、塗り替えたマスだけになること**

import (
	"image"
	"slices"
	"testing"
)

// fastService は 9x9（1 マス 20px）のマス割りを控えた CaptureService。
func fastService() *CaptureService {
	s := gateService()
	rects := make([]image.Rectangle, 81)
	for r := 0; r < 9; r++ {
		for c := 0; c < 9; c++ {
			x, y := gateBoard.Min.X+c*20, gateBoard.Min.Y+r*20
			rects[r*9+c] = image.Rect(x, y, x+20, y+20)
		}
	}
	s.cellRects, s.cellRegion, s.cellBounds = rects, gateBoard, image.Rect(0, 0, 400, 300)
	return s
}

func TestFastCellsNeedsBase(t *testing.T) {
	s := fastService()
	s.gateRead = gateReadSettled
	if _, comparable, fast := s.fastCells(gateMoved(0)); comparable || fast {
		t.Fatal("比べる相手が無いのに速い経路を使いました")
	}
}

func TestFastCellsOnlyWhenSettled(t *testing.T) {
	s := fastService()
	s.lastQuiet = gateFrame()
	s.SetCellBase()
	for _, why := range []string{gateReadFirst, gateReadRestless, gateReadInsurance} {
		s.gateRead = why
		cells, comparable, fast := s.fastCells(gateMoved(0))
		if fast {
			t.Fatalf("ふるいの理由が %q なのに速い経路を使いました", why)
		}
		// ⚠️ **変わったマスは返すこと**（81 マスを読んだあと `FrameFits` に使う）。
		if !comparable || !slices.Equal(cells, []int{1*9 + 1}) {
			t.Fatalf("ふるいの理由が %q のとき、変わったマスを返していません: %v（%v）", why, cells, comparable)
		}
	}
}

func TestFastCellsFindsTheChangedCell(t *testing.T) {
	s := fastService()
	s.lastQuiet = gateFrame()
	s.SetCellBase()
	s.gateRead = gateReadSettled
	// gateMoved(0) は (120,80)-(140,100) ＝ 2 行目・2 列目のマス。
	cells, _, fast := s.fastCells(gateMoved(0))
	if !fast {
		t.Fatal("速い経路を使いませんでした")
	}
	if !slices.Equal(cells, []int{1*9 + 1}) {
		t.Fatalf("変わったマス = %v, want [10]", cells)
	}
	// 何も変わっていなければ空（ok は真。呼ぶ側は「同じ」として省く）。
	if cells, _, fast := s.fastCells(gateFrame()); !fast || len(cells) != 0 {
		t.Fatalf("何も変わっていないのに %v（fast=%v）", cells, fast)
	}
}
