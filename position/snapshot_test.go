package position_test

import (
	"reflect"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// build は枝も印も一通り入った検討を組み立てる（控えの往復を見るための材料）。
func build(t *testing.T) *position.Study {
	t.Helper()
	s := position.NewStudy(hirate(t))
	// 本譜 2 手（人が指した印が付く）。
	for _, m := range []string{"7g7f", "3c3d"} {
		if err := s.Play(m); err != nil {
			t.Fatalf("Play(%s): %v", m, err)
		}
	}
	// エンジンの読み筋（`Sources` が付く・今の局面は動かない）。
	if _, _, note := s.AddLine([]string{"2g2f", "8c8d"}, "engine-a"); note != "" {
		t.Fatalf("AddLine: %s", note)
	}
	// 1 手目に戻って別の手 → 枝。
	if err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if err := s.Play("8c8d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	return s
}

// 控えと復元で**木がそのまま戻ること**（2026-09-16）。
//
// ⚠️ **ここが崩れると、再起動のたびに解析が失われる**（Step 1 の目的そのもの）。
// 往復した控えが 1 バイトも変わらないことを見ている —— 節点の id・兄弟の順番・
// 印・見ていた場所・次に配る id のどれが落ちても、ここで落ちる。
func TestStudySnapshotRoundTrip(t *testing.T) {
	s := build(t)
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	back, err := position.RestoreStudy(snap)
	if err != nil {
		t.Fatalf("RestoreStudy: %v", err)
	}
	again, err := back.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot(復元後): %v", err)
	}
	if !reflect.DeepEqual(snap, again) {
		t.Fatalf("控えが往復しません:\n 元 = %+v\n 後 = %+v", snap, again)
	}
	// 見ていた場所と経路も同じであること。
	if back.CurrentID() != s.CurrentID() {
		t.Errorf("見ていた節点 = %d, want %d", back.CurrentID(), s.CurrentID())
	}
	if !reflect.DeepEqual(back.Line(), s.Line()) {
		t.Errorf("経路 = %v, want %v", back.Line(), s.Line())
	}
}

// ⚠️ **印を落とさないこと。** `Hand` / `Guess` / `Sources` が
// **「実際に現れた指し手か、仮定か」**を持っており、とくに `Guess`
// （追従が推測で足した手）が落ちると**嘘が実際の指し手として残る**。
func TestStudySnapshotKeepsMarks(t *testing.T) {
	s := build(t)
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var hand, sourced int
	for _, n := range snap.Nodes {
		if n.Hand {
			hand++
		}
		if len(n.Sources) > 0 {
			sourced++
		}
	}
	if hand == 0 || sourced == 0 {
		t.Fatalf("印が控えに入っていません: hand=%d sourced=%d", hand, sourced)
	}
	back, err := position.RestoreStudy(snap)
	if err != nil {
		t.Fatalf("RestoreStudy: %v", err)
	}
	got := map[int]position.Node{}
	for _, n := range back.Nodes() {
		got[n.ID] = n
	}
	for _, n := range snap.Nodes {
		b, ok := got[n.ID]
		if !ok {
			t.Fatalf("節点 %d が復元されていません", n.ID)
		}
		if b.Hand != n.Hand || b.Guess != n.Guess {
			t.Errorf("節点 %d の印が落ちています: hand=%v guess=%v", n.ID, b.Hand, b.Guess)
		}
		if !reflect.DeepEqual(b.Sources, n.Sources) && !(len(b.Sources) == 0 && len(n.Sources) == 0) {
			t.Errorf("節点 %d の出所が落ちています: %v, want %v", n.ID, b.Sources, n.Sources)
		}
	}
}

// ⚠️ **消した id を使い回さないこと。** 使い回すと、走っていた解析の途中経過が
// **別の節点の評価値として書き戻る**（画面を見ても気づけない壊れ方）。
func TestStudySnapshotKeepsNextID(t *testing.T) {
	s := build(t)
	// 枝を 1 本消す（id が飛ぶ）。
	if _, err := s.DropFrom(3); err != nil {
		t.Fatalf("DropFrom: %v", err)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	back, err := position.RestoreStudy(snap)
	if err != nil {
		t.Fatalf("RestoreStudy: %v", err)
	}
	// 復元したあとに 1 手足すと、**消した id ではなく続きの id** が付くこと。
	if err := back.GoTo(0); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if err := back.Play("2h6h"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	for _, n := range back.Nodes() {
		if n.USI == "2h6h" && n.ID < snap.NextID {
			t.Fatalf("消した id を使い回しています: %d（控えの nextId = %d）", n.ID, snap.NextID)
		}
	}
}

// ⚠️ **`HandsFixed` は SFEN では往復しない**ので別に持つこと。
// 落とすと、**撮って確定した局面の根で駒台の逆算が復活する**。
func TestStudySnapshotKeepsHandsFixed(t *testing.T) {
	// 撮った局面を確定したものは `HandsFixed` が偽（逆算が拠り所）。
	root := hirate(t)
	if root.HandsFixed {
		t.Fatalf("前提が崩れています（hirate の HandsFixed が真）")
	}
	snap, err := position.NewStudy(root).Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	back, err := position.RestoreStudy(snap)
	if err != nil {
		t.Fatalf("RestoreStudy: %v", err)
	}
	if back.Root().HandsFixed {
		t.Error("HandsFixed が復元で真になっています（FromFullSFEN が立てたまま）")
	}
}

// ⚠️ **見ていた節点が見つからなくても復元すること**（設計原則3）。
// 検討の中身は残っているので、**居場所が分からないだけで全部捨てる理由が無い。**
func TestStudyRestoreFallsBackToRoot(t *testing.T) {
	s := build(t)
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	snap.Current = 9999
	back, err := position.RestoreStudy(snap)
	if err != nil {
		t.Fatalf("RestoreStudy: %v", err)
	}
	if back.CurrentID() != 0 {
		t.Errorf("根に戻っていません: %d", back.CurrentID())
	}
	if len(back.Nodes()) != len(s.Nodes()) {
		t.Errorf("手順まで捨てています: %d 節点, want %d", len(back.Nodes()), len(s.Nodes()))
	}
}
