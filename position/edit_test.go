package position

import (
	"testing"

	"github.com/ShinteLab/core/sfen"
)

func stockOf(inv []Stock, piece int) Stock {
	for _, s := range inv {
		if s.Piece == piece {
			return s
		}
	}
	return Stock{Piece: -1}
}

// 初期局面は全部盤の上にあるので、残りは 0。
func TestInventoryInitial(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	inv := p.Inventory()
	if len(inv) != 8 {
		t.Fatalf("在庫の駒種が %d 個です（歩香桂銀金角飛王で 8）", len(inv))
	}
	// **駒台に置く順。** 訂正 UI がこの順でそのまま並べるので固定しておく。
	want := []int{sfen.Pawn, sfen.Lance, sfen.Knight, sfen.Silver,
		sfen.Gold, sfen.Bishop, sfen.Rook, sfen.King}
	for i, w := range want {
		if inv[i].Piece != w {
			t.Errorf("在庫の %d 番目 = %s, want %s", i, inv[i].Name, sfen.Name(w))
		}
	}
	for _, s := range inv {
		if s.Rest != 0 {
			t.Errorf("%s の残り = %d, want 0", s.Name, s.Rest)
		}
		if s.Over() {
			t.Errorf("%s が過剰になっています", s.Name)
		}
	}
	if got := stockOf(inv, sfen.Pawn); got.Limit != 18 || got.Black != 9 || got.White != 9 {
		t.Errorf("歩 = %+v, want Limit18 Black9 White9", got)
	}
}

// **認識が余計な駒を作った状態。** これを「負の残り」として出せることが
// 訂正 UI の前提（`L: 11枚（上限4）` のような結果を直せるようにするため）。
func TestInventoryOverLimit(t *testing.T) {
	// 香を 4 枚（上限）に加えて 2 枚余計に置く。
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	lance, err := NewCell(sfen.Lance, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Board.Set(4, 0, lance); err != nil {
		t.Fatal(err)
	}
	if err := p.Board.Set(4, 1, lance); err != nil {
		t.Fatal(err)
	}

	s := stockOf(p.Inventory(), sfen.Lance)
	if s.Rest != -2 {
		t.Errorf("香の残り = %d, want -2", s.Rest)
	}
	if !s.Over() {
		t.Error("香が過剰と判定されません")
	}
	// 過剰は警告にも出る（訂正 UI はどちらも出せる）。
	if len(p.Warnings()) == 0 {
		t.Error("上限を超えているのに警告が出ません")
	}
}

// 在庫が尽きていても置けること。**「余計な駒を外す前に正しい駒を置けない」を避ける。**
func TestPlaceIgnoresStock(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Place(4, 4, sfen.Rook, true, false); err != nil {
		t.Fatalf("在庫が無いと置けませんでした: %v", err)
	}
	if s := stockOf(p.Inventory(), sfen.Rook); s.Rest != -1 {
		t.Errorf("飛の残り = %d, want -1", s.Rest)
	}
}

func TestMove(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	// 先手の歩（7 段目）を 1 つ上へ。
	if err := p.Move(6, 0, 5, 0); err != nil {
		t.Fatal(err)
	}
	if got, want := p.BoardSFEN(),
		"lnsgkgsnl/1r5b1/ppppppppp/9/9/P8/1PPPPPPPP/1B5R1/LNSGKGSNL"; got != want {
		t.Errorf("BoardSFEN() = %q, want %q", got, want)
	}
	// 移動しても盤上の枚数は変わらない（残りは 0 のまま）。
	if s := stockOf(p.Inventory(), sfen.Pawn); s.Rest != 0 {
		t.Errorf("歩の残り = %d, want 0", s.Rest)
	}

	if err := p.Move(4, 4, 3, 3); err == nil {
		t.Error("空マスからの移動がエラーになりません")
	}
	if err := p.Move(0, 0, 9, 0); err == nil {
		t.Error("盤の外への移動がエラーになりません")
	}
}

// 移動先に駒があれば**入れ替える**（2026-08-18。以前は上書きしていた）。
//
// ⚠️ **駒が 1 枚も消えないことが要点。** 訂正で起きるのは「2 つのマスの駒を
// 取り違えている」なので、すげ替えが 1 操作で済むのが速い。上書きに戻すと、
// **退かす駒を先に避けておかないと駒が黙って消える。**
func TestMoveSwaps(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Move(8, 0, 8, 1); err != nil { // 先手の香を桂の上へ
		t.Fatal(err)
	}
	// 香と桂が入れ替わっただけ＝在庫は動かない。
	inv := p.Inventory()
	if s := stockOf(inv, sfen.Knight); s.Rest != 0 {
		t.Errorf("桂の残り = %d, want 0（消えていないこと）", s.Rest)
	}
	if s := stockOf(inv, sfen.Lance); s.Rest != 0 {
		t.Errorf("香の残り = %d, want 0", s.Rest)
	}
	// 位置がそのまま入れ替わっていること。
	if c, _ := p.Board.At(8, 0); c.Mark() != "N" {
		t.Errorf("9九 = %q, want \"N\"（桂が来ているはず）", c.Mark())
	}
	if c, _ := p.Board.At(8, 1); c.Mark() != "L" {
		t.Errorf("8九 = %q, want \"L\"", c.Mark())
	}
}

// 空マスへ動かしたら、移動元は空になる（入れ替えの相手が空マスなだけ）。
func TestMoveToEmptyLeavesEmpty(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Move(6, 0, 5, 0); err != nil {
		t.Fatal(err)
	}
	if c, _ := p.Board.At(6, 0); !c.IsEmpty() {
		t.Errorf("移動元が空になっていない: %q", c.Mark())
	}
}

func TestRemoveGoesBackToStock(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Remove(0, 0); err != nil {
		t.Fatal(err)
	}
	if s := stockOf(p.Inventory(), sfen.Lance); s.Rest != 1 {
		t.Errorf("香の残り = %d, want 1", s.Rest)
	}
}

// 盤 → 駒台 → 盤の往復。**駒台への出し入れはドラッグ 1 回で完結すること**が要点で、
// 「外す」と「どちらの駒台か決める」が別操作だと、割り振りが未決のまま溜まる。
func TestToHandAndFromHand(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}

	// 先手の歩を先手の駒台へ。
	if err := p.ToHand(6, 0, true); err != nil {
		t.Fatal(err)
	}
	black, _ := p.Hands()
	if black[sfen.Pawn] != 1 {
		t.Errorf("先手の駒台の歩 = %d, want 1", black[sfen.Pawn])
	}
	// **未割り当てにしない**（ドラッグ先で先後が決まっている）。
	if len(p.Unassigned()) != 0 {
		t.Errorf("未割り当てが残っています: %v", p.Unassigned())
	}
	if s := stockOf(p.Inventory(), sfen.Pawn); s.HandBlack != 1 || s.Unassigned != 0 {
		t.Errorf("在庫 = %+v, want HandBlack1 Unassigned0", s)
	}

	// 駒台から盤へ戻す。**割り振りも 1 減る**（減らないと、置いた 1 枚が
	// もう一方の側から引かれてしまう）。
	if err := p.FromHand(5, 0, sfen.Pawn, true); err != nil {
		t.Fatal(err)
	}
	black, white := p.Hands()
	if black[sfen.Pawn] != 0 || white[sfen.Pawn] != 0 {
		t.Errorf("駒台 = 先手%d/後手%d, want 0/0", black[sfen.Pawn], white[sfen.Pawn])
	}
	if len(p.Unassigned()) != 0 {
		t.Errorf("未割り当てが残っています: %v", p.Unassigned())
	}

	// 持っていない側からは置けない。
	if err := p.FromHand(4, 4, sfen.Pawn, false); err == nil {
		t.Error("駒台に無い駒が置けました")
	}
	// 玉は駒台に載らない。
	if err := p.ToHand(8, 4, true); err == nil {
		t.Error("玉が駒台に載りました")
	}
	if err := p.ToHand(4, 4, true); err == nil {
		t.Error("空マスから駒台へ移せました")
	}
}

// 成駒を駒台に載せると元の駒に戻ること（駒台に成駒は無い）。
func TestToHandDemotes(t *testing.T) {
	p, err := FromBoardSFEN("9/9/9/9/9/9/9/9/+P8")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ToHand(8, 0, true); err != nil {
		t.Fatal(err)
	}
	black, _ := p.Hands()
	if black[sfen.Pawn] != 1 {
		t.Errorf("先手の駒台の歩 = %d, want 1（と金は歩として載る）", black[sfen.Pawn])
	}
}

// 「外すだけ」は先後を決めない。**どちらの駒台か分からないまま外したいことがある。**
func TestRemoveLeavesUnassigned(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Remove(6, 0); err != nil {
		t.Fatal(err)
	}
	if n := p.Unassigned()[sfen.Pawn]; n != 1 {
		t.Errorf("未割り当て = %d, want 1", n)
	}
	black, white := p.Hands()
	if black[sfen.Pawn] != 0 || white[sfen.Pawn] != 0 {
		t.Errorf("駒台 = 先手%d/後手%d, want 0/0（決めていない）",
			black[sfen.Pawn], white[sfen.Pawn])
	}
}

// 1 マスを回す。**訂正でマスにやりたいことはこの 4 通りしかない**ので、
// 操作を左右のクリックに分けずに 1 つで回す。
func TestCycleCell(t *testing.T) {
	p, err := FromBoardSFEN("9/9/9/9/9/9/9/9/P8")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"+P", "p", "+p", "P"}
	for i, w := range want {
		if err := p.CycleCell(8, 0); err != nil {
			t.Fatal(err)
		}
		c, err := p.Board.At(8, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.Mark(); got != w {
			t.Fatalf("%d 回目 = %q, want %q", i+1, got, w)
		}
	}
}

// 成れない駒（金・玉）は先後の 2 状態だけを回る。
func TestCycleCellUnpromotable(t *testing.T) {
	for _, tc := range []struct{ start, next string }{
		{"G8", "g"},
		{"K8", "k"},
		{"9/9/9/9/9/9/9/9/g8", "G"},
	} {
		p, err := FromBoardSFEN(func() string {
			if len(tc.start) > 3 {
				return tc.start
			}
			return tc.start + "/9/9/9/9/9/9/9/9"
		}())
		if err != nil {
			t.Fatal(err)
		}
		rank := 0
		if len(tc.start) > 3 {
			rank = 8
		}
		if err := p.CycleCell(rank, 0); err != nil {
			t.Fatal(err)
		}
		c, err := p.Board.At(rank, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.Mark(); got != tc.next {
			t.Errorf("%s を回すと %q, want %q", tc.start, got, tc.next)
		}
	}
}

func TestTogglePromotedAndFlipSide(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	// 先手の歩を成る → と金。
	if err := p.TogglePromoted(6, 0); err != nil {
		t.Fatal(err)
	}
	c, err := p.Board.At(6, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Promoted() || c.Name() != "先手のと" {
		t.Errorf("成り after = %+v (%s)", c, c.Name())
	}
	// 成駒でも枚数はベース駒に合算されるので、残りは 0 のまま。
	if s := stockOf(p.Inventory(), sfen.Pawn); s.Rest != 0 {
		t.Errorf("歩の残り = %d, want 0", s.Rest)
	}

	// 先後の反転。
	if err := p.FlipSide(6, 0); err != nil {
		t.Fatal(err)
	}
	c, _ = p.Board.At(6, 0)
	if c.Black() {
		t.Error("先後が入れ替わっていません")
	}

	// 金は成れない。玉も。
	if err := p.TogglePromoted(8, 3); err == nil {
		t.Error("金が成れてしまいました")
	}
	if err := p.TogglePromoted(4, 4); err == nil {
		t.Error("空マスで成りがエラーになりません")
	}
}
