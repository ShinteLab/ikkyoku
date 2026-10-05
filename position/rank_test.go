package position_test

import (
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// evenCost は全マス同じ費用の表（＝「何マス違うか」で測る）。
func evenCost(v float64) *position.CellCost {
	c := &position.CellCost{}
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			c[r][f] = v
		}
	}
	return c
}

// ⚠️ **認識が数マス外していても、正しい手が 1 位に来ること。**
//
// **これが「認識器が 100% でないと動かない」への答え。** 候補どうしは 2〜4 マスしか
// 違わないので、**残りのマスの認識ミスは全候補に等しく乗って相殺される**。
func TestRankPicksRightMoveDespiteErrors(t *testing.T) {
	from := mustPos(t, connectHirate)
	// ▲7六歩まで進んだ盤面。**さらに認識が 3 マス外している**
	// （9三・8三の歩を落とし、5五に無い駒を作った）。
	target := "lnsgkgsnl/1r5b1/2ppppppp/9/4p4/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL"

	r, err := position.Rank(from, boardOf(t, target), position.ConnectOptions{Cost: evenCost(1)})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(r.Candidates) == 0 {
		t.Fatal("候補が 1 つも出ませんでした")
	}
	top := r.Candidates[0]
	if len(top.Moves) != 1 || top.Moves[0] != "7g7f" {
		t.Fatalf("1 位 = %v, want [7g7f]（cost=%v）", top.Moves, top.Cost)
	}
	// ⚠️ **外したマスのぶんは費用として残る**（0 にはならない）。
	if top.Cost == 0 {
		t.Error("認識が外しているのに費用が 0 です")
	}
	// ⚠️ **それでも一致度は高いまま**（81 マス中 3 マスの話なので）。
	if top.Fit < 0.9 {
		t.Errorf("Fit = %v, want 0.9 以上（3 マスしか外していない）", top.Fit)
	}
	// ⚠️ **差が付いていること** —— これが「言い切ってよいか」の根拠。
	if r.Margin <= 0 {
		t.Errorf("Margin = %v, want 正の値", r.Margin)
	}
	if !r.Decided(position.DefaultMinFit, position.DefaultMinMargin) {
		t.Errorf("決め打てるはずが決められていません: fit=%v margin=%v", top.Fit, r.Margin)
	}
}

// ⚠️ **何も進んでいなければ「0 手」が 1 位に来ること。**
// 中継がまだ進んでいないのは普通にある。
func TestRankNoMove(t *testing.T) {
	from := mustPos(t, connectHirate)
	r, err := position.Rank(from, from.Board, position.ConnectOptions{Cost: evenCost(1)})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(r.Candidates[0].Moves) != 0 {
		t.Fatalf("1 位 = %v, want 0 手", r.Candidates[0].Moves)
	}
	if r.Candidates[0].Cost != 0 || r.Candidates[0].Fit != 1 {
		t.Errorf("ぴったり合っているのに cost=%v fit=%v", r.Candidates[0].Cost, r.Candidates[0].Fit)
	}
}

// ⚠️ **盤として読めていない画面（CM・解説）は `Readable` が偽になること。**
//
// **どの候補とも合わない**ので一致度が落ちる。ここで落とせないと、
// **CM の画面から適当な手を選んで棋譜に足す**ことになる。
func TestRankRejectsUnreadableFrame(t *testing.T) {
	from := mustPos(t, connectHirate)
	// 盤とは似ても似つかない「認識結果」。
	junk := "9/9/9/9/9/9/9/9/9"
	r, err := position.Rank(from, boardOf(t, junk), position.ConnectOptions{Cost: evenCost(1)})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if r.Readable(position.DefaultMinFit) {
		t.Fatalf("盤として読めないはずが通りました: fit=%v", r.Candidates[0].Fit)
	}
	if r.Decided(position.DefaultMinFit, position.DefaultMinMargin) {
		t.Fatal("読めていないのに決め打ちました")
	}
}

// ⚠️ **差が付かないときは決め打たないこと。**
//
// **一番よくあるのは「2 手進んだのに 1 手しか探していない」とき。** 撮った盤面が
// ▲7六歩 と ▲8六歩 の両方を含んでいると、**どちらの 1 手も同じだけ食い違う**ので
// 並びが決まらない。ここで 1 本選ぶと**指していない手を棋譜に足す**ことになる。
//
// ⚠️ **「読めていない」とは違う**（一致度は高いまま）。**次にすることが違う** ——
// こちらは**深く探す**か人に聞く、あちらは撮り直す。
func TestRankUndecidedWhenClose(t *testing.T) {
	from := mustPos(t, connectHirate)
	// ▲7六歩 と ▲8六歩 の両方が指されている（＝ 2 手進んでいる）。
	target := "lnsgkgsnl/1r5b1/ppppppppp/9/9/1PP6/P2PPPPPP/1B5R1/LNSGKGSNL"
	r, err := position.Rank(from, boardOf(t, target), position.ConnectOptions{Cost: evenCost(1)})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if r.Margin >= position.DefaultMinMargin {
		t.Fatalf("差が付きすぎています: margin=%v（%v / %v）",
			r.Margin, r.Candidates[0].Moves, r.Candidates[1].Moves)
	}
	if r.Decided(position.DefaultMinFit, position.DefaultMinMargin) {
		t.Fatal("決められないはずが決め打ちました")
	}
	// ⚠️ **それでも「盤としては読めている」こと**（撮り直す話ではない）。
	if !r.Readable(position.DefaultMinFit) {
		t.Errorf("盤としては読めているはず: fit=%v", r.Candidates[0].Fit)
	}
}

// ⚠️ **確信度が低いマスの食い違いは軽いこと**（費用の重み付けが効いていること）。
func TestRankWeightsByConfidence(t *testing.T) {
	from := mustPos(t, connectHirate)
	target := "lnsgkgsnl/1r5b1/1pppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL" // 9三が抜けている

	heavy := evenCost(1)
	light := evenCost(1)
	light[2][0] = 0.1 // 認識器が自信の無かったマス

	a, err := position.Rank(from, boardOf(t, target), position.ConnectOptions{Cost: heavy})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	b, err := position.Rank(from, boardOf(t, target), position.ConnectOptions{Cost: light})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if b.Candidates[0].Cost >= a.Candidates[0].Cost {
		t.Fatalf("確信度が効いていません: 重い=%v 軽い=%v",
			a.Candidates[0].Cost, b.Candidates[0].Cost)
	}
}

// ⚠️ **行き先に、指した側の駒が読めていない手を候補にしないこと**（`backed`。2026-10-05）。
//
// **実機の症状**: 指している手が 2八の飛車を隠して「空き」と読ませ、手の影が 4八の
// 「後手の香」と読まれた 1 枚から ▲4八飛を採った。4八はどの候補でも外れるマスなので
// 行き先にしても損が無く、**2八が空いたことだけで 1 位になっていた。**
func TestRankNeedsArrivalEvidence(t *testing.T) {
	from := mustPos(t, connectHirate)
	covered := "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B3l3/LNSGKGSNL"
	r, err := position.Rank(from, boardOf(t, covered), position.ConnectOptions{Cost: evenCost(1)})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(r.Candidates[0].Moves) != 0 {
		t.Fatalf("1 位 = %v, want 0 手（空いたことしか根拠が無い）", r.Candidates[0].Moves)
	}
	for _, c := range r.Candidates {
		if len(c.Moves) == 1 && c.Moves[0] == "2h4h" {
			t.Fatalf("行き先が後手の駒と読まれているのに ▲4八飛 が候補に残っています: %+v", r.Candidates)
		}
	}
}

// ⚠️ **行き先の駒の種類までは求めないこと**（先後が合えばよい）。
//
// 種類は認識が一番外すところで、**銀や飛車を香と読む中継が実際にある**（2026-10-05）。
// 求めると正しい手まで落ちる。
func TestRankIgnoresKindAtArrival(t *testing.T) {
	from := mustPos(t, connectHirate)
	// ▲7六歩。ただし 7六の歩を「先手の香」と読んでいる。
	target := "lnsgkgsnl/1r5b1/ppppppppp/9/9/2L6/PP1PPPPPP/1B5R1/LNSGKGSNL"
	r, err := position.Rank(from, boardOf(t, target), position.ConnectOptions{Cost: evenCost(1)})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	top := r.Candidates[0]
	if len(top.Moves) != 1 || top.Moves[0] != "7g7f" {
		t.Fatalf("1 位 = %v, want [7g7f]", top.Moves)
	}
	if !r.Decided(position.DefaultMinFit, position.DefaultMinMargin) {
		t.Errorf("決め打てるはずが決められていません: fit=%v margin=%v", top.Fit, r.Margin)
	}
}

// ⚠️ **取る手も同じ扱いであること**（行き先に指した側の駒が来る）。
//
// 取られた駒がそのまま読めているなら、取ったという根拠は無い。
func TestRankCaptureNeedsArrivalEvidence(t *testing.T) {
	// ▲7六歩 △3四歩 まで。次に ▲2二角成 で角を取れる。
	from := mustPos(t, "lnsgkgsnl/1r5b1/pppppp1pp/6p2/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL b - 3")

	// 8八の角が消えただけで、2二は後手の角のまま読めている → 取ったことにしない。
	stay := "lnsgkgsnl/1r5b1/pppppp1pp/6p2/9/2P6/PP1PPPPPP/7R1/LNSGKGSNL"
	r, err := position.Rank(from, boardOf(t, stay), position.ConnectOptions{Cost: evenCost(1)})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	for _, c := range r.Candidates {
		if len(c.Moves) == 1 && c.Moves[0] == "8h2b+" {
			t.Fatalf("2二が後手の駒と読まれているのに ▲2二角成 が候補に残っています: %+v", r.Candidates)
		}
	}

	// 2二に先手の駒（種類は外して銀）が読めている → 取った手が 1 位。
	took := "lnsgkgsnl/1r5S1/pppppp1pp/6p2/9/2P6/PP1PPPPPP/7R1/LNSGKGSNL"
	r, err = position.Rank(from, boardOf(t, took), position.ConnectOptions{Cost: evenCost(1)})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if top := r.Candidates[0]; len(top.Moves) != 1 || top.Moves[0] != "8h2b+" && top.Moves[0] != "8h2b" {
		t.Fatalf("1 位 = %v, want ▲2二角（成・不成）", top.Moves)
	}
}

// 手番が未決なら断ること（1 手も指せないので並べようが無い）。
func TestRankRefusesUnknownTurn(t *testing.T) {
	p, err := position.FromBoardSFEN("lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL")
	if err != nil {
		t.Fatalf("FromBoardSFEN: %v", err)
	}
	if _, err := position.Rank(p, p.Board, position.ConnectOptions{}); err == nil {
		t.Fatal("手番が未決なのに通りました")
	}
}

// BenchmarkRank は**時間の歯止め**。⚠️ **撮るたびに走る**ので、ここが重いと
// 1 枚ごとに待たされる。
func BenchmarkRank(b *testing.B) {
	p, err := position.FromFullSFEN(connectHirate)
	if err != nil {
		b.Fatal(err)
	}
	board, err := position.FromSFEN("lnsgkgsnl/1r5b1/1pppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL")
	if err != nil {
		b.Fatal(err)
	}
	opt := position.ConnectOptions{Cost: evenCost(1)}
	for i := 0; i < b.N; i++ {
		if _, err := position.Rank(p, board, opt); err != nil {
			b.Fatal(err)
		}
	}
}

func boardOf(t *testing.T, sfen string) *position.Board {
	t.Helper()
	b, err := position.FromSFEN(sfen)
	if err != nil {
		t.Fatalf("FromSFEN(%q): %v", sfen, err)
	}
	return b
}
