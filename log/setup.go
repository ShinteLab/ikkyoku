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

// Init はログの出口を組み、slog.Default() をそこへ向ける。main の最初に 1 回だけ呼ぶ。
//
//   - ファイル: Dir/ikkyoku_<日付>.log（日ごと。keepDays より古いものは消す）
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
	var closer io.Closer = nopCloser{}
	var file string
	if dir != "" {
		w, err := wlog.NewRollingFileWriter(dir, wlog.Day)
		if err != nil {
			errs = append(errs, fmt.Errorf("ログのディレクトリを作れません: %w", err))
		} else {
			w.SetPrefix(FilePrefix)
			sinks = append(sinks, wlog.NewSimpleHandler(w, LevelTrace))
			closer = w
			file = filepath.Join(dir, FilePrefix+"_"+time.Now().Format("20060102")+".log")
			fileDir = dir
		}
	}
	if opts.Console {
		sinks = append(sinks, wlog.NewSimpleHandler(os.Stderr, LevelTrace))
	}

	// 出口は一番低いレベルで作り、絞るのはパッケージごとのレベルに任せる
	// （出口が出さないレベルは、パッケージのレベルを下げても出ない）。
	root, pkgs := defaultLevels()
	sink := slog.NewMultiHandler(sinks...)
	h := wlog.NewPackageLevelHandler(sink, root)
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
	return closer, errors.Join(errs...)
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// removeOld は keepDays より古いログファイルを消す。日付はファイル名から読む
// （更新時刻は見ない。コピーや展開で変わるため）。消せなくても何もしない。
func removeOld(dir string, now time.Time) {
	names, err := filepath.Glob(filepath.Join(dir, FilePrefix+"_*.log"))
	if err != nil {
		return
	}
	limit := now.AddDate(0, 0, -keepDays)
	for _, path := range names {
		date := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), FilePrefix+"_"), ".log")
		t, err := time.ParseInLocation("20060102", date, now.Location())
		if err != nil || !t.Before(limit) {
			continue
		}
		os.Remove(path)
	}
}
