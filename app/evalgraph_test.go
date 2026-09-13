package app

import (
	"fmt"
	"testing"

	"github.com/ShinteLab/ikkyoku/analyze"
)

// score は先手視点の評価値（`analyze` が組み立てたものの代わり）。
func score(cp int) analyze.Score {
	return analyze.Score{CP: cp, Label: fmt.Sprintf("%+d", cp)}
}

// record は解析の代わりに 1 点だけ書き込む（`analyzeTarget` を通す経路を真似る）。
func record(t *testing.T, s *StudyService, engineID string, sc analyze.Score) {
	t.Helper()
	target, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	s.recordEval(target.Epoch, target.NodeID, engineID, engineID, sc, 12)
}

// 点が**勝率も持って**いること（2026-09-13。グラフの縦軸の切り替え用）。
//
// ⚠️ **フロントで評価値から計算し直させないための歯止め。** 式もポナンザ定数も
// `analyze.WinRate` の 1 か所にあり、点に入るのは**その結果の写し**
// （勝率バーと同じ値）。ここが落ちると、グラフだけ別の式で描かれる。
func TestEvalGraphKeepsWinRate(t *testing.T) {
	s := adopted(t)
	sc := score(600)
	sc.WinRate = analyze.WinRate(sc, 0)
	record(t, s, "a", sc)

	pts := s.Evals().Series[0].Points
	if len(pts) != 1 {
		t.Fatalf("点が %d 個（1 個のはず）", len(pts))
	}
	if pts[0].WinRate != sc.WinRate {
		t.Errorf("WinRate = %v, want %v（`analyze` が出した値をそのまま持つこと）",
			pts[0].WinRate, sc.WinRate)
	}
	// **評価値も残っていること**（軸を切り替えても同じ点を読むため）。
	if pts[0].CP != 600 {
		t.Errorf("CP = %d, want 600（勝率を足しても評価値は消さない）", pts[0].CP)
	}
}

// 手を進めながら記録すると、手数の順に並んだ折れ線になること。// 手を進めながら記録すると、手数の順に並んだ折れ線になること。
//
// ⚠️ **エンジンごとに別の折れ線であること。** 合成しない（平均も多数決も取らない）。
func TestEvalGraphRecordsPerEngine(t *testing.T) {
	s := adopted(t)
	record(t, s, "a", score(10))
	record(t, s, "b", score(-40))
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "a", score(30))

	g := s.Evals()
	if len(g.Series) != 2 {
		t.Fatalf("折れ線 = %d 本, want 2（エンジンごと）: %+v", len(g.Series), g.Series)
	}
	if g.Series[0].EngineID != "a" || g.Series[1].EngineID != "b" {
		t.Errorf("登場順に並んでいません: %+v", g.Series)
	}
	if len(g.Series[0].Points) != 2 || len(g.Series[1].Points) != 1 {
		t.Fatalf("点の数が違います: %+v", g.Series)
	}
	if g.Series[0].Points[0].Ply != 0 || g.Series[0].Points[1].Ply != 1 {
		t.Errorf("手数の昇順になっていません: %+v", g.Series[0].Points)
	}
	// 根が初期局面なので、棋譜の手数は根からの手数と一致する。
	if g.Series[0].Points[1].Number != 1 {
		t.Errorf("横軸の手数 = %d, want 1", g.Series[0].Points[1].Number)
	}
	// その局面に至った手をツールチップに出せること（根は空）。
	if g.Series[0].Points[0].Move != "" || g.Series[0].Points[1].Move != "▲７六歩" {
		t.Errorf("手の表記が付いていません: %+v", g.Series[0].Points)
	}
	if g.Ply != 1 || g.Last != 1 || g.First != 0 {
		t.Errorf("横軸の範囲が違います: %+v", g)
	}
}

// ⚠️ **別の枝を選んだら、折れ線もその枝のものになること。**
//
// 枝が入ってからは**捨てるのではなく「今の経路の点だけを出す」**（2026-08-13）。
// 混ぜると同じ手数に 2 つの値が並び、どちらの手順の評価値か分からなくなる。
// ⚠️ **戻れば前の枝の折れ線がそのまま出ること**（消していないので）。
func TestEvalGraphFollowsCurrentLine(t *testing.T) {
	s := adopted(t)
	record(t, s, "a", score(0))
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "a", score(30))

	if _, err := s.GoTo(0); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	// ⚠️ **GoTo では捨てない**（手順が消えないので、評価値も消さない）。
	if n := len(s.Evals().Series[0].Points); n != 2 {
		t.Fatalf("GoTo で評価値が消えました: %d 点", n)
	}

	// 別の手を指すと**枝が生える** → 折れ線はその枝のものになる。
	if _, err := s.Play("2g2f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	pts := s.Evals().Series[0].Points
	if len(pts) != 1 || pts[0].Ply != 0 {
		t.Fatalf("別の枝の評価値が混ざっています: %+v", pts)
	}
	// ⚠️ **前の枝へ戻せば、そちらの点はそのまま残っていること**（消していない）。
	if _, err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if n := len(s.Evals().Series[0].Points); n != 2 {
		t.Fatalf("枝の評価値が消えました: %d 点", n)
	}
}

// 同じ手を指し直しただけなら枝は増えないので、評価値も残ること。
func TestEvalGraphKeepsSameLine(t *testing.T) {
	s := adopted(t)
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "a", score(30))
	if _, err := s.GoTo(0); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if n := len(s.Evals().Series[0].Points); n != 1 {
		t.Errorf("同じ手を指し直しただけで評価値が消えました: %d 点", n)
	}
}

// DropFrom は手順から手を消すので、評価値も消すこと（GoTo との違い）。
func TestEvalGraphDropFromDropsPoint(t *testing.T) {
	s := adopted(t)
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "a", score(30))
	if _, err := s.DropFrom(1); err != nil {
		t.Fatalf("DropFrom: %v", err)
	}
	if len(s.Evals().Series) != 0 {
		t.Errorf("消した手の評価値が残っています: %+v", s.Evals().Series)
	}
}

// ⚠️ **根を入れ替えた後に届いた途中経過を書き戻さないこと**（`Epoch`）。
//
// 節点の id は**木ごとに 1 から振り直す**ので、これが無いと
// **前の対局の評価値が、同じ id の別の局面の点として書き戻る。**
func TestEvalGraphIgnoresStaleEpoch(t *testing.T) {
	s := adopted(t)
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	target, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	// 別の局面を採り直す（＝根が入れ替わり、木も id も作り直される）。
	if _, err := s.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	s.recordEval(target.Epoch, target.NodeID, "a", "a", score(30), 12)
	if len(s.Evals().Series) != 0 {
		t.Errorf("古い世代の評価値が書き戻りました: %+v", s.Evals().Series)
	}
}

// ⚠️ **消した節点には書かないこと。** 右クリックで消した枝の解析は
// **後から届く**ので、これが無いと消したはずの点が復活する。
func TestEvalGraphIgnoresDroppedNode(t *testing.T) {
	s := adopted(t)
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	target, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	if _, err := s.DropFrom(target.NodeID); err != nil {
		t.Fatalf("DropFrom: %v", err)
	}
	s.recordEval(target.Epoch, target.NodeID, "a", "a", score(30), 12)
	if len(s.Evals().Series) != 0 {
		t.Errorf("消した手の評価値が書き戻りました: %+v", s.Evals().Series)
	}
}

// 根を入れ替えたら全部捨てること（別の対局の話になる）。
func TestEvalGraphResetsOnNewRoot(t *testing.T) {
	s := adopted(t)
	record(t, s, "a", score(10))
	if _, err := s.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if len(s.Evals().Series) != 0 {
		t.Errorf("採り直したのに評価値が残っています: %+v", s.Evals().Series)
	}
	record(t, s, "a", score(10))
	s.Clear()
	if len(s.Evals().Series) != 0 {
		t.Errorf("空に戻したのに評価値が残っています: %+v", s.Evals().Series)
	}
}

// ⚠️ **根は初期局面とは限らない。** 撮った中盤の局面が根なら、横軸は
// その手数から始まる（根からの手数をそのまま横軸にすると、棋譜と食い違う）。
func TestEvalGraphAxisStartsAtRootMoveNumber(t *testing.T) {
	s := adopted(t)
	// SFEN の数え方の 41 ＝「次が 41 手目」＝ 40 手が指されている。
	if _, err := s.src.SetMoveNumber(41); err != nil {
		t.Fatalf("SetMoveNumber: %v", err)
	}
	if _, err := s.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	record(t, s, "a", score(0))
	g := s.Evals()
	if g.First != 40 || g.Series[0].Points[0].Number != 40 {
		t.Errorf("横軸が根の手数から始まっていません: %+v", g)
	}
	// ⚠️ **手順リストの手数とグラフの横軸は同じ数え方であること。**
	// 食い違うと、リストの「1手目」とグラフの「41手目」が同じ手になる。
	if st := s.State(); st.First != g.First {
		t.Errorf("StudyState.First(%d) と EvalGraph.First(%d) が違います", st.First, g.First)
	}
}

// ⚠️ **`Node.Number` は根からの手数で、棋譜の手数ではない。**
// 画面に手数として出すときは `StudyState.First` を足す ——
// **足し忘れると、撮った中盤の局面から始めたときにリストだけ 1 から数え直す。**
// ⚠️ **`GoTo` に渡すのは `Node.ID`**（手数ではない。枝があると同じ手数が何個もある）。
func TestStudyStateMoveNumberIsRelativeToRoot(t *testing.T) {
	s := adopted(t)
	if _, err := s.src.SetMoveNumber(41); err != nil {
		t.Fatalf("SetMoveNumber: %v", err)
	}
	if _, err := s.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	st, err := s.Play("7g7f")
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if st.First != 40 {
		t.Fatalf("First = %d, want 40", st.First)
	}
	if st.Nodes[0].Number != 1 {
		t.Errorf("Node.Number = %d, want 1（根からの手数）", st.Nodes[0].Number)
	}
	if st.Nodes[0].ID != st.CurrentID {
		t.Errorf("今見ている節点 = %d, 手順の 1 手目 = %d", st.CurrentID, st.Nodes[0].ID)
	}
}

// ⚠️ **枝に居るときは「分かれなかったほうの線」も返すこと**（2026-08-13）。
//
// **枝を選んだ結果がどう転んだかは、元の線と並べて初めて読める。**
// ⚠️ **共有している手前は入れないこと**（同じ点を 2 本描くことになる）。
func TestEvalGraphRefLineOnBranch(t *testing.T) {
	s := adopted(t)
	// 本譜 2 手（それぞれ評価値つき）。
	record(t, s, "a", score(0))
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "a", score(20))
	if _, err := s.Play("3c3d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "a", score(30))

	// 1 手目に戻って別の手 → 枝。
	if _, err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("8c8d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "a", score(-50))

	g := s.Evals()
	// 今の経路は 根 → ７六歩 → ８四歩。
	if len(g.Series) != 1 || len(g.Series[0].Points) != 3 {
		t.Fatalf("今の経路の点がおかしい: %+v", g.Series)
	}
	// **分かれた手数**（1 手目のあとで分かれた）。
	if g.Fork != 1 {
		t.Errorf("分かれた手数 = %d, want 1", g.Fork)
	}
	// 元の線（△３四歩）は**分岐点より先だけ**返ること。
	if len(g.Ref) != 1 {
		t.Fatalf("元の線が返っていません: %+v", g.Ref)
	}
	if len(g.Ref[0].Points) != 1 || g.Ref[0].Points[0].Ply != 2 {
		t.Errorf("共有している手前まで入っています: %+v", g.Ref[0].Points)
	}

	// 本譜に戻れば、元の線は要らない（分かれていない）。
	if _, err := s.GoTo(2); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	g = s.Evals()
	if g.Fork != 0 || len(g.Ref) != 0 {
		t.Errorf("本譜に居るのに分岐扱いです: fork=%d ref=%+v", g.Fork, g.Ref)
	}
}
