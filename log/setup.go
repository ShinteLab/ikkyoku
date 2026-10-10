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
	// 話題（Topic）のファイルは FilePrefix-<話題>（ikkyoku-follow_20261003.log）。
	FilePrefix = "ikkyoku"
	// LevelsFile はパッケージごとのレベルの設定。Dir に置けば Init が読む（無ければ defaultLevels）。
	// 形は wenteasy/log の LevelsFile:
	//
	//	{"root": "INFO", "packages": [{"name": "github.com/ShinteLab/ikkyoku/app", "level": "DEBUG"}]}
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

// topics は本体から分けてファイルへ出す話題（log.go の Topic）。
var topics = []Topic{Follow}

// TopicFile は話題のログファイルの名前の頭（"ikkyoku-follow"）。
func TopicFile(t Topic) string { return FilePrefix + "-" + t.name }

// Init はログの出口を組み、slog.Default() をそこへ向ける。main の最初に 1 回だけ呼ぶ。
//
//   - ファイル: Dir/ikkyoku_<日付>.log（日ごと。keepDays より古いものは消す）
//   - 話題（log.Follow など）: Dir/ikkyoku-<話題>_<日付>.log だけ（コンソールに出さない。Warn 以上は本体にも）
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
	topicSinks := map[string]slog.Handler{}
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
			// 話題のファイルは書いたときに開く（追わなかった日は作られない）。
			for _, t := range topics {
				tw, err := wlog.NewRollingFileWriter(dir, wlog.Day)
				if err != nil {
					continue // 出口が無い話題は本体へ出る（topicHandler）
				}
				tw.SetPrefix(TopicFile(t))
				topicSinks[t.name] = wlog.NewSimpleHandler(tw, LevelTrace)
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
	h := wlog.NewPackageLevelHandler(&topicHandler{main: sink, topics: topicSinks}, root)
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

// topicHandler は話題（log.go の Topic）の付いた記録を話題のファイルへ振り分ける。
// 話題の出口が無い（ファイルを作れなかった）ときは本体へ出す（ログを失わない）。
// ⚠️ **Warn 以上は本体にも出す** —— 話題のファイルはふだん見ないので、警告を見落とさないため。
type topicHandler struct {
	main   slog.Handler
	topics map[string]slog.Handler
}

func (h *topicHandler) Enabled(ctx context.Context, l slog.Level) bool {
	if t, ok := h.topics[topicOf(ctx)]; ok && t.Enabled(ctx, l) {
		return true
	}
	return h.main.Enabled(ctx, l)
}

func (h *topicHandler) Handle(ctx context.Context, r slog.Record) error {
	t, ok := h.topics[topicOf(ctx)]
	if !ok {
		return h.main.Handle(ctx, r)
	}
	err := t.Handle(ctx, r.Clone())
	if r.Level < LevelWarn {
		return err
	}
	return errors.Join(err, h.main.Handle(ctx, r))
}

func (h *topicHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.with(func(x slog.Handler) slog.Handler { return x.WithAttrs(attrs) })
}

func (h *topicHandler) WithGroup(name string) slog.Handler {
	return h.with(func(x slog.Handler) slog.Handler { return x.WithGroup(name) })
}

func (h *topicHandler) with(f func(slog.Handler) slog.Handler) slog.Handler {
	ts := make(map[string]slog.Handler, len(h.topics))
	for k, t := range h.topics {
		ts[k] = f(t)
	}
	return &topicHandler{main: f(h.main), topics: ts}
}

// removeOld は keepDays より古いログファイルを消す。日付はファイル名から読む
// （更新時刻は見ない。コピーや展開で変わるため）。消せなくても何もしない。
func removeOld(dir string, now time.Time) {
	prefixes := []string{FilePrefix}
	for _, t := range topics {
		prefixes = append(prefixes, TopicFile(t))
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
