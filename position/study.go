package position

// 検討の手順（Phase 5）。**確定した局面を根に、合法手だけを辿る。**
//
// **根は動かさない。** 手順は「根 + 指した手の並び」として持ち、今の局面は
// そこから毎回組み立て直す。こうしてあるのは 2 つの理由から:
//
//   - **エンジンに渡すのがまさにこの形**（`position sfen <根> moves ...`）。
//     千日手や連続王手は手順が無いと判定できないので、局面だけを渡すより良い
//   - **戻る操作が「並びを短くするだけ」で済む。** 逆再生を書かなくてよい
//
// ⚠️ **これは「履歴に依存する」ではない**（設計原則1）。根の 1 局面だけで解析は
// 成立していて、手順はそこからユーザーが自分で伸ばしたもの。**中継を最初から
// 観ていなくても、撮った 1 局面から手を進められる。**

import (
	"fmt"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/ikkyoku/legal"
)

// Move は手順の 1 手。
type Move struct {
	// Number は棋譜の数え方の手数（1 が初手）。**SFEN の手数とは 1 ずれる。**
	Number int `json:"number"`
	// USI は手文字列（"7g7f"）。**エンジンに渡すのはこれ。**
	USI string `json:"usi"`
	// Text は日本語表記（"▲７六歩"）。**画面に出すのはこちら。**
	//
	// ⚠️ **変換に失敗しても USI がそのまま入る**（設計原則3）。手順そのものは
	// 正しいのに、表記の都合で手が消えるほうが困る。
	Text string `json:"text"`
}

// Study は 1 つの検討（根の局面 + そこから指した手順）。
//
// **分岐ツリーはまだ持たない。** 途中まで戻って別の手を指すと、そこから先は
// 捨てる（一直線）。木にするのは `core/kifu` が木を持てるようになってから
// （保存形式が kicho に波及するので、形は core で決める）。
type Study struct {
	// root は確定した局面。**ここは動かさない。**
	root *Position
	// moves は根から指した手（棋譜の順）。
	moves []Move
	// ply は今どこまで進めて見ているか（0 なら根の局面）。
	//
	// **len(moves) より小さいことがある**（戻って見ている状態）。そこで新しい手を
	// 指すと、そこから先は捨てる。
	ply int
	// cur は root に moves[:ply] を適用した局面（表示と合法手の元）。
	cur *Position
}

// NewStudy は確定した局面を根にして検討を始める。
//
// ⚠️ **渡した局面の写しを持つ。** 呼び出し側があとから元をいじっても、
// こちらは動かない（動くと、解析結果がどの局面の値なのか分からなくなる）。
func NewStudy(root *Position) *Study {
	s := &Study{root: root.Clone()}
	s.cur = s.root.Clone()
	return s
}

// Root は根の局面の写しを返す。
func (s *Study) Root() *Position { return s.root.Clone() }

// Current は今見ている局面の写しを返す。**盤に描くのはこれ。**
func (s *Study) Current() *Position { return s.cur.Clone() }

// Moves は手順を返す（棋譜の順）。
func (s *Study) Moves() []Move {
	out := make([]Move, len(s.moves))
	copy(out, s.moves)
	return out
}

// Ply は今どこまで進めて見ているかを返す（0 なら根）。
func (s *Study) Ply() int { return s.ply }

// Played は今見ている局面までの手（USI）を返す。
//
// **エンジンに渡す `moves` はこれ。** 戻って見ているときは、そこまでの手だけを渡す
// （先の手はまだ指していないことになっているので、渡すと別の局面を解析してしまう）。
func (s *Study) Played() []string {
	out := make([]string, 0, s.ply)
	for _, m := range s.moves[:s.ply] {
		out = append(out, m.USI)
	}
	return out
}

// Legal は今の局面で指せる手を返す。
//
// ⚠️ **エラーでも局面は生きている**（設計原則3）。玉の欠けた局面などでは合法手を
// 出せないが、盤は描けるし解析タブに居ることもできる。**手が指せなくなるだけ。**
func (s *Study) Legal() ([]legal.Move, error) {
	sfenStr, err := s.cur.SFEN()
	if err != nil {
		return nil, err
	}
	return legal.Moves(sfenStr)
}

// Play は 1 手指して局面を進める。
//
// ⚠️ **合法手でなければエラー。** 訂正タブと違い、ここは「実際の対局と同じように
// 進める」面なので、指せない手は指せない（そのための `legal`）。
//
// **戻って見ている途中で指すと、そこから先の手順は捨てる。** 別の手を選んだ以上、
// 元の続きは別の話になる（分岐として残すのは `core/kifu` が木を持ってから）。
func (s *Study) Play(move string) error {
	moves, err := s.Legal()
	if err != nil {
		return err
	}
	ok := false
	for _, m := range moves {
		if m.USI == move {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("ikkyoku/position: その手は指せません: %s", move)
	}

	next := s.cur.Clone()
	if err := next.ApplyMove(move); err != nil {
		return err
	}

	// 戻って見ている途中なら、そこから先は捨てる。
	s.moves = append(s.moves[:s.ply:s.ply], Move{Number: s.ply + 1, USI: move})
	s.ply++
	s.cur = next
	s.retext()
	return nil
}

// DropFrom は n 手目**とその先**を手順から消す（n は棋譜の数え方で 1 が初手）。
//
// ⚠️ **「1 手戻す」の代わり**（2026-08-13）。消す量を**手そのもので指す**ので、
// 「どこから消えるか」が呼ぶ側でも画面でも一意に決まる（`Undo` は「今どこを
// 見ているか」に依存していて、**戻って見ている最中に押すと何が消えるか
// 分かりにくかった**）。`DropFrom(len(moves))` が以前の `Undo` と同じ。
//
// **見るだけなら GoTo。** こちらは手順そのものを短くする操作。
//
// ⚠️ **分岐ツリーが入ったら、ここは「消す」ではなく「枝として切り離す」になる。**
// 消す範囲の決め方（n 手目以下）は変わらないので、**直すのはこの中だけ**。
func (s *Study) DropFrom(n int) error {
	if n < 1 || n > len(s.moves) {
		return fmt.Errorf("ikkyoku/position: %d手目はありません", n)
	}
	s.moves = s.moves[:n-1]
	// 消した先を見ていたなら、残った最後まで戻す（手前を見ていたならそのまま）。
	if s.ply > n-1 {
		return s.GoTo(n - 1)
	}
	return nil
}

// GoTo は手順の n 手目まで進めた局面を見る（0 なら根）。
//
// **手順は消さない。** 戻ってから進め直せる（そこで別の手を指したときだけ捨てる）。
func (s *Study) GoTo(n int) error {
	if n < 0 || n > len(s.moves) {
		return fmt.Errorf("ikkyoku/position: %d手目はありません", n)
	}
	p := s.root.Clone()
	for i := 0; i < n; i++ {
		if err := p.ApplyMove(s.moves[i].USI); err != nil {
			return fmt.Errorf("ikkyoku/position: %d手目を再現できません: %w", i+1, err)
		}
	}
	s.ply = n
	s.cur = p
	return nil
}

// retext は手順の日本語表記を付け直す。
//
// **根から通しで変換する。** "同" も "右左上引" も直前の手と盤全体を見ないと
// 決まらないので、1 手だけを後から名付けることはできない（`core/kifu` の担当）。
//
// ⚠️ **失敗しても手順は捨てない**（設計原則3）。表記が作れなかった手は USI が
// そのまま入る（`kifu.FormatMoves` がそうしてくれる）。
func (s *Study) retext() {
	if len(s.moves) == 0 {
		return
	}
	root, err := s.root.SFEN()
	if err != nil {
		return
	}
	usis := make([]string, 0, len(s.moves))
	for _, m := range s.moves {
		usis = append(usis, m.USI)
	}
	texts, _ := kifu.FormatMoves(root, usis)
	for i := range s.moves {
		if i < len(texts) {
			s.moves[i].Text = texts[i].Text
		} else {
			s.moves[i].Text = s.moves[i].USI
		}
	}
}
