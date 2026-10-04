package app

import (
	"os"
	"path/filepath"
	"testing"
)

// 壊れた設定ファイルの写し（2026-10-04）の歯止め。
//
// ⚠️ **元のファイルを動かさず、中身をそのまま写すこと** —— 既定の設定で動いたまま
// 何かを変えると元の場所へ保存されるので、写しが無いと手で直す材料が消える。
func TestBackupBrokenConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	broken := []byte(`{"engines": [`)
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatal(err)
	}

	dst := backupBrokenConfig(path)
	if dst == "" {
		t.Fatal("写せていない")
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != string(broken) {
		t.Fatalf("写しの中身 = %q, err=%v", got, err)
	}
	if orig, err := os.ReadFile(path); err != nil || string(orig) != string(broken) {
		t.Fatalf("元のファイルが変わった: %q, err=%v", orig, err)
	}

	// 読めないファイル（ここでは無いファイル）は写せないので空。
	if got := backupBrokenConfig(filepath.Join(dir, "missing.json")); got != "" {
		t.Fatalf("無いファイルで %q が返った", got)
	}
}
