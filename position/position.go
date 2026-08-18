package position

import (
	"fmt"
	"strings"

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
	// HandsFixed は「駒台は書いてあるとおりで、盤上から逆算しない」ことを表す。
	//
	// **画像から作った局面では常に false。** 撮った盤に写っていない駒は駒台に
	// あるはずなので、駒数保存則の逆算（HandTotal）が訂正 UI の拠り所になる。
	//
	// ⚠️ **持ち駒まで書いてある SFEN から作った局面（KIF の初期局面を含む）では
	// true にする。** 駒落ちのように**そもそも盤にも駒台にも存在しない駒**があると、
	// 逆算は「駒台にあるはず」と言い続け、未決が消えないので **SFEN() が永久に
	// 組み上がらない**（＝二枚落ちの棋譜が 1 手も進められない。実際に踏んだ）。
	// 持ち駒が明記されている以上、そこに推測を混ぜる理由が無い。
	HandsFixed bool
	// MoveNumber は手数。0 は「不明」。
	//
	// 中継の画面から読めることもあるが、**撮った 1 枚からは分からないのが普通**。
	// 分からなくても解析はできる（千日手・連続王手の判定ができないだけ）。
	MoveNumber int

	// handBlack / handWhite は駒台に割り振った枚数（ベース駒コード → 枚数）。
	//
	// **合計は持たない。** 駒台の合計は盤上の駒数から逆算されるもので、盤を直すたびに
	// 変わる。ここが持つのは「人間が先手/後手に決めた枚数」そのもの。
	//
	// ⚠️ **逆算した合計で丸めない。** 以前は Hands / assigned が合計に収まるよう
	// 切り詰めていたが、そのせいで**既に足りている駒を駒台に載せられなかった**
	// （載せても表示に出ないので、駒台から先後を決める操作が途中で詰む）。
	// 上限を超えるのは Place が在庫を見ないのと同じで、**警告に出すだけ**にする
	// （設計原則3・4）。
	//
	// **残りを片方に寄せない。** 割り振っていないぶんは「先後不明」のまま残る
	// （設計原則5。手番と同じく、盤面からは決まらないものを勝手に決めない）。
	// 未割り当ては Unassigned で取れる。
	handBlack map[int]int
	handWhite map[int]int
}

// New は盤面から局面を作る。手番は未決、手数は不明。
func New(b *Board) *Position {
	if b == nil {
		b = NewBoard()
	}
	return &Position{Board: b, handBlack: map[int]int{}, handWhite: map[int]int{}}
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
// **割り振っていないぶんは含まれない**（どちらの持ち駒か決まっていないので、
// 先手にも後手にも入れられない）。
//
// ⚠️ **逆算した合計で丸めない。** 人間が決めた枚数をそのまま返す（多すぎるぶんは
// Warnings に出る）。丸めると、駒台に載せた駒が黙って消える。
func (p *Position) Hands() (black, white map[int]int) {
	black = map[int]int{}
	white = map[int]int{}
	for base := range p.handBlack {
		if n := p.handBlack[base]; n > 0 {
			black[base] = n
		}
	}
	for base := range p.handWhite {
		if n := p.handWhite[base]; n > 0 {
			white[base] = n
		}
	}
	return black, white
}

// assigned は駒種 1 つぶんの割り振り（人間が決めた枚数）を返す。
func (p *Position) assigned(base int) (black, white int) {
	return max(p.handBlack[base], 0), max(p.handWhite[base], 0)
}

// Unassigned はまだ先後を決めていない駒台の枚数を返す（ベース駒コード → 枚数）。
//
// **これが 0 になるまで局面は確定しない**（SFEN は持ち駒を先後に分けて書くため）。
// 訂正 UI は「どちらの駒台か」をここから減らしていく操作として作る。
func (p *Position) Unassigned() map[int]int {
	out := map[int]int{}
	if p.HandsFixed {
		// 駒台が書いてある局面では逆算しない（決めるものが残っていない）。
		return out
	}
	for base, total := range p.HandTotal() {
		b, w := p.assigned(base)
		if rest := total - b - w; rest > 0 {
			out[base] = rest
		}
	}
	return out
}

// SetHand は駒台のうち片側の枚数を n 枚にする。
//
// **どちらの持ち駒かは局面からは決まらない**ので、決めるのは人間。
//
// ⚠️ **逆算した合計を超える指定も通す。** 「もう足りている駒は駒台に載せられない」
// にすると、**駒台から先後を決める操作が途中で詰む**（余計な駒を盤から外す前に、
// 正しい持ち駒を載せられない）。Place が在庫を見ないのと同じ理由で、
// 多すぎることは Warnings に出すだけにする（設計原則3・4）。
func (p *Position) SetHand(base int, black bool, n int) error {
	if sfen.Letter(base) == "None" || base == sfen.King {
		return fmt.Errorf("ikkyoku/position: 駒台に持てない駒です: %d", base)
	}
	if n < 0 {
		return fmt.Errorf("ikkyoku/position: 枚数が負です: %d", n)
	}
	p.hand(black)[base] = n
	return nil
}

// hand は片側の駒台のマップを返す（nil なら作る）。
func (p *Position) hand(black bool) map[int]int {
	if black {
		if p.handBlack == nil {
			p.handBlack = map[int]int{}
		}
		return p.handBlack
	}
	if p.handWhite == nil {
		p.handWhite = map[int]int{}
	}
	return p.handWhite
}

// BoardSFEN は盤面部分だけの SFEN を返す。手番が未決でも使える。
func (p *Position) BoardSFEN() string { return p.Board.SFEN() }

// SFEN は局面全体の SFEN（盤面・手番・持ち駒・手数）を返す。
//
// **手番が未決ならエラー。** 盤面だけが要るなら BoardSFEN を使うこと。
// ここで先手に倒して文字列を作ると、決めていない手番が決まったことになってしまう
// （エンジンに渡ってからでは、どちらの手番として読まれたのかが分からない）。
//
// **駒台の割り振りが残っていてもエラー。** SFEN は持ち駒を先後に分けて書くので、
// 「どちらの駒台か決まっていない駒」を書き表せない。手番と同じで、
// **決めていないことを黙って決めない**。
//
// 手数は 0（不明）でも 1 と書く。SFEN の書式が手数を必須にしているためで、
// 手番と違って**局面の解釈を変えない**（履歴を持たない以上どのみち使えない）。
func (p *Position) SFEN() (string, error) {
	if p.Turn == TurnUnknown {
		return "", fmt.Errorf("ikkyoku/position: 手番が決まっていません")
	}
	if rest := p.Unassigned(); len(rest) > 0 {
		names := make([]string, 0, len(rest))
		for _, base := range sfen.HandOrder {
			if n := rest[base]; n > 0 {
				names = append(names, fmt.Sprintf("%s%d", sfen.Name(base), n))
			}
		}
		return "", fmt.Errorf("ikkyoku/position: 駒台の先後が決まっていません: %s",
			strings.Join(names, " "))
	}
	black, white := p.Hands()
	num := p.MoveNumber
	if num <= 0 {
		num = 1
	}
	return fmt.Sprintf("%s %s %s %d",
		p.Board.SFEN(), p.Turn.mark(), sfen.FormatHands(black, white), num), nil
}

// LabelSFEN は「**画像に付けるラベル**」としての SFEN を best-effort で組み立てて返す。
// 2 つめの戻り値は、そのために妥協した点（無ければ空）。
//
// ⚠️ **SFEN() の代わりに使わないこと。** これは suteme への学習データ登録専用で、
// **エンジンに渡してよい局面ではない。** 使い分けは次のとおり:
//
//	SFEN()      … 局面として確定したもの。未決があればエラー（決めていないことを決めない）
//	LabelSFEN() … 画像のラベル。**必ず何かを返す**（撮った 1 枚は捨てない。設計原則3）
//
// **持ち駒は決まっているぶんを必ず書く。** 手番が未決だからといって持ち駒まで
// 落とさない（駒台の枚数は駒数保存則の逆算そのもので、画像に写っていない情報を
// 人が入れた成果でもある。SFEN は位置で意味が決まる書式なので、持ち駒を書くには
// 手番の欄を埋めるしかない）。
//
// 妥協しているのは 2 点だけで、**どちらも notes に出す**（黙って捨てない）:
//
//   - **手番が未決なら b と書く。** suteme 側は手番の欄が無い SFEN を読むときも
//     先手として扱う（`setTurn(f[1] || 'b')`）ので、**書いても書かなくても向こうの
//     見え方は変わらない**。学習は盤面部分（fields[0]）しか見ないので影響もしない
//   - **先後が未決の持ち駒は書かない。** どちらの駒台か決まっていないものを
//     片側に寄せると、そちらの持ち駒として記録される（こちらは見え方が変わるので
//     推測しない）
func (p *Position) LabelSFEN() (string, []string) {
	var notes []string
	if p.Turn == TurnUnknown {
		notes = append(notes, "手番が未決なので先手番として送ります（学習には使われません）")
	}
	if rest := p.Unassigned(); len(rest) > 0 {
		names := make([]string, 0, len(rest))
		for _, base := range sfen.HandOrder {
			if n := rest[base]; n > 0 {
				names = append(names, fmt.Sprintf("%s%d", sfen.Name(base), n))
			}
		}
		notes = append(notes,
			"先後が未決の駒は持ち駒に含まれません: "+strings.Join(names, " "))
	}
	black, white := p.Hands()
	num := p.MoveNumber
	if num <= 0 {
		num = 1
	}
	return fmt.Sprintf("%s %s %s %d",
		p.Board.SFEN(), p.Turn.mark(), sfen.FormatHands(black, white), num), notes
}

// Warnings は局面として成立していない点を日本語で返す（無ければ空）。
//
// **訂正 UI が「ここが怪しい」を出すための入口。** 直している最中の盤は当然
// 途中で壊れるので、**エラーとして扱わないこと**（設計原則3）。
//
// 盤面の検証は core/sfen（自前で書かない）。ここが足すのは**駒台まで数えた枚数**の
// 話で、これは盤面だけを見る Inspect には出せない:
//
//   - 超過 … 「盤上 + 駒台」で初めて上限を超えたぶん（`handWarnings`）
//   - 不足 … 盤にも駒台にも無い駒（`missingWarnings`。＝訂正 UI の「足りない駒」）
func (p *Position) Warnings() []string {
	out := p.Board.Inspect(sfen.CheckAll).Messages()
	out = append(out, p.handWarnings()...)
	return append(out, p.missingWarnings()...)
}

// missingWarnings は**盤にも駒台にも無い駒**の警告を返す（訂正 UI の「足りない駒」）。
//
// 駒数保存則からすると、それらは**どちらかの駒台にあるはず**なのに置き場所が
// 決まっていない状態で、**そのあいだ局面は確定しない**（`SFEN()` が組み上がらない）。
// 過剰だけを言って不足を黙っていると、**何をすれば確定するのかが警告からは読めない**。
//
// ⚠️ **`HandsFixed` のときは言わない。** 駒台が書いてある局面（KIF・完全形 SFEN から
// 読んだもの）では逆算しないので未決が存在せず、**駒落ちは盤にも駒台にも無い駒が
// あって正常**（言うと毎回警告が出る）。
//
// ⚠️ **玉は数えない。** 駒台に載らないので「足りない」は盤の枚数の話になり、
// それは core/sfen の CheckKing が既に言っている（二重に言わない）。
func (p *Position) missingWarnings() []string {
	if p.HandsFixed {
		return nil
	}
	info := p.Board.Inspect(sfen.CheckSyntax)
	var out []string
	for _, base := range inventoryOrder {
		if base == sfen.King {
			continue
		}
		onBoard := info.Black[base] + info.White[base]
		limit := sfen.PieceLimit(base)
		b, w := p.assigned(base)
		// 過剰（rest < 0）や、駒台へ割り振り済みのぶんはここでは言わない。
		if missing := limit - onBoard - b - w; missing > 0 {
			out = append(out, fmt.Sprintf("%sが %d枚足りません(上限 %d枚。盤上 %d枚・駒台 %d枚)",
				sfen.Name(base), missing, limit, onBoard, b+w))
		}
	}
	return out
}

// handWarnings は「盤上 + 駒台」が上限を超えた駒種の警告を返す。
//
// **盤上だけで超えているぶんは core/sfen が既に言っている**ので、ここでは
// 二重に言わない（駒台を足して初めて超えるものだけ）。
func (p *Position) handWarnings() []string {
	info := p.Board.Inspect(sfen.CheckSyntax)
	var out []string
	for _, base := range inventoryOrder {
		if base == sfen.King {
			continue // 玉は駒台に載らない（枚数は CheckKing の担当）
		}
		onBoard := info.Black[base] + info.White[base]
		limit := sfen.PieceLimit(base)
		if onBoard > limit {
			continue // 盤上だけで超過。core/sfen が報告済み
		}
		b, w := p.assigned(base)
		if total := onBoard + b + w; total > limit {
			out = append(out, fmt.Sprintf("%sが %d枚あります(上限 %d枚。盤上 %d枚・駒台 %d枚)",
				sfen.Name(base), total, limit, onBoard, b+w))
		}
	}
	return out
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
		// ⚠️ **HandsFixed も写すこと。** 落とすと、手を 1 つ進めた（＝Clone した）
		// 瞬間に駒台の逆算が復活し、駒落ちの棋譜がそこで進まなくなる。
		HandsFixed: p.HandsFixed,
		MoveNumber: p.MoveNumber,
		handBlack:  make(map[int]int, len(p.handBlack)),
		handWhite:  make(map[int]int, len(p.handWhite)),
	}
	for k, v := range p.handBlack {
		c.handBlack[k] = v
	}
	for k, v := range p.handWhite {
		c.handWhite[k] = v
	}
	return c
}
