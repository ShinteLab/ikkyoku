package log

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 異常終了の記録（2026-10-04）の歯止め。
//
//   - **前回の記録を残すこと**（改名して返す）。次の起動が CrashFile を空で作り直すので、
//     ここで退けておかないと「前回は異常終了しました」の中身が消える
//   - **空のファイルは異常終了ではない**（毎回空で作るので、正常に終わればそのまま残る）
//   - **古いログの片付け（removeOld）が記録を消さないこと**（名前が日付で始まらない）
func TestKeepPreviousCrash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, CrashFile)

	if got := keepPreviousCrash(dir); got != "" {
		t.Fatalf("ファイルが無いのに %q", got)
	}

	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := keepPreviousCrash(dir); got != "" {
		t.Fatalf("空のファイルで %q（正常に終わった回）", got)
	}

	if err := os.WriteFile(path, []byte("panic: boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := keepPreviousCrash(dir)
	if got == "" || !strings.HasPrefix(filepath.Base(got), "ikkyoku_crash_") {
		t.Fatalf("改名先 = %q", got)
	}
	if b, err := os.ReadFile(got); err != nil || string(b) != "panic: boom\n" {
		t.Fatalf("改名先の中身 = %q, err=%v", b, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("元の名前が残っている: %v", err)
	}

	// 15 日後に片付けても消えない。
	removeOld(dir, time.Now().AddDate(0, 0, 15))
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("古いログの片付けで記録が消えた: %v", err)
	}
}

// Dir は書けているときだけ場所を返すこと（画面の ⚠ が「ログの場所」として出す）。
func TestDirFollowsInit(t *testing.T) {
	dir := t.TempDir()
	c, err := Init(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if Dir() != dir {
		t.Fatalf("Dir() = %q, want %q", Dir(), dir)
	}

	// 作れない場所（ファイルをディレクトリとして渡す）なら空。
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c2, _ := Init(Options{Dir: filepath.Join(file, "sub")})
	defer c2.Close()
	if Dir() != "" {
		t.Fatalf("書けないのに Dir() = %q", Dir())
	}
}
