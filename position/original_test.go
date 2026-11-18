package position_test

import (
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

func mustOriginal(t *testing.T, text string) *position.Original {
	t.Helper()
	o, err := position.ReadOriginal(text)
	if err != nil {
		t.Fatalf("ReadOriginal: %v", err)
	}
	return o
}

func mainLine(s *position.Study) string { return strings.Join(s.MainLine(), " ") }

// **止まった手を置き換えると、残りの原本を直した局面から読み直す**（2026-11-19。直しの表）。
// ⚠️ 原本の手は止まった手の先まで持っていること（捨てると続きが読めない）。
func TestBuildReplaceAtStop(t *testing.T) {
	o := mustOriginal(t, "▲７六歩 △３四歩 ▲５五金 △８四歩 ▲２六歩\n")
	_, _, b, err := o.Build(nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if b.Stop == nil || b.Stop.Move != 3 || b.Stop.Ply != 2 {
		t.Fatalf("止まった手が違う: %+v", b.Stop)
	}
	if got := strings.Join(b.Stop.Rest, " "); got != "△８四歩 ▲２六歩" {
		t.Errorf("残りの原本 = %q", got)
	}
	fix := position.KifuFix{Move: 3, Text: "▲５五金", Kind: position.FixReplace, USI: "7i6h"}
	study, load, b, err := o.Build([]position.KifuFix{fix})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if b.Stop != nil || load.Note != "" {
		t.Fatalf("直したのに止まった: %+v %q", b.Stop, load.Note)
	}
	if got := mainLine(study); got != "7g7f 3c3d 7i6h 8c8d 2g2f" {
		t.Errorf("本譜 = %s", got)
	}
	if b.Plies[2].Fix == nil || b.Plies[2].Orig != 3 || b.Plies[3].Orig != 4 {
		t.Errorf("どこから来た手かが違う: %+v", b.Plies)
	}
}

// **抜けていた手を足すと、原本のその手が後ろへ回る**。「同」は足した手から数える。
func TestBuildInsertMissingMove(t *testing.T) {
	o := mustOriginal(t, "▲７六歩 ▲２二角成 △同　銀\n") // △３四歩が抜けている
	_, _, b, _ := o.Build(nil)
	if b.Stop == nil || b.Stop.Move != 2 {
		t.Fatalf("2 手目で止まるはず: %+v", b.Stop)
	}
	fix := position.KifuFix{Move: 2, Text: "▲２二角成", Kind: position.FixInsert, USI: "3c3d"}
	study, _, b, _ := o.Build([]position.KifuFix{fix})
	if b.Stop != nil {
		t.Fatalf("足したのに止まった: %+v", b.Stop)
	}
	if got := mainLine(study); got != "7g7f 3c3d 8h2b+ 3a2b" {
		t.Errorf("本譜 = %s", got)
	}
	if b.Plies[1].Orig != 0 || b.Plies[2].Orig != 2 {
		t.Errorf("足した手は原本に無い（Orig 0）: %+v", b.Plies)
	}
}

// **二重に書いた手は飛ばせる**。飛ばした直しは次の手に付く（取り消す口）。
func TestBuildSkipDoubledMove(t *testing.T) {
	o := mustOriginal(t, "▲７六歩 ▲７六歩 △３四歩\n")
	fix := position.KifuFix{Move: 2, Text: "▲７六歩", Kind: position.FixSkip}
	study, _, b, _ := o.Build([]position.KifuFix{fix})
	if b.Stop != nil {
		t.Fatalf("飛ばしたのに止まった: %+v", b.Stop)
	}
	if got := mainLine(study); got != "7g7f 3c3d" {
		t.Errorf("本譜 = %s", got)
	}
	if len(b.Plies[1].Skipped) != 1 || b.Plies[1].Orig != 3 {
		t.Errorf("飛ばした直しが次の手に付かない: %+v", b.Plies)
	}
}

// **盤に当たる誤記を置き換えたあとの「同」は、直した手の移動先を指す**（KI2 の変換器を直した手で進める）。
func TestBuildReplaceLegalMisnote(t *testing.T) {
	o := mustOriginal(t, "▲７六歩 △３四歩 ▲２二角 △同　銀\n") // 実際は ▲２二角成
	fix := position.KifuFix{Move: 3, Text: "▲２二角", Kind: position.FixReplace, USI: "8h2b+", OrigUSI: "8h2b"}
	study, _, b, _ := o.Build([]position.KifuFix{fix})
	if b.Stop != nil {
		t.Fatalf("止まった: %+v", b.Stop)
	}
	if got := mainLine(study); got != "7g7f 3c3d 8h2b+ 3a2b" {
		t.Errorf("本譜 = %s", got)
	}
}

// ⚠️ **原本の表記が変わった直しは当てずに外す**（サイトが直した）。黙って外さない（Dropped で返す）。
func TestBuildDropsFixWhenOriginalChanged(t *testing.T) {
	o := mustOriginal(t, "▲７六歩 △３四歩 ▲６八銀\n")
	fix := position.KifuFix{Move: 3, Text: "▲５五金", Kind: position.FixReplace, USI: "5i4h"}
	study, _, b, _ := o.Build([]position.KifuFix{fix})
	if len(b.Dropped) != 1 {
		t.Fatalf("外した直しが返らない: %+v", b.Dropped)
	}
	if got := mainLine(study); got != "7g7f 3c3d 7i6h" {
		t.Errorf("本譜 = %s（原本どおりのはず）", got)
	}
}

// **盤には当たるが反則の手（KIF の移動元を信じた手）は Illegal で止まる**（ikkyoku が指摘にする）。
func TestBuildStopsAtIllegalKIFMove(t *testing.T) {
	o := mustOriginal(t, "手数----指手---------消費時間--\n"+
		"   1 ７六歩(77)\n   2 ３四歩(33)\n   3 ９九飛(28)\n   4 ８四歩(83)\n")
	_, _, b, _ := o.Build(nil)
	if b.Stop == nil || !b.Stop.Illegal || b.Stop.Move != 3 {
		t.Fatalf("反則手で止まらない: %+v", b.Stop)
	}
	if b.Stop.Text != "▲９九飛(28)" || strings.Join(b.Stop.Rest, " ") != "△８四歩(83)" {
		t.Errorf("原本の表記が違う: %q / %q", b.Stop.Text, b.Stop.Rest)
	}
}

// 原本の終局は、直したあと最後まで読めたときに付く（投了の行が出ないと訂正版を登録できない）。
func TestBuildEndAfterFix(t *testing.T) {
	o := mustOriginal(t, "▲７六歩 △３四歩 ▲５五金\nまで3手で先手の勝ち\n")
	study, _, _, _ := o.Build(nil)
	if study.RecordEndMark() != "" {
		t.Errorf("止まったのに終局が付いた")
	}
	fix := position.KifuFix{Move: 3, Text: "▲５五金", Kind: position.FixReplace, USI: "7i6h"}
	study, _, _, _ = o.Build([]position.KifuFix{fix})
	if study.RecordEndMark() != "投了" {
		t.Errorf("終局 = %q, want 投了", study.RecordEndMark())
	}
}
