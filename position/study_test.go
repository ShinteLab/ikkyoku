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

	first, added, note := s.AddLine([]string{"3c3d", "2g2f", "8c8d"})
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
	_, added, note := s.AddLine([]string{"7g7f", "3c3d", "6g6f"})
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
	_, added, note := s.AddLine([]string{"7g7f", "9i9h"})
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
	if _, added, note := s.AddLine([]string{"2g2f"}); added != 1 || note != "" {
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
	if _, added, _ := s.AddLine([]string{"2g2f"}); added != 1 {
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
