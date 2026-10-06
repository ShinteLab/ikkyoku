package main

// 速い経路の撮る側（`fastCells`）の歯止め（2026-10-07）。
//
// ⚠️ **ここが守っているのは 3 つ。**
//   - **比べる相手（`SetCellBase`）が無ければ使わないこと**（81 マスを読む）
//   - **ふるいが「変化して止まった」と言った 1 枚でしか使わないこと**（保険の 1 枚は答え合わせの役）
//   - **変わったマスの番号が、塗り替えたマスだけになること**
//
// 下半分は**変わったマスの組で「止まった」と見なすふるい**（`gateReadCells`。2026-10-07）の歯止め:
//   - **盤全体が動き続けても、変わったマスの組が 2 枚続けて同じなら速い経路に入ること**（ゲーム画面の演出）
//   - **組が毎周変わるあいだは待つこと**（手が盤の上を動いている）
//   - **速い経路で決まらなかった組は見なさないこと**（`cellsFailed`。それでも待つ上限で読むこと）
//   - **何も変わっていない組が続くのは「変わっていない」に数えること**（保険の 1 枚を飢えさせない）

import (
	"image"
	"image/color"
	"slices"
	"testing"
)

// fastImg は `CaptureQuiet` と同じ順に、変わったマスを測ってから速い経路で扱えるかを返す。
func (s *CaptureService) fastImg(img image.Image) (cells []int, comparable, fast bool) {
	fc := s.cellsOf(img)
	return fc.cells, fc.ok, s.fastCells(fc)
}

// paintCell は r 行 c 列のマスを塗りつぶす（指し手のつもり）。
func paintCell(img *image.RGBA, r, c int) {
	x, y := gateBoard.Min.X+c*20, gateBoard.Min.Y+r*20
	for yy := y; yy < y+20; yy++ {
		for xx := x; xx < x+20; xx++ {
			img.SetRGBA(xx, yy, color.RGBA{R: 30, G: 20, B: 10, A: 255})
		}
	}
}

// gameFrame は**ゲーム画面の演出が重なった 1 枚**（2026-10-07）。cells のマスを塗り（指し手のつもり）、
// 7 行目に小さな光を 3 つ置く。光は n で場所が変わり、前の 1 枚との画素差分はふるいのしきい値を超えるが、
// 1 マスの内側の 1 割強しか塗らないので変わったマスには数えない。
func gameFrame(n int, cells ...int) *image.RGBA {
	img := gateFrame()
	for _, i := range cells {
		paintCell(img, i/9, i%9)
	}
	for k := 0; k < 3; k++ {
		col := (n*3 + k) % 9
		x, y := gateBoard.Min.X+col*20+8, gateBoard.Min.Y+6*20+8
		for yy := y; yy < y+5; yy++ {
			for xx := x; xx < x+5; xx++ {
				img.SetRGBA(xx, yy, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}
	return img
}

// gameService は比べる相手（演出の無い 1 枚）を入れ、ふるいに 1 枚目を通した CaptureService。
func gameService(t *testing.T) *CaptureService {
	t.Helper()
	s := fastService()
	s.lastQuiet = gateFrame()
	s.SetCellBase()
	if got := s.gateImg(gameFrame(0)); got != "" {
		t.Fatalf("1 枚目なのに省きました: %q", got)
	}
	return s
}

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
	if _, comparable, fast := s.fastImg(gateMoved(0)); comparable || fast {
		t.Fatal("比べる相手が無いのに速い経路を使いました")
	}
}

func TestFastCellsOnlyWhenSettled(t *testing.T) {
	s := fastService()
	s.lastQuiet = gateFrame()
	s.SetCellBase()
	for _, why := range []string{gateReadFirst, gateReadRestless, gateReadInsurance} {
		s.gateRead = why
		cells, comparable, fast := s.fastImg(gateMoved(0))
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
	cells, _, fast := s.fastImg(gateMoved(0))
	if !fast {
		t.Fatal("速い経路を使いませんでした")
	}
	if !slices.Equal(cells, []int{1*9 + 1}) {
		t.Fatalf("変わったマス = %v, want [10]", cells)
	}
	// 何も変わっていなければ空（ok は真。呼ぶ側は「同じ」として省く）。
	if cells, _, fast := s.fastImg(gateFrame()); !fast || len(cells) != 0 {
		t.Fatalf("何も変わっていないのに %v（fast=%v）", cells, fast)
	}
}

// ⚠️ **盤全体が動き続けても、変わったマスの組が 2 枚続けて同じなら止まったと見なすこと**（2026-10-07 に実機で踏んだ）。
//
// **実機の症状**: ゲーム画面は駒を掴むと行き先が光り、「技有」の演出や盤の減光が重なるので、盤全体の画素は
// 止まらなかった。ふるいは `moving` を言い続け、待つ上限で 81 マスを読み（3〜5 秒）、確かめでもう一度読んで、
// 指してから手が入るまで 1 手 10 秒かかった（速い経路は止まってからでないと使えなかった）。
func TestGateSettlesWhenCellsStayTheSame(t *testing.T) {
	s := gameService(t)
	moved := 1*9 + 1
	if got := s.gateImg(gameFrame(1, moved)); got != gateSkipMoving {
		t.Fatalf("指した 1 枚目で %q（組がまだ 1 枚しか無いので待つはず）", got)
	}
	if got := s.gateImg(gameFrame(2, moved)); got != "" || s.gateRead != gateReadCells {
		t.Fatalf("組が 2 枚続けて同じなのに %q / %q（止まったと見なしていない）", got, s.gateRead)
	}
	cells, _, fast := s.fastImg(gameFrame(2, moved))
	if !fast || !slices.Equal(cells, []int{moved}) {
		t.Fatalf("速い経路を使っていません: %v（fast=%v）", cells, fast)
	}
}

// ⚠️ **組が毎周変わるあいだは待つこと**（手が盤の上を動いている）。⚠️ 待つ上限では今までどおり読む。
func TestGateWaitsWhileCellsChange(t *testing.T) {
	s := gameService(t)
	for i := 1; i < gateMovingMax; i++ {
		if got := s.gateImg(gameFrame(i, 9+i%9)); got != gateSkipMoving {
			t.Fatalf("%d 周目で %q（変わったマスが動いているのに止まったと見なした）", i, got)
		}
	}
	if got := s.gateImg(gameFrame(gateMovingMax, 9+gateMovingMax%9)); got != "" || s.gateRead != gateReadRestless {
		t.Fatalf("待つ上限で %q / %q（止まらなくても、いずれ読むはず）", got, s.gateRead)
	}
}

// ⚠️ **速い経路で決まらなかった組は、続いても止まったと見なさないこと**（`cellsFailed`）。
// 駒を掴んだまま行き先が光り続けると同じ組が続くので、見なすと 0.5 秒ごとに 81 マスを読む。
// ⚠️ **それでも待つ上限では読むこと**（止まらなくても、いずれ読む）。
func TestGateIgnoresFailedCells(t *testing.T) {
	s := gameService(t)
	moved := 1*9 + 1
	s.markCellsFailed(s.cellsOf(gameFrame(0, moved))) // フロントが 81 マスを読み直した（`RecognizeQuiet`）
	for i := 1; i < gateMovingMax; i++ {
		if got := s.gateImg(gameFrame(i, moved)); got != gateSkipMoving {
			t.Fatalf("%d 周目で %q（決まらなかった組で止まったと見なした）", i, got)
		}
	}
	if got := s.gateImg(gameFrame(gateMovingMax, moved)); got != "" || s.gateRead != gateReadRestless {
		t.Fatalf("待つ上限で %q / %q（止まらなくても、いずれ読むはず）", got, s.gateRead)
	}
}

// ⚠️ **多すぎて 81 マスへ回した組も、決まらなかった組として覚えること**（`fastCells`）。
func TestFastCellsRemembersTooManyCells(t *testing.T) {
	s := gameService(t)
	many := []int{}
	for i := 0; i <= cellFastMax; i++ {
		many = append(many, 2*9+i%9+9*(i/9))
	}
	s.gateImg(gameFrame(1, many...))
	if got := s.gateImg(gameFrame(2, many...)); got != "" || s.gateRead != gateReadCells {
		t.Fatalf("前提: 組が 2 枚続けて同じなら読むはず: %q / %q", got, s.gateRead)
	}
	if _, _, fast := s.fastImg(gameFrame(2, many...)); fast {
		t.Fatal("変わったマスが多いのに速い経路を使いました")
	}
	if got := s.gateImg(gameFrame(3, many...)); got != gateSkipMoving {
		t.Fatalf("81 マスへ回した組で、また止まったと見なしました: %q", got)
	}
}

// ⚠️ **比べる相手を替えたら、決まらなかった組と前の周の組を忘れること**（相手が違えば同じ組でも意味が違う）。
func TestSetCellBaseForgetsCells(t *testing.T) {
	s := gameService(t)
	s.markCellsFailed(s.cellsOf(gameFrame(0, 10)))
	gen := s.cellGen
	s.lastQuiet = gameFrame(0, 10)
	s.SetCellBase()
	if s.cellsFailed.ok || s.gateCells.ok || s.cellGen == gen {
		t.Fatalf("比べる相手を替えたのに残っています: failed=%+v gate=%+v gen=%d→%d", s.cellsFailed, s.gateCells, gen, s.cellGen)
	}
	// 替える前に測った組（前の版）とは、同じ組でも「2 枚続けて同じ」にならないこと。
	old := frameCells{cells: []int{}, ok: true, gen: gen}
	if old.same(s.cellsOf(gameFrame(1, 10))) {
		t.Fatal("前の版の組と比べました")
	}
}

// ⚠️ **何も変わっていない組が続くのは「変わっていない」に数えること**（2026-10-07）。
//
// 演出だけが動いているあいだ（比べる相手から、どのマスも変わっていない）は `same` で省く（81 マスは読まない）。
// ⚠️ **それでも保険（`gateSkipMax`）では読むこと** —— 待つ上限の周に数えると演出のあいだ 8 周ごとに 81 マスを
// 読み、数え直すと保険が二度と来ない（変わったマスの測り違いで手を見落としたとき戻れない）。
func TestGateCountsEmptyCellsAsUnchanged(t *testing.T) {
	s := gameService(t)
	for i := 1; i < gateSkipMax; i++ {
		if got := s.gateImg(gameFrame(i)); got != cellSkipSame {
			t.Fatalf("%d 周目で %q（何も変わっていないのに待つか読みました）", i, got)
		}
	}
	if got := s.gateImg(gameFrame(gateSkipMax)); got != "" || s.gateRead != gateReadInsurance {
		t.Fatalf("省き続けたのに %q / %q（保険の 1 枚が来ない）", got, s.gateRead)
	}
}
