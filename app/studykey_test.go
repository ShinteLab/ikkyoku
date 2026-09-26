package app

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

const (
	keyKifA = "手合割：平手\n手数----指手---------消費時間--\n   1 ７六歩(77)\n   2 ３四歩(33)\n"
	keyKifB = "手合割：平手\n手数----指手---------消費時間--\n   1 ２六歩(27)\n"
)

// storedStudy は控えを自動保存する解析タブを返す（**Start まで済ませる**）。
func storedStudy(t *testing.T, dir string) (*StudyService, *StudyStore) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewStudyService(logger, NewPositionService(logger))
	store := NewStudyStore(logger, dir, s)
	store.Start()
	t.Cleanup(func() { _ = store.Close() })
	return s, store
}

// hasMove は今の木にその手があるか。
func hasMove(s *StudyService, usi string) bool {
	for _, n := range s.State().Nodes {
		if n.USI == usi {
			return true
		}
	}
	return false
}

// 貼り付けの鍵は**改行コードと前後の空白だけ**揃えること。
func TestPasteKeyOf(t *testing.T) {
	a := pasteKeyOf(keyKifA)
	if a == "" || !strings.HasPrefix(a, "kif:") {
		t.Fatalf("鍵が作れません: %q", a)
	}
	if b := pasteKeyOf("  " + strings.ReplaceAll(keyKifA, "\n", "\r\n") + "\n"); b != a {
		t.Error("改行コードが違うだけで別の鍵になります（貼り付け元で CRLF/LF は入れ替わる）")
	}
	if pasteKeyOf(keyKifB) == a {
		t.Error("別の棋譜が同じ鍵になります")
	}
	if pasteKeyOf("  \n") != "" {
		t.Error("空の本文に鍵が付いています")
	}
	if sourceKeyOf("jsa", "") != "" || sourceKeyOf("", "x") != "" {
		t.Error("取得元が欠けているのに鍵が付いています")
	}
}

// 同じ棋譜を貼り直して「解析する」と、**前の検討の続きから開く**こと（2026-09-26）。
//
// ⚠️ **ここが崩れると、別の棋譜を挟むたびに解析がゼロからになる**
// （中継は数時間かけて完成し、そのあいだに別の棋譜を解析するのが普通）。
func TestStudyServiceLoadKifuResumesPastedStudy(t *testing.T) {
	s, _ := storedStudy(t, t.TempDir())
	if _, err := s.LoadKifu(keyKifA); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	// 検討を積む（枝と評価値）。
	record(t, s, "e1", score(20))
	if _, err := s.GoTo(s.State().Line[1]); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("8c8d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "e1", score(-50))

	// 別の棋譜を挟む。
	if _, err := s.LoadKifu(keyKifB); err != nil {
		t.Fatalf("LoadKifu(B): %v", err)
	}
	if hasMove(s, "8c8d") {
		t.Fatal("別の棋譜に前の枝が残っています")
	}

	// 同じ本文を（改行コードを変えて）貼り直す。
	load, err := s.LoadKifu(strings.ReplaceAll(keyKifA, "\n", "\r\n"))
	if err != nil {
		t.Fatalf("LoadKifu(A again): %v", err)
	}
	if !hasMove(s, "8c8d") {
		t.Error("前の検討の枝が戻っていません")
	}
	if g := s.Evals(); len(g.Series) != 1 {
		t.Errorf("評価値が戻っていません: %+v", g.Series)
	}
	if !strings.Contains(load.Summary, "続き") {
		t.Errorf("続きから開いたことが出ていません: %q", load.Summary)
	}
}

// 中継カードの「解析する」も**取得元が同じなら続きから開く**こと。
// 棋譜タブの行から来たなら、**そこで棋譜 id を結ぶ**こと。
func TestKifuServiceSendToStudyGameResumes(t *testing.T) {
	dir := t.TempDir()
	s, store := storedStudy(t, dir)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewKifuService(logger, s)
	svc.Store = store

	card := GameDetail{GameSummary: GameSummary{Source: "jsa", SourceID: "live-1"}, KIF: keyKifA}
	if _, err := svc.SendToStudyGame(card); err != nil {
		t.Fatalf("SendToStudyGame: %v", err)
	}
	if _, err := s.Play("2g2f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "e1", score(30))

	// 別の棋譜を挟み、中継が 1 手進んでから同じカードを開き直す。
	if _, err := s.LoadKifu(keyKifB); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	card.KIF = keyKifA + "   3 ２六歩(27)\n"
	if _, err := svc.SendToStudyGame(card); err != nil {
		t.Fatalf("SendToStudyGame(again): %v", err)
	}
	if !hasMove(s, "2g2f") {
		t.Error("前の検討が戻っていません")
	}
	if len(s.State().Line) == 0 {
		t.Fatal("局面がありません")
	}
	// **中継の伸びたぶんは据え直しで入る。**
	if got := len(s.State().Nodes); got < 4 {
		t.Errorf("伸びた手が載っていません: %d 節点", got)
	}
	if s.State().GameID != "" {
		t.Errorf("棋譜タブに入れていないのに id が付いています: %q", s.State().GameID)
	}

	// 棋譜タブの行（id 付き）から開き直すと、そこで結ばれる。
	card.ID = "game-7"
	if _, err := svc.SendToStudyGame(card); err != nil {
		t.Fatalf("SendToStudyGame(row): %v", err)
	}
	if s.State().GameID != "game-7" {
		t.Errorf("棋譜タブの行から開いたのに結ばれていません: %q", s.State().GameID)
	}
}

// 棋譜タブに入れたら、**同じ出どころの検討を結ぶ**こと（今の検討が相手のとき）。
func TestStudyStoreLinkGameCurrent(t *testing.T) {
	dir := t.TempDir()
	s, store := storedStudy(t, dir)
	if _, err := s.LoadKifu(keyKifA); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	record(t, s, "e1", score(10))
	store.LinkGame(pasteKeyOf(keyKifA), "game-1")
	if s.State().GameID != "game-1" {
		t.Fatalf("今の検討に結ばれていません: %q", s.State().GameID)
	}
	// 別のアプリから棋譜タブの「解析する」で開ける（**すぐ書かれていること**）。
	back := empty(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, ok := NewStudyStore(logger, dir, back).RestoreGame("game-1"); !ok {
		t.Error("結んだ検討が棋譜タブから開けません")
	}
}

// 別の棋譜を解析している最中に保存しても、**前の検討の控えに結ぶ**こと。
//
// ⚠️ **これが普段の流れ**（中継は終局してから保存し、そのとき解析しているのは
// 別の棋譜であることが多い）。
func TestStudyStoreLinkGameStored(t *testing.T) {
	dir := t.TempDir()
	s, store := storedStudy(t, dir)
	if _, err := s.LoadKifu(keyKifA); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	if _, err := s.Play("2g2f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if _, err := s.LoadKifu(keyKifB); err != nil {
		t.Fatalf("LoadKifu(B): %v", err)
	}
	store.LinkGame(pasteKeyOf(keyKifA), "game-1")
	if s.State().GameID != "" {
		t.Errorf("別の棋譜を開いているのに今の検討が結ばれました: %q", s.State().GameID)
	}
	if _, ok := store.RestoreGame("game-1"); !ok {
		t.Fatal("前の検討が棋譜タブから開けません")
	}
	if !hasMove(s, "2g2f") {
		t.Error("開いたのが前の検討ではありません")
	}

	// ⚠️ **既に結んでいる控えは別の棋譜へ移さないこと。**
	if _, err := s.LoadKifu(keyKifB); err != nil {
		t.Fatalf("LoadKifu(B): %v", err)
	}
	store.LinkGame(pasteKeyOf(keyKifA), "game-2")
	if _, ok := store.RestoreGame("game-2"); ok {
		t.Error("結び済みの控えが別の棋譜へ移りました（前の行から開けなくなる）")
	}
}

// ⚠️ **撮った局面・新規対局には鍵が付かないこと**（別の棋譜の控えと結ばれる）。
func TestStudySourceKeyClearedOnNewRoot(t *testing.T) {
	s, _ := storedStudy(t, t.TempDir())
	if _, err := s.LoadKifu(keyKifA); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	if _, err := s.NewGame(""); err != nil {
		t.Fatalf("NewGame: %v", err)
	}
	if rec, _ := s.sessionRecord(); rec.SourceKey != "" {
		t.Errorf("新規対局に前の棋譜の鍵が残っています: %q", rec.SourceKey)
	}
}
