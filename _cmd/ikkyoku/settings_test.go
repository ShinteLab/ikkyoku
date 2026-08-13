package main

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	ikkyoku "github.com/ShinteLab/ikkyoku"
)

// newTestSettings は保存先を一時ディレクトリに向けた SettingsService を作る。
//
// **NewSettingsService は使えない**（あちらは os.UserConfigDir を見るので、
// テストが本物の設定ファイルを書き換えてしまう）。
//
// ⚠️ **ファイルにも書いておくこと。** `save` は**保存の前にファイルを読み直す**ので、
// メモリ上の cfg だけ用意しても書き換えの土台にならない（登録が空とみなされ、
// 同梱エンジン 1 件に実体化される）。
func newTestSettings(t *testing.T, engines []ikkyoku.EngineEntry) *SettingsService {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := ikkyoku.Config{Engines: engines}
	if err := ikkyoku.SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	return &SettingsService{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		path:   path,
		cfg:    cfg,
	}
}

func engineIDs(s AppSettings) []string {
	ids := make([]string, 0, len(s.Engines))
	for _, e := range s.Engines {
		ids = append(ids, e.ID)
	}
	return ids
}

// TestMoveEngine は**エンジンの並べ替え**を固定する。
//
// 並び順は表示の順序そのもの（解析タブのエンジンごとの結果・評価値グラフの折れ線・
// 勝率バーが最初に出すエンジン）なので、**動かした結果が保存されること**と、
// ⚠️ **端で回り込まないこと**（一番上を上げたら一番下へ飛ぶ、では驚く）を見る。
func TestMoveEngine(t *testing.T) {
	list := []ikkyoku.EngineEntry{
		{ID: "a", Enabled: true},
		{ID: "b", Enabled: false},
		{ID: "c", Enabled: true},
	}

	t.Run("下へ動かす", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, err := s.MoveEngine("a", 1)
		if err != nil {
			t.Fatalf("MoveEngine: %v", err)
		}
		want := []string{"b", "a", "c"}
		if ids := engineIDs(got); !equalStrings(ids, want) {
			t.Errorf("並び = %v, want %v", ids, want)
		}
		// 保存されていること（読み直しても同じ順であること）。
		cfg, err := ikkyoku.LoadConfig(s.path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if len(cfg.Engines) != 3 || cfg.Engines[0].ID != "b" || cfg.Engines[1].ID != "a" {
			t.Errorf("保存された並び = %v", cfg.Engines)
		}
	})

	t.Run("上へ動かす", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, _ := s.MoveEngine("c", -1)
		want := []string{"a", "c", "b"}
		if ids := engineIDs(got); !equalStrings(ids, want) {
			t.Errorf("並び = %v, want %v", ids, want)
		}
	})

	// ⚠️ **端では動かない。回り込ませない。**
	t.Run("端では動かない", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, _ := s.MoveEngine("a", -1)
		want := []string{"a", "b", "c"}
		if ids := engineIDs(got); !equalStrings(ids, want) {
			t.Errorf("先頭を上へ: 並び = %v, want %v", ids, want)
		}
		got, _ = s.MoveEngine("c", 1)
		if ids := engineIDs(got); !equalStrings(ids, want) {
			t.Errorf("末尾を下へ: 並び = %v, want %v", ids, want)
		}
	})

	// ⚠️ **「解析に使う」を外した登録も数に入れる。** 走るものだけを詰めて数えると、
	// チェックを外した瞬間に見えている順番と食い違う。
	t.Run("解析に使わない登録も飛ばさない", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, _ := s.MoveEngine("c", -1) // b（無効）と入れ替わる
		want := []string{"a", "c", "b"}
		if ids := engineIDs(got); !equalStrings(ids, want) {
			t.Errorf("並び = %v, want %v", ids, want)
		}
	})

	t.Run("知らない ID では何も変えない", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, _ := s.MoveEngine("zzz", 1)
		want := []string{"a", "b", "c"}
		if ids := engineIDs(got); !equalStrings(ids, want) {
			t.Errorf("並び = %v, want %v", ids, want)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSetEngineColor は**エンジンの色**を固定する（2026-08-14）。
//
// 色は**エンジンの登録に紐づく**（並び順ではない）。複数のエンジンを並べて読むのが
// この一覧の目的なので、**どの線がどのエンジンか**は見た目で覚えるもの。
// ⚠️ **並べ替えたり 1 つ外したりして色が入れ替わると、前に見ていた線と同じ色が
// 別のエンジンを指す**ことになるので、そこを見ている。
func TestSetEngineColor(t *testing.T) {
	list := []ikkyoku.EngineEntry{
		{ID: "a", Enabled: true},
		{ID: "b", Enabled: true},
	}

	t.Run("色が保存され、返る設定にも入る", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, err := s.SetEngineColor("b", "#FF8FA3") // 大文字でも受ける
		if err != nil {
			t.Fatalf("SetEngineColor: %v", err)
		}
		if got.Engines[1].Color != "#ff8fa3" {
			t.Errorf("色 = %q, want #ff8fa3", got.Engines[1].Color)
		}
		cfg, err := ikkyoku.LoadConfig(s.path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Engines[1].Color != "#ff8fa3" {
			t.Errorf("保存された色 = %q", cfg.Engines[1].Color)
		}
	})

	// ⚠️ **未設定なら登録順の既定色**（解決するのは Go 側。フロントに書かない）。
	t.Run("未設定は登録順の既定色", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got := s.Settings()
		for i, e := range got.Engines {
			if want := ikkyoku.DefaultEngineColor(i); e.Color != want {
				t.Errorf("engines[%d].Color = %q, want %q", i, e.Color, want)
			}
		}
	})

	// ⚠️ **並べ替えても、色を付けたエンジンの色は動かない。**
	t.Run("並べ替えても色は付いてくる", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		if _, err := s.SetEngineColor("a", "#6fd3c7"); err != nil {
			t.Fatalf("SetEngineColor: %v", err)
		}
		got, err := s.MoveEngine("a", 1)
		if err != nil {
			t.Fatalf("MoveEngine: %v", err)
		}
		if got.Engines[1].ID != "a" || got.Engines[1].Color != "#6fd3c7" {
			t.Errorf("並べ替え後 = %+v", got.Engines[1])
		}
	})

	t.Run("空にすると既定へ戻る", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		if _, err := s.SetEngineColor("a", "#6fd3c7"); err != nil {
			t.Fatalf("SetEngineColor: %v", err)
		}
		got, err := s.SetEngineColor("a", "")
		if err != nil {
			t.Fatalf("SetEngineColor(空): %v", err)
		}
		if want := ikkyoku.DefaultEngineColor(0); got.Engines[0].Color != want {
			t.Errorf("色 = %q, want %q（既定）", got.Engines[0].Color, want)
		}
	})

	// 壊れた値は断る。**そのとき今の設定は変えない**（画面が食い違ったままにならない）。
	t.Run("色の形が違えば断る", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, err := s.SetEngineColor("a", "赤")
		if err == nil {
			t.Fatal("エラーになるべき")
		}
		if want := ikkyoku.DefaultEngineColor(0); got.Engines[0].Color != want {
			t.Errorf("断ったのに色が変わった: %q", got.Engines[0].Color)
		}
	})
}
