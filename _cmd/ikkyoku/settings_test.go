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
