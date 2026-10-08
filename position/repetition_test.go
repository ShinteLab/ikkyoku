package position_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// playAll は手を順に指す（どれも指せること —— 4 回目を作る手も合法手）。
func playAll(t *testing.T, s *position.Study, moves ...string) {
	t.Helper()
	for i, m := range moves {
		if err := s.Play(m); err != nil {
			t.Fatalf("%d手目 %s が指せない: %v", i+1, m, err)
		}
	}
}

func studyFrom(t *testing.T, sfenStr string) *position.Study {
	t.Helper()
	root, err := position.FromFullSFEN(sfenStr)
	if err != nil {
		t.Fatalf("FromFullSFEN: %v", err)
	}
	return position.NewStudy(root)
}

// lastEnd は本譜の最後の手と、その終局の印。
func lastEnd(s *position.Study) position.Node {
	nodes := s.Nodes()
	return nodes[len(nodes)-1]
}

// **同じ局面が 4 回目に現れた手で千日手になる**（2026-10-09）。
//
// ⚠️ 根も 1 回に数える（根から指し始めた対局の初形）。⚠️ 3 回では終わらない。
// ⚠️ 4 回目を作る手も指せる（`legal.Moves` は履歴を知らない。ルールでも指した時点で終わる）。
func TestStudyRepetitionEndsGame(t *testing.T) {
	s := position.NewStudy(hirate(t))
	cycle := []string{"2h3h", "8b7b", "3h2h", "7b8b"}
	playAll(t, s, append(cycle, cycle...)...)
	if s.RecordEnd() != 0 {
		t.Fatalf("3 回目で終わった: %q", s.RecordEndMark())
	}
	playAll(t, s, cycle...)
	last := lastEnd(s)
	if s.RecordEnd() != last.ID || last.End != "千日手" || last.Number != 12 {
		t.Fatalf("12 手目で千日手にならない: end=%d %+v", s.RecordEnd(), last)
	}
	if !strings.Contains(last.EndNote, "4 回") {
		t.Errorf("説明が出ない: %q", last.EndNote)
	}
	// ⚠️ ルールで決まる終局は取り消せない（人の投了ではない）。
	if s.RecordEndByHand() || s.Unresign() == nil {
		t.Error("千日手を投了と同じに取り消せる")
	}
	if s.CanResign() {
		t.Error("千日手で終わったのに投了できる")
	}
	// その先に指した手は千日手図以下（変化）。
	playAll(t, s, "2h3h")
	if got := len(s.MainLine()); got != 12 {
		t.Errorf("本譜の手数 = %d, want 12（千日手で止まる）", got)
	}
	// 控えを通しても説明が残る。
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	back, err := position.RestoreStudy(snap)
	if err != nil {
		t.Fatalf("RestoreStudy: %v", err)
	}
	for _, n := range back.Nodes() {
		if n.End != "" && n.EndNote == "" {
			t.Errorf("控えから戻すと説明が消える: %+v", n)
		}
	}
}

// 4 回目の手を消したら千日手も消える（ルールから決まるので、その手が無ければ終局していない）。
func TestStudyRepetitionDroppedWithMove(t *testing.T) {
	s := position.NewStudy(hirate(t))
	cycle := []string{"2h3h", "8b7b", "3h2h", "7b8b"}
	playAll(t, s, append(append(cycle, cycle...), cycle...)...)
	if _, err := s.DropFrom(lastEnd(s).ID); err != nil {
		t.Fatalf("DropFrom: %v", err)
	}
	if s.RecordEnd() != 0 {
		t.Errorf("4 回目の手を消しても終局が残る: %q", s.RecordEndMark())
	}
	// 指し直せばまた千日手になる。
	playAll(t, s, "7b8b")
	if lastEnd(s).End != "千日手" {
		t.Errorf("指し直しても千日手にならない: %+v", lastEnd(s))
	}
}

// **連続王手の千日手は王手をかけ続けた側の反則負け**（2026-10-09）。
//
// ▲の飛車が 5二と 5一を往復して王手をかけ続け、△の玉は 1一と 1二を逃げるだけ。
// KIF の行は**手番の側から見た結果**なので、4 回目を作った手で文言が変わる:
//   - △の逃げで 4 回目 → 手番は▲（かけ続けた側）→「反則負け」
//   - ▲の王手で 4 回目 → 手番は△（かけられた側）→「反則勝ち」
func TestStudyPerpetualCheck(t *testing.T) {
	cycle := []string{"5b5a", "1a1b", "5a5b", "1b1a"}
	s := studyFrom(t, "8k/4R4/9/9/9/9/9/9/K8 b - 1")
	playAll(t, s, append(append(cycle, cycle...), cycle...)...)
	last := lastEnd(s)
	if last.End != "反則負け" || last.Number != 12 {
		t.Fatalf("△の逃げで 4 回目 → 反則負けのはず: %+v", last)
	}
	if !strings.Contains(last.EndNote, "▲の反則負け") {
		t.Errorf("どちらが負けたかが出ない: %q", last.EndNote)
	}

	// 王手をかけられた局面から始めると、▲の王手で 4 回目になる。
	cycle = []string{"1a1b", "5a5b", "1b1a", "5b5a"}
	s = studyFrom(t, "4R3k/9/9/9/9/9/9/9/K8 w - 1")
	playAll(t, s, append(append(cycle, cycle...), cycle...)...)
	last = lastEnd(s)
	if last.End != "反則勝ち" || !strings.Contains(last.EndNote, "▲の反則負け") {
		t.Fatalf("▲の王手で 4 回目 → 反則勝ち（▲の反則負け）のはず: %+v", last)
	}
}

// **棋譜に終局が書いていなくても、千日手ならそこで終わっている**（中継の棋譜は
// 千日手の行が載る前に取れることがある）。⚠️ 書いてあれば棋譜の終局が優先。
func TestFromKIFRepetitionWithoutEndMark(t *testing.T) {
	var b strings.Builder
	b.WriteString("手合割：平手\n手数----指手---------消費時間--\n")
	names := []string{"３八飛(28)", "７二飛(82)", "２八飛(38)", "８二飛(72)"}
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&b, "%4d %s\n", i+1, names[i%4])
	}
	study, load, err := position.FromKIF(b.String())
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	if load.Loaded != 12 {
		t.Fatalf("Loaded = %d (%s)", load.Loaded, load.Note)
	}
	if got := study.RecordEndMark(); got != "千日手" {
		t.Errorf("終局 = %q, want 千日手", got)
	}

	study, _, err = position.FromKIF(b.String() + "  13 千日手\n")
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	if got := study.RecordEndMark(); got != "千日手" || lastEnd(study).Number != 12 {
		t.Errorf("棋譜の千日手の行で終わらない: %q", got)
	}
}
