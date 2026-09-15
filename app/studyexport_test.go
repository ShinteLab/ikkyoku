package app

import (
	"errors"
	"strings"
	"testing"
)

// 検討の本譜が KIF になること（2026-09-16。Step 3）。
func TestStudyExportKIF(t *testing.T) {
	s := adopted(t)
	for _, m := range []string{"7g7f", "3c3d", "2g2f"} {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%s): %v", m, err)
		}
	}
	got, err := s.exportKIF()
	if err != nil {
		t.Fatalf("exportKIF: %v", err)
	}
	// ⚠️ **移動元（"(77)"）まで出ること** —— 落ちると「同」の解釈が要る棋譜で
	// 読み手が別の駒を動かしうる。
	for _, want := range []string{"手合割：平手", "７六歩(77)", "３四歩(33)", "２六歩(27)"} {
		if !strings.Contains(got, want) {
			t.Errorf("KIF に %q がありません:\n%s", want, got)
		}
	}
	// ⚠️ **消費時間は出さないこと** —— どこからも時間を受け取っていないので、
	// 出すと "( 0:00/00:00:00)" が全手に並ぶ。
	if strings.Contains(got, "0:00/") {
		t.Errorf("消費時間が出ています:\n%s", got)
	}
}

// ⚠️ **枝は KIF に出ないこと**（`core/kifu` がまだ木を持っていない）。
// **枝が消えるわけではない** —— 木は控えにそのまま残る。
func TestStudyExportKIFMainLineOnly(t *testing.T) {
	s := adopted(t)
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if _, err := s.Play("3c3d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	// 1 手目に戻って別の手（枝）。
	if _, err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("8c8d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	got, err := s.exportKIF()
	if err != nil {
		t.Fatalf("exportKIF: %v", err)
	}
	if !strings.Contains(got, "３四歩") {
		t.Errorf("本譜が出ていません:\n%s", got)
	}
	if strings.Contains(got, "８四歩") {
		t.Errorf("枝まで KIF に出ています:\n%s", got)
	}
	// 控えには枝が残っていること（KIF に出ないことと、失うことは別）。
	rec, _ := s.sessionRecord()
	found := false
	for _, n := range rec.Study.Nodes {
		if n.USI == "8c8d" {
			found = true
		}
	}
	if !found {
		t.Error("控えから枝が消えています")
	}
}

// ⚠️ **根が初期局面でないなら断ること**（2026-09-16）。
//
// **KIF は「手合割 ＋ 初手からの指し手」でしか局面を表せない。** 撮った中盤の
// 局面をそのまま書き出すと、**平手の初形に手順だけが乗った別の対局**になる。
// **黙って平手として出すのが一番たちが悪い**（嘘の棋譜が棚に入る）。
func TestStudyExportKIFRefusesMidGameRoot(t *testing.T) {
	s := adoptedFrom(t, "lnsgkgsnl/1r5b1/ppppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL")
	_, err := s.exportKIF()
	if err == nil {
		t.Fatal("途中の局面から始まった検討を棋譜にしています")
	}
	if !errors.Is(err, errStudyNotFromStart) {
		t.Errorf("断る理由が違います: %v", err)
	}
	// ⚠️ **失うものが無いことを言っていること**（控えには残る）。
	if !strings.Contains(err.Error(), "控えて") {
		t.Errorf("控えが残ることに触れていません: %v", err)
	}
}

// 手合割の対局も、その手合割の初期局面として書き出せること。
func TestStudyExportKIFHandicap(t *testing.T) {
	s := adopted(t)
	if _, err := s.NewGame("二枚落ち"); err != nil {
		t.Fatalf("NewGame: %v", err)
	}
	got, err := s.exportKIF()
	if err != nil {
		t.Fatalf("exportKIF: %v", err)
	}
	if !strings.Contains(got, "手合割：二枚落ち") {
		t.Errorf("手合割が出ていません:\n%s", got)
	}
}

// 棚に結んだら `StudyState` に出ること（**「棚に登録する」を出すかの鍵**）。
func TestStudySetGameID(t *testing.T) {
	s := adopted(t)
	if st := s.State(); st.GameID != "" {
		t.Fatalf("結んでいないのに id があります: %q", st.GameID)
	}
	st := s.setGameID("game-7")
	if st.GameID != "game-7" {
		t.Errorf("GameID = %q, want game-7", st.GameID)
	}
	// ⚠️ **根を入れ替えたら捨てること。**
	if _, err := s.NewGame(""); err != nil {
		t.Fatalf("NewGame: %v", err)
	}
	if got := s.State().GameID; got != "" {
		t.Errorf("別の対局に前の id が残っています: %q", got)
	}
}
