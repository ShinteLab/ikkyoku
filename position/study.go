package position

// 検討の手順（Phase 5）。**確定した局面を根に、合法手だけを辿る。**
//
// **根は動かさない。** 手順は「根 + 指した手の木」として持ち、今の局面は
// そこから毎回組み立て直す。こうしてあるのは 2 つの理由から:
//
//   - **エンジンに渡すのがまさにこの形**（`position sfen <根> moves ...`）。
//     千日手や連続王手は手順が無いと判定できないので、局面だけを渡すより良い
//   - **戻る操作が「経路を辿り直すだけ」で済む。** 逆再生を書かなくてよい
//
// ⚠️ **これは「履歴に依存する」ではない**（設計原則1）。根の 1 局面だけで解析は
// 成立していて、手順はそこからユーザーが自分で伸ばしたもの。**中継を最初から
// 観ていなくても、撮った 1 局面から手を進められる。**
//
// # 分岐（2026-08-13）
//
// **一直線ではなく木。** 同じ局面から別の手を指したら、前の手を捨てるのではなく
// **兄弟として並べる**（「それもまた一局」——別の選択も一つの局として辿らせる、
// というこのアプリの名前そのもの）。
//
//	70 ５四歩
//	├─ ９七角 …          ← 解析の候補手から足した枝（AddLine）
//	└─ 71 ３二飛打 …      ← 本譜（＝最初の子）
//
// - ⚠️ **枝と本譜を別の型で持たない。** ソフトの上ではどちらもただの指し手で、
//   違いは「親の何番目の子か」だけ（`kids[0]` が本譜側）。**片方だけ消せる/
//   消せないといった区別も付けない**（本譜は URL から取り直せる）
// - ⚠️ **同じ手を指したら枝を増やさない**（既にある子を辿る）。これが
//   「候補手が本譜と同じなら、食い違うところまで辿ってからそこで枝にする」
//   の実体で、`Play` と `AddLine` と `Graft` の 3 つが同じ規則を共有する
// - **木の形は最終的に `core/kifu` が決める**（KIF の `変化：N手`。保存形式が
//   kicho に波及する）。ここはそれが入るまでの**仮置き**なので、
//   ⚠️ **他の層から木の内部構造を触らせないこと**（触るのは `Node` の写しだけ）
// - **節点は id で指す。** 手数では枝を区別できない（同じ 71 手目が何本もある）。
//   ⚠️ **id を手数として扱わないこと**

import (
	"fmt"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/ikkyoku/legal"
)

// Node は手順ツリーの 1 手（**表示と `GoTo` の単位**）。
//
// ⚠️ **木の写しであって、木そのものではない。** 親子は `ID`/`Parent` で辿る。
type Node struct {
	// ID は節点の識別子（1 から。**0 は根の局面**）。
	//
	// ⚠️ **手数ではない。** 枝があると同じ手数の節点が何個もあるので、
	// 「どの局面か」を指せるのは id だけ。
	ID int `json:"id"`
	// Parent は親の ID（**根の直後なら 0**）。
	Parent int `json:"parent"`
	// Number は根からの手数（1 が根の次の手）。**画面に出すのは棋譜の手数**なので、
	// 呼び出し側が根の手数を足す（`StudyState.First`）。
	Number int `json:"number"`
	// USI は手文字列（"7g7f"）。**エンジンに渡すのはこれ。**
	USI string `json:"usi"`
	// Text は日本語表記（"▲７六歩"）。**画面に出すのはこちら。**
	//
	// ⚠️ **変換に失敗しても USI がそのまま入る**（設計原則3）。手順そのものは
	// 正しいのに、表記の都合で手が消えるほうが困る。
	Text string `json:"text"`
	// Depth は枝の深さ（**本譜だけを辿ってきたら 0**）。ツリービューの字下げ。
	Depth int `json:"depth"`
	// Main は親の最初の子か（＝その分岐での本筋）。
	Main bool `json:"main"`
}

// treeNode は手順ツリーの節点（**パッケージの外へ出さない**）。
type treeNode struct {
	id     int
	number int
	usi    string
	text   string
	parent *treeNode
	// kids は子。**kids[0] が本譜側**（`Graft` はここへ据え直す）。
	kids []*treeNode
}

// Study は 1 つの検討（根の局面 + そこから指した手の木）。
type Study struct {
	// root は確定した局面。**ここは動かさない。**
	root *Position
	// top は木の根（**指し手を持たない**。id 0 ＝ 根の局面）。
	top *treeNode
	// cur は今見ている節点（top なら根の局面）。
	cur *treeNode
	// curPos は root に cur までの手を適用した局面（表示と合法手の元）。
	curPos *Position
	// index は id → 節点。**`GoTo` と `DropFrom` の入口。**
	index map[int]*treeNode
	// nextID は次に配る id。**使い回さない**（消した id を再利用すると、
	// 走っている解析の途中経過が**別の節点の評価値として書き戻る**）。
	nextID int
}

// NewStudy は確定した局面を根にして検討を始める。
//
// ⚠️ **渡した局面の写しを持つ。** 呼び出し側があとから元をいじっても、
// こちらは動かない（動くと、解析結果がどの局面の値なのか分からなくなる）。
func NewStudy(root *Position) *Study {
	top := &treeNode{}
	s := &Study{
		root:   root.Clone(),
		top:    top,
		cur:    top,
		index:  map[int]*treeNode{0: top},
		nextID: 1,
	}
	s.curPos = s.root.Clone()
	return s
}

// Root は根の局面の写しを返す。
func (s *Study) Root() *Position { return s.root.Clone() }

// Current は今見ている局面の写しを返す。**盤に描くのはこれ。**
func (s *Study) Current() *Position { return s.curPos.Clone() }

// CurrentID は今見ている節点の id（0 なら根の局面）。
func (s *Study) CurrentID() int { return s.cur.id }

// Ply は今どこまで進めて見ているかを返す（0 なら根）。
func (s *Study) Ply() int { return s.cur.number }

// Played は今見ている局面までの手（USI）を返す。
//
// **エンジンに渡す `moves` はこれ。** 枝に居るなら**その枝を通る経路**になる。
func (s *Study) Played() []string {
	return pathUSI(s.cur)
}

// pathUSI は根から n までの手を並べる。
func pathUSI(n *treeNode) []string {
	out := make([]string, n.number)
	for p := n; p.parent != nil; p = p.parent {
		out[p.number-1] = p.usi
	}
	return out
}

// Nodes は木を**表示順**に並べて返す（根は含まない）。
//
// 並びは「その節点 → **枝**（字下げ）→ 本譜の続き」。⚠️ **枝を後ろへ回さないこと**
// —— 足した枝が 100 手先の最後尾に出ると、どこから分かれたのか分からない。
// この順なら**ある節点の子孫は必ずその直後に固まる**ので、消す範囲も見た目で分かる。
func (s *Study) Nodes() []Node {
	out := []Node{}
	var walk func(n *treeNode, depth int)
	walk = func(n *treeNode, depth int) {
		// **枝が先、本譜があと**（枝は 1 段下げる）。
		for _, k := range n.kids[min(1, len(n.kids)):] {
			out = append(out, k.node(depth+1, false))
			walk(k, depth+1)
		}
		if len(n.kids) > 0 {
			k := n.kids[0]
			out = append(out, k.node(depth, true))
			walk(k, depth)
		}
	}
	walk(s.top, 0)
	return out
}

func (n *treeNode) node(depth int, main bool) Node {
	parent := 0
	if n.parent != nil {
		parent = n.parent.id
	}
	return Node{
		ID: n.id, Parent: parent, Number: n.number,
		USI: n.usi, Text: n.text, Depth: depth, Main: main,
	}
}

// Line は今の経路（根 → 今の節点 → そこから本譜側へ辿った先）の節点 id を返す。
//
// **ply 番目の要素がその手数の節点**（`Line()[0]` は根の 0）。
// 連続解析が次に進む先も、評価値グラフの横軸も**この 1 本**で決まる。
func (s *Study) Line() []int {
	up := []int{}
	for p := s.cur; p != nil; p = p.parent {
		up = append(up, p.id)
	}
	out := make([]int, 0, len(up))
	for i := len(up) - 1; i >= 0; i-- {
		out = append(out, up[i])
	}
	for n := s.cur; len(n.kids) > 0; {
		n = n.kids[0]
		out = append(out, n.id)
	}
	return out
}

// MainLine は本譜（根から `kids[0]` を辿った並び）の手を返す。
//
// **棋譜の取り直し（`Graft`）の突き合わせ相手。** 枝に居ても本譜が返る。
func (s *Study) MainLine() []string {
	out := []string{}
	for n := s.top; len(n.kids) > 0; {
		n = n.kids[0]
		out = append(out, n.usi)
	}
	return out
}

// Legal は今の局面で指せる手を返す。
//
// ⚠️ **エラーでも局面は生きている**（設計原則3）。玉の欠けた局面などでは合法手を
// 出せないが、盤は描けるし解析タブに居ることもできる。**手が指せなくなるだけ。**
func (s *Study) Legal() ([]legal.Move, error) {
	sfenStr, err := s.curPos.SFEN()
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
// ⚠️ **同じ手が既にあるならそれを辿る**（枝を増やさない）。別の手なら**枝が生える**
// ——**前の手順は消えない**（2026-08-13。それまでは捨てていた）。
func (s *Study) Play(move string) error {
	next, err := s.grow(s.cur, s.curPos, move)
	if err != nil {
		return err
	}
	s.cur = next
	s.curPos, err = s.positionAt(next)
	return err
}

// AddLine は読み筋（USI の並び）を今の局面から木に足す（**解析の候補手から**）。
//
// ⚠️ **今見ている局面は動かさない。** 足しただけで盤が動くと、走っている解析が
// 別の局面のものになる（候補を続けて足せなくなる）。
//
// ⚠️ **既にある手はそのまま辿る。** 候補の頭が本譜と同じなら、**食い違うところまで
// 本譜を辿ってから枝になる**（`Play` と同じ規則）。
//
// ⚠️ **途中で指せない手が出たら、そこで止めて足せたぶんは残す**（設計原則3）。
// 戻り値は（最初に生えた節点の id・足した手数・止まった理由）。
// **1 手も足せなかった**（全部が既にある）ときは id が 0 になる。
func (s *Study) AddLine(moves []string) (int, int, string) {
	at, pos := s.cur, s.curPos.Clone()
	first, added := 0, 0
	for i, mv := range moves {
		before := len(at.kids)
		next, err := s.grow(at, pos, mv)
		if err != nil {
			return first, added, fmt.Sprintf("%d手目「%s」で止まりました: %v", i+1, mv, err)
		}
		if len(at.kids) > before {
			added++
			if first == 0 {
				first = next.id
			}
		}
		at = next
		if err := pos.ApplyMove(mv); err != nil {
			return first, added, fmt.Sprintf("%d手目で止まりました: %v", i+1, err)
		}
	}
	return first, added, ""
}

// Graft は棋譜（本譜）の手順を木に据え直す（**URL からの取り直し**）。
//
// ⚠️ **消さずに据える。** 食い違ったところで、**新しい手を `kids[0]`（本譜側）に
// 置き、それまでの続きは枝として残る**。ユーザーが足した検討を、中継が 1 手進む
// たびに捨てないための形（`TODO.md`「本譜のロック」の一番よくある使い方）。
func (s *Study) Graft(moves []string) GraftResult {
	var r GraftResult
	at, pos := s.top, s.root.Clone()
	for i, mv := range moves {
		before := len(at.kids)
		next, err := s.grow(at, pos, mv)
		if err != nil {
			r.Note = fmt.Sprintf("%d手目で止まりました: %v", i+1, err)
			return r
		}
		if len(at.kids) > before {
			r.Added++
		} else {
			r.Kept++
		}
		// **押しのけたか**（＝そこまで本譜だった続きが枝に下がったか）。
		// ⚠️ **「手が増えた」とは別物。** 中継が進んだだけなら誰も押しのけて
		// いないので、**断りを出す必要が無い**（毎回出ると読み飛ばされる）。
		if before > 0 && at.kids[0] != next && r.MovedAt == 0 {
			r.MovedAt = i + 1
		}
		// **本譜側（先頭）へ据え直す。** 既にあった続きは枝として後ろへ下がる。
		promote(at, next)
		at = next
		if err := pos.ApplyMove(mv); err != nil {
			r.Note = fmt.Sprintf("%d手目で止まりました: %v", i+1, err)
			return r
		}
	}
	return r
}

// GraftResult は取り直しの内訳。
type GraftResult struct {
	// Kept は既にあってそのまま辿った手数。
	Kept int
	// Added は新しく生えた手数。
	Added int
	// MovedAt は**それまで本譜だった続きを押しのけた手数**（0 なら押しのけていない）。
	//
	// ⚠️ **`Added` と混同しないこと。** 中継が 1 手進んだだけなら Added は増えるが
	// MovedAt は 0 のまま。**断りを出すのはこちらが立ったときだけ。**
	MovedAt int
	// Note は途中で止まった理由（最後まで据えられたなら空）。
	Note string
}

// promote は子を先頭（本譜側）へ移す。
func promote(parent, kid *treeNode) {
	for i, k := range parent.kids {
		if k != kid {
			continue
		}
		copy(parent.kids[1:i+1], parent.kids[:i])
		parent.kids[0] = kid
		return
	}
}

// grow は at の子として move を生やす（**既にあるならそれを返す**）。
//
// pos は at の局面（合法手の判定に要る）。⚠️ **合法手でなければ足さない。**
func (s *Study) grow(at *treeNode, pos *Position, move string) (*treeNode, error) {
	for _, k := range at.kids {
		if k.usi == move {
			return k, nil
		}
	}
	if err := checkLegal(pos, move); err != nil {
		return nil, err
	}
	n := &treeNode{
		id:     s.nextID,
		number: at.number + 1,
		usi:    move,
		text:   move,
		parent: at,
	}
	s.nextID++
	s.index[n.id] = n
	at.kids = append(at.kids, n)
	// **表記は生えたときに 1 回だけ付ける。** 親から上は動かないので、
	// あとから付け直す必要が無い（"同" も "右左上引" も祖先だけで決まる）。
	if nt, err := s.notationAt(at); err == nil {
		if t, err := nt.Next(move); err == nil && t.Text != "" {
			n.text = t.Text
		}
	}
	return n, nil
}

// checkLegal は pos でその手が指せるかを見る。
func checkLegal(pos *Position, move string) error {
	sfenStr, err := pos.SFEN()
	if err != nil {
		return err
	}
	moves, err := legal.Moves(sfenStr)
	if err != nil {
		return err
	}
	for _, m := range moves {
		if m.USI == move {
			return nil
		}
	}
	return fmt.Errorf("ikkyoku/position: その手は指せません: %s", move)
}

// notationAt は n までの手を進めた日本語表記の変換器を作る。
//
// ⚠️ **枝ごとに作り直すこと。** "同" は直前の手の移動先で決まるので、
// **別の枝の変換器を使い回すと「同」が嘘になる。**
func (s *Study) notationAt(n *treeNode) (*kifu.Notation, error) {
	rootSfen, err := s.root.SFEN()
	if err != nil {
		return nil, err
	}
	nt, err := kifu.NewNotation(rootSfen)
	if err != nil {
		return nil, err
	}
	for _, mv := range pathUSI(n) {
		if _, err := nt.Next(mv); err != nil {
			return nil, err
		}
	}
	return nt, nil
}

// positionAt は n の局面を根から組み立てる。
func (s *Study) positionAt(n *treeNode) (*Position, error) {
	p := s.root.Clone()
	for i, mv := range pathUSI(n) {
		if err := p.ApplyMove(mv); err != nil {
			return nil, fmt.Errorf("ikkyoku/position: %d手目を再現できません: %w", i+1, err)
		}
	}
	return p, nil
}

// GoTo はその節点の局面を見る（0 なら根）。**手順は消さない。**
//
// ⚠️ **引数は節点の id で、手数ではない**（枝があると同じ手数が何個もある）。
func (s *Study) GoTo(id int) error {
	n, ok := s.index[id]
	if !ok {
		return fmt.Errorf("ikkyoku/position: その手はありません: %d", id)
	}
	p, err := s.positionAt(n)
	if err != nil {
		return err
	}
	s.cur = n
	s.curPos = p
	return nil
}

// DropFrom はその節点**とその先**を木から消す（**手順リストの右クリック**）。
//
// **枝も本譜も同じように消せる**（2026-08-13 決定。本譜は URL から取り直せる）。
// 消した節点の id を返す（**評価値もそれで捨てる**）。
//
// ⚠️ **今見ている節点が消える範囲に入っていたら、親へ戻す。**
func (s *Study) DropFrom(id int) ([]int, error) {
	n, ok := s.index[id]
	if !ok || n.parent == nil {
		return nil, fmt.Errorf("ikkyoku/position: その手はありません: %d", id)
	}
	parent := n.parent
	for i, k := range parent.kids {
		if k == n {
			parent.kids = append(parent.kids[:i], parent.kids[i+1:]...)
			break
		}
	}
	// 消える範囲（自分と子孫）を集めて index から外す。
	gone := []int{}
	var walk func(*treeNode)
	walk = func(x *treeNode) {
		gone = append(gone, x.id)
		delete(s.index, x.id)
		for _, k := range x.kids {
			walk(k)
		}
	}
	walk(n)

	// 消えた先を見ていたなら親へ（見ていた節点が残っているならそのまま）。
	if _, alive := s.index[s.cur.id]; !alive {
		if err := s.GoTo(parent.id); err != nil {
			return gone, err
		}
	}
	return gone, nil
}
