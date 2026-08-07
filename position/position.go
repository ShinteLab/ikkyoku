package position

import (
	"fmt"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/core/sfen"
)

// Game は対局の素性（誰と誰・棋戦名・対局場所・開始日時・持ち時間）。
//
// **core/kifu の Document をそのまま使う。** 対局者のフィールドを ikkyoku 側で
// 定義し直さないこと（KIF に書き出すのも kicho に渡すのも同じ語彙で済む）。
// Moves は使わない。**分岐は core/kifu が木を持てるようになってから**乗せる。
type Game = kifu.Document

// Turn は手番。
//
// **盤面からは決まらない**（設計原則5）。手数も無いので導出もできない。
// だから「不明」がゼロ値で、決めるのは人間（UI のトグル）。
// **ここで先手に寄せるような既定値を作らないこと。**
type Turn int

const (
	// TurnUnknown は手番が未決。
	TurnUnknown Turn = iota
	// TurnBlack は先手番。
	TurnBlack
	// TurnWhite は後手番。
	TurnWhite
)

// String は日本語表記を返す。
func (t Turn) String() string {
	switch t {
	case TurnBlack:
		return "先手番"
	case TurnWhite:
		return "後手番"
	}
	return "手番不明"
}

// mark は SFEN の手番表記。未決では呼ばない。
func (t Turn) mark() string {
	if t == TurnWhite {
		return "w"
	}
	return "b"
}

// Position は 1 つの局面。盤面に加えて、**盤面からは決まらないもの**
// （手番・駒台の先後の割り振り・手数）を人が決めた結果を載せる。
//
// 他の局面とは完全に独立している（履歴を持たない）。
type Position struct {
	// Board は盤面。訂正はここへの Set で行う。
	Board *Board
	// Turn は手番。TurnUnknown のままでも盤は成立する。
	Turn Turn
	// MoveNumber は手数。0 は「不明」。
	//
	// 中継の画面から読めることもあるが、**撮った 1 枚からは分からないのが普通**。
	// 分からなくても解析はできる（千日手・連続王手の判定ができないだけ）。
	MoveNumber int

	// handBlack は駒台のうち先手に割り振った枚数（ベース駒コード → 枚数）。
	//
	// **合計は持たない。** 駒台の合計は盤上の駒数から逆算されるもので、盤を直すたびに
	// 変わる。ここが持つのは「そのうち何枚が先手のものか」だけなので、
	// **訂正で合計が変わっても割り振りが合計を超えられない**（Hands で丸める）。
	handBlack map[int]int
}

// New は盤面から局面を作る。手番は未決、手数は不明。
func New(b *Board) *Position {
	if b == nil {
		b = NewBoard()
	}
	return &Position{Board: b, handBlack: map[int]int{}}
}

// FromBoardSFEN は盤面部分の SFEN から局面を作る。
// **recognize が返した盤面をここに渡す**のが、画像を知っている層との継ぎ目。
//
// 盤面が壊れていても読めた分で局面を作って返す（error は「どこが読めなかったか」）。
func FromBoardSFEN(board string) (*Position, error) {
	b, err := FromSFEN(board)
	return New(b), err
}

// HandTotal は駒台にあるはずの枚数を盤上の駒数から逆算して返す
// （ベース駒コード → 枚数）。**先後の区別は付かない。**
//
// 逆算は core/sfen の仕事（`Inspect(...).Hands`）。**自前で数えないこと。**
func (p *Position) HandTotal() map[int]int {
	return p.Board.Inspect(sfen.CheckCounts).Hands
}

// Hands は先後に割り振った駒台を返す。
//
// 先手は SetHandBlack で決めた枚数（合計を超えていれば合計まで丸める）、
// **後手は残り全部**。この形にしてあるので、**割り振りで合計が壊れることが無い**
// （UI 側は「先手に何枚」を動かすだけでよく、後手側は勝手に辻褄が合う）。
func (p *Position) Hands() (black, white map[int]int) {
	total := p.HandTotal()
	black = map[int]int{}
	white = map[int]int{}
	for base, n := range total {
		if n <= 0 {
			continue
		}
		b := p.handBlack[base]
		if b < 0 {
			b = 0
		}
		if b > n {
			b = n
		}
		if b > 0 {
			black[base] = b
		}
		if n-b > 0 {
			white[base] = n - b
		}
	}
	return black, white
}

// SetHandBlack は駒台のうち先手のものを n 枚にする（残りは後手）。
//
// **どちらの持ち駒かは局面からは決まらない**ので、決めるのは人間。
// 合計を超える指定はエラー（黙って丸めない。UI の操作は意図なので）。
func (p *Position) SetHandBlack(base, n int) error {
	if sfen.Letter(base) == "None" || base == sfen.King {
		return fmt.Errorf("ikkyoku/position: 駒台に持てない駒です: %d", base)
	}
	if n < 0 {
		return fmt.Errorf("ikkyoku/position: 枚数が負です: %d", n)
	}
	if total := p.HandTotal()[base]; n > total {
		return fmt.Errorf("ikkyoku/position: %s は駒台に %d 枚しかありません: %d",
			sfen.Name(base), total, n)
	}
	if p.handBlack == nil {
		p.handBlack = map[int]int{}
	}
	p.handBlack[base] = n
	return nil
}

// BoardSFEN は盤面部分だけの SFEN を返す。手番が未決でも使える。
func (p *Position) BoardSFEN() string { return p.Board.SFEN() }

// SFEN は局面全体の SFEN（盤面・手番・持ち駒・手数）を返す。
//
// **手番が未決ならエラー。** 盤面だけが要るなら BoardSFEN を使うこと。
// ここで先手に倒して文字列を作ると、決めていない手番が決まったことになってしまう
// （エンジンに渡ってからでは、どちらの手番として読まれたのかが分からない）。
//
// 手数は 0（不明）でも 1 と書く。SFEN の書式が手数を必須にしているためで、
// 手番と違って**局面の解釈を変えない**（履歴を持たない以上どのみち使えない）。
func (p *Position) SFEN() (string, error) {
	if p.Turn == TurnUnknown {
		return "", fmt.Errorf("ikkyoku/position: 手番が決まっていません")
	}
	black, white := p.Hands()
	num := p.MoveNumber
	if num <= 0 {
		num = 1
	}
	return fmt.Sprintf("%s %s %s %d",
		p.Board.SFEN(), p.Turn.mark(), sfen.FormatHands(black, white), num), nil
}

// Warnings は局面として成立していない点を日本語で返す（無ければ空）。
//
// **訂正 UI が「ここが怪しい」を出すための入口。** 直している最中の盤は当然
// 途中で壊れるので、**エラーとして扱わないこと**（設計原則3）。
func (p *Position) Warnings() []string {
	return p.Board.Inspect(sfen.CheckAll).Messages()
}

// Violations は違反を種類つきで返す。どの駒・どのマスかで選り分けたいとき用。
func (p *Position) Violations() []sfen.Violation {
	return p.Board.Inspect(sfen.CheckAll).Violations
}

// Clone は局面を複製する。**分岐を作るときに元の局面を壊さないため。**
func (p *Position) Clone() *Position {
	c := &Position{
		Board:      p.Board.Clone(),
		Turn:       p.Turn,
		MoveNumber: p.MoveNumber,
		handBlack:  make(map[int]int, len(p.handBlack)),
	}
	for k, v := range p.handBlack {
		c.handBlack[k] = v
	}
	return c
}
