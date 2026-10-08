package position_test

import (
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// resignStudy は平手から 3 手指した検討（本譜の先端を見ている）。
func resignStudy(t *testing.T) *position.Study {
	t.Helper()
	root, err := position.FromFullSFEN("lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL b - 1")
	if err != nil {
		t.Fatalf("FromFullSFEN: %v", err)
	}
	s := position.NewStudy(root)
	for _, m := range []string{"7g7f", "3c3d", "2g2f"} {
		if err := s.Play(m); err != nil {
			t.Fatalf("Play(%s): %v", m, err)
		}
	}
	return s
}

// **投了は本譜の先端で、手番の側が行う**（2026-10-09）。
//
// ⚠️ 終局は節点にしない（指し手ではない）。最後の手に「投了」が付くだけ。
// ⚠️ 自分で投了したものは取り消せる（棋譜の終局は取り消せない。下のテスト）。
func TestStudyResign(t *testing.T) {
	s := resignStudy(t)
	if !s.CanResign() {
		t.Fatal("本譜の先端なのに投了できない")
	}
	if err := s.Resign(); err != nil {
		t.Fatalf("Resign: %v", err)
	}
	nodes := s.Nodes()
	if len(nodes) != 3 {
		t.Fatalf("投了で節点が増えた: %d", len(nodes))
	}
	if nodes[2].End != "投了" || s.RecordEnd() != nodes[2].ID || !s.RecordEndByHand() {
		t.Fatalf("最後の手に投了が付かない: %+v end=%d hand=%v", nodes[2], s.RecordEnd(), s.RecordEndByHand())
	}
	if s.CanResign() || s.Resign() == nil {
		t.Error("終局したのにもう一度投了できる")
	}
	if got := len(s.MainLine()); got != 3 {
		t.Errorf("本譜の手数 = %d, want 3", got)
	}

	// 控えを通しても「自分の投了」のまま（取り消せること）。
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	back, err := position.RestoreStudy(snap)
	if err != nil {
		t.Fatalf("RestoreStudy: %v", err)
	}
	if !back.RecordEndByHand() {
		t.Error("控えから戻すと自分の投了だと分からなくなる")
	}

	if err := s.Unresign(); err != nil {
		t.Fatalf("Unresign: %v", err)
	}
	if s.RecordEnd() != 0 || s.RecordEndMark() != "" || s.RecordEndByHand() {
		t.Errorf("取り消しても終局が残る: end=%d mark=%q", s.RecordEnd(), s.RecordEndMark())
	}
	if !s.CanResign() {
		t.Error("取り消したのに投了できない")
	}
}

// ⚠️ **本譜の途中・根・枝の上では投了させない**（後ろの手がまとめて投了図以下に回る事故）。
func TestStudyResignOnlyAtMainTip(t *testing.T) {
	s := resignStudy(t)
	nodes := s.Nodes()
	if err := s.GoTo(nodes[1].ID); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if s.CanResign() || s.Resign() == nil {
		t.Error("本譜の途中で投了できる")
	}
	if err := s.GoTo(0); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if s.CanResign() || s.Resign() == nil {
		t.Error("1 手も指していない局面で投了できる")
	}
	// 2 手目から枝を生やして（本譜の 3 手目は ２六歩）、その先端では投了できない。
	if err := s.GoTo(nodes[1].ID); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if err := s.Play("6g6f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if err := s.Play("8c8d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if s.CanResign() || s.Resign() == nil {
		t.Error("枝の先端で投了できる")
	}
	if s.RecordEnd() != 0 {
		t.Errorf("断ったのに終局が立った: %d", s.RecordEnd())
	}
}

// ⚠️ **棋譜に書いてあった終局は取り消せない**（記録なので消せてはいけない）。
func TestStudyUnresignKeepsRecordedEnd(t *testing.T) {
	study, _, err := position.FromKIF(sampleKIF)
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	if study.RecordEndByHand() {
		t.Error("棋譜の終局が自分の投了として扱われている")
	}
	if err := study.Unresign(); err == nil {
		t.Error("棋譜の終局を取り消せてしまう")
	}
	if study.RecordEndMark() != "投了" {
		t.Errorf("断ったのに終局が消えた: %q", study.RecordEndMark())
	}
}
