package app

import (
	"strings"
	"testing"
)

// 投了図以下にも印が出ること（2026-09-16。実機で足りなかった）。
//
// ⚠️ **本譜の投了位置から詰みまで並べるのは中継を観ながら普通にやること。**
// そのとき**子は 1 つしかない**ので、「子が 2 つ以上」だけでは印が出ない。
// **一番印が欲しいのがそこ** —— そこから先は対局の手ではないので、
// 評価値の折れ線の意味が変わる。
func TestEvalGraphMarksRecordEnd(t *testing.T) {
	s := adopted(t)
	// 「投了まで 2 手」の棋譜を読む（`FromKIF` が対局の終わりを覚える）。
	kif := strings.Join([]string{
		"手合割：平手",
		"手数----指手---------消費時間--",
		"   1 ７六歩(77)",
		"   2 ３四歩(33)",
		"   3 投了",
	}, "\n")
	if _, err := s.LoadKifu(kif); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	// まだ投了図から先が無いので印は出ない。
	if g := s.Evals(); len(g.Branches) != 0 {
		t.Fatalf("投了図の先が無いのに印が出ています: %+v", g.Branches)
	}
	// 投了図から並べる（**子は 1 つだけ**）。
	if _, err := s.GoTo(2); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("2g2f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	g := s.Evals()
	if len(g.Branches) != 1 || g.Branches[0] != 2 {
		t.Fatalf("投了図に印が出ません: %+v", g.Branches)
	}
}

// 「分岐にする」で下げた手にも印が出ること。
//
// ⚠️ **子が 1 つでも印が要る** —— `variation` は**まさに「子が 1 つだと
// 本譜と区別できない」から在る印**なので、ここで見ないと意味が無い。
func TestEvalGraphMarksVariation(t *testing.T) {
	s := adopted(t)
	for _, m := range []string{"7g7f", "3c3d", "2g2f"} {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%s): %v", m, err)
		}
	}
	if g := s.Evals(); len(g.Branches) != 0 {
		t.Fatalf("一直線なのに印が出ています: %+v", g.Branches)
	}
	// 3 手目から先を「分岐にする」。
	if _, err := s.Branch(3); err != nil {
		t.Fatalf("Branch: %v", err)
	}
	g := s.Evals()
	if len(g.Branches) != 1 || g.Branches[0] != 2 {
		t.Errorf("分岐にした手の親に印が出ません: %+v", g.Branches)
	}
}

// ⚠️ **対局の終わりを推測で立てないこと。** 棋譜に終局が書いていなければ、
// 最後の手はただの「今のところ最後の手」でしかない（中継は進む）。
func TestEvalGraphNoRecordEndWithoutEndMark(t *testing.T) {
	s := adopted(t)
	kif := strings.Join([]string{
		"手合割：平手",
		"手数----指手---------消費時間--",
		"   1 ７六歩(77)",
		"   2 ３四歩(33)",
	}, "\n")
	if _, err := s.LoadKifu(kif); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	if _, err := s.GoTo(2); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("2g2f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if g := s.Evals(); len(g.Branches) != 0 {
		t.Errorf("終局が書いていないのに印が出ています: %+v", g.Branches)
	}
}

// ⚠️ **対局の終わりが控えから戻ること。** 落とすと、開き直したときに
// **投了図以下の印が消える**（棋譜から読み直す口が無いので二度と戻らない）。
func TestStudySessionKeepsRecordEnd(t *testing.T) {
	s := adopted(t)
	kif := strings.Join([]string{
		"手合割：平手",
		"手数----指手---------消費時間--",
		"   1 ７六歩(77)",
		"   2 ３四歩(33)",
		"   3 投了",
	}, "\n")
	if _, err := s.LoadKifu(kif); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	if _, err := s.GoTo(2); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("2g2f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	rec, ok := s.sessionRecord()
	if !ok {
		t.Fatal("控えが作れません")
	}
	if rec.Study.End == 0 {
		t.Fatal("対局の終わりが控えに入っていません")
	}
	back := empty(t)
	if err := back.restoreSession(rec); err != nil {
		t.Fatalf("restoreSession: %v", err)
	}
	if g := back.Evals(); len(g.Branches) != 1 || g.Branches[0] != 2 {
		t.Errorf("開き直すと投了図以下の印が消えています: %+v", g.Branches)
	}
}
