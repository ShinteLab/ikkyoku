// Package position は「とある局面」を扱う層。訂正（Phase 3 の局面矯正）と、
// その先の検討（Phase 5）がここに入る。
//
// **このパッケージは画像を知らない。** 画像 → 盤面は recognize の仕事で、
// こちらが受け取るのは盤面の SFEN 文字列から先。撮った PNG はディスクに残っていれば
// 足りるので、**画像もその座標系もここの型に持ち込まないこと**（持ち込むと、局面を
// 持ち回るコードが全部それを引きずる）。
//
// **将棋の仕様は core/sfen が持つ。** 駒文字の対応・盤面文字列の解析と生成・
// 駒数や二歩の検証はすべて向こうにある。ここにあるのは「人が 1 マスずつ直せる形」と
// 「盤面からは決まらないもの（手番・駒台の先後）を人が決めた結果」を載せる器だけ。
//
// **履歴に依存しない**（設計原則1）。1 つの局面は他の局面と完全に独立していて、
// 前の局面を引数に取る関数をここに足さないこと。
package position

import (
	"fmt"

	"github.com/ShinteLab/core/sfen"
)

// Cell は盤面の 1 マス。
//
// **ゼロ値が空マス。** フィールドを非公開にしてあるのはそのためで、
// 公開フィールドにすると `Cell{}` が「先手の歩」（sfen.Pawn == 0）になってしまう。
type Cell struct {
	filled   bool
	piece    int // sfen のベース駒コード（成駒は promoted で表す）
	black    bool
	promoted bool
}

// NewCell は駒の載ったマスを作る。piece は sfen のベース駒コード
// （成駒コードは受け付けない。成っているかは promoted で表す）。
func NewCell(piece int, black, promoted bool) (Cell, error) {
	if sfen.Letter(piece) == "None" {
		return Cell{}, fmt.Errorf("ikkyoku/position: 駒コードが不正です: %d", piece)
	}
	if promoted && (piece == sfen.Gold || piece == sfen.King) {
		return Cell{}, fmt.Errorf("ikkyoku/position: %s は成れません", sfen.Name(piece))
	}
	return Cell{filled: true, piece: piece, black: black, promoted: promoted}, nil
}

// IsEmpty は空マスかを返す。
func (c Cell) IsEmpty() bool { return !c.filled }

// Piece はベース駒コードを返す（空マスなら sfen.NotFound）。
func (c Cell) Piece() int {
	if !c.filled {
		return sfen.NotFound
	}
	return c.piece
}

// Black は先手の駒かを返す（空マスでは意味を持たない）。
func (c Cell) Black() bool { return c.black }

// Promoted は成駒かを返す。
func (c Cell) Promoted() bool { return c.promoted }

// Mark は SFEN でのマス表記を返す（空マスは ""）。後手は小文字。
func (c Cell) Mark() string {
	if !c.filled {
		return ""
	}
	l := sfen.Letter(c.piece)
	if !c.black {
		l = lower(l)
	}
	if c.promoted {
		return "+" + l
	}
	return l
}

// Name は人に見せる日本語名を返す（"先手の歩" など。空マスは "空"）。
// 訂正 UI がマスを説明するのに使う。
func (c Cell) Name() string {
	if !c.filled {
		return "空"
	}
	code := c.piece
	if c.promoted {
		code = promotedCode(c.piece)
	}
	side := "先手"
	if !c.black {
		side = "後手"
	}
	return side + "の" + sfen.Name(code)
}

// promotedCode はベース駒コードを成駒コードにする（日本語名を引くためだけに使う）。
// **表記の変換ではない**ので sfen 側には足していない（SFEN の成駒は "+" + ベース駒文字）。
func promotedCode(base int) int {
	switch base {
	case sfen.Pawn:
		return sfen.GrowthPawn
	case sfen.Lance:
		return sfen.GrowthLance
	case sfen.Knight:
		return sfen.GrowthKnight
	case sfen.Silver:
		return sfen.GrowthSilver
	case sfen.Rook:
		return sfen.GrowthRook
	case sfen.Bishop:
		return sfen.GrowthBishop
	}
	return base
}

func lower(s string) string {
	if len(s) == 1 && s[0] >= 'A' && s[0] <= 'Z' {
		return string(s[0] + 32)
	}
	return s
}

// Board は 1 マスずつ直せる 9x9 の盤面。
//
// 座標は **SFEN の記述順**（rank=0 が一段目、file=0 が 9 筋）。core/sfen の
// ParseBoard / FormatBoard と同じ並びなので、**ここで座標を読み替えないこと**。
type Board struct {
	cells [9][9]Cell
}

// NewBoard は駒の無い盤を作る。
func NewBoard() *Board { return &Board{} }

// FromSFEN は SFEN の盤面部分（'/' 区切りの 9 段）から盤を作る。
//
// **壊れた盤面でも読めた分は入れて返す**（設計原則3「段階的に劣化すること」）。
// 認識結果を訂正するのがこのパッケージの役目なので、読めないところがあるからと
// いって何も返さないのでは訂正のしようがない。error は「どこが読めなかったか」。
//
// ただし**段の数が 9 でない場合だけは 1 マスも入らない**（core/sfen が段を分ける
// 時点で止まるため）。盤は空で返る。
func FromSFEN(board string) (*Board, error) {
	b := NewBoard()
	err := sfen.ParseBoard(board, func(rank, file, base int, black, promoted bool) {
		if rank < 0 || rank >= 9 || file < 0 || file >= 9 {
			return
		}
		if sfen.Letter(base) == "None" {
			return // 読めない駒文字は空マスのまま残す（訂正 UI で埋めてもらう）
		}
		b.cells[rank][file] = Cell{filled: true, piece: base, black: black, promoted: promoted}
	})
	if err != nil {
		return b, fmt.Errorf("ikkyoku/position: 盤面を読めません: %w", err)
	}
	return b, nil
}

// At は 1 マスを返す。
func (b *Board) At(rank, file int) (Cell, error) {
	if !inRange(rank, file) {
		return Cell{}, outOfRange(rank, file)
	}
	return b.cells[rank][file], nil
}

// Set は 1 マスを置き換える。**訂正の入口。**
// 空マスにするならゼロ値の Cell を渡す。
func (b *Board) Set(rank, file int, c Cell) error {
	if !inRange(rank, file) {
		return outOfRange(rank, file)
	}
	b.cells[rank][file] = c
	return nil
}

// SFEN は盤面部分の SFEN を返す（手番・持ち駒・手数は含まない）。
func (b *Board) SFEN() string {
	return sfen.FormatBoard(func(rank, file int) string {
		return b.cells[rank][file].Mark()
	})
}

// Inspect は今の盤面を core/sfen に調べさせる。駒数・駒台の逆算・違反が返る。
//
// **検証を自前で書かないこと。** 二歩も行き所のない駒も駒数の上限も仕様であって、
// 仕様は core に一本化してある。
func (b *Board) Inspect(checks sfen.Check) *sfen.BoardInfo {
	return sfen.Inspect(b.SFEN(), checks)
}

// Clone は盤を複製する。検討で分岐を作るときに、元の局面を壊さないために使う。
func (b *Board) Clone() *Board {
	c := *b
	return &c
}

func inRange(rank, file int) bool {
	return rank >= 0 && rank < 9 && file >= 0 && file < 9
}

func outOfRange(rank, file int) error {
	return fmt.Errorf("ikkyoku/position: 盤の外です: rank=%d file=%d", rank, file)
}
