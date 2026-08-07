package position

import (
	"testing"

	"github.com/ShinteLab/core/sfen"
)

const initialBoard = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"

// 盤面の SFEN を読んで書き戻したら元に戻ること。**訂正の土台**なので、
// ここが崩れると直した結果が別の局面になる。
func TestBoardRoundTrip(t *testing.T) {
	for _, board := range []string{
		initialBoard,
		"9/9/9/9/9/9/9/9/9",
		"lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSN1",
		// 成駒（"+" 付き）と後手の成駒
		"9/9/4+P4/9/9/9/4+p4/9/9",
	} {
		b, err := FromSFEN(board)
		if err != nil {
			t.Fatalf("FromSFEN(%q) = %v", board, err)
		}
		if got := b.SFEN(); got != board {
			t.Errorf("SFEN() = %q, want %q", got, board)
		}
	}
}

func TestBoardAtAndSet(t *testing.T) {
	b, err := FromSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}

	// 一段目の 9 筋（SFEN 記述順の先頭）は後手の香。
	c, err := b.At(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.IsEmpty() || c.Black() || c.Piece() != sfen.Lance {
		t.Errorf("At(0,0) = %+v, want 後手の香", c)
	}
	if got, want := c.Name(), "後手の香"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}

	// 空マスにする → SFEN に反映される。
	if err := b.Set(0, 0, Cell{}); err != nil {
		t.Fatal(err)
	}
	if got, want := b.SFEN(), "1nsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"; got != want {
		t.Errorf("SFEN() = %q, want %q", got, want)
	}

	// 駒を置き直す（訂正）。
	cell, err := NewCell(sfen.Rook, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Set(0, 0, cell); err != nil {
		t.Fatal(err)
	}
	if got, want := b.SFEN(), "+Rnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"; got != want {
		t.Errorf("SFEN() = %q, want %q", got, want)
	}
}

// ゼロ値が空マスであること。**公開フィールドにすると `Cell{}` が先手の歩
// （sfen.Pawn == 0）になる**ので、その事故が起きていないかを固定する。
func TestZeroCellIsEmpty(t *testing.T) {
	var c Cell
	if !c.IsEmpty() {
		t.Fatal("ゼロ値の Cell が空マスではありません")
	}
	if got := c.Mark(); got != "" {
		t.Errorf("Mark() = %q, want %q", got, "")
	}
	if got := c.Piece(); got != sfen.NotFound {
		t.Errorf("Piece() = %d, want NotFound", got)
	}
}

func TestNewCellRejects(t *testing.T) {
	if _, err := NewCell(sfen.NotFound, true, false); err == nil {
		t.Error("不正な駒コードが通りました")
	}
	if _, err := NewCell(sfen.Gold, true, true); err == nil {
		t.Error("成れない駒（金）が通りました")
	}
	if _, err := NewCell(sfen.King, false, true); err == nil {
		t.Error("成れない駒（玉）が通りました")
	}
}

func TestBoardOutOfRange(t *testing.T) {
	b := NewBoard()
	if _, err := b.At(9, 0); err == nil {
		t.Error("At(9,0) がエラーになりません")
	}
	if err := b.Set(0, -1, Cell{}); err == nil {
		t.Error("Set(0,-1) がエラーになりません")
	}
}

// 壊れた盤面でも読めた分は返すこと（設計原則3）。**直すために受け取る**ので、
// 読めないところがあるからと言って何も返さないのでは訂正のしようがない。
func TestFromSFENBrokenBoardStillUsable(t *testing.T) {
	// 3 段目の筋数が足りない。**そこで解析は止まるが、1〜2 段目は入っている。**
	b, err := FromSFEN("lnsgkgsnl/1r5b1/pppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL")
	if err == nil {
		t.Fatal("筋数の足りない盤面がエラーになりません")
	}
	if b == nil {
		t.Fatal("盤が返っていません")
	}
	c, err := b.At(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.IsEmpty() {
		t.Error("読めた分が入っていません")
	}
}

func TestBoardClone(t *testing.T) {
	b, err := FromSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	c := b.Clone()
	if err := c.Set(0, 0, Cell{}); err != nil {
		t.Fatal(err)
	}
	if b.SFEN() != initialBoard {
		t.Error("複製を直したら元の盤まで変わりました")
	}
}
