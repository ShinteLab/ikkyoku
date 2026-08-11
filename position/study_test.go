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
	ms := s.Moves()
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

// 戻って別の手を指すと、**そこから先の手順は捨てる**（今は一直線）。
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
	if len(s.Moves()) != 2 || s.Ply() != 1 {
		t.Fatalf("戻っただけで手順が消えました: moves=%d ply=%d", len(s.Moves()), s.Ply())
	}
	if got := strings.Join(s.Played(), " "); got != "7g7f" {
		t.Errorf("Played = %q, want %q（先の手を渡さないこと）", got, "7g7f")
	}
	// そこで別の手を指すと、先は捨てる。
	if err := s.Play("8c8d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if got := strings.Join(s.Played(), " "); got != "7g7f 8c8d" {
		t.Errorf("分岐後の Played = %q", got)
	}
	if len(s.Moves()) != 2 {
		t.Errorf("分岐したのに古い手が残っています: %+v", s.Moves())
	}
}

// Undo は「指し間違えた」の取り消しなので、**手順からも消す**。
func TestStudyUndo(t *testing.T) {
	s := position.NewStudy(hirate(t))
	if err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if err := s.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if s.Ply() != 0 || len(s.Moves()) != 0 {
		t.Errorf("Undo で消えていません: ply=%d moves=%+v", s.Ply(), s.Moves())
	}
	if err := s.Undo(); err == nil {
		t.Error("根から更に戻せました")
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
