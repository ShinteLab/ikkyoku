package app

import (
	"strings"
	"testing"
)

// finishedKIF は**投了まで入った**棋譜（対局の終わりが分かる）。
var finishedKIF = strings.Join([]string{
	"手合割：平手",
	"手数----指手---------消費時間--",
	"   1 ７六歩(77)",
	"   2 ３四歩(33)",
	"   3 投了",
}, "\n")

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

// 横軸の「全て」が**対局の終わりで止まること**（2026-09-16。実機で指摘された）。
//
// ⚠️ **90 手目から生やした枝は経路に入らないのに、投了図以下だけ入る**という
// 食い違いになっていた（木の中で**何番目の子か**だけで扱いが変わっていた）。
// **どちらも「本譜ではない手順」**なので揃える。
func TestEvalGraphAxisStopsAtRecordEnd(t *testing.T) {
	s := adopted(t)
	if _, err := s.LoadKifu(finishedKIF); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	// 投了図（2 手目）から並べる。
	if _, err := s.GoTo(2); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("2g2f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	// **投了図以下を見ているあいだは、そこまで伸びる**（点が画面の外に出ては困る）。
	if g := s.Evals(); g.Last != 3 {
		t.Fatalf("投了図以下を見ているのに横軸が伸びません: last=%d", g.Last)
	}
	// **本譜へ戻ると対局の終わりで止まる**（枝へ戻ったときと同じ振る舞い）。
	if _, err := s.GoTo(2); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	g := s.Evals()
	if g.Last != 2 {
		t.Errorf("本譜に戻っても投了図以下まで出ています: last=%d, want 2", g.Last)
	}
	// ⚠️ **印は出たままであること** —— 「その先に何かある」は見えていてほしい。
	if len(g.Branches) != 1 || g.Branches[0] != 2 {
		t.Errorf("投了図の印が消えています: %+v", g.Branches)
	}
}

// 途中から生やした枝は**今までどおり**経路に入らないこと（比較の相手）。
func TestEvalGraphAxisIgnoresSideBranch(t *testing.T) {
	s := adopted(t)
	for _, m := range []string{"7g7f", "3c3d", "2g2f"} {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%s): %v", m, err)
		}
	}
	// 1 手目から別の手を生やして本譜へ戻る。
	if _, err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("8c8d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if _, err := s.GoTo(3); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if g := s.Evals(); g.Last != 3 {
		t.Errorf("枝のぶんまで横軸が伸びています: last=%d, want 3", g.Last)
	}
}

// ⚠️ **投了図以下を棚の棋譜に書かないこと**（2026-09-16）。
//
// **これが一番まずい穴だった** —— 本譜として扱われていたので、棚へ登録すると
// **検討の手が対局の手として棋譜に載っていた**（「嘘の棋譜が棚に入る」）。
func TestStudyExportKIFStopsAtRecordEnd(t *testing.T) {
	s := adopted(t)
	if _, err := s.LoadKifu(finishedKIF); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	if _, err := s.GoTo(2); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("2g2f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	got, err := s.exportKIF()
	if err != nil {
		t.Fatalf("exportKIF: %v", err)
	}
	if !strings.Contains(got, "３四歩") {
		t.Fatalf("本譜が出ていません:\n%s", got)
	}
	if strings.Contains(got, "２六歩") {
		t.Errorf("投了図以下が対局の手として載っています:\n%s", got)
	}
}
