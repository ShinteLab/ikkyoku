package main

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
// **戻って別の手を指したら、その先の評価値は捨てる** —— 別の手順の値なので。

import "sort"

// EvalPoint は折れ線の 1 点＝「ある局面に、あるエンジンが付けた評価値」。
type EvalPoint struct {
	// Ply は根からの手数（0 なら根の局面）。**押したときに `GoTo` へ渡す値。**
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
}

// evalStore は評価値の記録。**`StudyService` のロックの中でだけ触ること。**
type evalStore struct {
	// order はエンジンの登場順（凡例と折れ線の並び）。
	order []string
	label map[string]string
	// points は engineID → ply → 点。**ply で上書きする**（深いほうが後から届く）。
	points map[string]map[int]EvalPoint
	// epoch は手順の世代。**手順を切ったときだけ進める。**
	//
	// ⚠️ **これが無いと、捨てたはずの枝の評価値が後から書き戻る。** 解析は
	// 非同期なので、手を進めた後に前の局面の途中経過が届く。
	epoch int
}

// record は 1 点を記録する。**epoch が食い違っていたら捨てる**（捨てた枝の値）。
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
	s.points[engineID][p.Ply] = p
}

// dropAfter は ply より先の記録を捨てる（**手順を切ったとき**）。
//
// **epoch を進めるのはここだけ。** 走っている解析の途中経過が、捨てた枝の
// 値として書き戻るのを止める。
func (s *evalStore) dropAfter(ply int) {
	s.epoch++
	for _, m := range s.points {
		for n := range m {
			if n > ply {
				delete(m, n)
			}
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

// series は折れ線を Ply の昇順で組み立てる。
func (s *evalStore) series() []EvalSeries {
	out := make([]EvalSeries, 0, len(s.order))
	for _, id := range s.order {
		m := s.points[id]
		if len(m) == 0 {
			continue
		}
		plies := make([]int, 0, len(m))
		for n := range m {
			plies = append(plies, n)
		}
		sort.Ints(plies)
		pts := make([]EvalPoint, 0, len(plies))
		for _, n := range plies {
			pts = append(pts, m[n])
		}
		out = append(out, EvalSeries{EngineID: id, Label: s.label[id], Points: pts})
	}
	return out
}
