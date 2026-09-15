package app

// 評価値グラフの記録（解析タブ。2026-08-12）。
//
// **手順の 1 手ごとに「エンジンが出した最善手の評価値」を残す**だけの入れ物。
// 折れ線にして並べると、**どの手で形勢が動いたか**が一目で分かる ——
// 「次善手を選んだらどう転ぶか」を辿るのがこのアプリの中心なので、
// **辿った結果がどう転んだかを見せる面**が要る。
//
// ⚠️ **エンジンごとに別の折れ線にすること。** 評価値はエンジンが違えば食い違うのが
// 普通で、**その食い違いこそ見たいもの**（CLAUDE.md「エンジンをまたいで結果を
// 合成しないこと」）。**平均も多数決も取らない。**
//
// ⚠️ **記録は手順に紐づく**（局面の SFEN ではない）。だから置き場所は
// `StudyService`（手順を持っている側）で、`AnalyzeService` は記録を頼むだけ。
//
// ⚠️ **鍵は手順ツリーの節点 id**（2026-08-13。以前は手数だった）。枝が入ってからは
// 同じ手数の局面が何本もあるので、手数では指せない。**節点で持つと、戻って別の手を
// 指しても前の枝の評価値がそのまま残る** —— 捨てるのは**その節点を消したとき**だけ。

// EvalPoint は折れ線の 1 点＝「ある局面に、あるエンジンが付けた評価値」。
type EvalPoint struct {
	// ID は手順ツリーの節点（**押したときに `GoTo` へ渡す値**。0 は根）。
	//
	// ⚠️ **記録の鍵はこれ**（2026-08-13。以前は手数だった）。枝が入ると同じ手数の
	// 局面が何個もあるので、**手数では「どの局面に付いた評価値か」を指せない。**
	ID int `json:"id"`
	// Ply は根からの手数（0 なら根の局面）。**横軸の位置を出すのに使う。**
	Ply int `json:"ply"`
	// Number は棋譜の数え方の手数（**グラフの横軸**）。
	//
	// ⚠️ **Ply と別に持つ。** 根は初期局面とは限らない（撮った中盤の局面が
	// 根になる）ので、根の手数ぶんずれる。横軸に出すのは棋譜の手数のほう。
	Number int `json:"number"`
	// CP はセンチポーン相当（**先手が良ければ正**）。Mate が 0 のときだけ意味を持つ。
	//
	// ⚠️ **符号は `analyze` が先手視点に直したもの。** ここでも表示側でも
	// いじらないこと（手番が入れ替わっても折れ線の上下の意味が変わらないのは、
	// これが揃っているから）。
	CP int `json:"cp"`
	// Mate は詰みまでの手数（0 なら詰みなし。正なら先手が詰ます）。
	Mate int `json:"mate"`
	// WinRate は**先手の勝率**（0.0〜1.0）。グラフを勝率で見るときの縦位置。
	//
	// ⚠️ **フロントで評価値から計算し直さないこと。** 式もポナンザ定数も
	// `analyze.WinRate` の 1 か所にあり、ここに入っているのは
	// **その結果を写したもの**（`analyze.Score.WinRate`。勝率バーと同じ値）。
	//
	// ⚠️ **記録した時点の定数で決まる。** 設定タブで定数を変えても、既に
	// 記録した点は動かない（解析し直せば新しい定数で書き直される）——
	// **点は「そのとき何と出たか」の記録**なので、後から計算し直さない。
	WinRate float64 `json:"winRate"`
	// Label は表示用の文字列（"+230" / "▲詰 5手"）。
	//
	// **書式は `analyze` が組み立てたものをそのまま持つ。** フロントで作り直さない。
	Label string `json:"label"`
	// Depth はその評価値が出たときの深さ（ツールチップ用）。
	Depth int `json:"depth"`
	// Move はその局面に至った手の表記（根なら空）。ツールチップに出す。
	Move string `json:"move"`
}

// EvalSeries はエンジン 1 つぶんの折れ線。
type EvalSeries struct {
	// EngineID は設定の登録 ID。**同じ exe を option 違いで登録できる**ので、
	// 折れ線を分ける鍵はパスでも名前でもなくこれ。
	EngineID string `json:"engineId"`
	// Label は設定タブで付けた名前（凡例に出す）。
	Label string `json:"label"`
	// Points は Ply の昇順。
	Points []EvalPoint `json:"points"`
}

// EvalGraph はグラフを描くのに要るもの一式。
//
// **横軸の範囲を決める材料もここに入れる。** フロントが `StudyState` と
// 突き合わせて計算すると、2 つの値が別のタイミングで届くぶんだけずれる。
type EvalGraph struct {
	// Series はエンジンごとの折れ線（**解析に登場した順**）。
	Series []EvalSeries `json:"series"`
	// Ply は今見ている手（**縦線を引く位置**）。
	Ply int `json:"ply"`
	// Number は今見ている局面の棋譜手数。
	Number int `json:"number"`
	// Last は手順の最後の棋譜手数（**横軸の「自動」の右端**）。
	Last int `json:"last"`
	// First は根の棋譜手数（横軸の左端。根が初期局面なら 0）。
	First int `json:"first"`
	// Ref は**分かれなかったほうの経路**の折れ線（枝に居るときだけ。2026-08-13）。
	//
	// **枝を選んだ結果がどう転んだかは、元の線と並べて初めて読める。**
	// ⚠️ **共有している手前は入れない**（同じ点を 2 本描くことになる）。
	// ⚠️ **薄く描くこと** —— 主役は今辿っている線で、これは比べる相手。
	Ref []EvalSeries `json:"ref"`
	// Fork は今の経路が分かれた手数（棋譜の数え方。**0 なら分かれていない**）。
	//
	// グラフに縦線を引く位置。⚠️ **「今見ている手」の線とは別物**（あちらは
	// カーソル、こちらは**枝がどこから始まっているか**）。
	Fork int `json:"fork"`
	// Branches は**枝が分かれている手**の棋譜手数（昇順。無ければ空）。
	//
	// **欄外の帯に「ここに別の手順がある」という印を打つ**（2026-09-16）。
	// 折れ線は今の経路 1 本しか描かないので、**掘った枝が在ること自体が
	// グラフから読めなかった。**
	//
	// ⚠️ **`Fork` とは別物。** あちらは**今居る枝がどこから分かれたか**の 1 本で、
	// 欄内に縦線を引く。こちらは**経路上の分かれ道ぜんぶ**で、欄外に印を打つだけ。
	//
	// ⚠️ **評価値が付いているかは見ない**（解析していない枝も印が出る）。
	// **次に掘る候補**がそこに在ることこそ読みたいので、落とすと意味が減る。
	// ⚠️ **本数も持たない**（印は「在るか無いか」だけ。`position.Study.Branching`）。
	Branches []int `json:"branches"`
	// IDs は今の経路の節点 id（**ply 番目の要素がその手数の節点**。IDs[0] は根の 0）。
	//
	// ⚠️ **グラフを押したときの行き先はこれで引く。** 枝が入ってからは
	// 「手数 → 局面」が一意に決まらないので、**手数から `GoTo` の引数を作らないこと。**
	IDs []int `json:"ids"`
}

// evalStore は評価値の記録。**`StudyService` のロックの中でだけ触ること。**
type evalStore struct {
	// order はエンジンの登場順（凡例と折れ線の並び）。
	order []string
	label map[string]string
	// points は engineID → 節点 id → 点。**節点で上書きする**（深いほうが後から届く）。
	//
	// ⚠️ **手数で持たないこと**（枝が入ると同じ手数が何本もある）。節点で持つと、
	// **戻って別の手を指しても前の枝の評価値がそのまま残る**（消す必要が無い）。
	points map[string]map[int]EvalPoint
	// epoch は根の世代。**根を入れ替えたときだけ進める。**
	//
	// ⚠️ **これが無いと、前の対局の途中経過が新しい木の同じ id に書き戻る**
	// （id は木ごとに 1 から振り直すため）。⚠️ **枝を消したときは進めない** ——
	// 消えた節点の点は `drop` が消しており、**走っている他の解析まで捨てる必要は無い。**
	epoch int
}

// record は 1 点を記録する。**epoch が食い違っていたら捨てる**（前の対局の値）。
func (s *evalStore) record(epoch int, engineID, label string, p EvalPoint) {
	if epoch != s.epoch || engineID == "" {
		return
	}
	if s.points == nil {
		s.points = map[string]map[int]EvalPoint{}
		s.label = map[string]string{}
	}
	if _, ok := s.points[engineID]; !ok {
		s.points[engineID] = map[int]EvalPoint{}
		s.order = append(s.order, engineID)
	}
	if label != "" {
		s.label[engineID] = label
	}
	s.points[engineID][p.ID] = p
}

// drop は消えた節点の記録を捨てる（**手順を消したとき**）。
//
// ⚠️ **epoch は進めない。** 消えたのはこの節点だけで、**他の枝で走っている解析の
// 途中経過まで捨てる理由が無い**（進めると、消した瞬間に全部の結果が届かなくなる）。
func (s *evalStore) drop(ids []int) {
	for _, m := range s.points {
		for _, id := range ids {
			delete(m, id)
		}
	}
}

// reset は全部捨てる（**根が入れ替わったとき**。別の対局の話になる）。
func (s *evalStore) reset() {
	s.epoch++
	s.order = nil
	s.label = nil
	s.points = nil
}

// series は**今の経路にある点だけ**を手数の順で組み立てる。
//
// ⚠️ **木の全部を出さないこと。** 枝と本譜の点を 1 本の折れ線に混ぜると、
// 同じ手数に 2 つの値が並んで**どちらの手順の評価値か分からなくなる**。
// 見せるのは「今辿っている 1 本」で、枝を選べばそちらの折れ線になる。
//
// line は経路の節点 id（`position.Study.Line()`。先頭は根の 0）。
func (s *evalStore) series(line []int) []EvalSeries {
	out := make([]EvalSeries, 0, len(s.order))
	for _, id := range s.order {
		m := s.points[id]
		if len(m) == 0 {
			continue
		}
		pts := make([]EvalPoint, 0, len(m))
		for _, node := range line {
			if p, ok := m[node]; ok {
				pts = append(pts, p)
			}
		}
		if len(pts) == 0 {
			continue
		}
		out = append(out, EvalSeries{EngineID: id, Label: s.label[id], Points: pts})
	}
	return out
}
