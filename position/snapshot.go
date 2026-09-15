// 検討（`Study`）の控えと復元（2026-09-16）。
//
// **開発中は再起動が多く、そのたびに解析の結果が失われていた。** 中継を追って
// 積み上げた評価値が、アプリを落とすだけで全部消える。
//
// ⚠️ **これは「棋譜」ではない。** 棋譜として配る形（KIF の `変化：N手`）は
// `core/kifu` が決めるもので、**まだ木を持っていない**（ワークスペースの
// `TODO.md` 1）。ここが持つのは**自分で読み直すためだけの控え**。
// ⚠️ **交換形式として使わないこと** —— 他のソフトに渡すなら KIF に書き出す。
package position

import (
	"fmt"
	"strings"
)

// StudySnapshot は検討を丸ごと控えたもの（JSON にしてディスクへ）。
//
// ⚠️ **節点の id をそのまま持つ。** 評価値の鍵が id なので、**復元で振り直すと
// 点が全部迷子になる**（`app.EvalPoint.ID`）。`Play` で積み直す形の復元は
// **できない**のはこのため。
type StudySnapshot struct {
	// Root は根の局面（**完全な SFEN**）。
	//
	// ⚠️ **根は初期局面とは限らない**（撮った中盤の局面が根になる）。
	Root string `json:"root"`
	// HandsFixed は根の「駒台は書いてあるとおり」（駒落ち）。
	//
	// ⚠️ **SFEN では往復しないので別に持つ。** `FromFullSFEN` は必ず true に
	// するが、**撮った局面を確定したものは false** —— そこが食い違うと、
	// 駒台の逆算が復活する。
	HandsFixed bool `json:"handsFixed,omitempty"`
	// MateProblem は根が詰将棋か。**これも SFEN では往復しない。**
	MateProblem bool `json:"mateProblem,omitempty"`
	// Nodes は根を**除いた**全節点（**親が必ず先に来る前順**）。
	//
	// ⚠️ **並び順が兄弟の順序そのもの。** 兄弟の 1 番目（`kids[0]`）が本譜側なので、
	// **並べ替えて保存すると本譜と変化が入れ替わる。**
	Nodes []NodeSnapshot `json:"nodes"`
	// Current は控えた時点で見ていた節点（0 は根）。
	Current int `json:"current"`
	// End は**実際の対局が終わった節点**（0 なら分かっていない。2026-09-16）。
	//
	// ⚠️ **落とさないこと。** 落とすと、開き直したときに**投了図以下の印が消える**
	// （棋譜から読み直す口が無いので、二度と戻らない）。
	End int `json:"end,omitempty"`
	// NextID は次に配る id。
	//
	// ⚠️ **消した id を使い回さないために持つ。** 最大の id + 1 で済ませると、
	// **消した枝の解析が後から別の節点の評価値として書き戻る**（`Study.nextID`）。
	NextID int `json:"nextId"`
}

// NodeSnapshot は節点 1 つ。
//
// ⚠️ **印（`Hand` / `Guess` / `Sources` / `Variation` / `Chosen`）を落とさないこと。**
// あれが**「実際に現れた指し手か、仮定か」**を持っている。とくに `Guess`
// （追従が推測で足した印）を落とすと、**嘘が実際の指し手として残る** ——
// ワークスペースの `TODO.md`「本譜のロック」が一番避けたい壊れ方そのもの。
type NodeSnapshot struct {
	ID     int    `json:"id"`
	Parent int    `json:"parent"`
	USI    string `json:"usi"`
	// Text は棋譜の表記（"７六歩"）。
	//
	// ⚠️ **控えておくこと**（復元で組み立て直さない）。表記は祖先だけで決まるので
	// 組み立て直せるが、**節点ごとに根から辿り直す**ので節点の数の 2 乗になる。
	// **控えは「そのとき何と出たか」の記録**でもあるので、写しで正しい。
	Text      string   `json:"text,omitempty"`
	Sources   []string `json:"sources,omitempty"`
	Hand      bool     `json:"hand,omitempty"`
	Guess     bool     `json:"guess,omitempty"`
	Variation bool     `json:"variation,omitempty"`
	Chosen    bool     `json:"chosen,omitempty"`
}

// Snapshot は今の検討を控える。
//
// ⚠️ **根の SFEN が組み上がらないなら控えない**（未決が残っている局面）。
// 検討の根は確定した局面のはずなので、ここで落ちるのは呼ぶ場所を間違えている。
func (s *Study) Snapshot() (StudySnapshot, error) {
	sfen, err := s.root.SFEN()
	if err != nil {
		return StudySnapshot{}, fmt.Errorf("ikkyoku/position: 根の局面を控えられません: %w", err)
	}
	snap := StudySnapshot{
		Root:        sfen,
		HandsFixed:  s.root.HandsFixed,
		MateProblem: s.root.MateProblem,
		Nodes:       []NodeSnapshot{},
		Current:     s.cur.id,
		End:         s.end,
		NextID:      s.nextID,
	}
	// ⚠️ **前順（親が先・兄弟は順番どおり）で並べること。** 復元は
	// 「親へ順に足す」だけなので、**この並びが木の形そのもの**になる。
	var walk func(n *treeNode)
	walk = func(n *treeNode) {
		for _, k := range n.kids {
			snap.Nodes = append(snap.Nodes, NodeSnapshot{
				ID:        k.id,
				Parent:    n.id,
				USI:       k.usi,
				Text:      k.text,
				Sources:   append([]string(nil), k.sources...),
				Hand:      k.hand,
				Guess:     k.guess,
				Variation: k.variation,
				Chosen:    k.chosen,
			})
			walk(k)
		}
	}
	walk(s.top)
	return snap, nil
}

// RestoreStudy は控えから検討を組み立て直す。
//
// ⚠️ **合法性は見直さない。** 控えは自分が書いたもので、書いた時点では合法だった。
// 全節点で `legal.Moves` を回すと節点の数だけ探索が走る（200 手ぶんで数百 ms）。
// 代わりに**見ていた節点の局面だけ組み立て直し**、そこで落ちたら復元しない ——
// 壊れた控えで起動して**別の局面を解析し続ける**よりは、復元しないほうがよい。
//
// ⚠️ **見ていた節点が見つからなければ根に戻す**（復元そのものは続ける）。
// 検討の中身は残っているので、**居場所が分からないだけで全部捨てる理由が無い**
// （設計原則3）。
func RestoreStudy(snap StudySnapshot) (*Study, error) {
	if strings.TrimSpace(snap.Root) == "" {
		return nil, fmt.Errorf("ikkyoku/position: 控えに根の局面がありません")
	}
	root, err := FromFullSFEN(snap.Root)
	if err != nil {
		return nil, fmt.Errorf("ikkyoku/position: 控えの根が読めません: %w", err)
	}
	// ⚠️ **SFEN では往復しない 2 つを戻すこと**（`FromFullSFEN` は必ず
	// `HandsFixed` を立てるので、撮った局面が根だと食い違う）。
	root.HandsFixed = snap.HandsFixed
	root.MateProblem = snap.MateProblem

	s := NewStudy(root)
	for _, ns := range snap.Nodes {
		if ns.ID <= 0 {
			return nil, fmt.Errorf("ikkyoku/position: 控えの節点 id が不正です: %d", ns.ID)
		}
		if _, dup := s.index[ns.ID]; dup {
			return nil, fmt.Errorf("ikkyoku/position: 控えの節点 id が重複しています: %d", ns.ID)
		}
		parent, ok := s.index[ns.Parent]
		if !ok {
			// 前順で並んでいれば親は必ず先に入っている。入っていないなら
			// **並びが壊れている**ので、黙って拾わない。
			return nil, fmt.Errorf("ikkyoku/position: 控えの親が見つかりません: id=%d parent=%d", ns.ID, ns.Parent)
		}
		n := &treeNode{
			id:        ns.ID,
			number:    parent.number + 1,
			usi:       ns.USI,
			text:      ns.Text,
			parent:    parent,
			sources:   append([]string(nil), ns.Sources...),
			hand:      ns.Hand,
			guess:     ns.Guess,
			variation: ns.Variation,
			chosen:    ns.Chosen,
		}
		if n.text == "" {
			n.text = n.usi
		}
		s.index[n.id] = n
		parent.kids = append(parent.kids, n)
		if n.id >= s.nextID {
			s.nextID = n.id + 1
		}
	}
	// ⚠️ **控えの `NextID` のほうが大きければそちらを採る** —— 消した枝のぶん
	// 飛んでいるのが正しい（使い回すと、走っていた解析が別の節点に書き戻る）。
	if snap.NextID > s.nextID {
		s.nextID = snap.NextID
	}

	// ⚠️ **知らない節点なら忘れる**（`SetRecordEnd` が見る）。
	s.SetRecordEnd(snap.End)
	cur, ok := s.index[snap.Current]
	if !ok {
		cur = s.top
	}
	pos, err := s.positionAt(cur)
	if err != nil {
		return nil, fmt.Errorf("ikkyoku/position: 控えの手順が組み立てられません: %w", err)
	}
	s.cur = cur
	s.curPos = pos
	return s, nil
}
