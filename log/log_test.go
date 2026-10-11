package log_test

import (
	stdlog "log"
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
	c, err := log.Init(log.Options{Dir: dir, Console: console, Follow: true})
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
	// 1 週間（KeepDays）を過ぎたものだけ消す（2026-10-11 に 14 日から縮めた）。
	old := filepath.Join(dir, log.FilePrefix+"_"+time.Now().AddDate(0, 0, -(log.KeepDays+1)).Format("20060102")+".log")
	recent := filepath.Join(dir, log.FilePrefix+"_"+time.Now().AddDate(0, 0, -(log.KeepDays-1)).Format("20060102")+".log")
	other := filepath.Join(dir, log.FilePrefix+"_memo.log")
	oldFollow := filepath.Join(dir, log.NamedFile(log.FollowName)+"_"+time.Now().AddDate(0, 0, -30).Format("20060102")+".log")
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

// 名前付き（log.Follow）のログは名前のファイルにだけ出る。Warn 以上は本体にも出す。
func TestNamedFile(t *testing.T) {
	dir := t.TempDir()
	initIn(t, dir, false)

	log.Info("本体の行")
	log.Follow.Info("追従の行", "k", 1)
	log.Follow.With("n", 2).Info("派生しても追従の行")
	log.Follow.Debug("追従の debug は既定では出ない")
	log.Follow.Warn("追従の警告")

	main := todayLog(t, dir)
	follow := todayFile(t, dir, log.NamedFile(log.FollowName))
	for _, e := range []string{"追従の行 logger=follow k=1", "派生しても追従の行", "追従の警告"} {
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

// USI のやり取り（log.USI）は配るビルドでも自分のファイルに残る（2026-10-10。
// エンジンが stop に応えなくなったとき、何を送って何が返っていたかを後から読むため）。
func TestUSIFile(t *testing.T) {
	dir := t.TempDir()
	initIn(t, dir, false)

	log.USI.Info("送ります", "line", "stop")
	log.USI.Warn("エンジンを落としました")

	main := todayLog(t, dir)
	usi := todayFile(t, dir, log.NamedFile(log.USIName))
	if !strings.Contains(usi, "送ります logger=usi line=stop") {
		t.Errorf("usi file missing the sent line:\n%s", usi)
	}
	if strings.Contains(main, "送ります") {
		t.Errorf("usi lines should not go to the main file:\n%s", main)
	}
	if !strings.Contains(main, "エンジンを落としました") {
		t.Errorf("usi warnings should also go to the main file:\n%s", main)
	}
}

// "loggers" に設定が無ければ、名前付きでもレベルは呼び出し元のパッケージで決まる。
func TestNamedFollowsPackageLevel(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, log.LevelsFile), []byte(
		`{"root":"WARN","packages":[{"name":"github.com/ShinteLab/ikkyoku/log_test","level":"TRACE"}]}`), 0o644)
	initIn(t, dir, false)

	log.Follow.Debug("debug from test") // root の WARN なら出ない
	if out := todayFile(t, dir, log.NamedFile(log.FollowName)); !strings.Contains(out, "debug from test") {
		t.Errorf("package level should apply to the caller's package:\n%s", out)
	}
}

// "loggers" の follow で、追従だけレベルを変えられる。
func TestNamedLevel(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, log.LevelsFile), []byte(
		`{"root":"INFO","loggers":[{"name":"follow","level":"DEBUG"}]}`), 0o644)
	initIn(t, dir, false)

	log.Debug("本体の debug は出ない")
	log.Follow.Debug("追従の debug")
	if out := todayFile(t, dir, log.NamedFile(log.FollowName)); !strings.Contains(out, "追従の debug") {
		t.Errorf("loggers setting should apply to log.Follow:\n%s", out)
	}
	if out := todayLog(t, dir); strings.Contains(out, "本体の debug") {
		t.Errorf("loggers setting should not affect the main log:\n%s", out)
	}
}

// 追わなかった日は名前のファイルを作らない。
func TestNamedFileLazy(t *testing.T) {
	dir := t.TempDir()
	initIn(t, dir, false)
	log.Info("本体だけ")
	if names, _ := filepath.Glob(filepath.Join(dir, log.NamedFile(log.FollowName)+"_*.log")); len(names) != 0 {
		t.Errorf("named file should not be created until written: %v", names)
	}
}

// initDiag は Options.Diag を指定して Init する（終わりに slog と標準の log を戻す）。
func initDiag(t *testing.T, dir string, diag bool) {
	t.Helper()
	old := slog.Default()
	c, err := log.Init(log.Options{Dir: dir, Diag: diag, Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Close() // 標準の log を戻してから slog を戻す
		slog.SetDefault(old)
	})
}

// dev（Diag）では診断を Debug まで診断のファイルに残す。標準の log（WebView2 の行）もそこへ。
// 本体には Warn 以上だけ。
func TestDiagDev(t *testing.T) {
	dir := t.TempDir()
	initDiag(t, dir, true)

	log.Diag.Debug("表示状態")
	log.Diag.Info("心拍を検知")
	stdlog.Printf("[WebView2] Eval failed: x")
	log.Diag.Warn("心拍が途切れた")

	diag := todayFile(t, dir, log.NamedFile(log.DiagName))
	for _, e := range []string{"表示状態", "心拍を検知", "[WebView2] Eval failed: x", "心拍が途切れた"} {
		if !strings.Contains(diag, e) {
			t.Errorf("diag file missing %q:\n%s", e, diag)
		}
	}
	main := todayLog(t, dir)
	for _, e := range []string{"表示状態", "心拍を検知", "WebView2"} {
		if strings.Contains(main, e) {
			t.Errorf("main file should not have %q:\n%s", e, main)
		}
	}
	if !strings.Contains(main, "心拍が途切れた") {
		t.Errorf("diag warnings should also go to the main file:\n%s", main)
	}
}

// 配るビルド（Diag なし）では診断を残さない。ファイルも作らない。Warn 以上だけ本体に出す。
func TestDiagRelease(t *testing.T) {
	dir := t.TempDir()
	initDiag(t, dir, false)

	log.Diag.Debug("表示状態")
	log.Diag.Info("心拍を検知")
	stdlog.Printf("[WebView2] Environment created successfully")
	log.Diag.Warn("心拍が途切れた")

	if names, _ := filepath.Glob(filepath.Join(dir, log.NamedFile(log.DiagName)+"_*.log")); len(names) != 0 {
		t.Errorf("diag file should not be created in release: %v", names)
	}
	main := todayLog(t, dir)
	for _, e := range []string{"表示状態", "心拍を検知", "WebView2"} {
		if strings.Contains(main, e) {
			t.Errorf("main file should not have %q:\n%s", e, main)
		}
	}
	if !strings.Contains(main, "心拍が途切れた") {
		t.Errorf("diag warnings should still go to the main file:\n%s", main)
	}
}

// LevelsFile の "loggers" に diag が無ければ、dev の既定（Debug）が残る。あればそちらが効く。
func TestDiagLevelsFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, log.LevelsFile), []byte(
		`{"root":"INFO","loggers":[{"name":"follow","level":"DEBUG"}]}`), 0o644)
	initDiag(t, dir, true)
	log.Diag.Debug("表示状態")
	log.Follow.Debug("追従の debug")
	if out := todayFile(t, dir, log.NamedFile(log.DiagName)); !strings.Contains(out, "表示状態") {
		t.Errorf("diag default should survive a levels file without diag:\n%s", out)
	}
	if out := todayFile(t, dir, log.NamedFile(log.FollowName)); !strings.Contains(out, "追従の debug") {
		t.Errorf("levels file should still apply:\n%s", out)
	}

	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, log.LevelsFile), []byte(
		`{"root":"INFO","loggers":[{"name":"diag","level":"WARN"}]}`), 0o644)
	initDiag(t, dir2, true)
	log.Diag.Info("心拍を検知")
	if names, _ := filepath.Glob(filepath.Join(dir2, log.NamedFile(log.DiagName)+"_*.log")); len(names) != 0 {
		t.Errorf("diag level from the levels file should win: %v", names)
	}
}

// 設定「中継の録画とログを残す（デバッグ）」が切ってあれば、追従のログはファイルに残さない（ファイルも作らない）。
// Warn 以上は本体に出す。切り替えたその場で効く（log.Follow は Init より前に作られた Logger のまま）。
func TestFollowSwitch(t *testing.T) {
	dir := t.TempDir()
	old := slog.Default()
	c, err := log.Init(log.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		slog.SetDefault(old)
		c.Close()
	})

	log.Follow.Info("切ってあるあいだの行")
	log.Follow.Warn("切ってあるあいだの警告")
	if names, _ := filepath.Glob(filepath.Join(dir, log.NamedFile(log.FollowName)+"_*.log")); len(names) != 0 {
		t.Errorf("切ってあるのに追従のファイルを作りました: %v", names)
	}
	if out := todayLog(t, dir); !strings.Contains(out, "切ってあるあいだの警告") || strings.Contains(out, "切ってあるあいだの行") {
		t.Errorf("切ってあっても警告だけは本体に出るはず:\n%s", out)
	}

	log.SetFollow(true)
	log.Follow.Info("入れたあとの行")
	if out := todayFile(t, dir, log.NamedFile(log.FollowName)); !strings.Contains(out, "入れたあとの行") {
		t.Errorf("入れたのに追従のファイルに出ません:\n%s", out)
	}
	log.SetFollow(false)
	log.Follow.Info("また切ったあとの行")
	if out := todayFile(t, dir, log.NamedFile(log.FollowName)); strings.Contains(out, "また切ったあとの行") {
		t.Errorf("切ったのに追従のファイルに出ました:\n%s", out)
	}
}

// レベルの設定は LevelsDir から読む（アプリは実行ファイルのディレクトリを渡し、ログは一時ディレクトリへ書く）。
func TestLevelsDir(t *testing.T) {
	dir, conf := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(conf, log.LevelsFile), []byte(`{"root":"DEBUG"}`), 0o644)
	old := slog.Default()
	c, err := log.Init(log.Options{Dir: dir, LevelsDir: conf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		slog.SetDefault(old)
		c.Close()
	})
	log.Debug("設定で出す debug")
	if out := todayLog(t, dir); !strings.Contains(out, "設定で出す debug") {
		t.Errorf("LevelsDir の設定が効いていません:\n%s", out)
	}
}
