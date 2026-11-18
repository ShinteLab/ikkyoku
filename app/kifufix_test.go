package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ShinteLab/kicho/store"
)

// mainUSI は本譜の手（USI）を空白で繋いだもの。
func mainUSI(st StudyState) string {
	var out []string
	for _, n := range st.Nodes {
		if n.Main {
			out = append(out, n.USI)
		}
	}
	return strings.Join(out, " ")
}

func nodeByUSI(t *testing.T, st StudyState, usi string, main bool) int {
	t.Helper()
	for _, n := range st.Nodes {
		if n.USI == usi && n.Main == main {
			return n.ID
		}
	}
	t.Fatalf("手 %s (main=%v) がありません: %+v", usi, main, st.Nodes)
	return 0
}

func hasAction(st StudyState, id int, op string) bool {
	for _, a := range st.MoveActions {
		if a.ID == id && a.Op == op {
			return true
		}
	}
	return false
}

func markOf(st StudyState, id int) MoveMark {
	for _, m := range st.MoveMarks {
		if m.ID == id {
			return m
		}
	}
	return MoveMark{}
}

const miswrittenKI2 = "先手：太郎\n後手：花子\n" +
	"▲７六歩    △３四歩\n" +
	"▲５五金    △８四歩\n" + // 先手の金は５五へ行けない（実際は ▲６八銀）
	"▲２六歩\n" +
	"まで5手で先手の勝ち\n"

// **止まった手の 1 つ前の局面で指してから「直しにする」と、残りの原本を読み直して本譜になる**（2026-11-19）。
// ⚠️ 取り消すと、直した手は人が指した枝として残る（消さない）。
func TestFixMoveAtStop(t *testing.T) {
	s := NewStudyService(NewPositionService())
	load, err := s.LoadKifu(miswrittenKI2)
	if err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	st := load.State
	stop := st.KifuStop
	if stop == nil || stop.Number != 3 || len(stop.Rest) != 2 || stop.Text != "▲５五金" {
		t.Fatalf("⚠ の行が違う: %+v", stop)
	}
	second := nodeByUSI(t, st, "3c3d", true)
	if stop.After != second {
		t.Errorf("⚠ の行は 2 手目の直後のはず: after=%d", stop.After)
	}
	if len(stop.Actions) != 1 || stop.Actions[0].Op != "skip" {
		t.Errorf("⚠ の行の項目が違う: %+v", stop.Actions)
	}
	if st.CanRegister {
		t.Errorf("止まっているのに登録できる")
	}
	if _, err := s.GoTo(second); err != nil {
		t.Fatal(err)
	}
	st, err = s.Play("7i6h")
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	fixed := nodeByUSI(t, st, "7i6h", true)
	if !hasAction(st, fixed, "replace") || !hasAction(st, fixed, "insert") {
		t.Fatalf("直しの項目が出ない: %+v", st.MoveActions)
	}
	st, err = s.FixMove(fixed, "replace")
	if err != nil {
		t.Fatalf("FixMove: %v", err)
	}
	if st.KifuStop != nil {
		t.Errorf("直したのに止まったまま: %+v", st.KifuStop)
	}
	if got := mainUSI(st); got != "7g7f 3c3d 7i6h 8c8d 2g2f" {
		t.Errorf("本譜 = %s", got)
	}
	if m := markOf(st, fixed); !strings.Contains(m.Fixed, "▲５五金") {
		t.Errorf("直した手の印 = %+v", m)
	}
	// 原本の最後まで読めて終局が付いたので、訂正版として登録できる。
	if !st.CanRegister || st.RegisterLabel != "訂正版として登録" {
		t.Errorf("訂正版として登録できない: can=%v label=%q", st.CanRegister, st.RegisterLabel)
	}

	st, err = s.FixMove(fixed, "unfix")
	if err != nil {
		t.Fatalf("unfix: %v", err)
	}
	if st.KifuStop == nil {
		t.Errorf("取り消したのに ⚠ の行が出ない")
	}
	if id := nodeByUSI(t, st, "7i6h", false); id != fixed {
		t.Errorf("直した手が枝として残らない")
	}
}

// **盤に当たる誤記は、枝で指した手を「本譜の N 手目の直しにする」**。原本の手は取り消し線の枝に下がり、
// ⚠️ **取り直しても直しは押し戻されない**（表を当ててから据え直す）。
func TestFixMoveLegalMisnoteSurvivesReload(t *testing.T) {
	const text = "▲７六歩 △３四歩 ▲２二角 △同　銀\n" // 実際は ▲２二角成
	s := NewStudyService(NewPositionService())
	load, err := s.LoadKifu(text)
	if err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	st := load.State
	if _, err := s.GoTo(nodeByUSI(t, st, "3c3d", true)); err != nil {
		t.Fatal(err)
	}
	st, err = s.Play("8h2b+")
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	branch := nodeByUSI(t, st, "8h2b+", false)
	var label string
	for _, a := range st.MoveActions {
		if a.ID == branch && a.Op == "replace" {
			label = a.Label
		}
	}
	if label != "本譜の 3 手目の直しにする" {
		t.Fatalf("項目 = %q（%+v）", label, st.MoveActions)
	}
	st, err = s.FixMove(branch, "replace")
	if err != nil {
		t.Fatalf("FixMove: %v", err)
	}
	if got := mainUSI(st); got != "7g7f 3c3d 8h2b+ 3a2b" {
		t.Errorf("本譜 = %s", got)
	}
	orig := nodeByUSI(t, st, "8h2b", false)
	if m := markOf(st, orig); !strings.Contains(m.Struck, "3 手目で直しました") {
		t.Errorf("原本の手の取り消し線 = %+v", m)
	}

	// 同じ原本を読み直す（取り直し・棚から開き直すのと同じ据え直し）。
	if _, err := s.graftKifu(text); err != nil {
		t.Fatalf("graftKifu: %v", err)
	}
	if got := mainUSI(s.State()); got != "7g7f 3c3d 8h2b+ 3a2b" {
		t.Errorf("読み直したら直しが押し戻された: %s", got)
	}
}

// ⚠️ **原本が変わったら、その直しは外して 1 回だけ知らせる**（黙って外さない）。
func TestFixDroppedWhenOriginalChanges(t *testing.T) {
	s := NewStudyService(NewPositionService())
	load, _ := s.LoadKifu(miswrittenKI2)
	second := nodeByUSI(t, load.State, "3c3d", true)
	_, _ = s.GoTo(second)
	st, _ := s.Play("7i6h")
	if _, err := s.FixMove(nodeByUSI(t, st, "7i6h", true), "replace"); err != nil {
		t.Fatalf("FixMove: %v", err)
	}
	fixedByRelay := strings.Replace(miswrittenKI2, "▲５五金", "▲６八銀", 1)
	got, err := s.graftKifu(fixedByRelay)
	if err != nil {
		t.Fatalf("graftKifu: %v", err)
	}
	if !strings.Contains(got.Note, "3 手目") || !strings.Contains(got.Note, "直しを外しました") {
		t.Errorf("外したことを知らせない: %q", got.Note)
	}
	again, _ := s.graftKifu(fixedByRelay)
	if strings.Contains(again.Note, "直しを外しました") {
		t.Errorf("2 回知らせた: %q", again.Note)
	}
}

// 「直しにする」は原本を持つ検討だけ（新規対局には原本が無い）。
func TestFixMoveNeedsOriginal(t *testing.T) {
	s := NewStudyService(NewPositionService())
	if _, err := s.NewGame(""); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Play("7g7f")
	if len(st.MoveActions) != 0 || st.KifuStop != nil {
		t.Errorf("原本の無い検討に直しの項目が出た: %+v", st.MoveActions)
	}
	if _, err := s.FixMove(st.CurrentID, "replace"); err == nil {
		t.Error("原本の無い検討を直せた")
	}
}

// **棚の棋譜を読んで反則手で止まったら、その手に ikkyoku の指摘を付ける**。⚠️ 開き直しても重ねない・
// 人が外した指摘は付け直さない。
func TestAutoFlagIllegalMove(t *testing.T) {
	const text = "手数----指手---------消費時間--\n" +
		"   1 ７六歩(77)\n   2 ３四歩(33)\n   3 ９九飛(28)\n   4 ８四歩(83)\n"
	s := NewStudyService(NewPositionService())
	var flags []store.Flag
	calls := 0
	s.flagsOf = func(string) ([]store.Flag, int) { return flags, 0 }
	s.addFlag = func(gameID string, move int, reason, by string) error {
		calls++
		flags = append(flags, store.Flag{ID: fmt.Sprint("f", calls), GameID: gameID, Move: move, Reason: reason, By: by})
		return nil
	}
	load, err := s.loadKifuFrom(text, "", "g1", "")
	if err != nil {
		t.Fatalf("loadKifuFrom: %v", err)
	}
	if calls != 1 || flags[0].Move != 3 || flags[0].By != flagByIkkyoku {
		t.Fatalf("反則手に指摘が付かない: calls=%d %+v", calls, flags)
	}
	// 返した状態に指摘の項目が載っている（2 手目の「指摘する…」は出る）。
	if !hasAction(load.State, nodeByUSI(t, load.State, "3c3d", true), "flag") {
		t.Errorf("棚の棋譜の手に「指摘する…」が出ない: %+v", load.State.MoveActions)
	}
	if _, err := s.graftKifu(text); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("開き直したら指摘が重なった: %d", calls)
	}
	// 人が外したら、同じ原本のままなら付け直さない。
	s.dismissFlag(flags[0])
	flags = nil
	if _, err := s.graftKifu(text); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("外した指摘を付け直した: %d", calls)
	}
}
