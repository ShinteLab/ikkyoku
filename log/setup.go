package log

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	stdlog "log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	wlog "github.com/wenteasy/log"
)

// Options は Init の設定。
type Options struct {
	// Dir はログファイルを書くディレクトリ。空なら DefaultDir（一時ディレクトリの ikkyoku。2026-10-11）。
	Dir string
	// LevelsDir はレベルの設定（LevelsFile）を読むディレクトリ。空なら Dir。
	// ⚠️ **アプリは実行ファイルのディレクトリを渡す**（設定は書き出すものではなく置くもの。一時ディレクトリは
	// OS の掃除で消えることがある）。
	LevelsDir string
	// Follow は中継を追うログ（log.Follow）を ikkyoku-follow_<日付>.log に残すか（2026-10-11。設定「中継の録画と
	// ログを残す（デバッグ）」）。あとから SetFollow で切り替えられる。偽なら Warn 以上だけ本体に出し、残りは捨てる
	// （ファイルも作らない）。
	Follow bool
	// Console は標準エラーにも出すか。wails3 dev で動かすとき（ターミナルで見るとき）に立てる。
	// ⚠️ 配る exe（-H windowsgui）には標準エラーの行き先が無いので、立てても見えない。
	Console bool
	// Diag は画面まわりの診断のログ（log.Diag と、Init が向ける Wails・標準の log の出力）を
	// ikkyoku-diag_<日付>.log に Debug まで残すか。**dev のときだけ立てる**（2026-10-10）。
	// 偽なら Warn 以上だけ本体に出し、残りは捨てる（ファイルも作らない）。
	Diag bool
}

const (
	// FilePrefix はログファイルの名前の頭。ファイルは日ごとに分かれる（ikkyoku_20261003.log）。
	// 名前付きの Logger（log.Follow）のファイルは FilePrefix-<名前>（ikkyoku-follow_20261003.log）。
	FilePrefix = "ikkyoku"
	// LevelsFile はパッケージごとのレベルの設定。Dir に置けば Init が読む（無ければ defaultLevels）。
	// 形は wenteasy/log の LevelsFile（"loggers" は名前付きの Logger。log.Follow なら "follow"）:
	//
	//	{"root": "INFO", "packages": [{"name": "github.com/ShinteLab/ikkyoku/app", "level": "DEBUG"}],
	//	 "loggers": [{"name": "follow", "level": "DEBUG"}]}
	LevelsFile = "ikkyoku-log.json"
	// KeepDays より古いログファイルは Init のときに消す（2026-10-11 に 14 日から 7 日へ。何日も前の記録を
	// 掘ってデバッグすることは無い）。中継の録画も同じ日数で消す（`_cmd/ikkyoku`）。
	KeepDays = 7
	// dirName は DefaultDir のディレクトリの名前。
	dirName = "ikkyoku"
)

// DefaultDir はログ（と中継の録画）を書く既定のディレクトリ（一時ディレクトリの ikkyoku。2026-10-11）。
// それまでは実行ファイルの隣で、配る exe の置き場所にデバッグの記録が溜まっていた。
func DefaultDir() string { return filepath.Join(os.TempDir(), dirName) }

// followOn は中継を追うログをファイルに残すか（`SetFollow`）。
var followOn atomic.Bool

// SetFollow は中継を追うログ（log.Follow）をファイルに残すかを切り替える（設定を変えたその場で効く）。
// 偽でも Warn 以上は本体に出る。
func SetFollow(on bool) { followOn.Store(on) }

// FollowOn は中継を追うログをファイルに残しているか。
func FollowOn() bool { return followOn.Load() }

// defaultLevels は LevelsFile が無いときの設定。全体を Info にするだけ。
// ⚠️ **ライブラリを個別に絞って黙らせないこと。** うるさいなら、そのライブラリで
// レベルを下げる（engine の USI の送受信は engine 側で Debug にした。2026-10-03）。
func defaultLevels() (slog.Level, map[string]slog.Level) {
	return LevelInfo, nil
}

// fileDir はログファイルを書いているディレクトリ（Init が決める。書けていなければ空）。
var fileDir string

// Dir はログファイルを書いているディレクトリを返す。**書けていなければ空**
// （画面の ⚠ に「ログの場所」として出す・異常終了の記録を置く）。
func Dir() string { return fileDir }

// separated は本体から分けて自分のファイルへ出す名前付きの Logger の名前（log.go の Follow / Diag / USI）。
var separated = []string{FollowName, DiagName, USIName}

// NamedFile は名前付きの Logger のログファイルの名前の頭（"ikkyoku-follow"）。
func NamedFile(name string) string { return FilePrefix + "-" + name }

// Init はログの出口を組み、slog.Default() をそこへ向ける。main の最初に 1 回だけ呼ぶ。
//
//   - ファイル: Dir/ikkyoku_<日付>.log（日ごと。KeepDays より古いものは消す）
//   - 名前付き（log.Follow / log.Diag / log.USI）: Dir/ikkyoku-<名前>_<日付>.log だけ（コンソールに出さない。Warn 以上は本体にも）。
//     Diag は Options.Diag のときだけ残し、Debug まで出す（LevelsFile の "loggers" に diag があればそちら）。
//     Follow は Options.Follow / SetFollow が真のあいだだけ残す
//   - 標準の log の出力（Wails の [WebView2] の行など）: log.Diag へ
//   - コンソール: Options.Console のときだけ標準エラーにも
//   - レベル: 呼び出し元のパッケージごと（LevelsDir/LevelsFile。無ければ defaultLevels）
//
// ⚠️ **ログが書けなくてもアプリは動かす**（設計原則3）。ファイルを作れないときも、出せる
// 出口（コンソール）だけで組んで slog.Default() を差し替え、理由をエラーで返す。
// 返す io.Closer は必ず nil ではない（終了時に Close する）。
func Init(opts Options) (io.Closer, error) {
	var errs []error
	fileDir = ""
	followOn.Store(opts.Follow)
	dir := opts.Dir
	if dir == "" {
		dir = DefaultDir()
	}
	levelsDir := opts.LevelsDir
	if levelsDir == "" {
		levelsDir = dir
	}

	var sinks []slog.Handler
	var closers multiCloser
	var file string
	namedSinks := map[string]slog.Handler{}
	if dir != "" {
		w, err := wlog.NewRollingFileWriter(dir, wlog.Day)
		if err != nil {
			errs = append(errs, fmt.Errorf("ログのディレクトリを作れません: %w", err))
		} else {
			w.SetPrefix(FilePrefix)
			sinks = append(sinks, wlog.NewSimpleHandler(w, LevelTrace))
			closers = append(closers, w)
			file = filepath.Join(dir, FilePrefix+"_"+time.Now().Format("20060102")+".log")
			fileDir = dir
			// 名前付きのファイルは書いたときに開く（追わなかった日は作られない）。
			for _, name := range separated {
				if name == DiagName && !opts.Diag {
					continue // 下で捨てる口にする
				}
				tw, err := wlog.NewRollingFileWriter(dir, wlog.Day)
				if err != nil {
					continue // 出口が無い名前は本体へ出る（namedRouter）
				}
				tw.SetPrefix(NamedFile(name))
				namedSinks[name] = wlog.NewSimpleHandler(tw, LevelTrace)
				if name == FollowName {
					// 設定で切り替える（切ってあるあいだは書かないので、ファイルも作られない）。
					namedSinks[name] = &switchHandler{on: &followOn, h: namedSinks[name]}
				}
				closers = append(closers, tw)
			}
		}
	}
	if !opts.Diag {
		// 配るビルドでは診断を残さない（Warn 以上は namedRouter が本体にも出す）。
		namedSinks[DiagName] = slog.DiscardHandler
	}
	if opts.Console {
		sinks = append(sinks, wlog.NewSimpleHandler(os.Stderr, LevelTrace))
	}

	// 出口は一番低いレベルで作り、絞るのはパッケージごとのレベルに任せる
	// （出口が出さないレベルは、パッケージのレベルを下げても出ない）。
	root, pkgs := defaultLevels()
	loggers := defaultLoggers(opts)
	sink := slog.NewMultiHandler(sinks...)
	h := wlog.NewPackageLevelHandler(&namedRouter{main: sink, named: namedSinks}, root)
	h.SetLevels(root, pkgs)
	h.SetLoggerLevels(loggers)
	levels := "既定"
	if levelsDir != "" {
		path := filepath.Join(levelsDir, LevelsFile)
		switch err := h.LoadJSON(path); {
		case err == nil:
			levels = path
			// LoadJSON は "loggers" も丸ごと差し替えるので、書いていない名前は既定を足し直す。
			if len(loggers) > 0 {
				h.SetLoggerLevels(withDefaults(path, loggers))
			}
		case errors.Is(err, fs.ErrNotExist):
		default:
			// 読めない設定は使わず、既定のまま続ける（LoadJSON は失敗したら変えない）。
			errs = append(errs, fmt.Errorf("%s を読めません（既定のレベルで続けます）: %w", LevelsFile, err))
		}
	}
	slog.SetDefault(slog.New(h))
	// 標準の log の出力は log.Diag へ（Wails の WebView2 は log.Printf で書く。黒くなったときの
	// 決め手だった [WebView2] Eval failed / Focus failed もここ）。SetDefault が本体へ向けたのを上書きする。
	// 閉じるときに戻す（戻さないと、slog の既定へ戻したあとに log → slog → log と回り続ける）。
	prevOut, prevFlags := stdlog.Writer(), stdlog.Flags()
	stdlog.SetOutput(slog.NewLogLogger(Diag.Handler(), LevelInfo).Writer())
	stdlog.SetFlags(0)
	closers = append(closers, closerFunc(func() error {
		stdlog.SetOutput(prevOut)
		stdlog.SetFlags(prevFlags)
		return nil
	}))

	if dir != "" {
		removeOld(dir, time.Now())
	}
	// 始まりの 1 行は、レベルの設定に関係なく必ず残す（どの設定で動いているかを後から読むため）。
	r := slog.NewRecord(time.Now(), LevelNotice, "ログを開始しました", 0)
	r.Add("file", file, "console", opts.Console, "diag", opts.Diag, "follow", opts.Follow, "levels", levels)
	_ = sink.Handle(context.Background(), r)
	return closers, errors.Join(errs...)
}

// defaultLoggers は名前付きの Logger の既定のレベル。dev では診断を Debug まで残す
// （ウィンドウの表示状態は Debug。黒くなったその場で要るので、上げてから待つのでは間に合わない）。
func defaultLoggers(opts Options) map[string]slog.Level {
	if !opts.Diag {
		return nil
	}
	return map[string]slog.Level{DiagName: LevelDebug}
}

// withDefaults は LevelsFile の "loggers" に、書いていない名前の既定を足したものを返す
// （LoadJSON が読めたあとに呼ぶので、読めないことは無い）。
func withDefaults(path string, defs map[string]slog.Level) map[string]slog.Level {
	out := map[string]slog.Level{}
	if b, err := os.ReadFile(path); err == nil {
		var f wlog.LevelsFile
		if json.Unmarshal(b, &f) == nil {
			for _, p := range f.Loggers {
				if l, err := wlog.ParseLevel(p.Level); err == nil {
					out[p.Name] = l
				}
			}
		}
	}
	for name, l := range defs {
		if _, ok := out[name]; !ok {
			out[name] = l
		}
	}
	return out
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var errs []error
	for _, c := range m {
		errs = append(errs, c.Close())
	}
	return errors.Join(errs...)
}

// namedRouter は名前付きの Logger（wenteasy/log の Named）の記録を、その名前のファイルへ振り分ける。
// 名前は Named が WithAttrs で付ける印（wlog.LoggerAttr）で分かるので、印を受け取った派生だけが
// 名前のファイルへ書く。出口が無い名前（ファイルを作れなかった・分けない名前）は本体へ出す（ログを失わない）。
// ⚠️ **Warn 以上は本体にも出す** —— 名前のファイルはふだん見ないので、警告を見落とさないため。
type namedRouter struct {
	main  slog.Handler
	named map[string]slog.Handler
	to    slog.Handler // 印を受け取った派生の行き先（無ければ nil = 本体）
}

func (h *namedRouter) Enabled(ctx context.Context, l slog.Level) bool {
	if h.to != nil && h.to.Enabled(ctx, l) {
		return true
	}
	return h.main.Enabled(ctx, l)
}

func (h *namedRouter) Handle(ctx context.Context, r slog.Record) error {
	if h.to == nil {
		return h.main.Handle(ctx, r)
	}
	err := h.to.Handle(ctx, r.Clone())
	if r.Level < LevelWarn {
		return err
	}
	return errors.Join(err, h.main.Handle(ctx, r))
}

func (h *namedRouter) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := h.with(func(x slog.Handler) slog.Handler { return x.WithAttrs(attrs) })
	for _, a := range attrs {
		for name, sink := range nh.named {
			if a.Equal(wlog.LoggerAttr(name)) {
				nh.to = sink
			}
		}
	}
	return nh
}

func (h *namedRouter) WithGroup(name string) slog.Handler {
	return h.with(func(x slog.Handler) slog.Handler { return x.WithGroup(name) })
}

func (h *namedRouter) with(f func(slog.Handler) slog.Handler) *namedRouter {
	nh := &namedRouter{main: f(h.main), named: make(map[string]slog.Handler, len(h.named))}
	for k, x := range h.named {
		y := f(x)
		nh.named[k] = y
		if x == h.to {
			nh.to = y
		}
	}
	return nh
}

// switchHandler は on が偽のあいだ何も書かない（名前付きの Logger の出口を設定で切り替える。`SetFollow`）。
// ⚠️ **派生（WithAttrs / WithGroup）でも同じ on を見ること**（log.Follow は Init より前に作られ、書くたびに派生する）。
type switchHandler struct {
	on *atomic.Bool
	h  slog.Handler
}

func (s *switchHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return s.on.Load() && s.h.Enabled(ctx, l)
}

func (s *switchHandler) Handle(ctx context.Context, r slog.Record) error {
	if !s.on.Load() {
		return nil
	}
	return s.h.Handle(ctx, r)
}

func (s *switchHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &switchHandler{on: s.on, h: s.h.WithAttrs(attrs)}
}

func (s *switchHandler) WithGroup(name string) slog.Handler {
	return &switchHandler{on: s.on, h: s.h.WithGroup(name)}
}

// removeOld は KeepDays より古いログファイルを消す。日付はファイル名から読む
// （更新時刻は見ない。コピーや展開で変わるため）。消せなくても何もしない。
func removeOld(dir string, now time.Time) {
	prefixes := []string{FilePrefix}
	for _, name := range separated {
		prefixes = append(prefixes, NamedFile(name))
	}
	limit := now.AddDate(0, 0, -KeepDays)
	for _, prefix := range prefixes {
		names, err := filepath.Glob(filepath.Join(dir, prefix+"_*.log"))
		if err != nil {
			continue
		}
		for _, path := range names {
			date := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), prefix+"_"), ".log")
			t, err := time.ParseInLocation("20060102", date, now.Location())
			if err != nil || !t.Before(limit) {
				continue
			}
			os.Remove(path)
		}
	}
}
