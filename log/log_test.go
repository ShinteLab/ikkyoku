package log_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ShinteLab/ikkyoku/log"
)

// initIn は dir で Init し、テストの終わりに slog.Default() を戻してファイルを閉じる。
func initIn(t *testing.T, dir string, console bool) {
	t.Helper()
	old := slog.Default()
	c, err := log.Init(log.Options{Dir: dir, Console: console})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		slog.SetDefault(old)
		c.Close()
	})
}

func todayLog(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, log.FilePrefix+"_"+time.Now().Format("20060102")+".log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitWritesFile(t *testing.T) {
	dir := t.TempDir()
	initIn(t, dir, false)

	log.Info("info", "k", 1)
	log.Infof("infof %d", 2)
	log.Debug("debug は既定では出ない")
	slog.Info("slog を直接呼んでも同じファイル")

	out := todayLog(t, dir)
	for _, e := range []string{"ログを開始しました", "info k=1", "infof 2", "slog を直接呼んでも同じファイル"} {
		if !strings.Contains(out, e) {
			t.Errorf("missing %q:\n%s", e, out)
		}
	}
	if strings.Contains(out, "debug は既定では出ない") {
		t.Errorf("debug should be filtered by default:\n%s", out)
	}
}

// パッケージごとのレベルは、呼び出し元の位置で決まる（ここの関数を通しても、呼んだ側のパッケージ）。
func TestLevelsFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, log.LevelsFile), []byte(
		`{"root":"WARN","packages":[{"name":"github.com/ShinteLab/ikkyoku/log_test","level":"TRACE"}]}`), 0o644)
	initIn(t, dir, false)

	log.Trace("trace from test")
	out := todayLog(t, dir)
	if !strings.Contains(out, "trace from test") {
		t.Errorf("package level should apply to the caller's package:\n%s", out)
	}
	if !strings.Contains(out, log.LevelsFile) {
		t.Errorf("startup line should tell which levels were used:\n%s", out)
	}
}

// 読めない設定でも止まらず、既定のレベルで続ける（エラーは返す）。
func TestBrokenLevelsFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, log.LevelsFile), []byte(`{"root":"LOUD"}`), 0o644)
	old := slog.Default()
	c, err := log.Init(log.Options{Dir: dir})
	t.Cleanup(func() {
		slog.SetDefault(old)
		c.Close()
	})
	if err == nil {
		t.Error("broken levels file should be reported")
	}
	log.Info("still works")
	if out := todayLog(t, dir); !strings.Contains(out, "still works") {
		t.Errorf("should keep logging with defaults:\n%s", out)
	}
}

// ファイルを作れなくても slog.Default() は差し替わり、Closer は nil にならない。
func TestInitWithoutFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	os.WriteFile(blocker, nil, 0o644)
	old := slog.Default()
	c, err := log.Init(log.Options{Dir: filepath.Join(blocker, "sub")}) // ファイルの下にディレクトリは作れない
	t.Cleanup(func() { slog.SetDefault(old) })
	if err == nil {
		t.Error("unwritable dir should be reported")
	}
	if c == nil {
		t.Fatal("closer should never be nil")
	}
	c.Close()
	log.Info("no sink, no panic")
}

func TestRemoveOld(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, log.FilePrefix+"_"+time.Now().AddDate(0, 0, -30).Format("20060102")+".log")
	recent := filepath.Join(dir, log.FilePrefix+"_"+time.Now().AddDate(0, 0, -3).Format("20060102")+".log")
	other := filepath.Join(dir, log.FilePrefix+"_memo.log")
	oldFollow := filepath.Join(dir, log.TopicFile(log.Follow)+"_"+time.Now().AddDate(0, 0, -30).Format("20060102")+".log")
	for _, p := range []string{old, recent, other, oldFollow} {
		os.WriteFile(p, []byte("x\n"), 0o644)
	}
	initIn(t, dir, false)

	for _, p := range []string{old, oldFollow} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should be removed", filepath.Base(p))
		}
	}
	for _, p := range []string{recent, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s should be kept: %v", filepath.Base(p), err)
		}
	}
}

func todayFile(t *testing.T, dir, prefix string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, prefix+"_"+time.Now().Format("20060102")+".log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// 話題（log.Follow）のログは話題のファイルにだけ出る。Warn 以上は本体にも出す。
// レベルは話題でも呼び出し元のパッケージで決まる。
func TestTopicFile(t *testing.T) {
	dir := t.TempDir()
	initIn(t, dir, false)

	log.Info("本体の行")
	log.Follow.Info("追従の行", "k", 1)
	log.Follow.Infof("追従の行 %d", 2)
	log.Follow.Debug("追従の debug は既定では出ない")
	log.Follow.Warn("追従の警告")

	main := todayLog(t, dir)
	follow := todayFile(t, dir, log.TopicFile(log.Follow))
	for _, e := range []string{"追従の行 k=1", "追従の行 2", "追従の警告"} {
		if !strings.Contains(follow, e) {
			t.Errorf("follow file missing %q:\n%s", e, follow)
		}
	}
	if strings.Contains(follow, "本体の行") {
		t.Errorf("main lines should not go to the follow file:\n%s", follow)
	}
	if strings.Contains(main, "追従の行") {
		t.Errorf("follow lines should not go to the main file:\n%s", main)
	}
	if !strings.Contains(main, "追従の警告") {
		t.Errorf("follow warnings should also go to the main file:\n%s", main)
	}
	if strings.Contains(follow, "追従の debug") {
		t.Errorf("debug should be filtered by default:\n%s", follow)
	}
}

// 話題のメソッドを通しても、記録の位置は呼んだ側のパッケージになる。
func TestTopicLevelsFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, log.LevelsFile), []byte(
		`{"root":"WARN","packages":[{"name":"github.com/ShinteLab/ikkyoku/log_test","level":"TRACE"}]}`), 0o644)
	initIn(t, dir, false)

	log.Follow.Trace("trace from test")
	if out := todayFile(t, dir, log.TopicFile(log.Follow)); !strings.Contains(out, "trace from test") {
		t.Errorf("package level should apply to the caller's package:\n%s", out)
	}
}

// 追わなかった日は話題のファイルを作らない。
func TestTopicFileLazy(t *testing.T) {
	dir := t.TempDir()
	initIn(t, dir, false)
	log.Info("本体だけ")
	if names, _ := filepath.Glob(filepath.Join(dir, log.TopicFile(log.Follow)+"_*.log")); len(names) != 0 {
		t.Errorf("topic file should not be created until written: %v", names)
	}
}
