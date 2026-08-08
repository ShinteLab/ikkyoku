package ikkyoku

import (
	"path/filepath"
	"reflect"
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

	want := Config{Engine: EngineConfig{
		Path:    filepath.Join(dir, "engine.exe"),
		Options: map[string]string{"USI_Hash": "1024", "Threads": "4"},
	}}
	if err := SaveConfig(path, want); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !reflect.DeepEqual(got.Engine, want.Engine) {
		t.Errorf("Engine = %+v, want %+v", got.Engine, want.Engine)
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
