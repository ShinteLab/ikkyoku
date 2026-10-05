package position_test

import (
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// 読めていないマス（`CellMask`。2026-10-06）。
//
// 中継で手や頭が盤に被ったマスは、suteme が「見えない」と返す。**その読みは空きにも駒にも
// 化けていて、どちらでも嘘**なので、根拠にも減点にもしない。そして**行き先が見えない手は採らない**。

// maskOf は (rank, file) の組を読めていないことにする。
func maskOf(cells ...[2]int) *position.CellMask {
	m := &position.CellMask{}
	for _, c := range cells {
		m[c[0]][c[1]] = true
	}
	return m
}

// topRow は一段目を丸ごと読めていないことにする（頭が盤の上辺に被った形）。
func topRow() *position.CellMask {
	m := &position.CellMask{}
	for f := 0; f < 9; f++ {
		m[0][f] = true
	}
	return m
}

// ⚠️ **読めていないマスは食い違いに数えないこと。**
//
// ▲2六歩のあと、2七 に手が被って「後手の金」と読まれた。2七 を数えると、正しい手に
// 1 マスぶんの費用と「覆したマス」が付く（覆したのではなく、もともと読めていない）。
func TestRankIgnoresUnseenCells(t *testing.T) {
	from := mustPos(t, connectHirate)
	target := "lnsgkgsnl/1r5b1/ppppppppp/9/9/7P1/PPPPPPPgP/1B5R1/LNSGKGSNL"
	r, err := position.Rank(from, boardOf(t, target), position.ConnectOptions{
		Cost: evenCost(1), Unseen: maskOf([2]int{6, 7}),
	})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	top := r.Candidates[0]
	if len(top.Moves) != 1 || top.Moves[0] != "2g2f" {
		t.Fatalf("1 位 = %v, want [2g2f]", top.Moves)
	}
	if top.Cost != 0 || len(top.Fixed) != 0 {
		t.Errorf("読めていないマスが食い違いに数えられています: cost=%v fixed=%+v", top.Cost, top.Fixed)
	}
	if !r.Decided(position.DefaultMinFit, position.DefaultMinMargin) {
		t.Errorf("決め打てるはずが決められていません: fit=%v margin=%v", top.Fit, r.Margin)
	}
}

// ⚠️ **行き先が読めていないマスになる手は採らないこと**（`backed`）。
//
// 実機の △3一歩打（髪が被った 3一 を「後手の駒」と読み、撮った盤と完全に一致した）と同じ形。
// ここでは ▲2六歩の 2六 に手が被って「先手の歩」と読まれている。**読みだけなら完全に一致**
// するが、見えていないマスに来たことは根拠にならない —— 待てば手がどいた次の 1 枚で入る。
func TestRankRefusesUnseenArrival(t *testing.T) {
	from := mustPos(t, connectHirate)
	target := advance(t, from, "2g2f")

	// 歯止めが効いていることの確かめ: 印が無ければ ▲2六歩 が 1 位（完全一致）。
	plain, err := position.Rank(from, boardOf(t, target), position.ConnectOptions{Cost: evenCost(1)})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if m := plain.Candidates[0].Moves; len(m) != 1 || m[0] != "2g2f" {
		t.Fatalf("前提が崩れています: 印が無いときの 1 位 = %v", m)
	}

	r, err := position.Rank(from, boardOf(t, target), position.ConnectOptions{
		Cost: evenCost(1), Unseen: maskOf([2]int{5, 7}),
	})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	for _, c := range r.Candidates {
		if len(c.Moves) == 1 && c.Moves[0] == "2g2f" {
			t.Fatalf("行き先が読めていないのに ▲2六歩 が候補に残っています: %+v", r.Candidates)
		}
	}
	if len(r.Candidates[0].Moves) != 0 {
		t.Errorf("1 位 = %v, want 0 手（行き先が見えるまで待つ）", r.Candidates[0].Moves)
	}
}

// ⚠️ **読めていないマスは一致度の分母にも入れないこと。**
//
// 分母にだけ入れると、手や頭が被るほど一致度が良く見える。一段目が丸ごと隠れた 1 枚で、
// 見えている 72 マスがぴったり合うなら一致度は 1。
func TestRankUnseenLeavesTotal(t *testing.T) {
	from := mustPos(t, connectHirate)
	target := "9/1r5b1/ppppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL"
	r, err := position.Rank(from, boardOf(t, target), position.ConnectOptions{
		Cost: evenCost(1), Unseen: topRow(),
	})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if r.Total != 72 {
		t.Errorf("Total = %v, want 72（読めていない 9 マスを除く）", r.Total)
	}
	top := r.Candidates[0]
	if len(top.Moves) != 1 || top.Moves[0] != "7g7f" || top.Fit != 1 {
		t.Fatalf("1 位 = %v (fit=%v), want [7g7f] で一致度 1", top.Moves, top.Fit)
	}
}

// ⚠️ **反対側で指された手は、頭が上辺に被っていても繋がること**（`Connect` の厳密一致）。
//
// 一段目が丸ごと「空き」に化けている。印が無ければ 9 枚の駒が消えたことになって繋がらない。
// ⚠️ **駒数の下界を外していること**の歯止めでもある（見えている範囲だけの駒数で下界を
// 取ると、読めていないマスから出入りした駒のぶんだけ足りずに取りこぼす）。
func TestConnectIgnoresUnseenCells(t *testing.T) {
	from := mustPos(t, connectHirate)
	moves := []string{"7g7f", "3c3d"}
	full := advance(t, from, moves...)
	target := "9" + full[strings.Index(full, "/"):]

	if r := connect(t, from, target, position.ConnectOptions{}); r.Stop == position.StopFound {
		t.Fatalf("前提が崩れています: 印が無いのに繋がりました: %+v", r)
	}

	r := connect(t, from, target, position.ConnectOptions{Unseen: topRow()})
	if r.Stop != position.StopFound || !r.Unique {
		t.Fatalf("Stop = %v unique=%v, want 一意に繋がる: %+v", r.Stop, r.Unique, r)
	}
	if strings.Join(r.Moves, " ") != strings.Join(moves, " ") {
		t.Fatalf("Moves = %v, want %v", r.Moves, moves)
	}
	if len(r.Fixed) != 0 {
		t.Errorf("読めていないマスが「覆したマス」に出ています: %+v", r.Fixed)
	}
}

// ⚠️ **読めていないマスがあるときは駒数の下界を使わないこと。**
//
// 一段目に隠れた △3一銀 が 2二 の馬を取った。見えている範囲だけで駒数を数えると
// 「後手の銀が湧いた（打った）」と「先手の駒が消えた（取られた）」の 2 つに見えて、
// 後手が 2 手指す（＝ 3 手かかる）ことになる。**実際は 1 手**なので、その下界は過大で
// 深さ 1 では「隔たりが大きすぎます」で落ちる。
func TestConnectUnseenDropsCountBound(t *testing.T) {
	from := after(t, connectHirate, "7g7f", "3c3d", "8h2b+")
	full := advance(t, from, "3a2b")
	target := "9" + full[strings.Index(full, "/"):]

	r := connect(t, from, target, position.ConnectOptions{MaxDepth: 1, Unseen: topRow()})
	if r.Stop != position.StopFound || !r.Unique || strings.Join(r.Moves, " ") != "3a2b" {
		t.Fatalf("Stop = %v moves=%v, want △2二同銀 が一意に繋がる: %+v", r.Stop, r.Moves, r)
	}
}

// ⚠️ **`Connect` でも、行き先が読めていない手順は解にしないこと。**
//
// 読めていないマスは食い違いに数えないので、裏付けまで素通しにすると**厳密一致のまま**
// 行き先を手の下へ逃がした手順が通る。
func TestConnectRefusesUnseenArrival(t *testing.T) {
	from := mustPos(t, connectHirate)
	target := advance(t, from, "7g7f")
	r := connect(t, from, target, position.ConnectOptions{Unseen: maskOf([2]int{5, 2})})
	if r.Stop == position.StopFound || len(r.Moves) != 0 {
		t.Fatalf("行き先が読めていないのに繋がりました: %+v", r)
	}
}

// ⚠️ **盤を回したら印も回すこと**（`CellCost.Rotate180` と同じ約束）。
func TestCellMaskRotate180(t *testing.T) {
	m := maskOf([2]int{0, 0}, [2]int{2, 7})
	got := m.Rotate180()
	if !got[8][8] || !got[6][1] || got[0][0] || got.Count() != 2 {
		t.Fatalf("回し方が違います: %v", got)
	}
	if (*position.CellMask)(nil).Rotate180() != nil {
		t.Error("nil を回したら nil のままのはず")
	}
	if (*position.CellMask)(nil).Count() != 0 {
		t.Error("nil は 0 マス")
	}
}
