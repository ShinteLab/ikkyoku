package position_test

import (
	"encoding/json"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// 棋譜の消費時間とコメントが手順に載ること（2026-10-04）。
const notedKIF = "手合割：平手\n" +
	"*開始局面のコメント\n" +
	"手数----指手---------消費時間--\n" +
	"   1 ７六歩(77)   ( 0:00/00:00:00)\n" +
	"*角道を開ける\n" +
	"*二行目\n" +
	"   2 ３四歩(33)   ( 4:05/00:04:05)\n" +
	"   3 ２六歩(27)   (125:00/02:05:00)\n"

func TestFromKIFKeepsNotes(t *testing.T) {
	study, _, err := position.FromKIF(notedKIF)
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	if got := study.RootComment(); got != "開始局面のコメント" {
		t.Errorf("開始局面のコメント = %q", got)
	}
	nodes := study.Nodes()
	if len(nodes) != 3 {
		t.Fatalf("手順の数 = %d, want 3", len(nodes))
	}
	// ⚠️ **0 秒の手も "0:00" と出ること**（「書いてない」とは別物）。
	// 分は繰り上げない（KIF の慣例）。
	for i, want := range []string{"0:00", "4:05", "125:00"} {
		if nodes[i].Time != want {
			t.Errorf("%d手目の時間 = %q, want %q", i+1, nodes[i].Time, want)
		}
	}
	if nodes[0].Comment != "角道を開ける\n二行目" {
		t.Errorf("1手目のコメント = %q", nodes[0].Comment)
	}
	if nodes[1].Comment != "" {
		t.Errorf("コメントの無い手に付いています: %q", nodes[1].Comment)
	}
}

// ⚠️ **時間の欄が無い棋譜では時間を出さないこと** —— 全手 0 秒として
// "0:00" が並ぶ（shogidb2 など、1 手ごとの時間が取れない取得元）。
func TestFromKIFWithoutTimes(t *testing.T) {
	study, _, err := position.FromKIF("手合割：平手\n   1 ７六歩(77)\n   2 ３四歩(33)\n")
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	for _, n := range study.Nodes() {
		if n.Time != "" {
			t.Errorf("時間の欄が無いのに時間が出ています: %+v", n)
		}
	}
}

// ⚠️ **盤で指した手には時間もコメントも付かないこと**（棋譜の手ではない）。
func TestPlayedMoveHasNoNotes(t *testing.T) {
	study, _, err := position.FromKIF(notedKIF)
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	if err := study.Play("8c8d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	nodes := study.Nodes()
	last := nodes[len(nodes)-1]
	if last.USI != "8c8d" || last.Time != "" || last.Comment != "" {
		t.Errorf("盤で指した手に注記が付いています: %+v", last)
	}
}

// 取り直し（`Graft` のあと `TakeNotes`）で、**新しく載った手にも注記が付くこと**。
// ⚠️ **棋譜の側を正にする**（書いてなければ消える）。
func TestTakeNotes(t *testing.T) {
	old, _, err := position.FromKIF("手合割：平手\n" +
		"   1 ７六歩(77)   ( 0:10/00:00:10)\n*古いコメント\n")
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	next, _, err := position.FromKIF(notedKIF)
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	old.Graft(next.MainLine())
	old.TakeNotes(next)

	if got := old.RootComment(); got != "開始局面のコメント" {
		t.Errorf("開始局面のコメント = %q", got)
	}
	nodes := old.Nodes()
	if len(nodes) != 3 {
		t.Fatalf("手順の数 = %d, want 3", len(nodes))
	}
	if nodes[0].Time != "0:00" || nodes[0].Comment != "角道を開ける\n二行目" {
		t.Errorf("1手目が棋譜の側になっていません: %+v", nodes[0])
	}
	if nodes[2].Time != "125:00" {
		t.Errorf("新しく載った手に時間が付いていません: %+v", nodes[2])
	}
	notes := old.MainNotes()
	if len(notes) != len(old.MainLine()) {
		t.Errorf("MainNotes と MainLine の長さが違います: %d / %d", len(notes), len(old.MainLine()))
	}
}

// ⚠️ **控えから開き直しても時間とコメントが残ること**（棋譜を読み直す口が無いので、
// 落とすと二度と戻らない）。0 秒の手が「書いてない」に化けないことも見る。
func TestStudySnapshotKeepsNotes(t *testing.T) {
	study, _, err := position.FromKIF(notedKIF)
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	snap, err := study.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back position.StudySnapshot
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	restored, err := position.RestoreStudy(back)
	if err != nil {
		t.Fatalf("RestoreStudy: %v", err)
	}
	if restored.RootComment() != study.RootComment() {
		t.Errorf("開始局面のコメントが落ちました: %q", restored.RootComment())
	}
	want, got := study.Nodes(), restored.Nodes()
	for i := range want {
		if got[i].Time != want[i].Time || got[i].Comment != want[i].Comment {
			t.Errorf("%d手目の注記が落ちました: got %+v, want %+v", i+1, got[i], want[i])
		}
	}
}

// 各手に**その側の、その手までの合計**が載ること（2026-11-25）。
//
// ⚠️ **先手と後手を別々に足すこと**（1 つにまとめると手順の合計になってしまう）。
// ⚠️ **枝の手は経路で足すこと** —— 枝の手そのものは時間を持たないので出ず、
// その先の棋譜の手も本譜の合計を壊さない。
func TestFromKIFTotals(t *testing.T) {
	study, _, err := position.FromKIF(notedKIF + "   4 ８四歩(83)   ( 1:00/00:05:05)\n")
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	nodes := study.Nodes()
	for i, want := range []string{"00:00:00", "00:04:05", "02:05:00", "00:05:05"} {
		if nodes[i].Total != want {
			t.Errorf("%d手目の合計 = %q, want %q", i+1, nodes[i].Total, want)
		}
	}
	// 2 手目まで戻って別の手を指す（時間を持たない枝の手）。
	if err := study.GoTo(nodes[1].ID); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if err := study.Play("6g6f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	found := false
	for _, n := range study.Nodes() {
		if n.USI == "6g6f" {
			found = true
			if n.Total != "" {
				t.Errorf("時間を持たない枝の手に合計が出ています: %+v", n)
			}
		}
		// 本譜の合計は枝を足しても変わらないこと。
		if n.ID == nodes[3].ID && n.Total != "00:05:05" {
			t.Errorf("枝を足したら本譜の 4 手目の合計が変わりました: %q", n.Total)
		}
	}
	if !found {
		t.Fatal("枝の手が手順にありません")
	}
}

// ⚠️ **時間の欄が無い棋譜では合計も出さないこと**（全手 0 秒の合計 "00:00:00" が並ぶ）。
func TestFromKIFWithoutTimesHasNoTotal(t *testing.T) {
	study, _, err := position.FromKIF("手合割：平手\n   1 ７六歩(77)\n   2 ３四歩(33)\n")
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	for _, n := range study.Nodes() {
		if n.Total != "" {
			t.Errorf("時間の欄が無いのに合計が出ています: %+v", n)
		}
	}
}
