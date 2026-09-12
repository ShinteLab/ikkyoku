package app

import (
	"io"
	"log/slog"
	"testing"

	ikkyoku "github.com/ShinteLab/ikkyoku"
)

// 詰将棋エンジンは**通常の解析からは外れる**こと（2026-09-12）。
//
// ⚠️ **混ぜると評価値の代わりに「投了」が並ぶ** —— 詰将棋エンジンは通常の `go` に
// 答えないことがある（KomoringHeights は `bestmove resign` を返す。実測）。
func TestMateEngineIsSeparateFromAnalysis(t *testing.T) {
	cfg := ikkyoku.Config{Engines: []ikkyoku.EngineEntry{
		{ID: "a", Path: "a.exe", Enabled: true},
		{ID: "m", Path: "komoring.exe", Enabled: true, Mate: true},
		{ID: "b", Path: "b.exe", Enabled: false},
	}}

	got := cfg.EnabledEngines()
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("解析に使うエンジン = %+v, want a だけ", got)
	}
	mate, ok := cfg.MateEngine()
	if !ok || mate.ID != "m" {
		t.Errorf("詰将棋エンジン = %+v (ok=%v), want m", mate, ok)
	}
}

// ⚠️ **「解析に使う」を外した詰将棋エンジンは拾わないこと**（一覧で外した以上使わない）。
func TestMateEngineSkipsDisabled(t *testing.T) {
	cfg := ikkyoku.Config{Engines: []ikkyoku.EngineEntry{
		{ID: "m", Path: "komoring.exe", Enabled: false, Mate: true},
	}}
	if _, ok := cfg.MateEngine(); ok {
		t.Error("外してある詰将棋エンジンを拾いました")
	}
}

// 詰将棋エンジンの印を切り替えられること（設定タブのチェック）。
func TestSetEngineMate(t *testing.T) {
	s := newTestSettings(t, []ikkyoku.EngineEntry{
		{ID: "a", Path: "a.exe", Enabled: true},
	})
	got, err := s.SetEngineMate("a", true)
	if err != nil {
		t.Fatalf("SetEngineMate: %v", err)
	}
	if len(got.Engines) != 1 || !got.Engines[0].Mate {
		t.Fatalf("印が付いていません: %+v", got.Engines)
	}
	// **保存されていること**（設定ファイルを読み直しても残る）。
	if e, ok := s.mateEngine(); !ok || e.ID != "a" {
		t.Errorf("詰将棋エンジンとして拾えません: %+v (ok=%v)", e, ok)
	}
	if _, err := s.SetEngineMate("a", false); err != nil {
		t.Fatalf("SetEngineMate(false): %v", err)
	}
	if _, ok := s.mateEngine(); ok {
		t.Error("外したのに拾えています")
	}
}

// ⚠️ **詰将棋エンジンが無いときは「入れてください」と言うだけ**（設計原則3。
// 通常の解析を巻き込まない）。
func TestSolveMateWithoutMateEngine(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pos := NewPositionService(logger)
	if _, err := pos.LoadEmpty(); err != nil {
		t.Fatalf("LoadEmpty: %v", err)
	}
	study := NewStudyService(logger, pos)
	if _, err := study.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	s := &AnalyzeService{
		logger:   logger,
		study:    study,
		settings: newTestSettings(t, []ikkyoku.EngineEntry{{ID: "a", Path: "a.exe", Enabled: true}}),
	}
	if _, err := s.SolveMate(1); err == nil {
		t.Fatal("詰将棋エンジンが無いのに通りました")
	}
}
