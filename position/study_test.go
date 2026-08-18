package position_test

import (
	"strings"
	"testing"

	"github.com/ShinteLab/core/sfen"
	"github.com/ShinteLab/ikkyoku/position"
)

const hirateBoard = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"

// hirate は初期局面（手番と手数を決めたもの）を返す。
func hirate(t *testing.T) *position.Position {
	t.Helper()
	p, err := position.FromBoardSFEN(hirateBoard)
	if err != nil {
		t.Fatalf("FromBoardSFEN: %v", err)
	}
	p.Turn = position.TurnBlack
	p.MoveNumber = 1
	return p
}

// 手を指すと駒が動き、**手番が入れ替わり、手数が 1 進む**。
// 訂正（Move）と違って、ここは実際の対局と同じ進み方をする。
func TestApplyMove(t *testing.T) {
	p := hirate(t)
	if err := p.ApplyMove("7g7f"); err != nil {
		t.Fatalf("ApplyMove: %v", err)
	}
	got, err := p.SFEN()
	if err != nil {
		t.Fatalf("SFEN: %v", err)
	}
	const want = "lnsgkgsnl/1r5b1/ppppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL w - 2"
	if got != want {
		t.Errorf("SFEN = %q, want %q", got, want)
	}
}

// 駒を取ると**指した側の駒台に載る**（成駒は元の駒に戻る）。
// 訂正の Move は「置き換え」なので取らない。**混同すると駒が消える。**
func TestApplyMoveCapture(t *testing.T) {
	// 先手の飛車(2八)が 2 二の後手の角(成っている)を取る。
	const board = "9/7+b1/9/9/9/9/9/7R1/9"
	p, err := position.FromBoardSFEN(board)
	if err != nil {
		t.Fatalf("FromBoardSFEN: %v", err)
	}
	p.Turn = position.TurnBlack
	if err := p.ApplyMove("2h2b+"); err != nil {
		t.Fatalf("ApplyMove: %v", err)
	}
	black, white := p.Hands()
	if black[sfen.Bishop] != 1 {
		t.Errorf("先手の駒台の角 = %d, want 1（成駒は元の駒に戻る）", black[sfen.Bishop])
	}
	if len(white) != 0 {
		t.Errorf("後手の駒台に何か載っています: %v", white)
	}
	c, _ := p.Board.At(1, 7) // 2 二
	if c.Piece() != sfen.Rook || !c.Promoted() || !c.Black() {
		t.Errorf("2 二 = %s, want 先手の竜", c.Name())
	}
}

// 打ちは駒台から 1 枚減る。**その側の駒台に無ければ指せない。**
func TestApplyMoveDrop(t *testing.T) {
	p := hirate(t)
	if err := p.SetHand(sfen.Pawn, true, 1); err != nil {
		t.Fatalf("SetHand: %v", err)
	}
	if err := p.ApplyMove("P*5e"); err != nil {
		t.Fatalf("ApplyMove: %v", err)
	}
	black, _ := p.Hands()
	if black[sfen.Pawn] != 0 {
		t.Errorf("駒台の歩 = %d, want 0", black[sfen.Pawn])
	}
	if err := p.ApplyMove("p*5f"); err == nil {
		t.Error("駒台に無い駒を打てました")
	}
}

// ⚠️ **手番が未決の局面では指せない。** どちらが指したのか決まらないと、
// 取った駒をどちらの駒台に載せるかも決まらない（設計原則5）。
func TestApplyMoveNeedsTurn(t *testing.T) {
	p, err := position.FromBoardSFEN(hirateBoard)
	if err != nil {
		t.Fatalf("FromBoardSFEN: %v", err)
	}
	if err := p.ApplyMove("7g7f"); err == nil {
		t.Fatal("手番未決なのに指せました")
	}
}

// 手順は「根 + 指した手の並び」。**根は動かない。**
func TestStudyPlay(t *testing.T) {
	s := position.NewStudy(hirate(t))
	for _, m := range []string{"7g7f", "3c3d", "8h2b+"} {
		if err := s.Play(m); err != nil {
			t.Fatalf("Play(%s): %v", m, err)
		}
	}
	if s.Ply() != 3 {
		t.Errorf("Ply = %d, want 3", s.Ply())
	}
	rootSFEN, _ := s.Root().SFEN()
	if !strings.HasPrefix(rootSFEN, hirateBoard+" b ") {
		t.Errorf("根が動いています: %q", rootSFEN)
	}
	// **エンジンに渡すのは根 + Played。**
	if got := strings.Join(s.Played(), " "); got != "7g7f 3c3d 8h2b+" {
		t.Errorf("Played = %q", got)
	}
	// 読み筋と同じく**日本語表記で出す**（画面に出すのは Text）。
	ms := s.Nodes()
	if len(ms) != 3 {
		t.Fatalf("手数 = %d, want 3", len(ms))
	}
	if ms[0].Text != "▲７六歩" {
		t.Errorf("1手目 = %q, want %q", ms[0].Text, "▲７六歩")
	}
	if ms[2].Text != "▲２二角成" {
		t.Errorf("3手目 = %q, want %q", ms[2].Text, "▲２二角成")
	}
	if ms[0].Number != 1 || ms[2].Number != 3 {
		t.Errorf("手数の番号が棋譜の数え方になっていません: %+v", ms)
	}
}

// ⚠️ **合法手でなければ指せない。** 訂正タブと違って、ここは合法手だけを辿る面。
func TestStudyPlayRejectsIllegal(t *testing.T) {
	s := position.NewStudy(hirate(t))
	if err := s.Play("7g7e"); err == nil {
		t.Fatal("2 マス進む歩が指せました")
	}
	if err := s.Play("3c3d"); err == nil {
		t.Fatal("先手番なのに後手の手が指せました")
	}
	if s.Ply() != 0 {
		t.Errorf("失敗したのに進んでいます: Ply = %d", s.Ply())
	}
}

// 戻って別の手を指すと**枝が生える**（2026-08-13。それまでは先を捨てていた）。
//
// ⚠️ **前の手順を消さないこと。** 「それもまた一局」——別の選択も一つの局として
// 辿らせるのがこのアプリの中心なので、選び直した瞬間に前の枝が消えては話にならない。
func TestStudyGoToAndBranch(t *testing.T) {
	s := position.NewStudy(hirate(t))
	for _, m := range []string{"7g7f", "3c3d"} {
		if err := s.Play(m); err != nil {
			t.Fatalf("Play(%s): %v", m, err)
		}
	}
	// **戻るだけなら手順は消えない**（進め直せる）。
	if err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if len(s.Nodes()) != 2 || s.Ply() != 1 {
		t.Fatalf("戻っただけで手順が消えました: moves=%d ply=%d", len(s.Nodes()), s.Ply())
	}
	if got := strings.Join(s.Played(), " "); got != "7g7f" {
		t.Errorf("Played = %q, want %q（先の手を渡さないこと）", got, "7g7f")
	}
	// そこで別の手を指すと、**枝が生える**（元の手は兄弟として残る）。
	if err := s.Play("8c8d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if got := strings.Join(s.Played(), " "); got != "7g7f 8c8d" {
		t.Errorf("分岐後の Played = %q", got)
	}
	if len(s.Nodes()) != 3 {
		t.Errorf("分岐で古い手が消えました: %+v", s.Nodes())
	}
	// ⚠️ **同じ手を指し直したら枝は増えない**（既にある子を辿る）。
	if err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if err := s.Play("3c3d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if len(s.Nodes()) != 3 {
		t.Errorf("同じ手で枝が増えました: %+v", s.Nodes())
	}
}

// DropFrom は指した手を**その手以下まとめて**消す（「1手戻す」の代わり）。
//
// ⚠️ **消す量は「今どこを見ているか」に依存しないこと。** 手そのもので指すので、
// 戻って見ている最中でも、消える範囲は同じ（n 手目以下）。
func TestStudyDropFrom(t *testing.T) {
	s := position.NewStudy(hirate(t))
	for _, mv := range []string{"7g7f", "3c3d", "2g2f"} {
		if err := s.Play(mv); err != nil {
			t.Fatalf("Play %s: %v", mv, err)
		}
	}
	// 1 手目まで戻って見ている状態で、2 手目以下を消す。
	if err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.DropFrom(2); err != nil {
		t.Fatalf("DropFrom: %v", err)
	}
	if len(s.Nodes()) != 1 || s.Nodes()[0].USI != "7g7f" {
		t.Errorf("2手目以下が消えていません: %+v", s.Nodes())
	}
	// **見ていた位置は消えていないのでそのまま**（勝手に動かさない）。
	if s.Ply() != 1 {
		t.Errorf("消していない手まで戻りました: ply=%d", s.Ply())
	}
	// 最後の 1 手を消すと根に戻る（＝以前の「1手戻す」と同じ）。
	if _, err := s.DropFrom(1); err != nil {
		t.Fatalf("DropFrom: %v", err)
	}
	if s.Ply() != 0 || len(s.Nodes()) != 0 {
		t.Errorf("手順が残っています: ply=%d moves=%+v", s.Ply(), s.Nodes())
	}
	if _, err := s.DropFrom(1); err == nil {
		t.Error("無い手を消せました")
	}
}

// 合法手が今の局面（手を進めたあと）のものになっていること。
// **根の合法手を返していると、進めた瞬間に嘘の移動先が光る。**
func TestStudyLegalFollowsCurrent(t *testing.T) {
	s := position.NewStudy(hirate(t))
	if err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	ms, err := s.Legal()
	if err != nil {
		t.Fatalf("Legal: %v", err)
	}
	for _, m := range ms {
		if strings.HasPrefix(m.USI, "7g") {
			t.Fatalf("動かしたはずの 7g から指せることになっています: %+v", m)
		}
	}
	if len(ms) != 30 {
		t.Errorf("後手の合法手 = %d, want 30", len(ms))
	}
}

// AddLine は読み筋を枝として足す（解析の候補手から）。
//
// ⚠️ **今見ている局面を動かさないこと。** 動くと走っている解析が別の局面のものに
// なり、候補を続けて足せなくなる。
func TestStudyAddLine(t *testing.T) {
	s := position.NewStudy(hirate(t))
	if err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	at := s.CurrentID()

	first, added, note := s.AddLine([]string{"3c3d", "2g2f", "8c8d"}, "e1")
	if note != "" {
		t.Fatalf("止まりました: %s", note)
	}
	if added != 3 || first == 0 {
		t.Fatalf("枝が生えていません: first=%d added=%d", first, added)
	}
	if s.CurrentID() != at || s.Ply() != 1 {
		t.Errorf("足しただけで局面が動きました: id=%d ply=%d", s.CurrentID(), s.Ply())
	}
	// **日本語表記も付くこと**（画面に出すのは Text）。
	for _, n := range s.Nodes() {
		if n.ID == first && n.Text != "△３四歩" {
			t.Errorf("枝の表記 = %q, want %q", n.Text, "△３四歩")
		}
	}
}

// エンジンが足した読み筋は**頭の 1 手だけが 1 段下がり、続きは更に下がる**こと
// （2026-08-18）。**畳めば「＋」で 1 手にまとまる形。**
//
// ⚠️ **続きの無いところへ足しても、読み筋がそのまま今の線として伸びないこと。**
// 伸びると**畳む場所が無く、15 手の読み筋がそのまま手順に並ぶ。**
func TestStudyAddLineIsFoldable(t *testing.T) {
	s := position.NewStudy(hirate(t))
	if err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	// **本譜の先端**（続きがまだ無い）に読み筋を足す。
	first, added, note := s.AddLine([]string{"3c3d", "2g2f", "8c8d"}, "e1")
	if note != "" || added != 3 {
		t.Fatalf("足せていません: added=%d note=%s", added, note)
	}
	var head position.Node
	deeper := 0
	for _, n := range s.Nodes() {
		if n.ID == first {
			head = n
		}
	}
	for _, n := range s.Nodes() {
		if n.ID != first && n.Number > head.Number && n.Depth > head.Depth {
			deeper++
		}
	}
	// **頭は 1 段下がる**（本譜の続きではない）。
	if head.Depth != 1 || head.Main {
		t.Errorf("読み筋の頭が本譜の続きになっています: %+v", head)
	}
	// ⚠️ **続きは更に下がること**（＝頭が畳める節点になる）。
	if deeper != 2 {
		t.Errorf("続きが頭にぶら下がっていません（畳めない）: %+v", s.Nodes())
	}
	// ⚠️ **本譜は伸びないこと**（実際に現れた指し手ではない）。
	if got := s.MainLine(); len(got) != 1 {
		t.Errorf("MainLine = %v, want 7g7f だけ", got)
	}
	// **続きに選べば、そこから先は普通の続きになる。**
	if err := s.Promote(first); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	for _, n := range s.Nodes() {
		if n.ID == first && (n.Depth != 0 || !n.Main) {
			t.Errorf("本線にしても続きになっていません: %+v", n)
		}
	}
}

// ⚠️ **候補が本譜と同じ手なら枝を増やさず、食い違うところまで辿ってから枝にする。**
// これが分岐の肝で、崩すと同じ手順が何本も並ぶ。
func TestStudyAddLineFollowsExisting(t *testing.T) {
	s := position.NewStudy(hirate(t))
	for _, mv := range []string{"7g7f", "3c3d", "2g2f"} {
		if err := s.Play(mv); err != nil {
			t.Fatalf("Play %s: %v", mv, err)
		}
	}
	if err := s.GoTo(0); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	// 頭 2 手は同じで、3 手目から食い違う読み筋。
	_, added, note := s.AddLine([]string{"7g7f", "3c3d", "6g6f"}, "e1")
	if note != "" {
		t.Fatalf("止まりました: %s", note)
	}
	if added != 1 {
		t.Fatalf("同じ手まで枝にしています: added=%d", added)
	}
	if got := len(s.Nodes()); got != 4 {
		t.Fatalf("節点 = %d, want 4（3 手 + 枝 1 手）: %+v", got, s.Nodes())
	}
	// 枝は 2 手目（△３四歩）の下に生えていること。
	var branch position.Node
	for _, n := range s.Nodes() {
		if n.USI == "6g6f" {
			branch = n
		}
	}
	if branch.Number != 3 || branch.Depth != 1 || branch.Main {
		t.Errorf("枝の生え方がおかしい: %+v", branch)
	}
}

// ⚠️ **指せない手が出ても、足せたぶんは残すこと**（設計原則3）。
func TestStudyAddLineStopsAtIllegal(t *testing.T) {
	s := position.NewStudy(hirate(t))
	_, added, note := s.AddLine([]string{"7g7f", "9i9h"}, "e1")
	if added != 1 {
		t.Errorf("足せたぶんが残っていません: added=%d", added)
	}
	if note == "" {
		t.Error("止まった理由が出ていません")
	}
}

// Graft は棋譜の取り直し。**消さずに据える**（食い違った先は枝として残る）。
//
// ⚠️ **ここが崩れると、中継が 1 手進むたびに自分の検討が消える。**
func TestStudyGraft(t *testing.T) {
	s := position.NewStudy(hirate(t))
	for _, mv := range []string{"7g7f", "3c3d"} {
		if err := s.Play(mv); err != nil {
			t.Fatalf("Play %s: %v", mv, err)
		}
	}
	// 自分で足した検討（2 手目の下の枝）。
	if _, added, note := s.AddLine([]string{"2g2f"}, "e1"); added != 1 || note != "" {
		t.Fatalf("AddLine: added=%d note=%s", added, note)
	}

	// 取り直したら 2 手目が別の手だった（URL が正）。
	r := s.Graft([]string{"7g7f", "8c8d", "2g2f"})
	if r.Note != "" {
		t.Fatalf("止まりました: %s", r.Note)
	}
	if r.Kept != 1 || r.Added != 2 {
		t.Errorf("突き合わせがおかしい: %+v", r)
	}
	// ⚠️ **押しのけた手数が出ること**（断りを出すのはこれが立ったときだけ）。
	if r.MovedAt != 2 {
		t.Errorf("押しのけた手数 = %d, want 2", r.MovedAt)
	}
	// **新しい手順が本譜**（先頭）になっていること。
	if got := strings.Join(s.MainLine(), " "); got != "7g7f 8c8d 2g2f" {
		t.Errorf("本譜 = %q", got)
	}
	// **前の手順も枝として残っていること**（消さない）。
	found := false
	for _, n := range s.Nodes() {
		if n.USI == "3c3d" {
			found = true
			if n.Main {
				t.Errorf("古い手が本譜のままです: %+v", n)
			}
		}
	}
	if !found {
		t.Error("取り直しで自分の検討が消えました")
	}
}

// 消した節点の id が返ること（**評価値をそれで捨てる**）。
func TestStudyDropFromReturnsGone(t *testing.T) {
	s := position.NewStudy(hirate(t))
	for _, mv := range []string{"7g7f", "3c3d"} {
		if err := s.Play(mv); err != nil {
			t.Fatalf("Play %s: %v", mv, err)
		}
	}
	if _, added, _ := s.AddLine([]string{"2g2f"}, "e1"); added != 1 {
		t.Fatal("AddLine")
	}
	gone, err := s.DropFrom(1)
	if err != nil {
		t.Fatalf("DropFrom: %v", err)
	}
	// 1 手目・2 手目・枝の 3 つが消える（**子孫も全部**）。
	if len(gone) != 3 {
		t.Errorf("消えた id = %v, want 3 つ", gone)
	}
	if s.Ply() != 0 || len(s.Nodes()) != 0 {
		t.Errorf("木が残っています: %+v", s.Nodes())
	}
}

// ⚠️ **中継が進んだだけ（手が増えただけ）なら「押しのけた」にしないこと。**
// ここが立つと、1 手進むたびに「本譜を入れ替えました」と断ることになる。
func TestStudyGraftAppendIsNotMove(t *testing.T) {
	s := position.NewStudy(hirate(t))
	if err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	r := s.Graft([]string{"7g7f", "3c3d"})
	if r.MovedAt != 0 {
		t.Errorf("伸びただけなのに押しのけた扱いです: %+v", r)
	}
	if r.Kept != 1 || r.Added != 1 {
		t.Errorf("突き合わせがおかしい: %+v", r)
	}
}

// ⚠️ **枝の中の分かれ道は、全部そろえて字下げすること**（2026-08-13）。
//
// 2 つのエンジンが同じ手（例: ９七角）を挙げ、その先だけが違うとき、
// **先に足したほうが本筋のように見えてはいけない**（実際にそう見えて直した）。
// 本譜だけは字下げしない —— 分岐のたびに右へ流れると深さが意味を失う。
func TestStudyNodesIndentsBranchSiblingsEqually(t *testing.T) {
	s := position.NewStudy(hirate(t))
	if err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	// 本譜は 1 手目のあと △３四歩。
	if err := s.Play("3c3d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	// 枝（△８四歩）に、続きの違う読み筋を 2 本足す。
	if _, added, note := s.AddLine([]string{"8c8d", "2g2f"}, "e1"); added != 2 || note != "" {
		t.Fatalf("AddLine: added=%d note=%s", added, note)
	}
	if _, added, note := s.AddLine([]string{"8c8d", "6g6f"}, "e1"); added != 1 || note != "" {
		t.Fatalf("AddLine: added=%d note=%s", added, note)
	}

	at := map[string]position.Node{}
	for _, n := range s.Nodes() {
		at[n.USI] = n
	}
	// **同じ △８四歩 の続き 2 つが同じ深さ**であること。
	if at["2g2f"].Depth != at["6g6f"].Depth {
		t.Errorf("先に足したほうが上位に見えます: %+v / %+v", at["2g2f"], at["6g6f"])
	}
	if at["2g2f"].Depth != at["8c8d"].Depth+1 {
		t.Errorf("枝の中の分かれ道が下がっていません: %+v", at["2g2f"])
	}
	// ⚠️ **変化の頭（△８四歩）は 1 段上に居ること** —— そこが「畳める節点」になる。
	if at["8c8d"].Depth != 1 {
		t.Errorf("変化の頭の深さ = %d, want 1: %+v", at["8c8d"].Depth, at["8c8d"])
	}
	// **本譜は下がらない**（1 手目と同じ深さのまま）。
	if at["3c3d"].Depth != 0 || !at["3c3d"].Main {
		t.Errorf("本譜が字下げされました: %+v", at["3c3d"])
	}
	// 枝は本譜ではない（色分けの鍵）。
	if at["8c8d"].Main || at["2g2f"].Main {
		t.Errorf("枝が本譜になっています: %+v / %+v", at["8c8d"], at["2g2f"])
	}
}

// ⚠️ **2 本目を足しても 1 本目の字下げが変わらないこと**（2026-08-13）。
//
// 変化の頭の子を「1 本でも下げる」ようにしてある理由がこれ。下げないと、
// **2 本目を足した瞬間に 1 本目が右へずれて、別のものになったように見える。**
func TestStudyNodesShapeIsStableWhenSiblingAdded(t *testing.T) {
	s := position.NewStudy(hirate(t))
	if err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	// 1 本目（本譜から分かれる変化）。
	if _, added, note := s.AddLine([]string{"8c8d", "2g2f"}, "e1"); added != 2 || note != "" {
		t.Fatalf("AddLine: added=%d note=%s", added, note)
	}
	before := map[string]int{}
	for _, n := range s.Nodes() {
		before[n.USI] = n.Depth
	}
	// 2 本目（頭は同じ、その先だけ違う）。
	if _, added, note := s.AddLine([]string{"8c8d", "6g6f"}, "e1"); added != 1 || note != "" {
		t.Fatalf("AddLine: added=%d note=%s", added, note)
	}
	for _, n := range s.Nodes() {
		if d, ok := before[n.USI]; ok && d != n.Depth {
			t.Errorf("2 本目を足したら 1 本目がずれました: %s %d -> %d", n.USI, d, n.Depth)
		}
	}
}

// ⚠️ **分かれ道の子は「変化の頭」と同じ扱いにすること**（2026-08-13）。
//
// 続きを 1 段下げる＝**その手が畳める節点になる**ので、分かれた手どうしを
// 隣り合わせて見比べられる。読み筋は 15 手ぶら下がることがあるので、
// 下げないと**次の候補が画面の外**に出て、分岐を見る意味が薄れる。
func TestStudyNodesForkChildrenAreHeads(t *testing.T) {
	s := position.NewStudy(hirate(t))
	for _, mv := range []string{"7g7f", "3c3d", "2g2f", "8c8d"} {
		if err := s.Play(mv); err != nil {
			t.Fatalf("Play %s: %v", mv, err)
		}
	}
	if err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	// 頭（△８四歩 ▲２六歩）まで同じで、その次から食い違う 2 本。
	s.AddLine([]string{"8c8d", "2g2f", "8d8e", "2f2e"}, "e1")
	s.AddLine([]string{"8c8d", "2g2f", "4a3b", "6i7h"}, "e1")

	at := map[string]position.Node{}
	for _, n := range s.Nodes() {
		at[n.USI] = n
	}
	// 分かれた 2 手は同じ深さ。
	if at["8d8e"].Depth != at["4a3b"].Depth {
		t.Errorf("分かれた手の深さが違います: %+v / %+v", at["8d8e"], at["4a3b"])
	}
	// ⚠️ **その続きは 1 段下がっていること**（＝分かれた手が畳める節点になる）。
	if at["2f2e"].Depth != at["8d8e"].Depth+1 {
		t.Errorf("分かれた手の続きが下がっていません: %+v", at["2f2e"])
	}
	if at["6i7h"].Depth != at["4a3b"].Depth+1 {
		t.Errorf("分かれた手の続きが下がっていません: %+v", at["6i7h"])
	}
}

// ⚠️ **変化の頭のすぐ下で分かれる場合も、分かれた手が「頭」になること**
// （2026-08-13。実際にここが `cont` に固定されていて分かれて見えなかった）。
//
// 同じ手を挙げた 2 つのエンジンの読み筋が**次の手から違う**、というのは普通にある。
func TestStudyNodesForkRightUnderHead(t *testing.T) {
	s := position.NewStudy(hirate(t))
	for _, mv := range []string{"7g7f", "3c3d"} {
		if err := s.Play(mv); err != nil {
			t.Fatalf("Play %s: %v", mv, err)
		}
	}
	if err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	// 頭（△８四歩）は同じで、その次の手から違う 2 本。
	s.AddLine([]string{"8c8d", "2g2f", "8d8e"}, "e1")
	s.AddLine([]string{"8c8d", "6g6f", "4a3b"}, "e1")

	at := map[string]position.Node{}
	for _, n := range s.Nodes() {
		at[n.USI] = n
	}
	// 分かれた 2 手は同じ深さ。
	if at["2g2f"].Depth != at["6g6f"].Depth {
		t.Errorf("分かれた手の深さが違います: %+v / %+v", at["2g2f"], at["6g6f"])
	}
	// ⚠️ **その続きは 1 段下がっていること**（＝分かれた手が畳める節点になる）。
	if at["8d8e"].Depth != at["2g2f"].Depth+1 {
		t.Errorf("分かれた手の続きが下がっていません: %+v", at["8d8e"])
	}
	if at["4a3b"].Depth != at["6g6f"].Depth+1 {
		t.Errorf("分かれた手の続きが下がっていません: %+v", at["4a3b"])
	}
}

// TestStudyAddLineSources は**その手を挙げたエンジン**の記録を固定する（2026-08-14）。
//
// 手順リストで**誰が言った手なのか**を色で出すためのもの。枝は「エンジンが
// そう読んだ」だけの手なので、**本譜と同じ見た目で並ぶと誰の読み筋か分からない。**
func TestStudyAddLineSources(t *testing.T) {
	sourcesOf := func(s *position.Study, id int) []string {
		for _, n := range s.Nodes() {
			if n.ID == id {
				return n.Sources
			}
		}
		return nil
	}

	t.Run("足した手にエンジンが付く", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		first, _, note := s.AddLine([]string{"7g7f", "3c3d"}, "e1")
		if note != "" {
			t.Fatalf("止まりました: %s", note)
		}
		if got := sourcesOf(s, first); len(got) != 1 || got[0] != "e1" {
			t.Errorf("Sources = %v, want [e1]", got)
		}
	})

	// ⚠️ **同じ手を 2 つのエンジンが挙げたら両方残す。** 後勝ちで上書きすると、
	// **一番読みたい一致**（2 つが同じ手を推している）が見えなくなる。
	t.Run("同じ手を挙げたエンジンは並ぶ", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		first, _, _ := s.AddLine([]string{"7g7f"}, "e1")
		if _, _, note := s.AddLine([]string{"7g7f"}, "e2"); note != "" {
			t.Fatalf("止まりました: %s", note)
		}
		got := sourcesOf(s, first)
		if len(got) != 2 || got[0] != "e1" || got[1] != "e2" {
			t.Errorf("Sources = %v, want [e1 e2]", got)
		}
	})

	// 連続解析では同じ読み筋を何度も足しうる。**同じエンジンを重ねないこと。**
	t.Run("同じエンジンは重ならない", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		first, _, _ := s.AddLine([]string{"7g7f"}, "e1")
		s.AddLine([]string{"7g7f"}, "e1")
		if got := sourcesOf(s, first); len(got) != 1 {
			t.Errorf("Sources = %v, want 1 件", got)
		}
	})

	// ⚠️ **人が指した手にエンジンは付かない。** 代わりに `Hand` が立つ
	// （手順リストでは黒い丸）。
	t.Run("人が指した手にはエンジンが付かない", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		if err := s.Play("7g7f"); err != nil {
			t.Fatalf("Play: %v", err)
		}
		if got := sourcesOf(s, s.CurrentID()); len(got) != 0 {
			t.Errorf("Sources = %v, want 空", got)
		}
	})

	// ⚠️ **既にある手にも付ける。** 人が指した手をエンジンも推していたなら、
	// それは**その手が誰の読みと一致したか**という読みたい情報そのもの。
	t.Run("既にある手にも足す", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		if err := s.Play("7g7f"); err != nil {
			t.Fatalf("Play: %v", err)
		}
		at := s.CurrentID()
		if err := s.GoTo(0); err != nil {
			t.Fatalf("GoTo: %v", err)
		}
		if _, added, _ := s.AddLine([]string{"7g7f"}, "e1"); added != 0 {
			t.Fatalf("枝が増えました: added=%d", added)
		}
		if got := sourcesOf(s, at); len(got) != 1 || got[0] != "e1" {
			t.Errorf("Sources = %v, want [e1]", got)
		}
	})
}

// TestStudyHandMark は**人が盤で指した手**の印を固定する（2026-08-14）。
//
// ⚠️ **棋譜（KIF / URL）の手には付かない。** あちらは**実際に現れた指し手**で、
// 「自分で試しに指した手」とは別物。⚠️ **`Play` は `FromKIF` も通る**ので、
// **`Play` に印を付ける実装にすると棋譜の手まで自分の手になる。**
func TestStudyHandMark(t *testing.T) {
	handOf := func(s *position.Study, id int) bool {
		for _, n := range s.Nodes() {
			if n.ID == id {
				return n.Hand
			}
		}
		return false
	}
	sourcesOf := func(s *position.Study, id int) []string {
		for _, n := range s.Nodes() {
			if n.ID == id {
				return n.Sources
			}
		}
		return nil
	}

	t.Run("盤で指した手には付く", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		if err := s.Play("7g7f"); err != nil {
			t.Fatalf("Play: %v", err)
		}
		if !handOf(s, s.CurrentID()) {
			t.Error("Hand が立っていません")
		}
	})

	t.Run("エンジンの読み筋には付かない", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		first, _, _ := s.AddLine([]string{"7g7f"}, "e1")
		if handOf(s, first) {
			t.Error("エンジンの手に Hand が立っています")
		}
	})

	// ⚠️ **棋譜の手には付かない**（`FromKIF` は印なしで指す）。
	t.Run("棋譜の手には付かない", func(t *testing.T) {
		src := "手合割：平手\n手数----指手---------消費時間--\n   1 ７六歩(77)\n"
		st, _, err := position.FromKIF(src)
		if err != nil {
			t.Fatalf("FromKIF: %v", err)
		}
		nodes := st.Nodes()
		if len(nodes) != 1 {
			t.Fatalf("手数 = %d, want 1", len(nodes))
		}
		if nodes[0].Hand {
			t.Error("棋譜の手に Hand が立っています")
		}
	})

	// ⚠️ **エンジンが挙げた手を自分でも指したら、どちらの印も残ること。**
	// 片方だけ残すと、どちらが消えたのか画面からは分からない。
	t.Run("印は消さずに重なる", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		first, _, _ := s.AddLine([]string{"7g7f"}, "e1")
		if err := s.Play("7g7f"); err != nil {
			t.Fatalf("Play: %v", err)
		}
		if !handOf(s, first) || len(sourcesOf(s, first)) != 1 {
			t.Errorf("印が落ちました: hand=%v sources=%v", handOf(s, first), sourcesOf(s, first))
		}
	})
}

// TestStudyBranch は「分岐にする」を固定する（2026-08-14）。
//
// **本譜の先端から試しに指した手を、エンジンの読み筋と同じ扱いに落とす操作。**
// ⚠️ **順番の入れ替えでは表せない** —— 子が 1 つならその子は必ず `kids[0]`＝本譜
// なので、印（`treeNode.variation`）で表している。
func TestStudyBranch(t *testing.T) {
	nodeOf := func(s *position.Study, id int) (position.Node, bool) {
		for _, n := range s.Nodes() {
			if n.ID == id {
				return n, true
			}
		}
		return position.Node{}, false
	}

	// 1 本道（子が 1 つ）でも変化に落ちること。**ここが順番では表せない場面。**
	t.Run("1本道でも変化になる", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		if err := s.Play("7g7f"); err != nil {
			t.Fatalf("Play: %v", err)
		}
		id := s.CurrentID()
		if err := s.Play("3c3d"); err != nil {
			t.Fatalf("Play: %v", err)
		}
		if n, _ := nodeOf(s, id); !n.Main {
			t.Fatal("最初から本譜ではありません")
		}
		if err := s.Branch(id); err != nil {
			t.Fatalf("Branch: %v", err)
		}
		n, ok := nodeOf(s, id)
		if !ok {
			t.Fatal("手が消えました")
		}
		if n.Main {
			t.Error("本譜のままです")
		}
		// **1 段下がって前の手にぶら下がること**（エンジンの読み筋と同じ形）。
		if n.Depth != 1 {
			t.Errorf("Depth = %d, want 1", n.Depth)
		}
		// ⚠️ **その先も本譜ではなくなること。**
		for _, m := range s.Nodes() {
			if m.ID != id && m.Main {
				t.Errorf("先の手が本譜のままです: %+v", m)
			}
		}
	})

	// ⚠️ **手は 1 手も消えない**（`DropFrom` と混同しない）。局面も動かない。
	t.Run("手も局面も動かない", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		for _, mv := range []string{"7g7f", "3c3d", "2g2f"} {
			if err := s.Play(mv); err != nil {
				t.Fatalf("Play %s: %v", mv, err)
			}
		}
		at, ply, n := s.CurrentID(), s.Ply(), len(s.Nodes())
		if err := s.Branch(1); err != nil {
			t.Fatalf("Branch: %v", err)
		}
		if len(s.Nodes()) != n {
			t.Errorf("手数が変わりました: %d -> %d", n, len(s.Nodes()))
		}
		if s.CurrentID() != at || s.Ply() != ply {
			t.Errorf("局面が動きました: id=%d ply=%d", s.CurrentID(), s.Ply())
		}
	})

	// ⚠️ **本譜はそこで終わる**（棋譜の取り直しが「最後の手を見ていたか」の
	// 判定に `MainLine` を使うので、自分の検討が本譜として扱われると狂う）。
	t.Run("MainLine がそこで止まる", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		if err := s.Play("7g7f"); err != nil {
			t.Fatalf("Play: %v", err)
		}
		id := s.CurrentID()
		if err := s.Play("3c3d"); err != nil {
			t.Fatalf("Play: %v", err)
		}
		if got := len(s.MainLine()); got != 2 {
			t.Fatalf("MainLine = %d 手, want 2", got)
		}
		if err := s.Branch(id); err != nil {
			t.Fatalf("Branch: %v", err)
		}
		if got := s.MainLine(); len(got) != 0 {
			t.Errorf("MainLine = %v, want 空", got)
		}
	})

	// ⚠️ **棋譜が同じ手を本譜として持ってきたら印は消える**（実際に現れた指し手）。
	t.Run("取り直しで本譜に戻る", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		if err := s.Play("7g7f"); err != nil {
			t.Fatalf("Play: %v", err)
		}
		id := s.CurrentID()
		if err := s.Branch(id); err != nil {
			t.Fatalf("Branch: %v", err)
		}
		s.Graft([]string{"7g7f", "3c3d"})
		if n, _ := nodeOf(s, id); !n.Main {
			t.Error("取り直しても本譜に戻っていません")
		}
	})

	t.Run("知らない id は断る", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		if err := s.Branch(999); err == nil {
			t.Error("エラーになるべき")
		}
		// 根（開始局面）は手ではない。
		if err := s.Branch(0); err == nil {
			t.Error("根はエラーになるべき")
		}
	})
}

// 分かれ道の続き（本線）を選べること（「本線にする」。2026-08-18）。
//
// ⚠️ **エンジンの読み筋を 2 本足すと、どちらも同格の候補として並ぶ**
// （続きが決まっていない）。**それ自体は正しい**ので、そこから
// 「この続きを辿る」と決める操作がこれ。**`Branch` の裏返し。**
func TestStudyPromote(t *testing.T) {
	nodeOf := func(s *position.Study, id int) (position.Node, bool) {
		for _, n := range s.Nodes() {
			if n.ID == id {
				return n, true
			}
		}
		return position.Node{}, false
	}
	// forked は「変化の中で分かれ道を作った」木を返す（画面で起きるのと同じ形）。
	//
	//	7g7f 3c3d ── 2g2f（本譜）
	//	           └ 6g6f（変化の頭）── 8c8d / 4c4d ← ここが分かれ道
	forked := func(t *testing.T) (s *position.Study, at, a, b int) {
		t.Helper()
		s = position.NewStudy(hirate(t))
		for _, mv := range []string{"7g7f", "3c3d", "2g2f"} {
			if err := s.Play(mv); err != nil {
				t.Fatalf("Play %s: %v", mv, err)
			}
		}
		if err := s.GoTo(2); err != nil { // 3c3d まで戻る
			t.Fatalf("GoTo: %v", err)
		}
		// 変化の頭（本譜の 2g2f とは別の手）とその先の 2 本。
		if _, _, note := s.AddLine([]string{"6g6f", "8c8d"}, "e1"); note != "" {
			t.Fatalf("AddLine: %s", note)
		}
		if _, _, note := s.AddLine([]string{"6g6f", "4c4d"}, "e2"); note != "" {
			t.Fatalf("AddLine: %s", note)
		}
		for _, n := range s.Nodes() {
			switch n.USI {
			case "6g6f":
				at = n.ID
			case "8c8d":
				a = n.ID
			case "4c4d":
				b = n.ID
			}
		}
		if at == 0 || a == 0 || b == 0 {
			t.Fatalf("木が組めていません: %+v", s.Nodes())
		}
		return s, at, a, b
	}

	// ⚠️ **選ぶ前はどちらも同格**（同じ深さに並ぶ）で、**どちらも選べる**。
	t.Run("選ぶ前はどちらも同格", func(t *testing.T) {
		s, _, a, b := forked(t)
		na, _ := nodeOf(s, a)
		nb, _ := nodeOf(s, b)
		if na.Depth != nb.Depth {
			t.Errorf("候補の深さが違います: %d / %d", na.Depth, nb.Depth)
		}
		if !na.CanPromote || !nb.CanPromote {
			t.Errorf("どちらも選べるべき: %+v %+v", na, nb)
		}
	})

	// **選んだ手が続きになり、残りは 1 段下がること**（本譜と同じ形）。
	t.Run("選んだ手が続きになる", func(t *testing.T) {
		s, at, a, b := forked(t)
		if err := s.Promote(a); err != nil {
			t.Fatalf("Promote: %v", err)
		}
		na, _ := nodeOf(s, a)
		nb, _ := nodeOf(s, b)
		if !na.Chosen {
			t.Error("選んだ印が付いていません")
		}
		if na.Depth >= nb.Depth {
			t.Errorf("選んだ手が続きになっていません: 選=%d 枝=%d", na.Depth, nb.Depth)
		}
		// ⚠️ **今の経路（`Line`）もそちらを辿ること**（十字キーも連続解析もここを見る）。
		if err := s.GoTo(at); err != nil {
			t.Fatalf("GoTo: %v", err)
		}
		line := s.Line()
		if line[len(line)-1] != a {
			t.Errorf("経路が選んだ続きを辿っていません: %v", line)
		}
	})

	// ⚠️ **手も局面も評価値の拠り所（節点の id）も動かないこと。**
	t.Run("手も局面も動かない", func(t *testing.T) {
		s, _, a, _ := forked(t)
		at, ply, n := s.CurrentID(), s.Ply(), len(s.Nodes())
		if err := s.Promote(a); err != nil {
			t.Fatalf("Promote: %v", err)
		}
		if len(s.Nodes()) != n {
			t.Errorf("手数が変わりました: %d -> %d", n, len(s.Nodes()))
		}
		if s.CurrentID() != at || s.Ply() != ply {
			t.Errorf("局面が動きました: id=%d ply=%d", s.CurrentID(), s.Ply())
		}
	})

	// ⚠️ **もう本線があるなら断る**（黙って押しのけない）。
	t.Run("本線があるなら断る", func(t *testing.T) {
		s, _, a, b := forked(t)
		if err := s.Promote(a); err != nil {
			t.Fatalf("Promote: %v", err)
		}
		if err := s.Promote(b); err == nil {
			t.Error("もう本線があるのに通りました")
		}
		if n, _ := nodeOf(s, b); n.CanPromote {
			t.Error("選べないのに CanPromote が立っています")
		}
		// **外せば選び直せること**（`Branch` が唯一の外し方）。
		if err := s.Branch(a); err != nil {
			t.Fatalf("Branch: %v", err)
		}
		if err := s.Promote(b); err != nil {
			t.Errorf("外したのに選び直せません: %v", err)
		}
	})

	// ⚠️ **本譜の続きがあるあいだは、その枝を本線にできない**（同上）。
	t.Run("本譜の続きがあるなら断る", func(t *testing.T) {
		s := position.NewStudy(hirate(t))
		if err := s.Play("7g7f"); err != nil {
			t.Fatalf("Play: %v", err)
		}
		main := s.CurrentID()
		if err := s.GoTo(0); err != nil {
			t.Fatalf("GoTo: %v", err)
		}
		if _, _, note := s.AddLine([]string{"2g2f"}, "e1"); note != "" {
			t.Fatalf("AddLine: %s", note)
		}
		var side int
		for _, n := range s.Nodes() {
			if n.USI == "2g2f" {
				side = n.ID
			}
		}
		if err := s.Promote(side); err == nil {
			t.Error("本譜の続きがあるのに通りました")
		}
		// **「分岐にする」で本譜を外せば、こちらを本譜に据えられる。**
		if err := s.Branch(main); err != nil {
			t.Fatalf("Branch: %v", err)
		}
		if err := s.Promote(side); err != nil {
			t.Fatalf("Promote: %v", err)
		}
		if n, _ := nodeOf(s, side); !n.Main {
			t.Error("本譜に戻っていません")
		}
	})

	// ⚠️ **分かれ道の直後の手だけ**（その先は親から見れば 1 本道）。
	t.Run("分かれ道でなければ断る", func(t *testing.T) {
		s, _, a, _ := forked(t)
		if err := s.Promote(a); err != nil {
			t.Fatalf("Promote: %v", err)
		}
		// a の先（1 本道）は選べない。
		var deep int
		for _, n := range s.Nodes() {
			if n.Parent == a {
				deep = n.ID
			}
		}
		if deep != 0 {
			if err := s.Promote(deep); err == nil {
				t.Error("分かれ道でないのに通りました")
			}
		}
		if err := s.Promote(999); err == nil {
			t.Error("知らない id はエラーになるべき")
		}
		if err := s.Promote(0); err == nil {
			t.Error("根はエラーになるべき")
		}
	})
}
