package app

import (
	"strings"
	"testing"
)

// 自動更新で**増えた手にも消費時間とコメントが付くこと**（2026-10-04）。
// `Graft` は手しか受け取らないので、写し忘れると増えた手だけ時間が出ない。
func TestStudyServiceFollowKifuKeepsNotes(t *testing.T) {
	body := kifuHead + "   1 ７六歩(77)   ( 0:10/00:00:10)\n"
	s := followService(t, &body)

	body = kifuHead + "   1 ７六歩(77)   ( 0:10/00:00:10)\n" +
		"   2 ３四歩(33)   ( 3:00/00:03:00)\n*ここで長考\n"
	load, err := s.FollowKifu(true)
	if err != nil {
		t.Fatalf("FollowKifu: %v", err)
	}
	nodes := load.State.Nodes
	if len(nodes) != 2 {
		t.Fatalf("手順の数 = %d, want 2", len(nodes))
	}
	if nodes[1].Time != "3:00" || nodes[1].Comment != "ここで長考" {
		t.Errorf("増えた手に注記が付いていません: %+v", nodes[1])
	}
}

// 棚へ書き出す KIF に**棋譜から読んだ時間とコメントが戻ること**（落とすと、
// 棚から開き直したときに手順リストから消える）。
func TestStudyExportKIFKeepsNotes(t *testing.T) {
	s := NewStudyService(NewPositionService())
	if _, err := s.LoadKifu("手合割：平手\n*はじめに\n" +
		"   1 ７六歩(77)   ( 0:16/00:00:16)\n*角道を開ける\n" +
		"   2 ３四歩(33)   ( 0:04/00:00:04)\n"); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	got, err := s.exportKIF()
	if err != nil {
		t.Fatalf("exportKIF: %v", err)
	}
	for _, want := range []string{"*はじめに", "( 0:16/00:00:16)", "*角道を開ける", "( 0:04/00:00:04)"} {
		if !strings.Contains(got, want) {
			t.Errorf("KIF に %q がありません:\n%s", want, got)
		}
	}
}
