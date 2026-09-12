package position

// 訂正の操作と、その拠り所になる「在庫」。
//
// **訂正 UI は「存在するはずの駒」を中心に回す。** 将棋の駒は先後合わせて枚数が
// 決まっている（歩 18・香 4・桂 4・銀 4・金 4・飛 2・角 2・玉 2）ので、
// 「盤上に何枚あるか」ではなく「**あと何枚あるはずか**」を出すほうが、認識結果の
// 直し方がそのまま見える。認識は**余計な駒を作る**（実測で `L: 11枚（上限4）`）ので、
// 過剰は「負の残り」として出して、ユーザーが盤から外す先を用意する。
//
// **合法性は問わない。** 直している最中の盤は壊れていて当たり前で、置けるかどうかを
// ここで止めると「間違った駒を外す前に正しい駒を置けない」という詰みが起きる
// （設計原則3・原則4）。おかしさは Warnings で出す。

import (
	"fmt"

	"github.com/ShinteLab/core/sfen"
)

// Stock は駒種 1 つぶんの在庫。
type Stock struct {
	// Piece はベース駒コード（sfen.Pawn など）。
	Piece int `json:"piece"`
	// Letter は SFEN の大文字（"P"）。フロントが駒を描くのに使う。
	Letter string `json:"letter"`
	// Name は日本語名（"歩"）。
	Name string `json:"name"`
	// Limit は先後合計の上限枚数。**これが「存在するはず」の総数。**
	Limit int `json:"limit"`
	// Black / White は盤上の枚数（成駒はベース駒に合算）。
	Black int `json:"black"`
	White int `json:"white"`
	// Rest は残り = Limit - 盤上。**負なら過剰**（認識が余計な駒を作った状態）。
	// 正の値はそのまま「駒台にあるはずの枚数」（先後の区別は付かない）。
	Rest int `json:"rest"`
	// HandBlack / HandWhite は Rest のうち先後に割り振った枚数。
	HandBlack int `json:"handBlack"`
	HandWhite int `json:"handWhite"`
	// Unassigned は Rest のうち**まだ先後を決めていない**枚数。
	// **これが残っているあいだ局面は確定しない**（SFEN が書けない）。
	Unassigned int `json:"unassigned"`
	// Unused は **この対局で使わない枚数**（駒落ち・詰将棋。2026-09-12）。
	//
	// 盤にも駒台にも無い駒は、既定では「どちらかの駒台にあるはず」と逆算されて
	// `Unassigned` に入るが、**`HandsFixed` を立てるとその逆算をやめる**ので、
	// 行き場の無くなった枚数がこちらへ回る（**落とした駒・詰将棋で使わない駒**）。
	//
	// ⚠️ **`Unassigned` と同時に立つことは無い**（逆算するかしないかの裏表）。
	// ⚠️ **これは確定を止めない** —— 使わない駒があること自体が正常なので、
	// `SFEN()` は残っていても組み上がる（`Unassigned` との一番の違い）。
	Unused int `json:"unused"`
}

// Over は過剰かを返す（盤上が上限を超えている）。
func (s Stock) Over() bool { return s.Rest < 0 }

// inventoryOrder は在庫と駒台を並べる順（歩・香・桂・銀・金・角・飛・王）。
//
// **駒台に置く順そのもの**なので、`sfen.HandOrder`（持ち駒表記の順＝飛角金銀桂香歩）とは
// 逆向きに近い。SFEN の文字列を組むときは向こうの順を使うこと（`FormatHands` の担当）。
var inventoryOrder = []int{
	sfen.Pawn, sfen.Lance, sfen.Knight, sfen.Silver,
	sfen.Gold, sfen.Bishop, sfen.Rook, sfen.King,
}

// Inventory は全駒種の在庫を返す。並びは駒台に置く順（歩香桂銀金角飛王）。
//
// **訂正 UI の駒箱と駒台はこれをそのまま並べる。** 盤上の枚数ではなく残りを見せるのが
// 要点で、「まだ置いていない駒」と「余計に置いた駒」が 1 つの数で表せる。
func (p *Position) Inventory() []Stock {
	info := p.Board.Inspect(sfen.CheckSyntax)
	order := inventoryOrder

	out := make([]Stock, 0, len(order))
	for _, base := range order {
		black, white := info.Black[base], info.White[base]
		limit := sfen.PieceLimit(base)
		rest := limit - black - white
		hb, hw := p.assigned(base)
		// 駒台が書いてある局面（KIF・SFEN から読んだもの）では未決が無い。
		unassigned := max(max(rest, 0)-hb-hw, 0)
		unused := 0
		if p.HandsFixed {
			// **逆算をやめた局面**（駒落ち・詰将棋）。行き場の無い枚数は
			// 「未決」ではなく「**この対局で使わない**」になる。
			// ⚠️ **数を消さずに移し替えること** —— 0 にしてしまうと、
			// 落とした駒が画面から消えて、外し忘れとの区別が付かなくなる。
			unassigned, unused = 0, unassigned
		}
		out = append(out, Stock{
			Piece: base, Letter: sfen.Letter(base), Name: sfen.Name(base),
			Limit: limit, Black: black, White: white, Rest: rest,
			HandBlack: hb, HandWhite: hw, Unused: unused,
			// 割り振りが逆算した残りを超えていたら未割り当ては 0（負にはしない）。
			// **駒台の枚数のほうを丸めない**（人が決めた枚数なので消さない。
			// 多すぎることは Warnings に出る）。
			Unassigned: unassigned,
		})
	}
	return out
}

// Spare は「盤にも駒台にも無い駒」の枚数を返す（ベース駒コード → 枚数）。
//
// **`Inventory()` の `Unassigned` と `Unused` を 1 つにまとめたもの**（あちらは
// 逆算するかしないかで入る場所が変わるが、**どちらも「まだ置き場所が決まって
// いない駒」**という点では同じ）。⚠️ **数え方を 2 つ持たないために `Inventory`
// から作ること** —— ここで数え直すと、駒箱の表示と寄せた枚数が食い違いうる。
//
// ⚠️ **玉は数えない。** 駒台に載らないので（`core/sfen` の逆算も玉を含まない）、
// 盤の枚数の話になる。詰将棋では玉が 1 枚しかないのが正しい。
func (p *Position) Spare() map[int]int {
	out := map[int]int{}
	for _, s := range p.Inventory() {
		if s.Piece == sfen.King {
			continue
		}
		if n := s.Unassigned + s.Unused; n > 0 {
			out[s.Piece] = n
		}
	}
	return out
}

// FillHands は残っている駒を**まとめて片側の駒台に載せる**（戻り値は載せた枚数）。
//
// **詰将棋のための操作。** 詰将棋は「**残り全部が玉方の持駒**」という慣習なので、
// 歩が 17 枚といった枚数になる。1 枚ずつドラッグするのは現実的でないが、
// 駒台の枚数は指定で決められる（`SetHand`）ので、ここでまとめて決める。
//
// ⚠️ **これも「決めていないことを決めない」に反しない** —— 決めるのは人間で、
// ここは人間が押した 1 操作をそのまま反映するだけ。**自動では呼ばないこと。**
//
// ⚠️ **玉は載せない**（`Spare` が数えない）。⚠️ **逆算をやめている局面
// （手合割）でも動く** —— 「使わない駒」を持ち駒にし直す操作になる。
func (p *Position) FillHands(black bool) int {
	moved := 0
	for base, n := range p.Spare() {
		b, w := p.assigned(base)
		cur := w
		if black {
			cur = b
		}
		if err := p.SetHand(base, black, cur+n); err != nil {
			// 載せられない駒種は飛ばす（残りはそのまま。設計原則3）。
			continue
		}
		moved += n
	}
	return moved
}

// Place はマスに駒を置く（元あった駒は消える）。**駒箱から盤へのドロップ。**
//
// 在庫が尽きていても置ける。**「余計な駒を外す前に正しい駒を置けない」を避けるため**で、
// 上限超過は Warnings に出る（設計原則3）。
func (p *Position) Place(rank, file, piece int, black, promoted bool) error {
	c, err := NewCell(piece, black, promoted)
	if err != nil {
		return err
	}
	return p.Board.Set(rank, file, c)
}

// Remove はマスを空にする。**盤から駒箱へのドロップ**（＝認識が作った余計な駒を外す）。
func (p *Position) Remove(rank, file int) error {
	return p.Board.Set(rank, file, Cell{})
}

// Move は駒をマスからマスへ移す。**盤の中でのドラッグ＆ドロップ。**
// 空マスからの移動は何もしない（error）。
//
// ⚠️ **移動先に駒があれば入れ替える**（2026-08-18。以前は上書きしていた）。
// 訂正では「2 つのマスの駒を取り違えている」——認識が近い駒を隣どうしで
// 入れ違えた——のが普通に起きるので、**すげ替えが 1 操作で済む**ほうが速い。
// 上書きだと、退かす駒を先にどこかへ避ける手間が要る。
//
// ⚠️ **取るのではない。** 消えた駒は駒台にも行かない（駒数は変わらない）。
// **消したいなら盤の外へ放る**（`Remove`）——「動かす」と「消す」を
// 1 つの操作に混ぜると、上書きで駒が黙って消える。
func (p *Position) Move(fromRank, fromFile, toRank, toFile int) error {
	c, err := p.Board.At(fromRank, fromFile)
	if err != nil {
		return err
	}
	if c.IsEmpty() {
		return fmt.Errorf("ikkyoku/position: 移動元が空マスです")
	}
	if fromRank == toRank && fromFile == toFile {
		return nil
	}
	// 移動先の駒（空マスなら空のまま）を移動元へ戻す＝入れ替え。
	dst, err := p.Board.At(toRank, toFile)
	if err != nil {
		return err
	}
	if err := p.Board.Set(toRank, toFile, c); err != nil {
		return err
	}
	return p.Board.Set(fromRank, fromFile, dst)
}

// ToHand は盤の駒を駒台へ移す。**盤 → 駒台のドラッグ＆ドロップ。**
//
// 盤から外すのと「どちらの駒台か決める」のを 1 操作にしてある。外すだけなら Remove で、
// そちらは**先後を決めずに**「駒台にあるはず（先後不明）」へ戻る。
//
// 成駒は**元の駒に戻る**（と金を駒台に載せれば歩）。ベース駒コードで持っているので
// 自然にそうなるが、**訂正の意味としても正しい**（駒台に成駒は無い）。
func (p *Position) ToHand(rank, file int, black bool) error {
	c, err := p.Board.At(rank, file)
	if err != nil {
		return err
	}
	if c.IsEmpty() {
		return fmt.Errorf("ikkyoku/position: 空マスです")
	}
	if c.piece == sfen.King {
		return fmt.Errorf("ikkyoku/position: 玉は駒台に載りません")
	}
	if err := p.Board.Set(rank, file, Cell{}); err != nil {
		return err
	}
	// 盤から抜いたぶん駒台の合計が 1 増えるので、その 1 枚をこちら側に足す。
	p.hand(black)[c.piece]++
	return nil
}

// FromHand は駒台の駒を盤へ置く。**駒台 → 盤のドラッグ＆ドロップ。**
//
// **その側の駒台に無ければエラー。** 見本（駒箱）から置くのは Place で、
// あちらは在庫を見ない。ここは「持っている駒を打つ」に相当するので数を守る。
func (p *Position) FromHand(rank, file, piece int, black bool) error {
	b, w := p.assigned(piece)
	have := w
	if black {
		have = b
	}
	if have <= 0 {
		return fmt.Errorf("ikkyoku/position: %sの駒台に%sがありません",
			sideName(black), sfen.Name(piece))
	}
	if err := p.Place(rank, file, piece, black, false); err != nil {
		return err
	}
	// 盤に置いたぶん合計が 1 減るので、この側の割り振りも 1 減らす
	// （減らさないと、置いた 1 枚がもう一方の側から引かれてしまう）。
	p.hand(black)[piece] = have - 1
	return nil
}

func sideName(black bool) string {
	if black {
		return "先手"
	}
	return "後手"
}

// TogglePromoted は成/不成を切り替える。**認識は成駒の "+" を落としやすい。**
func (p *Position) TogglePromoted(rank, file int) error {
	c, err := p.Board.At(rank, file)
	if err != nil {
		return err
	}
	if c.IsEmpty() {
		return fmt.Errorf("ikkyoku/position: 空マスです")
	}
	n, err := NewCell(c.piece, c.black, !c.promoted)
	if err != nil {
		return err // 金と玉は成れない
	}
	return p.Board.Set(rank, file, n)
}

// CycleCell は 1 マスの状態を順に回す:
//
//	先手の不成 → 先手の成 → 後手の不成 → 後手の成 → 先手の不成 …
//
// **訂正でマスに対してやりたいこと（先後と成/不成）はこの 4 通りしかない。**
// 操作をクリックの左右で分けると「どちらがどちらだったか」を覚える必要が出るので、
// UI からは 1 つの操作として回す（editor.ts の右クリック）。
//
// 金と玉は成れないので、その 2 つは先後の 2 状態だけを回る。
func (p *Position) CycleCell(rank, file int) error {
	c, err := p.Board.At(rank, file)
	if err != nil {
		return err
	}
	if c.IsEmpty() {
		return fmt.Errorf("ikkyoku/position: 空マスです")
	}
	promotable := c.piece != sfen.Gold && c.piece != sfen.King

	var black, promoted bool
	switch {
	case c.black && !c.promoted:
		black, promoted = true, promotable // 先手の成（成れなければ後手へ）
		if !promotable {
			black = false
		}
	case c.black && c.promoted:
		black, promoted = false, false // 後手の不成
	case !c.black && !c.promoted:
		black, promoted = false, promotable // 後手の成（成れなければ先手へ）
		if !promotable {
			black = true
		}
	default:
		black, promoted = true, false // 先手の不成に戻る
	}
	return p.Place(rank, file, c.piece, black, promoted)
}

// FlipSide は駒の先後を入れ替える。**認識は駒の向きを外す**（後手の駒は 180 度回転で、
// 回転を戻して分類するため、向きの判定を誤ると先後が入れ替わる）。
func (p *Position) FlipSide(rank, file int) error {
	c, err := p.Board.At(rank, file)
	if err != nil {
		return err
	}
	if c.IsEmpty() {
		return fmt.Errorf("ikkyoku/position: 空マスです")
	}
	n, err := NewCell(c.piece, !c.black, c.promoted)
	if err != nil {
		return err
	}
	return p.Board.Set(rank, file, n)
}
