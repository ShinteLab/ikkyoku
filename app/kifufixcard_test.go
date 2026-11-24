package app

import (
	"strings"
	"testing"
)

// **カードの「⚠ N手目」は、直しの表で N 手目を越えられたときだけ「修正済」になる**（2026-11-25）。
// ⚠️ 直していない・取り消した・別の鍵のカード、では出ない。⚠️ 今の検討を離れても控えから答える。
func TestFixedNoteForCard(t *testing.T) {
	s := NewStudyService(NewPositionService())
	k := NewKifuService(s)
	k.Store = NewStudyStore(t.TempDir(), s)
	card := cardOf(3, "", "")

	load, err := s.LoadKifu(miswrittenKI2)
	if err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	if got := k.FixedNote(card, true); got != "" {
		t.Errorf("直していないのに修正済: %q", got)
	}
	if _, err := s.GoTo(nodeByUSI(t, load.State, "3c3d", true)); err != nil {
		t.Fatal(err)
	}
	st, err := s.Play("7i6h")
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	fixed := nodeByUSI(t, st, "7i6h", true)
	if _, err := s.FixMove(fixed, "replace"); err != nil {
		t.Fatalf("FixMove: %v", err)
	}
	got := k.FixedNote(card, true)
	if !strings.Contains(got, "直し 1 か所") {
		t.Errorf("直したのに修正済にならない: %q", got)
	}
	// 取り消したら出ない。直し直せばまた出る。
	if _, err := s.FixMove(fixed, "unfix"); err != nil {
		t.Fatalf("unfix: %v", err)
	}
	if got := k.FixedNote(card, true); got != "" {
		t.Errorf("取り消したのに修正済: %q", got)
	}
	if _, err := s.FixMove(fixed, "replace"); err != nil {
		t.Fatalf("FixMove（直し直し）: %v", err)
	}
	if got := k.FixedNote(card, true); got == "" {
		t.Errorf("直し直したのに修正済にならない")
	}
	// 取得したカードとしては別の鍵なので、この直しは当たらない。
	if got := k.FixedNote(cardOf(3, "url", "x"), false); got != "" {
		t.Errorf("別の鍵のカードに修正済: %q", got)
	}
	// ⚠️ kicho の ⚠ が止まった手より前なら、直しはそこを越えていない（元から越えていたことにしない）。
	if got := k.FixedNote(cardOf(2, "", ""), true); got != "" {
		t.Errorf("止まる手より前の ⚠ に修正済: %q", got)
	}

	// 今の検討を別の棋譜にしても、控えから答える。
	k.Store.flush()
	if _, err := s.LoadKifu("▲７六歩 △３四歩\n"); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	if got := k.FixedNote(card, true); got == "" {
		t.Errorf("控えの直しを読めていない")
	}
}

// **直しの表が無ければ、原本を読まずに空**（ふつうのカードは何も起きない）。
func TestFixedNoteNoFixes(t *testing.T) {
	if got := fixedNote(miswrittenKI2, 3, nil); got != "" {
		t.Errorf("直しが無いのに修正済: %q", got)
	}
}

// cardOf は誤記の棋譜（`miswrittenKI2`）のカード。
func cardOf(errorMove int, source, sourceID string) GameDetail {
	var d GameDetail
	d.KIF = miswrittenKI2
	d.ErrorMove = errorMove
	d.Source = source
	d.SourceID = sourceID
	return d
}

// ambiguousKI2 は 3 手目の ▲５八金 が絞れない KI2（実際は ▲５八金左 など）。
const ambiguousKI2 = "先手：太郎\n後手：花子\n▲７六歩    △３四歩\n▲５八金    △８四歩\n"

// **棋譜タブの行も、棚の棋譜 id で引いた直しの表で ⚠ の手を越えられたら修正済**（2026-11-25）。
// ⚠️ 直していない棋譜・⚠ の無い棋譜は返さない。
func TestFixedNotesForShelf(t *testing.T) {
	svc, study := newTestKifuService(t)
	// 3 手目が盤に当たらない（金が 2 枚とも行けるのに右・左が無い）。⚠️ kicho も ikkyoku も止まる棋譜にすること
	// （`miswrittenKI2` の ▲５五金は ikkyoku だけが打ちとして止まり、kicho は ⚠ を付けない）。
	bad, err := svc.ImportKIF(ambiguousKI2)
	if err != nil {
		t.Fatalf("ImportKIF: %v", err)
	}
	if bad.ErrorMove != 3 {
		t.Fatalf("kicho の ⚠ が 3 手目に付いていない: %d", bad.ErrorMove)
	}
	clean, err := svc.ImportKIF(testKIF)
	if err != nil {
		t.Fatalf("ImportKIF: %v", err)
	}
	ids := []string{bad.ID, clean.ID}
	if got := svc.FixedNotes(ids); len(got) != 0 {
		t.Errorf("直していないのに修正済: %v", got)
	}

	load, err := svc.SendToStudy(bad.ID)
	if err != nil {
		t.Fatalf("SendToStudy: %v", err)
	}
	if _, err := study.GoTo(nodeByUSI(t, load.State, "3c3d", true)); err != nil {
		t.Fatal(err)
	}
	st, err := study.Play("6i5h")
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if _, err := study.FixMove(nodeByUSI(t, st, "6i5h", true), "replace"); err != nil {
		t.Fatalf("FixMove: %v", err)
	}
	got := svc.FixedNotes(ids)
	if len(got) != 1 || !strings.Contains(got[bad.ID], "直し 1 か所") {
		t.Errorf("修正済 = %v", got)
	}
}
