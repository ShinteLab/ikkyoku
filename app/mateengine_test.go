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

// ⚠️ **詰将棋では足した手順が本線になること**（2026-09-12）。
//
// 詰将棋に「本譜」は無く、**足した手順そのものが答え**なので、1 段下げて
// 畳んだ形で置くと読みづらい。⚠️ **2 本目（余詰）は枝のまま** ——
// 先に足したほうを黙って押しのけないこと。
func TestAddLineOnMateProblemBecomesMainLine(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pos := NewPositionService(logger)
	// 1 手詰（後手玉 5一・先手歩 5三・攻方の持駒は金）。
	if _, err := pos.Load("4k4/9/4P4/9/9/9/9/9/9"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := pos.SetMateProblem(true); err != nil {
		t.Fatalf("SetMateProblem: %v", err)
	}
	if _, err := pos.SetHand(4, true, 1); err != nil { // 4=金 を攻方の駒台へ
		t.Fatalf("SetHand: %v", err)
	}
	study := NewStudyService(logger, pos)
	if _, err := study.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	got, err := study.AddLine("", []string{"G*5b"})
	if err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	if got.Added != 1 {
		t.Fatalf("足した手数 = %d, want 1", got.Added)
	}
	node := func(st StudyState, id int) (int, bool) {
		for _, n := range st.Nodes {
			if n.ID == id {
				return n.Depth, n.Main
			}
		}
		return -1, false
	}
	depth, main := node(got.State, got.FirstID)
	if depth != 0 || !main {
		t.Errorf("詰み手順が本線になっていません: depth=%d main=%v", depth, main)
	}

	// 2 本目（別の詰み＝余詰）は枝のまま。
	other, err := study.AddLine("", []string{"G*4b"})
	if err != nil {
		t.Fatalf("AddLine(2 本目): %v", err)
	}
	if d, m := node(other.State, other.FirstID); m || d == 0 {
		t.Errorf("2 本目が本線を押しのけました: depth=%d main=%v", d, m)
	}
	// ⚠️ **1 本目は本線のまま。**
	if d, m := node(other.State, got.FirstID); d != 0 || !m {
		t.Errorf("1 本目が本線から外れました: depth=%d main=%v", d, m)
	}
}

// ⚠️ **詰将棋エンジンでは MultiPV も接続の指紋に入ること**（2026-09-12 に実測）。
//
// **KomoringHeights は `isready` の前に送らないと MultiPV が効かない**
// （後から `setoption` を送っても無視され、候補は 1 本のまま）。指紋に入っていないと
// **本数を変えても繋ぎ直さない**ので、画面で変えても何も起きない。
// ⚠️ **通常のエンジンでは今までどおり外す**（本数を変えるたびに評価関数を読み直す
// ことになる）。
func TestEngineKeyMultiPV(t *testing.T) {
	one := map[string]string{"MultiPV": "1", "Threads": "4"}
	three := map[string]string{"MultiPV": "3", "Threads": "4"}

	normal := ikkyoku.EngineEntry{ID: "a", Path: "a.exe", Options: one}
	normal3 := ikkyoku.EngineEntry{ID: "a", Path: "a.exe", Options: three}
	if engineKey(normal) != engineKey(normal3) {
		t.Error("通常のエンジンで MultiPV が指紋に入っています（繋ぎ直しが起きる）")
	}

	mate := ikkyoku.EngineEntry{ID: "m", Path: "k.exe", Options: one, Mate: true}
	mate3 := ikkyoku.EngineEntry{ID: "m", Path: "k.exe", Options: three, Mate: true}
	if engineKey(mate) == engineKey(mate3) {
		t.Error("詰将棋エンジンで MultiPV が指紋に入っていません（変えても効かない）")
	}
}
