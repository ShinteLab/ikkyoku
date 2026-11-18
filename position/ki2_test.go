package position_test

import (
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// sampleKI2 は sampleKIF（kifu_test.go）と同じ対局を KI2 で書いたもの（ふわりと将棋の原本の形）。
const sampleKI2 = "開始日時：2026/08/12 09:00:00\n" +
	"棋戦：テスト戦\n" +
	"手合割：平手\n" +
	"先手：先手太郎\n" +
	"後手：後手花子\n" +
	"▲７六歩    △３四歩\n" +
	"*角道を開け合う\n" +
	"▲２二角成  △同　銀\n" +
	"まで4手で後手の勝ち\n"

// **KI2 も KIF と同じ検討になる**（2026-11-18）。kicho v0.5.0 からふわりと将棋の棋譜は KI2 の原本で届くので、
// 見分けを落とすと KIF として読まれて 0 手になる。
func TestFromKifuKI2(t *testing.T) {
	ki2, load, err := position.FromKifu(sampleKI2)
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
	if got, want := strings.Join(ki2.MainLine(), " "), strings.Join(kif.MainLine(), " "); got != want {
		t.Errorf("手順 = %s, want %s", got, want)
	}
	nodes := ki2.Nodes()
	if len(nodes) != 4 || nodes[0].Text != "▲７六歩" || nodes[3].Text != "△同　銀" {
		t.Errorf("表記が合わない: %+v", nodes)
	}
	// 「まで…の勝ち」は投了として載る。
	if ki2.RecordEndMark() != "投了" {
		t.Errorf("終局 = %q, want 投了", ki2.RecordEndMark())
	}
	// コメントは直前の手に付く。⚠️ KI2 に消費時間の欄は無いので Timed を立てない（"0:00" が並ぶ）。
	notes := ki2.MainNotes()
	if len(notes) != 4 || notes[1].Comment != "角道を開け合う" || notes[0].Timed {
		t.Errorf("コメントと消費時間の扱いが違う: %+v", notes)
	}
}

// **盤に当てられない手（手入力の誤記）があっても、そこまでを載せて理由を出す**（設計原則3）。
// ⚠️ Total は原本に書いてあった手数・終局は付けない（読めなかっただけで、対局の終わりではない）。
func TestFromKifuKI2StopsAtMiswrittenMove(t *testing.T) {
	src := "先手：太郎\n後手：花子\n" +
		"▲７六歩    △３四歩\n" +
		"▲５五金    △８四歩\n" + // 先手の金は５五へ行けない
		"まで4手で後手の勝ち\n"
	study, load, err := position.FromKifu(src)
	if err != nil {
		t.Fatalf("FromKifu: %v", err)
	}
	if load.Loaded != 2 || study.Ply() != 2 {
		t.Errorf("載った手数 = %d (ply %d), want 2", load.Loaded, study.Ply())
	}
	if load.Total != 4 {
		t.Errorf("Total = %d, want 4（原本の手数）", load.Total)
	}
	if !strings.Contains(load.Note, "3手目") || !strings.Contains(load.Note, "５五金") {
		t.Errorf("どこで止まったかが出ない: %q", load.Note)
	}
	if study.RecordEndMark() != "" {
		t.Errorf("途中で止まったのに終局が付いた: %q", study.RecordEndMark())
	}
}

// ⚠️ **ヘッダの無い KI2 が 1 手目から当てられなくても断らない**（開始局面は解析できる）。
// 「何か読めたか」を当てた後の Document で見ると、手が 0 になって断ってしまう。
func TestFromKifuKI2FirstMoveMiswritten(t *testing.T) {
	_, load, err := position.FromKifu("▲５五金\n△３四歩\n")
	if err != nil {
		t.Fatalf("FromKifu: %v", err)
	}
	if load.Loaded != 0 || load.Total != 2 || !strings.Contains(load.Note, "1手目") {
		t.Errorf("内訳が違う: %+v", load)
	}
}

// 駒落ちの KI2 は上手（後手）から指す。**平手として読まないこと**（KIF と同じ）。
func TestFromKifuKI2Handicap(t *testing.T) {
	study, load, err := position.FromKifu("手合割：二枚落ち\n△６二銀    ▲７六歩\n")
	if err != nil {
		t.Fatalf("FromKifu: %v", err)
	}
	if load.Loaded != 2 {
		t.Fatalf("Loaded = %d (%s)", load.Loaded, load.Note)
	}
	if !strings.HasPrefix(load.RootSFEN, "lnsgkgsnl/9/") {
		t.Errorf("二枚落ちの初期局面になっていない: %q", load.RootSFEN)
	}
	if study.Current().Turn != position.TurnWhite {
		t.Errorf("2 手指したあとは上手番のはず: %v", study.Current().Turn)
	}
}

// **未対応の手合割は平手に倒さずエラー**（KIF と同じ。倒すと以降が全部でたらめになる）。
func TestFromKifuKI2UnknownHandicap(t *testing.T) {
	if _, _, err := position.FromKifu("手合割：歩三兵\n▲７六歩\n"); err == nil {
		t.Fatal("未対応の手合割でエラーにならなかった")
	}
}
