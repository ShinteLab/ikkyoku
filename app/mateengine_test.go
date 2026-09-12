package app

import (
	"io"
	"log/slog"
	"testing"

	ikkyoku "github.com/ShinteLab/ikkyoku"
)

// ⚠️ **「解析に使う」と「詰将棋」は独立した軸**（2026-09-12）。
//
// **片方がもう片方を外さないこと。** 一度「詰将棋の印が付いた登録は解析から外す」と
// していたが、**チェックを入れたのに使われない**のは画面から理由が読めない。
// どう使うかを決めるのは人で、こちらは説明するだけにする。
func TestEngineAxesAreIndependent(t *testing.T) {
	cfg := ikkyoku.Config{Engines: []ikkyoku.EngineEntry{
		{ID: "a", Path: "a.exe", Enabled: true},
		{ID: "both", Path: "komoring.exe", Enabled: true, Mate: true},
		{ID: "off", Path: "b.exe", Enabled: false},
	}}

	got := cfg.EnabledEngines()
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "both" {
		t.Errorf("解析に使うエンジン = %+v, want a と both", got)
	}
	mate, ok := cfg.MateEngine()
	if !ok || mate.ID != "both" {
		t.Errorf("詰将棋エンジン = %+v (ok=%v), want both", mate, ok)
	}
}

// ⚠️ **「詰将棋にだけ使う」が成り立つこと**（解析には使わない設定が一番ありふれた形）。
func TestMateEngineWithoutAnalysis(t *testing.T) {
	cfg := ikkyoku.Config{Engines: []ikkyoku.EngineEntry{
		{ID: "m", Path: "komoring.exe", Enabled: false, Mate: true},
	}}
	if got := cfg.EnabledEngines(); len(got) != 0 {
		t.Errorf("解析に使うエンジン = %+v, want 無し", got)
	}
	mate, ok := cfg.MateEngine()
	if !ok || mate.ID != "m" {
		t.Errorf("詰将棋エンジン = %+v (ok=%v), want m", mate, ok)
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
