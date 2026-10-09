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

// ⚠️ **何も言えない 1 枚では、控えた差し替えを捨てないこと**（2026-10-09。`dropRewindUnlessFits`）。
//
// **実機の症状**: △7二角打 を △7二金 に差し替える案が 3 回出たが、そのたびに次に読んだ 1 枚（光ったマスが多く
// 「隔たりが大きすぎます」）で捨てられ、差し替わるまで 64 秒かかった。その 1 枚は先端を支持していない。
// ⚠️ **残した差し替えは、変わっていない 1 枚（`FollowConfirm`）では据えないこと**（同じ差し替えを言う 1 枚で据える）。
func TestFollowRewindSurvivesSilentFrame(t *testing.T) {
	s, _ := following(t)
	guessMoves(t, s, "2h4h")
	if a, err := s.FollowAuto(boardAfter(t, "7g7f"), evenConf(0.9), nil, []PromoEvidence{}); err != nil || !a.Pending {
		t.Fatalf("1 枚目で控えていません: %+v (%v)", a, err)
	}
	// 何も言えない 1 枚（5 段目に幻の駒が並んだ。どこからも繋がらないが、▲7六歩 の側のほうが合っている）。
	noisy := strings.Replace(boardAfter(t, "7g7f"), "/9/2P6/", "/lllllllll/2P6/", 1)
	if a, err := s.FollowAuto(noisy, evenConf(0.9), nil, []PromoEvidence{}); err != nil || a.Rewound || a.Applied {
		t.Fatalf("何も言えない 1 枚で差し替えたか足しました: %+v (%v)", a, err)
	}
	if a, err := s.FollowConfirm(); err != nil || a.Rewound {
		t.Fatalf("残しているだけの差し替えを、変わっていない 1 枚で据えました: %+v (%v)", a, err)
	}
	a, err := s.FollowAuto(boardAfter(t, "7g7f"), evenConf(0.9), nil, []PromoEvidence{})
	if err != nil || !a.Rewound {
		t.Fatalf("同じ差し替えを言う 1 枚で据えていません: %+v (%v)", a, err)
	}
	if got := mainLine(s); !slices.Equal(got, []string{"7g7f"}) {
		t.Fatalf("本譜 = %v, want [7g7f]", got)
	}
}

// cellsRewindLine は △3二角打 を推測で足す前の手順（▲7六歩 △3四歩 ▲2二角成 △同銀 ▲2六歩。後手は角を持っている）。
var cellsRewindLine = []string{"7g7f", "3c3d", "8h2b+", "3a2b", "2g2f"}

// cellsRewindStudy は cellsRewindLine を指し、△3二角打 を推測で足して、その 1 枚を速い経路の比べる相手にした解析タブ。
func cellsRewindStudy(t *testing.T) *StudyService {
	t.Helper()
	s, _ := following(t)
	for _, m := range cellsRewindLine {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%q): %v", m, err)
		}
	}
	guessMoves(t, s, "B*3b")
	s.markBase()
	return s
}

// ⚠️ **速い経路でも、推測で足した末尾の手を差し替えること**（2026-10-09。`followCellsRewind`）。
//
// **実機の形**（手で操作するデジタル盤）: 金をドラッグしているあいだ、元のマスに金が残ったまま行き先に金の絵が重なり、
// 変わったマスが行き先だけになって角打ちを推測で足した。次の 1 枚で元のマス 4一 が空き、金の動ける 3一・4二・5二 が光った。
// 先端から先には合う手順が無いが、角打ちを戻せば △3二金 で説明が付く（3二 の見た目は金のまま変わらない）。
// ⚠️ 行き先（3二）を読んで後手の駒を確かめてから控え、⚠️ 2 枚目で差し替えること。
func TestFollowCellsRewindsGuessedDrop(t *testing.T) {
	s := cellsRewindStudy(t)
	changed := cellIdx("4a", "3a", "4b", "5b")
	a := followCellsRead(t, s, changed, false, append(slices.Clone(cellsRewindLine), "4a3b")...)
	if !a.Pending || a.Rewound || a.Applied {
		t.Fatalf("1 枚目で控えていないか、差し替えました: %+v", a)
	}
	if got := mainLine(s); got[len(got)-1] != "B*3b" {
		t.Fatalf("1 枚目で本譜が動きました: %v", got)
	}
	a, err := s.FollowConfirm()
	if err != nil || !a.Rewound {
		t.Fatalf("変わっていない 1 枚で差し替えていません: %+v (%v)", a, err)
	}
	if got := mainLine(s); !slices.Equal(got, append(slices.Clone(cellsRewindLine), "4a3b")) {
		t.Fatalf("本譜 = %v", got)
	}
	if !hasBranch(s, "B*3b") {
		t.Error("元の手（△3二角打）が枝に残っていません")
	}
}

// ⚠️ **速い経路で控えた差し替えも、何も言えない 1 枚では捨てず、同じ差し替えを言う 1 枚で据えること**。
func TestFollowCellsRewindSurvivesUnclearFrame(t *testing.T) {
	s := cellsRewindStudy(t)
	changed := cellIdx("4a", "3a", "4b", "5b")
	seen := append(slices.Clone(cellsRewindLine), "4a3b")
	if a := followCellsRead(t, s, changed, false, seen...); !a.Pending {
		t.Fatalf("1 枚目で控えていません: %+v", a)
	}
	// 何も説明できない 1 枚（先手の 9九 だけが変わった）。
	if a, err := s.FollowCells(cellIdx("9i"), nil, nil); err != nil || a.Rewound || a.Applied || a.Pending {
		t.Fatalf("説明できない 1 枚で何かしました: %+v (%v)", a, err)
	}
	if a, err := s.FollowConfirm(); err != nil || a.Rewound {
		t.Fatalf("残しているだけの差し替えを、変わっていない 1 枚で据えました: %+v (%v)", a, err)
	}
	a := followCellsRead(t, s, changed, false, seen...)
	if !a.Rewound {
		t.Fatalf("同じ差し替えを言う 1 枚で据えていません: %+v", a)
	}
}

// ⚠️ **印の無い手は速い経路でも差し替えないこと**（今までどおり「説明できる手がありません」で待つ）。
func TestFollowCellsRewindKeepsUnguessedMoves(t *testing.T) {
	s, _ := following(t)
	for _, m := range append(slices.Clone(cellsRewindLine), "B*3b") {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%q): %v", m, err)
		}
	}
	s.markBase()
	a, err := s.FollowCells(cellIdx("4a", "3a", "4b", "5b"), nil, nil)
	if err != nil || a.Pending || a.Rewound || len(a.ReadCells) > 0 || !a.NoMoves {
		t.Fatalf("人が指した手を差し替えようとしました: %+v (%v)", a, err)
	}
}

// ⚠️ **打つ手は、候補が 1 通りでも行き先の種類を確かめること**（2026-10-09。`followDropsFit`）。
//
// **実機の症状**: 後手の持ち駒が角だけで、変わったマスが 7二 だけの 1 枚から △7二角打 を足した。7二 は「後手の金」と
// 読めていたが、候補が 1 通りだったので先後しか見なかった（銀も持っていれば種類を読んで落ちていた）。
// 金と読めたら控えずに 81 マスを読み、角と読めたら今までどおり控えること。
func TestFollowCellsDropChecksKindEvenWhenUnique(t *testing.T) {
	for name, tc := range map[string]struct {
		seen    string // 3二 に見えた駒（平手から cellsRewindLine のあとに指した手）
		pending bool
	}{
		"金と読めた": {"4a3b", false},
		"角と読めた": {"B*3b", true},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := following(t)
			for _, m := range cellsRewindLine {
				if _, err := s.Play(m); err != nil {
					t.Fatalf("Play(%q): %v", m, err)
				}
			}
			s.markBase()
			a := followCellsRead(t, s, cellIdx("3b"), false, append(slices.Clone(cellsRewindLine), tc.seen)...)
			if a.Pending != tc.pending || a.Applied {
				t.Fatalf("pending = %v, want %v: %+v", a.Pending, tc.pending, a)
			}
			if !tc.pending && a.NoMoves {
				t.Fatalf("種類が違うだけなら待たずに 81 マスを読むこと（NoMoves を立てない）: %+v", a)
			}
		})
	}
}
