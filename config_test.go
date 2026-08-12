package ikkyoku

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	display := 1
	want := Config{
		Display: &display,
		Region:  &Region{X: 0, Y: 0, Width: 1920, Height: 1080},
		OutDir:  filepath.Join(dir, "captures"),
	}

	if err := SaveConfig(path, want); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got.Display == nil || *got.Display != *want.Display {
		t.Errorf("Display = %v, want %v", got.Display, *want.Display)
	}
	if got.Region == nil || *got.Region != *want.Region {
		t.Errorf("Region = %v, want %v", got.Region, *want.Region)
	}
	if got.OutDir != want.OutDir {
		t.Errorf("OutDir = %q, want %q", got.OutDir, want.OutDir)
	}
}

// 勝率バーのポナンザ定数が往復すること。
//
// ⚠️ **既定値（1500）をここに書き残さないこと。** 0 は「未設定」で、既定への
// 解決は `analyze.PonanzaConstantOr` の 1 か所（設定ファイルに 1500 を焼くと、
// 既定を変えたときにその設定ファイルだけ追従しない）。
func TestConfigPonanzaConstantRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(path, Config{PonanzaConstant: 600}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got.PonanzaConstant != 600 {
		t.Errorf("PonanzaConstant = %v, want 600", got.PonanzaConstant)
	}

	// 未設定は 0 のまま（既定への倒しはここではしない）。
	bare := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(bare, Config{}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	if got, err := LoadConfig(bare); err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	} else if got.PonanzaConstant != 0 {
		t.Errorf("PonanzaConstant = %v, want 0（未設定）", got.PonanzaConstant)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil (ファイル無しはエラーにしない)", err)
	}
	// Config は map（Engine.Options）を含むので == で比べられない。
	if !reflect.DeepEqual(got, Config{}) {
		t.Errorf("LoadConfig() = %+v, want zero value", got)
	}
}

// エンジンの設定が往復すること。**設定ファイルは手で編集する前提**でもあるので、
// 書いたものがそのまま読めることを固定しておく。
func TestConfigEngineRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	want := Config{Engines: []EngineEntry{
		{ID: "engine-1", Name: "やねうら王", Path: filepath.Join(dir, "engine.exe"),
			Options: map[string]string{"USI_Hash": "1024", "Threads": "4"}, Enabled: true},
		// **外した登録も残ること。** 入れ替えて比べる作業では戻すことが多い。
		{ID: "engine-2", Path: filepath.Join(dir, "other.exe")},
	}}
	if err := SaveConfig(path, want); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !reflect.DeepEqual(got.Engines, want.Engines) {
		t.Errorf("Engines = %+v, want %+v", got.Engines, want.Engines)
	}
	if en := got.EnabledEngines(); len(en) != 1 || en[0].ID != "engine-1" {
		t.Errorf("EnabledEngines() = %+v, want engine-1 だけ", en)
	}
}

// 登録が 1 つも無いときは**同梱エンジン 1 つ**として振る舞うこと。
// 設定ファイルを作っていない状態でも解析できる、という既定の挙動。
func TestEngineListDefaultsToBuiltin(t *testing.T) {
	got := Config{}.EngineList()
	if len(got) != 1 || got[0].Path != "" || !got[0].Enabled {
		t.Fatalf("EngineList() = %+v, want 同梱エンジン 1 つ（有効）", got)
	}
	if name := got[0].DisplayName(); name != BuiltinEngineName {
		t.Errorf("DisplayName() = %q, want %q", name, BuiltinEngineName)
	}
	if len(Config{}.EnabledEngines()) != 1 {
		t.Errorf("EnabledEngines() が空。既定で同梱エンジンが使えなくなっている")
	}
}

// 旧形式（engine.path / engine.options）が読み込み時に engines へ移ること。
//
// **移行を落とすと、設定してあった外部エンジンが黙って同梱に戻る**（画面には
// 何も出ない）ので、ここで固定しておく。
func TestLoadConfigMigratesLegacyEngine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	legacy := `{"engine":{"path":"C:/shogi/YaneuraOu.exe","options":{"Threads":"4"}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got.Engine != nil {
		t.Errorf("Engine = %+v, want nil（移したら旧欄は捨てる）", got.Engine)
	}
	if len(got.Engines) != 1 {
		t.Fatalf("Engines = %+v, want 1 件", got.Engines)
	}
	e := got.Engines[0]
	if e.Path != "C:/shogi/YaneuraOu.exe" || e.Options["Threads"] != "4" || !e.Enabled || e.ID == "" {
		t.Errorf("Engines[0] = %+v, want 旧設定をそのまま引き継いだ有効なエントリ", e)
	}

	// 保存し直したら、ファイルからも旧欄が消えること。
	if err := SaveConfig(path, got); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(b), `"engine"`) {
		t.Errorf("保存後も旧欄が残っている: %s", b)
	}
}

// engines が既にあれば旧欄は無視すること（手で両方書いたときは新しいほうが正）。
func TestLoadConfigPrefersEngines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	both := `{"engine":{"path":"old.exe"},"engines":[{"id":"engine-1","path":"new.exe","enabled":true}]}`
	if err := os.WriteFile(path, []byte(both), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(got.Engines) != 1 || got.Engines[0].Path != "new.exe" {
		t.Errorf("Engines = %+v, want new.exe だけ", got.Engines)
	}
}

// 追加した ID が既存とぶつからないこと。
func TestNextEngineID(t *testing.T) {
	engines := []EngineEntry{{ID: "engine-1"}, {ID: "engine-3"}}
	if got := NextEngineID(engines); got != "engine-2" {
		t.Errorf("NextEngineID() = %q, want engine-2", got)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath() error = %v", err)
	}
	if filepath.Base(path) != "config.json" {
		t.Errorf("DefaultConfigPath() = %q, want basename %q", path, "config.json")
	}
}
