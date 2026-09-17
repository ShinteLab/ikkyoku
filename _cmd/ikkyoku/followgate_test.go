package main

// 画素差分のふるい（`CaptureService.gate`）の歯止め（2026-09-18）。
//
// ⚠️ **ここが守っているのは 3 つ。**
//   - **変わっていない周で認識を呼ばないこと**（これがふるいの目的そのもの。
//     崩れると長考中も 2.1 秒を払い続け、次の手に気づくのが遅れる）
//   - **変化したら、止まってから読むこと**（動いている最中の 1 枚は、
//     **棋士の手が被った盤**を 2.1 秒かけて読むことになる）
//   - ⚠️ **止まらなくても、いつかは読むこと**（`gateMovingMax`）。
//     盤に重なるテロップのような「動き続けるもの」で**永久に認識しない**という
//     壊れ方を作らないための保険。

import (
	"image"
	"image/color"
	"testing"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/recognize"
)

var gateBoard = image.Rect(100, 60, 280, 240)

func gateFrame() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 170, B: 120, A: 255})
		}
	}
	return img
}

// gateMoved は盤の中の 1 マスを塗り替えた 1 枚（指し手のつもり）。
func gateMoved(n int) *image.RGBA {
	img := gateFrame()
	cell := image.Rect(120+n*20, 80, 140+n*20, 100)
	for y := cell.Min.Y; y < cell.Max.Y; y++ {
		for x := cell.Min.X; x < cell.Max.X; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 30, G: 20, B: 10, A: 255})
		}
	}
	return img
}

func gateService() *CaptureService {
	s := &CaptureService{}
	s.boardAnchor = recognize.Signature{Board: gateBoard}
	return s
}

// ⚠️ **追う盤が決まっていない / 比べる相手が無いなら読む。**
// 見落とすより 1 枚余分に読むほうが軽い。
func TestGateReadsWithoutAnchorOrPrev(t *testing.T) {
	bare := &CaptureService{}
	if got := bare.gate(gateFrame()); got != "" {
		t.Fatalf("追う盤が決まっていないのに省きました: %q", got)
	}
	s := gateService()
	if got := s.gate(gateFrame()); got != "" {
		t.Fatalf("1 枚目なのに省きました: %q", got)
	}
}

func TestGateSkipsUnchanged(t *testing.T) {
	s := gateService()
	s.gate(gateFrame()) // 1 枚目（必ず読む）
	for i := 0; i < 3; i++ {
		if got := s.gate(gateFrame()); got != gateSkipUnchanged {
			t.Fatalf("変わっていないのに %q（認識を呼んでいます）", got)
		}
	}
	if s.gateShots != 4 {
		t.Fatalf("撮った枚数が %d（4 のはず）", s.gateShots)
	}
}

// ⚠️ **変化した周ではなく、その次の「止まった」周で読むこと。**
func TestGateWaitsForStill(t *testing.T) {
	s := gateService()
	s.gate(gateFrame())
	if got := s.gate(gateMoved(0)); got != gateSkipMoving {
		t.Fatalf("動いた周で %q（止まる前に読んでいます）", got)
	}
	if got := s.gate(gateMoved(0)); got != "" {
		t.Fatalf("止まった周で %q（ここで読まないと手を見落とす）", got)
	}
	// 読んだあとは元に戻ること（次の変化まで省く）。
	if got := s.gate(gateMoved(0)); got != gateSkipUnchanged {
		t.Fatalf("読んだ次の周で %q", got)
	}
}

// ⚠️ **止まらなくても、いつかは読むこと。**
func TestGateReadsWhenNeverStill(t *testing.T) {
	s := gateService()
	s.gate(gateFrame())
	for i := 1; i < gateMovingMax; i++ {
		if got := s.gate(gateMoved(i)); got != gateSkipMoving {
			t.Fatalf("%d 周目で %q（まだ待つはず）", i, got)
		}
	}
	if got := s.gate(gateMoved(gateMovingMax)); got != "" {
		t.Fatalf("上限まで動き続けたのに %q（永久に読まない壊れ方）", got)
	}
}

// ⚠️ **枚数は追跡ごとに 0 から。** 前の対局と混ぜると何を測ったのか分からない。
func TestResetGate(t *testing.T) {
	s := gateService()
	s.gate(gateFrame())
	s.gate(gateFrame())
	s.resetGate()
	if s.gateShots != 0 || s.gateReads != 0 || s.gateFrame != nil || s.gateDirty {
		t.Fatalf("白紙に戻っていません: shots=%d reads=%d dirty=%v", s.gateShots, s.gateReads, s.gateDirty)
	}
}

// ---- 盤の有無のふるい（`detectGate`。2026-09-18）--------------------------
//
// ⚠️ **実際の検出そのものはここでは見ない**（`suteme` の仕事で、合成画像で
// 通っても実機の中継とは別物）。ここが守るのは**通す側の約束** ——
// **判断できないときは読むほうへ倒すこと**と、**落とし続けないこと**。

// ⚠️ **追う盤が決まっていなければ通すこと。**
func TestDetectGatePassesWithoutAnchor(t *testing.T) {
	s := &CaptureService{}
	if kind, _ := s.detectGate(gateFrame(), ikkyoku.Region{X: 1, Y: 2, Width: 3, Height: 4}); kind != "" {
		t.Fatalf("追う盤が決まっていないのに落としました: %q", kind)
	}
}

// ⚠️ **枠を動かしたら通すこと**（2026-09-15 の「枠をずらしたら死ぬ」と同じ形）。
// マスタは**画像の中の座標**なので、枠が動けば盤は同じでも必ず食い違う ——
// ここで落とすと**マスタを取り直す後段に永久に辿り着かない**。
func TestDetectGatePassesWhenFrameMoved(t *testing.T) {
	s := gateService()
	s.anchorRegion = ikkyoku.Region{X: 10, Y: 10, Width: 400, Height: 300}
	moved := ikkyoku.Region{X: 40, Y: 10, Width: 400, Height: 300}
	if kind, _ := s.detectGate(gateFrame(), moved); kind != "" {
		t.Fatalf("枠を動かしたのに落としました: %q", kind)
	}
}

// ⚠️ **落とし続けないこと。** 検出だけでは判断の材料が少ないので、
// **見送りが続いたら 1 枚は読んで確かめる**（黙って何も起きない状態を作らない）。
func TestDetectMissGivesUp(t *testing.T) {
	s := gateService()
	for i := 1; i < detectMissMax; i++ {
		kind, why := s.detectMiss(detectOff, "盤の大きさが違います")
		if kind != detectOff || why == "" {
			t.Fatalf("%d 周目で %q / %q（まだ落とすはず）", i, kind, why)
		}
	}
	if kind, _ := s.detectMiss(detectOff, "盤の大きさが違います"); kind != "" {
		t.Fatalf("上限まで見送ったのに %q（永久に読まない壊れ方）", kind)
	}
	if s.detectMisses != 0 {
		t.Fatalf("数え直していません: %d", s.detectMisses)
	}
}

// ⚠️ **数えるのは実際に認識した 1 枚だけ**（`gate` が「読む」と返した枚数ではない）。
// 盤の有無のふるいでも落ちるので、混ぜると**省いた割合が実態より悪く見える**。
func TestNoteReadCountsOnlyRecognized(t *testing.T) {
	s := gateService()
	s.gate(gateFrame()) // 読むと決めた（まだ数えない）
	if s.gateReads != 0 {
		t.Fatalf("gate が数えています: %d", s.gateReads)
	}
	s.noteRead()
	if s.gateReads != 1 {
		t.Fatalf("認識した枚数が %d", s.gateReads)
	}
}
