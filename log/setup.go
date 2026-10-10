package log

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	wlog "github.com/wenteasy/log"
)

// Options は Init の設定。
type Options struct {
	// Dir はログファイルと設定（LevelsFile）を置くディレクトリ。空なら実行ファイルのあるディレクトリ。
	Dir string
	// Console は標準エラーにも出すか。wails3 dev で動かすとき（ターミナルで見るとき）に立てる。
	// ⚠️ 配る exe（-H windowsgui）には標準エラーの行き先が無いので、立てても見えない。
	Console bool
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
	// keepDays より古いログファイルは Init のときに消す。
	keepDays = 14
)

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

// separated は本体から分けて自分のファイルへ出す名前付きの Logger の名前（log.go の Follow）。
var separated = []string{FollowName}

// NamedFile は名前付きの Logger のログファイルの名前の頭（"ikkyoku-follow"）。
func NamedFile(name string) string { return FilePrefix + "-" + name }

// Init はログの出口を組み、slog.Default() をそこへ向ける。main の最初に 1 回だけ呼ぶ。
//
//   - ファイル: Dir/ikkyoku_<日付>.log（日ごと。keepDays より古いものは消す）
//   - 名前付き（log.Follow）: Dir/ikkyoku-<名前>_<日付>.log だけ（コンソールに出さない。Warn 以上は本体にも）
//   - コンソール: Options.Console のときだけ標準エラーにも
//   - レベル: 呼び出し元のパッケージごと（Dir/LevelsFile。無ければ defaultLevels）
//
// ⚠️ **ログが書けなくてもアプリは動かす**（設計原則3）。ファイルを作れないときも、出せる
// 出口（コンソール）だけで組んで slog.Default() を差し替え、理由をエラーで返す。
// 返す io.Closer は必ず nil ではない（終了時に Close する）。
func Init(opts Options) (io.Closer, error) {
	var errs []error
	fileDir = ""
	dir := opts.Dir
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			errs = append(errs, fmt.Errorf("実行ファイルの場所が分かりません: %w", err))
		} else {
			dir = filepath.Dir(exe)
		}
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
				tw, err := wlog.NewRollingFileWriter(dir, wlog.Day)
				if err != nil {
					continue // 出口が無い名前は本体へ出る（namedRouter）
				}
				tw.SetPrefix(NamedFile(name))
				namedSinks[name] = wlog.NewSimpleHandler(tw, LevelTrace)
				closers = append(closers, tw)
			}
		}
	}
	if opts.Console {
		sinks = append(sinks, wlog.NewSimpleHandler(os.Stderr, LevelTrace))
	}

	// 出口は一番低いレベルで作り、絞るのはパッケージごとのレベルに任せる
	// （出口が出さないレベルは、パッケージのレベルを下げても出ない）。
	root, pkgs := defaultLevels()
	sink := slog.NewMultiHandler(sinks...)
	h := wlog.NewPackageLevelHandler(&namedRouter{main: sink, named: namedSinks}, root)
	h.SetLevels(root, pkgs)
	levels := "既定"
	if dir != "" {
		path := filepath.Join(dir, LevelsFile)
		switch err := h.LoadJSON(path); {
		case err == nil:
			levels = path
		case errors.Is(err, fs.ErrNotExist):
		default:
			// 読めない設定は使わず、既定のまま続ける（LoadJSON は失敗したら変えない）。
			errs = append(errs, fmt.Errorf("%s を読めません（既定のレベルで続けます）: %w", LevelsFile, err))
		}
	}
	slog.SetDefault(slog.New(h))

	if dir != "" {
		removeOld(dir, time.Now())
	}
	// 始まりの 1 行は、レベルの設定に関係なく必ず残す（どの設定で動いているかを後から読むため）。
	r := slog.NewRecord(time.Now(), LevelNotice, "ログを開始しました", 0)
	r.Add("file", file, "console", opts.Console, "levels", levels)
	_ = sink.Handle(context.Background(), r)
	return closers, errors.Join(errs...)
}

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

// removeOld は keepDays より古いログファイルを消す。日付はファイル名から読む
// （更新時刻は見ない。コピーや展開で変わるため）。消せなくても何もしない。
func removeOld(dir string, now time.Time) {
	prefixes := []string{FilePrefix}
	for _, name := range separated {
		prefixes = append(prefixes, NamedFile(name))
	}
	limit := now.AddDate(0, 0, -keepDays)
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
