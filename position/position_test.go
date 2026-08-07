package position

import (
	"strings"
	"testing"

	"github.com/ShinteLab/core/sfen"
)

// 初期局面は駒台が空。手番を決めれば SFEN が組み上がる。
func TestSFEN(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}

	// 手番未決では組み立てない（先手に倒さない）。
	if _, err := p.SFEN(); err == nil {
		t.Fatal("手番が未決なのに SFEN が返りました")
	}
	// 盤面だけなら手番が未決でも取れる。
	if got := p.BoardSFEN(); got != initialBoard {
		t.Errorf("BoardSFEN() = %q, want %q", got, initialBoard)
	}

	p.Turn = TurnBlack
	got, err := p.SFEN()
	if err != nil {
		t.Fatal(err)
	}
	if want := initialBoard + " b - 1"; got != want {
		t.Errorf("SFEN() = %q, want %q", got, want)
	}

	// 手数は指定があればそれを書く。
	p.Turn = TurnWhite
	p.MoveNumber = 42
	got, err = p.SFEN()
	if err != nil {
		t.Fatal(err)
	}
	if want := initialBoard + " w - 42"; got != want {
		t.Errorf("SFEN() = %q, want %q", got, want)
	}
}

// 盤から駒を抜くと、その分が駒台の逆算に出ること（core/sfen がやっている）。
func TestHandTotalFollowsBoard(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	if n := p.HandTotal()[sfen.Pawn]; n != 0 {
		t.Fatalf("初期局面の駒台に歩が %d 枚あります", n)
	}

	// 先手の歩を 1 枚消す（訂正で消したのと同じ）。
	if err := p.Board.Set(6, 0, Cell{}); err != nil {
		t.Fatal(err)
	}
	if n := p.HandTotal()[sfen.Pawn]; n != 1 {
		t.Errorf("HandTotal[歩] = %d, want 1", n)
	}
}

// 駒台の割り振り。**合計は盤から逆算されるので、割り振りで壊れないこと**が肝。
func TestHandsSplit(t *testing.T) {
	// 歩を 2 枚、盤から抜いた局面。
	p, err := FromBoardSFEN("lnsgkgsnl/1r5b1/1ppppppp1/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL")
	if err != nil {
		t.Fatal(err)
	}
	if n := p.HandTotal()[sfen.Pawn]; n != 2 {
		t.Fatalf("HandTotal[歩] = %d, want 2", n)
	}

	// 既定では全部後手側（先手に 0 枚）。**先後不明を先手に寄せない。**
	black, white := p.Hands()
	if black[sfen.Pawn] != 0 || white[sfen.Pawn] != 2 {
		t.Errorf("既定の割り振り = 先手%d/後手%d, want 0/2", black[sfen.Pawn], white[sfen.Pawn])
	}

	if err := p.SetHandBlack(sfen.Pawn, 2); err != nil {
		t.Fatal(err)
	}
	black, white = p.Hands()
	if black[sfen.Pawn] != 2 || white[sfen.Pawn] != 0 {
		t.Errorf("割り振り = 先手%d/後手%d, want 2/0", black[sfen.Pawn], white[sfen.Pawn])
	}

	p.Turn = TurnBlack
	got, err := p.SFEN()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, " 2P ") {
		t.Errorf("SFEN() = %q に先手の歩 2 枚が出ていません", got)
	}

	// 合計を超える指定は通らない。
	if err := p.SetHandBlack(sfen.Pawn, 3); err == nil {
		t.Error("合計を超える割り振りが通りました")
	}
	if err := p.SetHandBlack(sfen.King, 1); err == nil {
		t.Error("玉が駒台に入りました")
	}

	// 訂正で合計が減っても、割り振りが合計を超えたままにならない。
	if err := p.Board.Set(6, 0, Cell{}); err != nil { // 歩をもう 1 枚消す → 合計 3
		t.Fatal(err)
	}
	if err := p.SetHandBlack(sfen.Pawn, 3); err != nil {
		t.Fatal(err)
	}
	pawn, err := NewCell(sfen.Pawn, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Board.Set(6, 0, pawn); err != nil { // 戻す → 合計 2
		t.Fatal(err)
	}
	black, white = p.Hands()
	if black[sfen.Pawn]+white[sfen.Pawn] != 2 {
		t.Errorf("割り振りの合計 = %d, want 2（逆算した合計を超えている）",
			black[sfen.Pawn]+white[sfen.Pawn])
	}
}

// 直している最中の盤は壊れて当たり前。**エラーではなく警告として出す**（設計原則3）。
func TestWarnings(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings()) != 0 {
		t.Errorf("初期局面に警告が出ました: %v", p.Warnings())
	}

	// 二歩にする。
	pawn, err := NewCell(sfen.Pawn, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Board.Set(5, 0, pawn); err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings()) == 0 {
		t.Fatal("二歩なのに警告が出ません")
	}
	found := false
	for _, v := range p.Violations() {
		if v.Check == sfen.CheckDoubledPawn {
			found = true
		}
	}
	if !found {
		t.Errorf("二歩の違反が入っていません: %v", p.Warnings())
	}
}

func TestClone(t *testing.T) {
	p, err := FromBoardSFEN("lnsgkgsnl/1r5b1/1ppppppp1/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL")
	if err != nil {
		t.Fatal(err)
	}
	p.Turn = TurnBlack
	if err := p.SetHandBlack(sfen.Pawn, 2); err != nil {
		t.Fatal(err)
	}

	c := p.Clone()
	if err := c.SetHandBlack(sfen.Pawn, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Board.Set(0, 0, Cell{}); err != nil {
		t.Fatal(err)
	}

	black, _ := p.Hands()
	if black[sfen.Pawn] != 2 {
		t.Error("複製の割り振りを変えたら元まで変わりました")
	}
	if p.BoardSFEN() != "lnsgkgsnl/1r5b1/1ppppppp1/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL" {
		t.Error("複製の盤を直したら元まで変わりました")
	}
}

func TestTurnString(t *testing.T) {
	for turn, want := range map[Turn]string{
		TurnUnknown: "手番不明",
		TurnBlack:   "先手番",
		TurnWhite:   "後手番",
	} {
		if got := turn.String(); got != want {
			t.Errorf("Turn(%d).String() = %q, want %q", turn, got, want)
		}
	}
}
