package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// guessMoves は追従が推測で足したのと同じ形（`followApply` → 推測の印）で本譜に手を据える。
func guessMoves(t *testing.T, s *StudyService, moves ...string) {
	t.Helper()
	s.mu.Lock()
	rev := s.rev
	s.mu.Unlock()
	if _, err := s.followApply(moves, rev, true); err != nil {
		t.Fatalf("followApply(%v): %v", moves, err)
	}
}

// studyAt は局面 sfen を根にした解析タブを作る（追従を始めたところ）。
func studyAt(t *testing.T, sfen string, moves ...string) *StudyService {
	t.Helper()
	p, err := position.FromFullSFEN(sfen)
	if err != nil {
		t.Fatalf("FromFullSFEN: %v", err)
	}
	for _, m := range moves {
		if err := p.ApplyMove(m); err != nil {
			t.Fatalf("ApplyMove(%q): %v", m, err)
		}
	}
	s := NewStudyService(NewPositionService())
	s.mu.Lock()
	s.study = position.NewStudy(p)
	s.newSessionLocked()
	s.mu.Unlock()
	return s
}

func mainLine(s *StudyService) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.study.MainLine()
}

// hasBranch は本譜の外に usi の手が残っているか（差し替えた元の手は枝に残す）。
func hasBranch(s *StudyService, usi string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.study.Nodes() {
		if n.USI == usi && !n.Main {
			return true
		}
	}
	return false
}

// ⚠️ **推測で足した末尾の手が撮った盤と食い違い、戻した局面からなら繋がるなら、2 枚目で差し替えること**
// （`followRewindCheck`。2026-10-08）。
//
// 形は 2026-10-05 の実機（手が覆った 1 枚から ▲4八飛 を足し、本当は別の手だった）。先端（▲4八飛 のあと）からは
// 後手が往復して戻る 4 手でしか辻褄が合わない。戻せば ▲7六歩 の 1 手でぴったり合う。
func TestFollowRewindReplacesGuessedTail(t *testing.T) {
	s, _ := following(t)
	guessMoves(t, s, "2h4h")
	board := boardAfter(t, "7g7f")

	a, err := s.FollowAuto(board, evenConf(0.9), nil, []PromoEvidence{})
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Applied || !a.Pending || a.Rewound {
		t.Fatalf("1 枚目で差し替えています（2 枚目で確かめるはず）: %+v", a)
	}
	if got := mainLine(s); !slices.Equal(got, []string{"2h4h"}) {
		t.Fatalf("1 枚目で本譜が動きました: %v", got)
	}

	a, err = s.FollowAuto(board, evenConf(0.9), nil, []PromoEvidence{})
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Applied || !a.Rewound || !strings.Contains(a.Note, "差し替えました") {
		t.Fatalf("2 枚目で差し替えていません: %+v", a)
	}
	if got := mainLine(s); !slices.Equal(got, []string{"7g7f"}) {
		t.Fatalf("本譜 = %v, want [7g7f]", got)
	}
	if !hasBranch(s, "2h4h") {
		t.Errorf("差し替えた元の手（▲4八飛）が枝に残っていません")
	}
}

// ⚠️ **変わっていない 1 枚（`FollowConfirm`）でも、控えた差し替えを確かめられること**（足す手の控えと同じ）。
func TestFollowRewindConfirmsOnUnchangedFrame(t *testing.T) {
	s, _ := following(t)
	guessMoves(t, s, "2h4h")
	if a, err := s.FollowAuto(boardAfter(t, "7g7f"), evenConf(0.9), nil, []PromoEvidence{}); err != nil || !a.Pending {
		t.Fatalf("1 枚目で控えていません: %+v (%v)", a, err)
	}
	a, err := s.FollowConfirm()
	if err != nil {
		t.Fatalf("FollowConfirm: %v", err)
	}
	if !a.Rewound || !slices.Equal(mainLine(s), []string{"7g7f"}) {
		t.Fatalf("変わっていない 1 枚で差し替えていません: %+v 本譜=%v", a, mainLine(s))
	}
}

// ⚠️ **戻すだけで撮った盤になるなら、取り消す（枝に下げる）こと**。
func TestFollowRewindUndoesGuessedMove(t *testing.T) {
	s, _ := following(t)
	guessMoves(t, s, "7g7f")
	guessMoves(t, s, "3c3d")
	guessMoves(t, s, "2h4h") // 推測で足したが、実際は指されていない
	board := boardAfter(t, "7g7f", "3c3d")

	var a FollowAuto
	for i := 0; i < 2; i++ {
		var err error
		if a, err = s.FollowAuto(board, evenConf(0.9), nil, []PromoEvidence{}); err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
	}
	if !a.Rewound || !strings.Contains(a.Note, "取り消しました") {
		t.Fatalf("取り消していません: %+v", a)
	}
	if got := mainLine(s); !slices.Equal(got, []string{"7g7f", "3c3d"}) {
		t.Fatalf("本譜 = %v, want [7g7f 3c3d]", got)
	}
	if !hasBranch(s, "2h4h") {
		t.Errorf("取り消した手が枝に残っていません")
	}
}

// ⚠️ **印の無い手は差し替えないこと**（本譜のロックで守る側。ぴったり一致した手・棋譜の手・人が指した手）。
func TestFollowRewindKeepsUnguessedMoves(t *testing.T) {
	s, _ := following(t)
	if _, err := s.Play("2h4h"); err != nil { // 人が盤で指した手（推測の印は無い）
		t.Fatalf("Play: %v", err)
	}
	board := boardAfter(t, "7g7f")
	for i := 0; i < 3; i++ {
		a, err := s.FollowAuto(board, evenConf(0.9), nil, []PromoEvidence{})
		if err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
		if a.Rewound {
			t.Fatalf("印の無い手を差し替えました: %+v", a)
		}
	}
	if got := mainLine(s); len(got) == 0 || got[0] != "2h4h" {
		t.Fatalf("人が指した手が本譜から外れました: %v", got)
	}
}

// ⚠️ **手や頭が被った 1 枚では差し替えないこと**（読みが崩れていて、正しい末尾を壊す）。
func TestFollowRewindIgnoresCoveredFrame(t *testing.T) {
	s, _ := following(t)
	guessMoves(t, s, "2h4h")
	board := boardAfter(t, "7g7f")
	covered := hiddenAt([2]int{0, 0}) // 9一（関係のないマス）
	for i := 0; i < 3; i++ {
		a, err := s.FollowAuto(board, evenConf(0.9), covered, []PromoEvidence{})
		if err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
		if a.Rewound {
			t.Fatalf("手が被った 1 枚で差し替えました: %+v", a)
		}
	}
	if got := mainLine(s); len(got) == 0 || got[0] != "2h4h" {
		t.Fatalf("本譜が動きました: %v", got)
	}
}

// ⚠️ **1 枚だけでは差し替えないこと。** 次の 1 枚が先端とぴったり合えば（読みの揺れだった）控えを捨てる。
func TestFollowRewindNeedsTwoFrames(t *testing.T) {
	s, _ := following(t)
	guessMoves(t, s, "2h4h")
	if a, err := s.FollowAuto(boardAfter(t, "7g7f"), evenConf(0.9), nil, []PromoEvidence{}); err != nil || !a.Pending {
		t.Fatalf("1 枚目で控えていません: %+v (%v)", a, err)
	}
	// 次の 1 枚は ▲4八飛 のままの盤（揺れが収まった）。
	a, err := s.FollowAuto(boardAfter(t, "2h4h"), evenConf(0.9), nil, []PromoEvidence{})
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Rewound {
		t.Fatalf("1 枚だけの答えで差し替えました: %+v", a)
	}
	if a2, err := s.FollowConfirm(); err != nil || a2.Rewound {
		t.Fatalf("捨てたはずの差し替えを確かめました: %+v (%v)", a2, err)
	}
	if got := mainLine(s); !slices.Equal(got, []string{"2h4h"}) {
		t.Fatalf("本譜 = %v, want [2h4h]", got)
	}
}

// furibishaFrame / furibishaFrameConf は 2026-10-08 の中継の録画 `follow/20261008-103837/013-4d3e_2g2f-guess.png` を
// 読み直した盤と確信度（`position` の `TestConnectRepairIgnoresStaleKindMisreads` と同じ 1 枚）。
const furibishaFrame = "kn5nl/lsgg3l1/ppppbp2p/4p4/P1PP2s2/1PP4P1/2PSPP2P/4BGGS1/L6NP"

var furibishaFrameConf = []float64{
	1.00, 0.65, 0.15, 0.15, 0.15, 0.15, 0.15, 0.42, 1.00,
	1.00, 0.81, 0.89, 1.00, 0.15, 0.15, 0.15, 0.63, 0.15,
	1.00, 1.00, 1.00, 1.00, 0.75, 1.00, 0.15, 0.15, 1.00,
	0.15, 0.15, 0.15, 0.15, 1.00, 0.15, 0.15, 0.15, 0.15,
	1.00, 0.15, 1.00, 1.00, 0.15, 0.15, 0.82, 0.15, 0.15,
	0.15, 0.41, 1.00, 0.15, 0.15, 0.15, 0.15, 1.00, 0.15,
	0.15, 0.15, 0.60, 0.85, 1.00, 1.00, 0.15, 0.15, 1.00,
	0.15, 0.15, 0.15, 0.15, 0.39, 0.62, 0.60, 1.00, 0.15,
	1.00, 0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 1.00, 0.79,
}

// ⚠️ **実機で手番がずれた 2 手（△同銀 ▲2六歩）を、△2六歩 ▲同歩 △同銀 に差し替えること**（2026-10-08 の中継）。
//
// ▲3五同歩 のあと、本当は △2六歩 ▲同歩 △同銀 と進んだが、追従は △同銀 ▲2六歩（同歩ではない）を推測で足し、
// △2六歩 が抜けて手番が 1 つずれた。盤では 2五 に後手の歩が残ったまま（撮った盤では空き。0.15）。
func TestFollowRewindRecoversMissedMove(t *testing.T) {
	s := studyAt(t, "kn5nl/lsgg3r1/ppppbp2p/4ps3/P1PP2pp1/1BR3P2/2NSPP1PP/4GGKS1/L6NL b P 1", "3f3e")
	guessMoves(t, s, "4d3e")
	guessMoves(t, s, "2g2f")

	var a FollowAuto
	for i := 0; i < 2; i++ {
		var err error
		if a, err = s.FollowAuto(furibishaFrame, furibishaFrameConf, nil, []PromoEvidence{}); err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
	}
	if !a.Rewound {
		t.Fatalf("差し替えていません: %+v", a)
	}
	if got := mainLine(s); !slices.Equal(got, []string{"2e2f", "2g2f", "4d3e"}) {
		t.Fatalf("本譜 = %v, want [2e2f 2g2f 4d3e]", got)
	}
}

// glareFrame / glareFrameConf は 2026-10-08 の中継の録画 `follow/20261008-140929/018-B@1e_B@2e_2f2e-guess.png` を
// 読み直した盤と確信度。**盤の右側（1〜2 筋）に光が差し込み**、空きの 1五 を「先手の香」（0.40）と読んでいる。
// 実際は ▲2五歩 だけで、先手・後手とも角を 1 枚ずつ持っている。
const glareFrame = "ln1g3pp/1ks3g2/1ppppp1pl/p4sr2/6pPL/PPP2P3/1SPPPSP1P/1KG1P2R1/L6PL"

var glareFrameConf = []float64{
	0.86, 1.00, 0.15, 0.83, 0.15, 0.15, 0.15, 0.62, 0.60,
	0.15, 1.00, 0.61, 0.15, 0.15, 0.15, 0.80, 0.15, 0.15,
	0.15, 1.00, 1.00, 1.00, 1.00, 1.00, 0.15, 1.00, 0.42,
	1.00, 0.15, 0.15, 0.15, 0.15, 0.61, 1.00, 0.15, 0.15,
	0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 0.81, 0.82, 0.40,
	1.00, 0.80, 1.00, 0.15, 0.15, 1.00, 0.15, 0.15, 0.15,
	0.15, 1.00, 0.41, 1.00, 1.00, 1.00, 1.00, 0.15, 0.81,
	0.15, 0.63, 1.00, 0.15, 0.40, 0.15, 0.15, 0.82, 0.15,
	1.00, 0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 0.68, 0.79,
}

// ⚠️ **読みの弱い駒（光の差し込んだマスの幻の駒）を、手を足して説明しないこと**（2026-10-08 に実機で踏んだ）。
//
// **実機の症状**: 光の差し込んだ 1五 を「先手の香」（0.40）と読んだ 1 枚で、推測で足した ▲2五歩 を
// ▲1五角打 △2五角打 ▲同歩 に差し替えた（幻の駒を角打ちで説明した。駒台は読んでいないので持ち駒が狂っても気づけない）。
// ▲2五歩 のまま 1五 を読み違いとして扱うのが正しい。
func TestFollowRewindIgnoresWeakPhantomPiece(t *testing.T) {
	s := studyAt(t, "ln1g3nl/1ks3g2/1ppppp1pp/p4sr2/6p2/PPP2P1P1/1SNPPSP1P/1KG1G2R1/L6NL b Bb 1")
	guessMoves(t, s, "2f2e")
	for i := 0; i < 3; i++ {
		a, err := s.FollowAuto(glareFrame, glareFrameConf, nil, []PromoEvidence{})
		if err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
		if a.Rewound || a.Applied {
			t.Fatalf("%d 枚目: 光の差し込んだ 1 枚から手を足しました: %+v", i+1, a)
		}
	}
	if got := mainLine(s); !slices.Equal(got, []string{"2f2e"}) {
		t.Fatalf("本譜 = %v, want [2f2e]", got)
	}
}
