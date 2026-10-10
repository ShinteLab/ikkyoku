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

// ⚠️ **成駒の載ったマスの読みで、推測の手を取り消さないこと**（2026-10-09。`promotedCells`）。
//
// **実機の症状**（中継）: ▲1一角成 のあと、1一 の馬を「後手の歩」（0.78）と読んだ 1 枚が 2 枚続き、取り消すと 1一 に後手の香が
// 戻って「合う」ので取り消した。2二 に角が無いことは空きの読みなので 0.15 しか数えない。成駒は読めないものとして扱い、
// 先端の盤で成駒が載っているマスは比べない。
func TestFollowRewindIgnoresPromotedPieceMisread(t *testing.T) {
	s := studyAt(t, "4k3l/7B1/9/9/9/9/9/9/4K4 b - 1")
	guessMoves(t, s, "2b1a+")
	// 撮った 1 枚: 1一 の馬を後手の歩と読んだ。2二 は空き（確信度 0）、5一・5九 の玉ははっきり読めている。
	frame := "4k3p/9/9/9/9/9/9/9/4K4"
	conf := make([]float64, 81)
	conf[4], conf[76] = 1, 1 // 5一・5九
	conf[8] = 0.78           // 1一
	for i := 0; i < 3; i++ {
		a, err := s.FollowAuto(frame, conf, nil, []PromoEvidence{})
		if err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
		if a.Rewound || a.Pending {
			t.Fatalf("%d 枚目: 成駒の読み違いで ▲1一角成 を取り消そうとしました: %+v", i+1, a)
		}
	}
	if got := mainLine(s); !slices.Equal(got, []string{"2b1a+"}) {
		t.Fatalf("本譜 = %v, want [2b1a+]", got)
	}
}

// ⚠️ **推測で積んだ手を、比べる相手の 1 枚の局面から説明し直して差し替えること**（2026-10-10 に中継で踏んだ）。
//
// 中継で大盤のあいだに 6 手進み、追従は 4 手の修復 ▲4五歩 △2五歩 ▲4四歩 △2三飛 に ▲2五桂 △同桂 を継ぎ足した。
// 正しい ▲2五桂 △4五桂 ▲同歩 …… とは先後と駒の有無では同じ盤で、2五 が歩（1.00）か桂かでしか分かれない。
// ⚠️ **戻るのは推測の手の末尾 3 手より深い**（6 手）ので、比べる相手の局面（先端とぴったり合った △2一飛 の 1 枚）まで戻る。
// ⚠️ **種類で選ぶのは、その 1 枚の種類の読みを信じてよいとき**（動いていない駒の読み違いは 1一 の 1 マスだけ）。
func TestFollowRewindFromBaseAcrossFrames(t *testing.T) {
	s := hakureiStudy(t)
	s.markBase()
	guessMoves(t, s, "4f4e", "2d2e", "4e4d", "2a2c")
	guessMoves(t, s, "3g2e", "3c2e")

	var a FollowAuto
	for i := 0; i < 2; i++ {
		var err error
		if a, err = s.FollowAuto(hakureiShot, hakureiConf, nil, []PromoEvidence{}); err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
	}
	if !a.Rewound {
		t.Fatalf("差し替えていません: %+v", a)
	}
	if got := tipSFEN(t, s); !strings.HasPrefix(got, hakureiWant) {
		t.Errorf("局面 = %s, want %s…", got, hakureiWant)
	}
	// 1 枚からは順番が決まらない（▲4五歩 △2五桂 …… も同じ局面に行き着く）。比べる相手の局面からの答えは、足し済みの手と
	// いちばん長く揃う順番を採る（いちばん浅く戻す。2026-10-11）ので、▲4五歩 は残り、△2五歩 から先を差し替える。
	if !hasBranch(s, "2d2e") {
		t.Errorf("差し替えた元の手が枝に残っていません")
	}
}

// ⚠️ **種類の読みを信じられない 1 枚では、種類で差し替えないこと**（2026-10-10。`position.KindsTrusted`）。
//
// 上と同じ形で、動いていない駒の種類を 2 マス読み違えた 1 枚（1一 の香を飛・9一 の香を飛）。書体に慣れていない中継では、
// 種類を外した読みにも確信度 1.00 が出る（録画 211 手の読み直し）。
func TestFollowRewindIgnoresKindsWhenUntrusted(t *testing.T) {
	s := hakureiStudy(t)
	s.markBase()
	guessMoves(t, s, "4f4e", "2d2e", "4e4d", "2a2c")
	guessMoves(t, s, "3g2e", "3c2e")
	shot := "r" + hakureiShot[1:]

	for i := 0; i < 2; i++ {
		a, err := s.FollowAuto(shot, hakureiConf, nil, []PromoEvidence{})
		if err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
		if a.Rewound {
			t.Fatalf("種類を信じられない 1 枚で、種類で差し替えました: %+v", a)
		}
	}
}

// ⚠️ **速い経路の差し替えで、消える手の行き先に元の手の駒が読めたら差し替えないこと**（2026-10-10 に中継で踏んだ）。
//
// **実機の症状**（実盤の中継）: ▲7九銀打 △5九金打 ▲同玉 を正しく足し、その 1 枚を比べる相手にしたあと、変わったマスが
// 7九 だけの 1 枚（銀に手か影が掛かった）で、速い経路の差し替えがこの 3 手を ▲5九玉 の 1 手に替えた（7九 の銀が消えたことに
// すれば説明が付く）。読んだのは新しい手の行き先 5九 だけで、銀が写ったままの 7九 は読んでいなかった。ここでは同じ形を
// 平手から作る（▲5五角打 △5八角打 ▲同玉 を ▲5八玉 に替えようとする。5五 には角が写ったまま）。
func TestFollowCellsRewindKeepsMoveWhosePieceIsSeen(t *testing.T) {
	s, _ := following(t)
	line := []string{"7g7f", "3c3d", "8h2b+", "3a2b"}
	for _, m := range line {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%q): %v", m, err)
		}
	}
	guessMoves(t, s, "B*5e", "B*5h", "5i5h")
	s.markBase() // 3 手を指した 1 枚を比べる相手にした
	seen := append(slices.Clone(line), "B*5e", "B*5h", "5i5h")

	changed := cellIdx("5e") // 5五 に影が掛かっただけ
	a, err := s.FollowCells(changed, nil, nil)
	if err != nil {
		t.Fatalf("FollowCells: %v", err)
	}
	if len(a.ReadCells) > 0 {
		a, err = s.FollowCells(changed, readAfter(t, false, a.ReadCells, seen...), nil)
		if err != nil {
			t.Fatalf("FollowCells: %v", err)
		}
	}
	if a.Rewound || a.Pending {
		t.Fatalf("角が読めている 5五 の手を差し替えようとしました: %+v", a)
	}
	if a2, err := s.FollowConfirm(); err != nil || a2.Rewound {
		t.Fatalf("差し替えました: %+v (%v)", a2, err)
	}
	if got := mainLine(s); !slices.Equal(got, seen) {
		t.Fatalf("本譜 = %v, want %v", got, seen)
	}
}

// ⚠️ **速い経路の差し替えで、種類の変わるマスの読みが元の盤と合い、新しい盤と合わなければ差し替えないこと**（2026-10-10 に中継で踏んだ）。
//
// **実機の症状**（実盤の中継）: ▲5九歩打 のあと、変わったマスが 5八 だけの 1 枚（△5八桂成 の手が掛かった）で、速い経路の差し替えが
// ▲5九歩打 を ▲5九金（5八 の金が動いた）に替えた。5九 は「歩」と読めていたが、差し替えは先後と駒の有無しか比べない。
// ここでは同じ形を平手から作る（▲4八角打 を、4九 の金が 4八 へ動いた手に替えようとする。4八 には角が写ったまま）。
// デジタル盤の △7二角打 → △7二金（7二 が金と読める。`TestFollowCellsRewindsGuessedDrop`）は新しい盤と合うので今までどおり替える。
func TestFollowCellsRewindKeepsDropWhoseKindIsSeen(t *testing.T) {
	s, _ := following(t)
	line := []string{"7g7f", "3c3d", "8h2b+", "3a2b"}
	for _, m := range line {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%q): %v", m, err)
		}
	}
	guessMoves(t, s, "B*4h")
	s.markBase() // 角を打った 1 枚を比べる相手にした
	seen := append(slices.Clone(line), "B*4h")

	changed := cellIdx("4i") // 4九 に手が掛かっただけ
	a, err := s.FollowCells(changed, nil, nil)
	if err != nil {
		t.Fatalf("FollowCells: %v", err)
	}
	if len(a.ReadCells) > 0 {
		a, err = s.FollowCells(changed, readAfter(t, false, a.ReadCells, seen...), nil)
		if err != nil {
			t.Fatalf("FollowCells: %v", err)
		}
	}
	if a.Rewound || a.Pending {
		t.Fatalf("角と読めている 4八 を金に差し替えようとしました: %+v", a.Reason)
	}
	if a2, err := s.FollowConfirm(); err != nil || a2.Rewound {
		t.Fatalf("差し替えました: %+v (%v)", a2.Reason, err)
	}
	if got := mainLine(s); !slices.Equal(got, seen) {
		t.Fatalf("本譜 = %v, want %v", got, seen)
	}
}
