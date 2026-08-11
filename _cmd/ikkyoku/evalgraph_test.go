package main

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
	s.recordEval(target.Epoch, target.Ply, engineID, engineID, sc, 12)
}

// 手を進めながら記録すると、手数の順に並んだ折れ線になること。
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

// ⚠️ **戻って別の手を指したら、その先の評価値は捨てること。**
// 別の手順に付いた値なので、残すと**指していない手の評価値がグラフに残る。**
func TestEvalGraphDropsBranchedFuture(t *testing.T) {
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

	// 別の手を指すと手順が切られる → その先の評価値も消える。
	if _, err := s.Play("2g2f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	pts := s.Evals().Series[0].Points
	if len(pts) != 1 || pts[0].Ply != 0 {
		t.Fatalf("捨てた枝の評価値が残っています: %+v", pts)
	}
}

// 同じ手を指し直しただけなら手順は変わらないので、評価値も残ること。
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

// Undo は手順から手を消すので、評価値も消すこと（GoTo との違い）。
func TestEvalGraphUndoDropsPoint(t *testing.T) {
	s := adopted(t)
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "a", score(30))
	if _, err := s.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if len(s.Evals().Series) != 0 {
		t.Errorf("Undo したのに評価値が残っています: %+v", s.Evals().Series)
	}
}

// ⚠️ **手順を切った後に届いた途中経過を書き戻さないこと。**
// 解析は非同期なので、これが無いと**捨てた枝の評価値がグラフに戻る。**
func TestEvalGraphIgnoresStaleEpoch(t *testing.T) {
	s := adopted(t)
	target, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if _, err := s.Undo(); err != nil { // 手順を切る（epoch が進む）
		t.Fatalf("Undo: %v", err)
	}
	s.recordEval(target.Epoch, target.Ply, "a", "a", score(30), 12)
	if len(s.Evals().Series) != 0 {
		t.Errorf("古い世代の評価値が書き戻りました: %+v", s.Evals().Series)
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
}
