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
//   違いは「親の何番目の子か」だけ（**根から `kids[0]` だけを辿った 1 本が本譜**）。
//   **片方だけ消せる/消せないといった区別も付けない**（本譜は URL から取り直せる）
// - ⚠️ **枝の中の `kids[0]` は「先に足した」以上の意味を持たない。** 本筋のように
//   見せないこと（`Nodes` の字下げの規則を読むこと）
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
	// Sources は**この手を挙げたエンジン**の登録 ID（解析の候補手から足した枝）。
	//
	// **誰が言った手なのかを画面に出すため**（2026-08-14。手の後ろに色の丸）。
	// 枝は「エンジンがそう読んだ」だけの手なので、**本譜と同じ見た目のまま
	// 並ぶと、どれが誰の読み筋か分からなくなる**。
	//
	// ⚠️ **人が指した手には付かない**（`Play` / `Graft` は空で足す）。
	// ⚠️ **複数入りうる**（同じ手を 2 つのエンジンが挙げるのは普通にある）。
	// **後勝ちで上書きしないこと** —— 消すと「両方が同じ手を指した」という、
	// **一番読みたい一致**が見えなくなる。
	Sources []string `json:"sources,omitempty"`
	// Hand は**人が盤で指した手か**（解析タブで駒を動かした手）。
	//
	// 手順リストでは**黒い丸**（`Sources` の色の丸と並ぶ）。⚠️ **目立たせない** ——
	// 読みたいのは「エンジンが挙げた手（色）」のほうで、これは
	// **自分で入れた手だと後から分かればよい**という程度の印。
	//
	// ⚠️ **棋譜（KIF / URL）の手には付かない。** あちらは**実際に現れた指し手**で、
	// 「自分が試しに指した手」とは別物（`FromKIF` / `Graft` は付けない）。
	// ⚠️ **`Play` を通るのは盤の操作だけではない**（`FromKIF` も 1 手ずつ指す）ので、
	// **`Play` に印を付ける実装にしないこと。**
	Hand bool `json:"hand,omitempty"`
	// Chosen は**この手を「本線」に選んだか**（手順リストの「本線にする」。2026-08-18）。
	//
	// 変化の中の分かれ道は既定でどれも同格（全部 1 段下げて並ぶ）なので、
	// **「この続きを辿る」と決めた 1 本**を指すのにこれが要る。⚠️ **`Main` とは
	// 別物** —— あちらは**根から本譜を辿って届くか**で、こちらは
	// **親が続きに選んだか**（変化の中でも立つ）。
	//
	// ⚠️ **画面では「分岐にする」を出す条件にも使う**（本線を選び直すには、
	// 先に今の本線を外す必要がある）。
	Chosen bool `json:"chosen,omitempty"`
	// CanPromote は**この手を「本線にする」ことができるか**（2026-08-18）。
	//
	// 立つのは**親の続きがまだ決まっていない分かれ道の子**だけ ——
	// ⚠️ **本線が既にあるなら立たない**（選び直すには先に「分岐にする」で外す）。
	// ⚠️ **分岐の直後の手だけ**（その先の手は、親から見れば 1 本道なので選ぶも何も無い）。
	//
	// ⚠️ **フロントで同じ判定を書かないこと。** 「続きが決まっているか」は
	// 木の形（`kids[0]` と印）で決まるので、**画面に出ている字下げからは読めない。**
	CanPromote bool `json:"canPromote,omitempty"`
	// Main は**本譜か**（根から `kids[0]` だけを辿って届く手）。
	//
	// ⚠️ **「親の最初の子」ではない**（2026-08-13 に意味を変えた）。枝の中の
	// 最初の子は「先に足した」以上の意味を持たないので、そこまで本筋扱いすると
	// **同じ手に続きを 2 つ足したときに、先に足したほうが上位に見える。**
	Main bool `json:"main"`
}

// treeNode は手順ツリーの節点（**パッケージの外へ出さない**）。
type treeNode struct {
	id     int
	number int
	usi    string
	text   string
	parent *treeNode
	// sources はこの手を挙げたエンジンの登録 ID（`Node.Sources`）。
	sources []string
	// hand は人が盤で指した手か（`Node.Hand`）。
	hand bool
	// variation は**この手から先は本譜ではない**という印（「分岐にする」）。
	//
	// ⚠️ **順番だけでは表せないから要る**（2026-08-14）。枝と本譜の違いは
	// 普段「親の何番目の子か」だけだが、**子が 1 つしかないとその子は必ず
	// `kids[0]`＝本譜**になる。本譜の先端から試しに指した手がまさにそれで、
	// **「これは自分の検討で、対局の手ではない」と言う手段が無かった。**
	variation bool
	// chosen は**この手を続き（本線）に選んだ**という印（「本線にする」。2026-08-18）。
	//
	// ⚠️ **`variation` の裏返しではない。** あちらは**本譜から外す**印で、
	// こちらは**変化の中の分かれ道で、どれを続きとして辿るか**を決める印。
	// 変化の中の分かれ道は既定でどれも同格（`Nodes` が全部 1 段下げる）なので、
	// **選んだ 1 本を「同じ深さで続く」側に置く**のにこれが要る。
	//
	// ⚠️ **`kids[0]` であることだけでは表せない** —— あちらは「先に足した」以上の
	// 意味を持たないと決めてある（先に足したほうを上位に見せない）。
	chosen bool
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
// 並びは「その節点 → **枝**（字下げ）→ 続き」。⚠️ **枝を後ろへ回さないこと**
// —— 足した枝が 100 手先の最後尾に出ると、どこから分かれたのか分からない。
// この順なら**ある節点の子孫は必ずその直後に固まる**ので、消す範囲も見た目で分かる。
//
// # 字下げの規則（**2026-08-13 に 2 回直した**）
//
// **節点は 3 つの立場のどれかで、それぞれ子の下げ方が違う。**
//
//	main（本譜）… 続き（kids[0]）は同じ深さのまま。変化だけ 1 段下げる（→ head）
//	head（変化の頭）… ⚠️ **子は 1 本でも必ず 1 段下げる**（→ cont）
//	cont（変化の続き）… 子が 1 つなら同じ深さ（→ cont）／2 つ以上なら全部 1 段下げる
//
// ```
//	*70 ５四歩              ← main
//	     71 ９七角           ← head（変化はここから始まる）
//	         72 ６四歩       ← ⚠️ **1 本でも下げる**。以降は同じ深さで続く
//	         73 同　角
//	         72 ２九龍       ← 2 本目も同じ深さ（**先に足したほうを上位にしない**）
//	         73 ３二飛打
//	*71 ３二飛打            ← 本譜は下げない
// ```
//
// **head で必ず下げるのが肝**（2 回目の修正）。こうすると:
//
//   - **変化の頭がそのまま「畳める節点」になる。** ９七角を選んで足したら、
//     ９七角の下にその読み筋がぶら下がる、という見え方と操作が一致する
//   - ⚠️ **2 本目を足しても 1 本目の字下げが変わらない**（形が動かない）。
//     「子が 2 つ以上なら下げる」だけだと、**2 本目を足した瞬間に 1 本目が
//     右へずれて別物に見える**
//
// ⚠️ **`kids[0]` を特別扱いするのは本譜の上だけ。** 枝の中の `kids[0]` は
// 「先に足した」以上の意味を持たないので、そこを本筋のように見せると
// **同じ手の続きを 2 つ足したときに、先に足したほうが上位に見える**
// （実際に「先に選んだ９七角と後に選んだ９七角が同等に見えない」という形で出た）。
//
// ⚠️ **本譜まで下げないこと。** 分岐のたびに本譜が右へ流れると、
// 150 手の棋譜では字下げが頭打ちに達して**深さが何も意味しなくなる。**
func (s *Study) Nodes() []Node {
	const (
		main = iota // 本譜の上
		head        // 変化の頭（ここから下は 1 段下がる）
		cont        // 変化の続き
	)
	out := []Node{}
	var walk func(n *treeNode, depth, mode int)
	walk = func(n *treeNode, depth, mode int) {
		if len(n.kids) == 0 {
			return
		}
		switch {
		case mode == main && n.kids[0].variation:
			// **本譜はここで終わり**（「分岐にする」で下げた手）。続きは
			// **全部変化**なので、順番のまま 1 段下げて頭にする。
			// ⚠️ **`kids[0]` を後ろへ回さないこと** —— 下げただけで順番まで
			// 変わると、**画面のどこへ動いたのか目で追えない。**
			for _, k := range n.kids {
				out = append(out, k.node(depth+1, false).promotable(true))
				walk(k, depth+1, head)
			}
		case mode == main:
			// **枝が先、本譜があと**（枝は 1 段下げて、そこが変化の頭になる）。
			for _, k := range n.kids[1:] {
				out = append(out, k.node(depth+1, false))
				walk(k, depth+1, head)
			}
			out = append(out, n.kids[0].node(depth, true))
			walk(n.kids[0], depth, main)
		case len(n.kids) > 1 && n.kids[0].chosen:
			// **続きに選んだ手がある**（「本線にする」。2026-08-18）。
			// **本譜と同じ形にする** —— 枝を先に 1 段下げて出し、選んだ続きは
			// **続きの深さのまま**伸ばす。⚠️ **同じ深さに並べないこと** ——
			// 並べると選んだ 1 本が他の候補と見分けられず、選んだ意味が消える。
			//
			// ⚠️ **続きの深さは「1 本しか無かったときと同じ」**にする ——
			// 頭（`head`）の続きは 1 本でも下げる規則なので、そこだけ +1。
			// ここを `depth` に固定すると、**選んだ瞬間に頭とその続きが同じ深さ**に
			// なって、どこから始まる変化なのか読めなくなる。
			d := depth
			if mode == head {
				d++
			}
			for _, k := range n.kids[1:] {
				out = append(out, k.node(d+1, false))
				walk(k, d+1, head)
			}
			out = append(out, n.kids[0].node(d, false))
			walk(n.kids[0], d, cont)
		case mode == head || len(n.kids) > 1:
			// ⚠️ **頭の子は 1 本でも下げる／分かれ道は全部そろえて下げる**（上記）。
			//
			// ⚠️ **下げた先が分かれ道なら、その子たちも「頭」**（2026-08-13）。
			// 分かれた手は「そこから始まる変化」なので、**続きを 1 段下げる＝
			// その手が畳める節点になる**。下げないと、読み筋が 15 手ぶら下がったとき
			// **次の候補が画面の外**に出て、**その手で何を指したのかを見比べられない**
			// （分岐を見る意味そのものが薄れる）。
			// ⚠️ **`mode == head` のときも見ること** —— 頭のすぐ下で分かれるのは
			// 普通にある（同じ手を挙げたエンジンの読み筋が次の手から違う）。
			// **ここを cont に固定していて、実際に分かれて見えなかった。**
			next := cont
			if len(n.kids) > 1 {
				next = head
			}
			for _, k := range n.kids {
				// ⚠️ **変化の頭は 1 本でも「頭」として歩くこと**（2026-08-18）。
				// そうしないと**その続きが同じ深さで伸びて畳めない**
				// （エンジンの読み筋 15 手がそのまま並ぶ）。
				m := next
				if k.variation {
					m = head
				}
				// **続きがまだ決まっていないなら「本線にする」で選べる。**
				// 分かれ道ならどれも同格、1 本でも**変化の頭なら続きではない**。
				out = append(out, k.node(depth+1, false).promotable(len(n.kids) > 1 || k.variation))
				walk(k, depth+1, m)
			}
		case n.kids[0].variation:
			// **1 本しか無いが、それは「変化の頭」**（エンジンが足した手・
			// 「分岐にする」で下げた手）。⚠️ **同じ深さで伸ばさないこと** ——
			// 続きではないうえ、**畳む場所が無くなる**（足した読み筋がそのまま並ぶ）。
			out = append(out, n.kids[0].node(depth+1, false).promotable(true))
			walk(n.kids[0], depth+1, head)
		default:
			// 1 本道の続き。下げる意味が無い。
			out = append(out, n.kids[0].node(depth, false))
			walk(n.kids[0], depth, cont)
		}
	}
	walk(s.top, 0, main)
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
		Chosen:  n.chosen,
		Sources: append([]string(nil), n.sources...),
		Hand:    n.hand,
	}
}

// promotable は「本線にする」で選べる手に印を付ける（`Nodes` の中だけで使う）。
//
// ⚠️ **付けるのは「親の続きがまだ決まっていない分かれ道の子」だけ。**
// 判定を `Nodes` の中に置いてあるのは、**そこが「続きが決まっているか」を
// 既に知っている唯一の場所**だから（表示の分岐がそのまま条件になる）。
func (n Node) promotable(ok bool) Node {
	n.CanPromote = ok
	return n
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

// Fork は今の経路が**どこで分かれたか**と、**分かれなかったほうの経路**を返す。
//
// 「分かれなかったほう」＝ その分かれ道で `kids[0]`（先に指した/本譜側）を選んで
// いたら辿っていた 1 本。**評価値グラフで元の線を薄く並べる**のに使う ——
// 枝を選んだ結果がどう転んだかは、**元の線と並べて初めて読める。**
//
// ⚠️ **本譜とは限らない。** 見るのは**経路の一番深い分かれ道**なので、枝の中の
// 枝に居るなら、比べる相手は 1 つ外側の枝になる（それが「親」）。
//
// forkID は分かれた**手前**の節点（＝分岐点。0 なら分かれていない＝本譜に居る）。
// ok が false なら分かれ道はひとつも通っていない。
func (s *Study) Fork() (forkID int, ref []int, ok bool) {
	// 経路を上へ辿って、**最後に「最初の子ではない子」を通ったところ**を探す。
	var at *treeNode
	for n := s.cur; n.parent != nil; n = n.parent {
		if len(n.parent.kids) > 0 && n.parent.kids[0] != n {
			at = n.parent
			break
		}
	}
	if at == nil {
		return 0, nil, false
	}
	// 分岐点までの道のり（共有している部分）＋ そこから `kids[0]` 側へ辿った先。
	up := []int{}
	for p := at; p != nil; p = p.parent {
		up = append(up, p.id)
	}
	for i := len(up) - 1; i >= 0; i-- {
		ref = append(ref, up[i])
	}
	for n := at; len(n.kids) > 0; {
		n = n.kids[0]
		ref = append(ref, n.id)
	}
	return at.id, ref, true
}

// MainLine は本譜（根から `kids[0]` を辿った並び）の手を返す。
//
// **棋譜の取り直し（`Graft`）の突き合わせ相手。** 枝に居ても本譜が返る。
func (s *Study) MainLine() []string {
	out := []string{}
	for n := s.top; len(n.kids) > 0; {
		// ⚠️ **「分岐にする」で下げた手から先は本譜ではない**（2026-08-14）。
		// ここで止めないと、**自分の検討が本譜として扱われる**
		// （棋譜の取り直しで「最後の手を見ていたか」の判定にも使う）。
		if n.kids[0].variation {
			break
		}
		n = n.kids[0]
		out = append(out, n.usi)
	}
	return out
}

// MainTip は**本譜の先端**の節点 id と、その局面を返す（手が 1 つも無ければ 0 と根）。
//
// **中継を追うときの繋ぎ先**（`position.Connect` に渡す元の局面）。
//
// ⚠️ **`Current()` ではない。** ユーザーが枝の途中を読んでいることは普通にあるので、
// そちらを繋ぎ先にすると**検討の枝に中継の手が生える**。
// ⚠️ **見ている場所は動かさない**（ここは読むだけ）。
// ⚠️ **`MainLine` と同じところで止まる** —— 「分岐にする」で下げた手から先は
// 本譜ではないので、**先端もそこまで戻る**（`Graft` と同じ見え方になる）。
func (s *Study) MainTip() (int, *Position, error) {
	n := s.top
	for len(n.kids) > 0 && !n.kids[0].variation {
		n = n.kids[0]
	}
	p, err := s.positionAt(n)
	if err != nil {
		return n.id, nil, err
	}
	return n.id, p, nil
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
func (s *Study) Play(move string) error { return s.play(move, mark{hand: true}) }

// play は印を指定して 1 手指す（`Play` の中身）。
//
// ⚠️ **棋譜の取り込み（`FromKIF`）もここを通る**ので、**印はゼロ値**で渡す。
// あちらは実際に現れた指し手で、「自分で試しに指した手」ではない。
func (s *Study) play(move string, m mark) error {
	next, err := s.grow(s.cur, s.curPos, move, m)
	if err != nil {
		return err
	}
	s.cur = next
	s.curPos, err = s.positionAt(next)
	return err
}

// PlayLine は**エンジンが挙げた手を 1 手指す**（解析タブの候補手の右クリック →
// 「手順を指す」。2026-09-14）。`Play` と `AddLine` の中間で、**足して、そこへ進む**。
//
// 自動で指し継ぐ（＝止めるまでエンジンの最善手を 1 手ずつ指し続ける）のに、
// 既にある 2 つでは足りない:
//
//   - **`AddLine` は進まない**（今見ている局面は動かない）ので、次の 1 手を
//     考えさせる局面にならない。返るのも最初に生えた節点の id だけで、
//     **既にある手をなぞったときは 0**（＝進む先を指せない）
//   - **`Play` では誰が指した手か残らない**（`Node.Sources` が空になり、
//     手順リストで人が盤で指した手と見分けが付かない）
//
// head は**この手を変化の頭にするか**。⚠️ **指し継ぐ列の 1 手目だけに立てること**
// —— 毎手立てると `Nodes` の字下げが 1 手ごとに 1 段増え、**40 手指させたら
// 40 段の階段**になる。2 手目から先はその手の続き（1 本道）なので下がらない。
//
// ⚠️ **既にある手には立てない**（本譜の続きをなぞったとき）。立てると
// **実際に現れた指し手を変化に落とす**ことになる（`Branch` を黙って押すのと同じ）。
func (s *Study) PlayLine(move, source string, head bool) error {
	at, before := s.cur, len(s.cur.kids)
	if err := s.play(move, mark{engine: source}); err != nil {
		return err
	}
	if head && len(at.kids) > before {
		s.cur.variation = true
	}
	return nil
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
//
// source は**その読み筋を出したエンジン**の登録 ID（`Node.Sources`）。
// ⚠️ **既にある手にも足す** —— 同じ手を 2 つのエンジンが挙げたことは
// **一番読みたい一致**なので、辿るだけで済ませずに記録する。
//
// 戻り値は（最初に生えた節点の id・足した手数・止まった理由）。
// **1 手も足せなかった**（全部が既にある）ときは id が 0 になる。
func (s *Study) AddLine(moves []string, source string) (int, int, string) {
	at, pos := s.cur, s.curPos.Clone()
	first, added := 0, 0
	for i, mv := range moves {
		before := len(at.kids)
		next, err := s.grow(at, pos, mv, mark{engine: source})
		if err != nil {
			return first, added, fmt.Sprintf("%d手目「%s」で止まりました: %v", i+1, mv, err)
		}
		if len(at.kids) > before {
			added++
			if first == 0 {
				first = next.id
				// ⚠️ **最初の 1 手は「変化の頭」にする**（2026-08-18）。
				// **エンジンが読んだだけの手は、その線の続きではない** ——
				// 印を立てないと、続きの無いところへ足したときに
				// **読み筋 15 手がそのまま今の線として伸びる**（畳む場所も無い）。
				// 頭にしておけば**1 手だけが 1 段下がって「＋」で畳める。**
				//
				// ⚠️ **本譜の先端に足したときも同じ** —— `MainLine` はそこで
				// 止まる（**実際に現れた指し手ではない**ので、それが正しい）。
				// 続きとして辿ると決めたら「本線にする」（`Promote`）。
				next.variation = true
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
		next, err := s.grow(at, pos, mv, mark{})
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
		// ⚠️ **「分岐にする」の印も落とすこと** —— URL が同じ手を本譜として
		// 持ってきたなら、それは**実際に現れた指し手**（自分の検討ではない）。
		next.variation = false
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

// pos は at の局面（合法手の判定に要る）。⚠️ **合法手でなければ足さない。**
// mark はその手が**どこから来たか**（手順リストに出す印）。
//
// ⚠️ **棋譜（KIF / URL）の手には何も付けない**（ゼロ値）。あちらは**実際に現れた
// 指し手**なので、「誰かがそう言った手」ではない。
type mark struct {
	// engine はこの手を挙げたエンジンの登録 ID（解析の候補手から足したとき）。
	engine string
	// hand は人が盤で指したか。
	hand bool
}

// grow は at の子として move を生やす（**既にあるならそれを返す**）。
//
// ⚠️ **既にある手にも印を足す**（同じ手を 2 つのエンジンが挙げるのは普通にあり、
// **その一致こそ読みたい**。棋譜の手を自分でも指したなら、それも印が要る）。
func (s *Study) grow(at *treeNode, pos *Position, move string, m mark) (*treeNode, error) {
	for _, k := range at.kids {
		if k.usi == move {
			k.addMark(m)
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
	n.addMark(m)
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

// addMark はその手に付いた印を足す（**消さない**）。
//
// ⚠️ **同じエンジンを重ねないこと**（連続解析では同じ読み筋を何度も足しうる）。
// ⚠️ **一度付いた印は落とさないこと** —— エンジンが挙げた手を自分でも指したなら、
// **どちらも本当**（片方だけ残すと、どちらが消えたのか画面からは分からない）。
func (n *treeNode) addMark(m mark) {
	if m.hand {
		n.hand = true
	}
	if m.engine == "" {
		return
	}
	for _, s := range n.sources {
		if s == m.engine {
			return
		}
	}
	n.sources = append(n.sources, m.engine)
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
// Branch はその手から先を**本譜ではなく変化にする**（手順リストの「分岐にする」）。
//
// **本譜の先端から試しに指した手を、エンジンの読み筋と同じ扱いに落とす操作。**
// 手順リストでは 1 段下がって**前の手にぶら下がり**、畳めるようになる。
//
// ⚠️ **順番の入れ替えでは表せない。** 枝と本譜の違いは普段「親の何番目の子か」
// だけだが、**子が 1 つならその子は必ず `kids[0]`＝本譜**になる。試しに指した手が
// まさにそれなので、印を持たせている（`treeNode.variation`）。
//
// ⚠️ **手順は 1 手も消えない**（`DropFrom` と混同しないこと）。見ている局面も動かない。
//
// **元に戻すのは棋譜の取り直し**（`Graft` が印を落とす）。⚠️ URL が同じ手を
// 本譜として持ってきたら、それは実際に現れた指し手なので**印は消える**のが正しい。
func (s *Study) Branch(id int) error {
	n, ok := s.index[id]
	if !ok || n.parent == nil {
		return fmt.Errorf("ikkyoku/position: その手はありません: %d", id)
	}
	n.variation = true
	// ⚠️ **「本線にする」で選んだ印も落とすこと**（2026-08-18）。落とさないと
	// **選び直せない** —— `Promote` は「続きがまだ決まっていない」ときだけ通すので、
	// 外す手段が無いと最初に選んだ 1 本で固まる。**これが唯一の外し方。**
	n.chosen = false
	return nil
}

// Promote はその手を**分かれ道の続き（本線）に選ぶ**（手順リストの「本線にする」。
// 2026-08-18）。**`Branch` の裏返し。**
//
// エンジンの読み筋を 2 本足すと、**どちらも同格の候補**として並ぶ（続きが
// 決まっていない状態。**それ自体は正しい** —— 先に足したほうを上位に見せない）。
// そこから**「この続きを辿る」と決める**のがこの操作で、選んだ手は
// **同じ深さで続く 1 本**になり、残りは枝として 1 段下がる。
//
// ⚠️ **手順は 1 手も消えない。見ている局面も動かない**（`DropFrom` と混同しない）。
// 評価値も捨てない（節点はそのままで、**どれを続きとするかが変わるだけ**）。
//
// ⚠️ **続きが既に決まっているなら断る**（本譜の続きがある場合も含む）。
// 選び直すときは**先に今の続きを「分岐にする」で外す** —— 黙って
// 押しのけると、**どれが本線だったのか分からないまま入れ替わる。**
//
// ⚠️ **分かれ道の直後の手だけ**（その先の手は、親から見れば 1 本道なので選べない）。
func (s *Study) Promote(id int) error {
	n, ok := s.index[id]
	if !ok || n.parent == nil {
		return fmt.Errorf("ikkyoku/position: その手はありません: %d", id)
	}
	// ⚠️ **断るのは「続きがもう決まっている」ときだけ**（`openFork`）。
	// **1 本しか無くても、それが変化の頭なら選べる** —— 読み筋を 1 本だけ
	// 足したときがそれ。**`len(kids) >= 2` を条件にしないこと。**
	if !openFork(n.parent) {
		return fmt.Errorf("ikkyoku/position: もう本線が決まっています: %d", id)
	}
	p := n.parent
	// **先頭へ移す**（`Line` も連続解析もここを辿る）。
	promote(p, n)
	// ⚠️ **本譜側の印も落とすこと** —— 親が本譜なら、この手から先が本譜に戻る
	//（`Branch` で下げた手をもう一度本譜に戻す、という道でもある）。
	n.variation = false
	n.chosen = true
	return nil
}

// openFork は**その分かれ道の続きがまだ決まっていないか**（`Promote` の可否）。
//
// ⚠️ **`Nodes` の「全部 1 段下げる」場合と揃えてあること。** 食い違うと、
// **画面では同格に並んでいるのにメニューに出ない**（あるいはその逆）になる。
func openFork(p *treeNode) bool {
	if len(p.kids) == 0 {
		return false
	}
	// **先頭が変化の頭なら続きは空いている**（エンジンが足した手・「分岐にする」で
	// 下げた手）。⚠️ **1 本しか無くても空いている** —— 読み筋を 1 本だけ足した
	// ときがそれで、**それを続きに選べないと「本線にする」が使えない。**
	if p.kids[0].variation {
		return true
	}
	if onMainLine(p) {
		// 本譜の上なら、`kids[0]` がそのまま続き。
		return false
	}
	if len(p.kids) == 1 {
		// 変化の中の 1 本道。**それが続きそのもの**なので選ぶまでもない。
		return false
	}
	// 変化の中の分かれ道は**選んだ手があるときだけ**続きが決まっている。
	return !p.kids[0].chosen
}

// onMainLine は根から本譜（`kids[0]` かつ「分岐にする」で下げていない）を
// 辿って届く節点か。
func onMainLine(n *treeNode) bool {
	for ; n.parent != nil; n = n.parent {
		if n.variation || n.parent.kids[0] != n {
			return false
		}
	}
	return true
}

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
