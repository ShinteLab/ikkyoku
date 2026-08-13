package position_test

import (
	"strings"
	"testing"

	"github.com/ShinteLab/core/sfen"
	"github.com/ShinteLab/ikkyoku/position"
)

const sampleKIF = "開始日時：2026/08/12 09:00:00\n" +
	"棋戦：テスト戦\n" +
	"手合割：平手\n" +
	"先手：先手太郎\n" +
	"後手：後手花子\n" +
	"手数----指手---------消費時間--\n" +
	"   1 ７六歩(77)   ( 0:16/00:00:16)\n" +
	"   2 ３四歩(33)   ( 0:04/00:00:04)\n" +
	"   3 ２二角成(88) ( 0:10/00:00:26)\n" +
	"   4 同　銀(31)   ( 0:02/00:00:06)\n" +
	"   5 投了\n"

// 貼り付けた KIF が「指し手が全て反映された状態」になること。
func TestFromKIF(t *testing.T) {
	study, load, err := position.FromKIF(sampleKIF)
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	if load.Note != "" {
		t.Errorf("止まった理由が出ている: %s", load.Note)
	}
	if load.Total != 4 || load.Loaded != 4 {
		t.Errorf("手数が合わない: total=%d loaded=%d", load.Total, load.Loaded)
	}
	// 投了は指し手ではないので手順に載らない。
	if got := len(study.Nodes()); got != 4 {
		t.Errorf("手順の数 = %d, want 4", got)
	}
	// **最終手まで進んだ状態**で返ること（戻って見ている状態にしない）。
	if study.Ply() != 4 {
		t.Errorf("Ply = %d, want 4", study.Ply())
	}
	if load.Game.Black != "先手太郎" || load.Game.White != "後手花子" {
		t.Errorf("対局者が入らない: %+v", load.Game)
	}

	cur := study.Current()
	if cur.Turn != position.TurnBlack {
		t.Errorf("4手目まで指したら先手番のはず: %v", cur.Turn)
	}
	// 角交換のあと、先手の駒台に角が 1 枚（取った駒が載ること）。
	black, _ := cur.Hands()
	if black[sfen.Bishop] == 0 {
		t.Errorf("取った角が駒台に載っていない: %v", black)
	}
	// 手順の日本語表記が付くこと（"同" も出る）。
	moves := study.Nodes()
	if moves[0].Text != "▲７六歩" || moves[3].Text != "△同　銀" {
		t.Errorf("表記が付かない: %q / %q", moves[0].Text, moves[3].Text)
	}
	if _, err := cur.SFEN(); err != nil {
		t.Errorf("解析できる局面になっていない: %v", err)
	}
}

// ⚠️ 途中で指せない手が出ても、そこまでの手順は残す（設計原則3）。
func TestFromKIFStopsAtIllegalMove(t *testing.T) {
	src := "手数----指手---------消費時間--\n" +
		"   1 ７六歩(77)\n" +
		"   2 ３四歩(33)\n" +
		"   3 ９九飛(28)\n" // 通れない
	study, load, err := position.FromKIF(src)
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	if load.Loaded != 2 {
		t.Errorf("Loaded = %d, want 2", load.Loaded)
	}
	if !strings.Contains(load.Note, "3手目") {
		t.Errorf("どこで止まったかが出ない: %q", load.Note)
	}
	if study.Ply() != 2 {
		t.Errorf("Ply = %d, want 2", study.Ply())
	}
}

// 指し手が 1 手も無ければエラー（貼り間違いを黙って受けない）。
func TestFromKIFWithoutMoves(t *testing.T) {
	if _, _, err := position.FromKIF("先手：太郎\n後手：花子\n"); err == nil {
		t.Fatal("指し手の無い KIF でエラーにならなかった")
	}
}

// 駒落ちは上手（後手）が初手。**平手として読まないこと。**
func TestFromKIFHandicap(t *testing.T) {
	src := "手合割：二枚落ち\n手数----指手---------消費時間--\n   1 ３四歩(33)\n"
	study, load, err := position.FromKIF(src)
	if err != nil {
		t.Fatalf("FromKIF: %v", err)
	}
	if load.Loaded != 1 {
		t.Fatalf("Loaded = %d (%s)", load.Loaded, load.Note)
	}
	if !strings.HasPrefix(load.RootSFEN, "lnsgkgsnl/9/") {
		t.Errorf("二枚落ちの初期局面になっていない: %q", load.RootSFEN)
	}
	if study.Current().Turn != position.TurnBlack {
		t.Errorf("上手が指したあとは下手番のはず: %v", study.Current().Turn)
	}
}

// 未対応の手合割は平手に倒さずエラー（倒すと以降が全部でたらめになる）。
func TestFromKIFUnknownHandicap(t *testing.T) {
	src := "手合割：歩三兵\n手数----指手---------消費時間--\n   1 ７六歩(77)\n"
	if _, _, err := position.FromKIF(src); err == nil {
		t.Fatal("未対応の手合割でエラーにならなかった")
	}
}

// FromFullSFEN は完全形の SFEN を「確定した局面」として読む。
func TestFromFullSFEN(t *testing.T) {
	p, err := position.FromFullSFEN("lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL w S2Pb 42")
	if err != nil {
		t.Fatalf("FromSFEN: %v", err)
	}
	if p.Turn != position.TurnWhite {
		t.Errorf("手番 = %v, want 後手", p.Turn)
	}
	if p.MoveNumber != 42 {
		t.Errorf("手数 = %d, want 42", p.MoveNumber)
	}
	black, white := p.Hands()
	if black[sfen.Pawn] != 2 || black[sfen.Silver] != 1 {
		t.Errorf("先手の駒台 = %v", black)
	}
	if white[sfen.Bishop] != 1 {
		t.Errorf("後手の駒台 = %v", white)
	}
	// **未決が残らない**（残ると SFEN が組み上がらず解析できない）。
	if len(p.Unassigned()) != 0 {
		t.Errorf("未決が残っている: %v", p.Unassigned())
	}
}
