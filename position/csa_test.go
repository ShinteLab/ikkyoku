package position_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ShinteLab/ikkyoku/position"
)

// sampleCSA は sampleKIF（kifu_test.go）と同じ対局を CSA で書いたもの。
const sampleCSA = "V2.2\n" +
	"N+先手太郎\n" +
	"N-後手花子\n" +
	"$EVENT:テスト戦\n" +
	"$START_TIME:2026/08/12 09:00:00\n" +
	"PI\n" +
	"+\n" +
	"+7776FU,T16\n" +
	"'*角道を開ける\n" +
	"-3334FU,T4\n" +
	"+8822UM,T10\n" +
	"-3122GI,T2\n" +
	"%TORYO\n"

// **CSA も KIF と同じ検討になる**（2026-11-15）。貼り付けの入口（`FromKifu`）が形式を見分ける。
func TestFromKifuCSA(t *testing.T) {
	csa, load, err := position.FromKifu(sampleCSA)
	if err != nil {
		t.Fatalf("FromKifu: %v", err)
	}
	if load.Note != "" {
		t.Errorf("止まった理由が出ている: %s", load.Note)
	}
	if load.Total != 4 || load.Loaded != 4 {
		t.Errorf("手数が合わない: total=%d loaded=%d", load.Total, load.Loaded)
	}
	if load.Game.Black != "先手太郎" || load.Game.White != "後手花子" || load.Game.Event != "テスト戦" {
		t.Errorf("対局の素性が入らない: %+v", load.Game)
	}
	kif, _, err := position.FromKIF(sampleKIF)
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	// 同じ対局の KIF と同じ手順・同じ表記になること。
	if got, want := strings.Join(csa.MainLine(), " "), strings.Join(kif.MainLine(), " "); got != want {
		t.Errorf("手順 = %s, want %s", got, want)
	}
	nodes := csa.Nodes()
	if len(nodes) != 4 || nodes[0].Text != "▲７六歩" || nodes[3].Text != "△同　銀" {
		t.Errorf("表記が合わない: %+v", nodes)
	}
	// 終局・消費時間・コメントも KIF と同じ扱いで載る。
	if csa.RecordEndMark() != "投了" {
		t.Errorf("終局 = %q, want 投了", csa.RecordEndMark())
	}
	notes := csa.MainNotes()
	if len(notes) != 4 || notes[0].Spend != 16*time.Second || !notes[0].Timed || notes[0].Comment != "角道を開ける" {
		t.Errorf("消費時間とコメントが載らない: %+v", notes)
	}
}

// **CSA の指し手の途中で読めなくなっても、そこまでを載せて理由を出す**（設計原則3）。
func TestFromKifuCSAStopsMidway(t *testing.T) {
	study, load, err := position.FromKifu("PI\n+\n+7776FU\n+3334FU\n")
	if err != nil {
		t.Fatalf("FromKifu: %v", err)
	}
	if load.Loaded != 1 || study.Ply() != 1 {
		t.Errorf("載った手数 = %d (ply %d), want 1", load.Loaded, study.Ply())
	}
	if !strings.Contains(load.Note, "2手目") {
		t.Errorf("止まった理由 = %q", load.Note)
	}
}

// **CSA の開始局面が読めなければエラー**（平手に倒さない）。
func TestFromKifuCSABrokenStart(t *testing.T) {
	if _, _, err := position.FromKifu("N+a\n+7776FU\n"); err == nil {
		t.Error("開始局面の無い CSA が通った")
	}
}

// **KIF は今までどおり FromKIF で読む**（見分けを入れても KIF の経路は変わらない）。
func TestFromKifuKIF(t *testing.T) {
	_, load, err := position.FromKifu(sampleKIF)
	if err != nil {
		t.Fatalf("FromKifu: %v", err)
	}
	if load.Loaded != 4 || load.Game.Black != "先手太郎" {
		t.Errorf("KIF が読めない: %+v", load)
	}
}
