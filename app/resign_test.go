package app

import (
	"strings"
	"testing"
)

// **投了すると棋譜タブに登録できるようになる**（2026-10-09）。
//
// ⚠️ **一番の歯止めは「終局していなければ登録できないこと」** —— 対局の途中で
// 誤って登録する事故を防ぐのがこの機能の目的（以前は「再読み込み」の隣にボタンがあった）。
func TestStudyResignEnablesRegister(t *testing.T) {
	s := adopted(t)
	for _, m := range []string{"7g7f", "3c3d", "2g2f"} {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%s): %v", m, err)
		}
	}
	st := s.State()
	if !st.CanResign || st.CanRegister || st.CanUnresign {
		t.Fatalf("投了前: resign=%v register=%v unresign=%v", st.CanResign, st.CanRegister, st.CanUnresign)
	}
	if err := s.registerable(); err == nil {
		t.Fatal("終局していないのに登録できる")
	}

	st, err := s.Resign()
	if err != nil {
		t.Fatalf("Resign: %v", err)
	}
	if st.CanResign || !st.CanRegister || !st.CanUnresign {
		t.Fatalf("投了後: resign=%v register=%v unresign=%v", st.CanResign, st.CanRegister, st.CanUnresign)
	}
	if err := s.registerable(); err != nil {
		t.Fatalf("投了したのに登録できない: %v", err)
	}
	// ⚠️ **登録する KIF に投了の行が入ること**（落とすと「未」の棋譜として入る）。
	got, err := s.exportKIF()
	if err != nil {
		t.Fatalf("exportKIF: %v", err)
	}
	if !strings.Contains(got, "4 投了") {
		t.Errorf("KIF に投了の行がありません:\n%s", got)
	}

	// 登録したあとは取り消せない・もう一度は登録できない。
	s.setGameID("g1")
	st = s.State()
	if st.CanUnresign || st.CanRegister {
		t.Errorf("登録後: register=%v unresign=%v", st.CanRegister, st.CanUnresign)
	}
	if _, err := s.Unresign(); err == nil {
		t.Error("登録したのに投了を取り消せる")
	}
}

// 取り消したら元どおり（登録できない・もう一度投了できる）。
func TestStudyUnresign(t *testing.T) {
	s := adopted(t)
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if _, err := s.Resign(); err != nil {
		t.Fatalf("Resign: %v", err)
	}
	st, err := s.Unresign()
	if err != nil {
		t.Fatalf("Unresign: %v", err)
	}
	if !st.CanResign || st.CanRegister || st.CanUnresign {
		t.Errorf("取り消し後: resign=%v register=%v unresign=%v", st.CanResign, st.CanRegister, st.CanUnresign)
	}
	got, err := s.exportKIF()
	if err != nil {
		t.Fatalf("exportKIF: %v", err)
	}
	if strings.Contains(got, "投了") {
		t.Errorf("取り消したのに KIF に投了が残る:\n%s", got)
	}
}

// ⚠️ **URL から読んだ棋譜では投了も登録もさせない**（終局は取得元が決める。
// 棋譜タブへは中継カードの「保存」で原本のまま入れる）。
func TestStudyResignRefusedForURL(t *testing.T) {
	body := kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n"
	srv := kifuServer(t, &body)
	s := NewStudyService(NewPositionService())
	if _, err := s.LoadKifuURL(srv.URL + "/live.kif"); err != nil {
		t.Fatalf("LoadKifuURL: %v", err)
	}
	if st := s.State(); st.CanResign {
		t.Error("URL の棋譜で投了が押せる")
	}
	if _, err := s.Resign(); err == nil {
		t.Error("URL の棋譜で投了できてしまう")
	}

	// 終局まで載っても登録の口は出さない。
	body = kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n   3 投了\n"
	if _, err := s.ReloadKifu(); err != nil {
		t.Fatalf("ReloadKifu: %v", err)
	}
	if st := s.State(); st.CanRegister || st.CanUnresign {
		t.Errorf("URL の棋譜で登録・取り消しが出る: register=%v unresign=%v", st.CanRegister, st.CanUnresign)
	}
}

// 貼り付けた終局済みの棋譜は登録できるが、棋譜の終局は取り消せない。
func TestStudyRecordedEndCannotUnresign(t *testing.T) {
	s := adopted(t)
	if _, err := s.LoadKifu(finishedKIF); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	st := s.State()
	if st.CanUnresign || st.CanResign || !st.CanRegister {
		t.Errorf("棋譜の終局: resign=%v register=%v unresign=%v", st.CanResign, st.CanRegister, st.CanUnresign)
	}
	got, err := s.exportKIF()
	if err != nil {
		t.Fatalf("exportKIF: %v", err)
	}
	if !strings.Contains(got, "3 投了") {
		t.Errorf("KIF に投了の行がありません:\n%s", got)
	}
}
