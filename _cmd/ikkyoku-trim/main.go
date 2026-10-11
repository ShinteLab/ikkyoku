// ikkyoku-trim は録画した動画を「盤が変わった前後だけ」に詰める（2026-10-11）。使い方と仕組みは README.md。
//
// 将棋の対局を録画ソフトで録った動画（何時間にもなる）から、盤が変わった前後（既定は 10 秒ずつ）だけを残してつなぎ、
// 1 本の短い動画にする。動画を詰めるだけの道具で、ikkyoku アプリとは関わらない。
//
//	go run ./_cmd/ikkyoku-trim [フラグ] <動画ファイルかディレクトリ>...
//
// 2 段で動く。⚠️ **動画を丸ごと展開しない**（どちらの段も ffmpeg が流しながら処理し、メモリは長さに関係なく一定）:
//
//  1. 変化を探す（`scan.go`）: ffmpeg に 1 秒 4 コマでデコードさせ、1 つ前のコマと比べて変わった時刻を集める。
//     残す区間を JSON に書く（`-segments` で、手で直した JSON から 2 段目だけをやり直せる）
//  2. 切ってつなぐ（`cut.go`）: 区間を ffmpeg の select に渡し、1 本の動画に書き出す
//
// ikkyoku から借りるのは `ikkyoku.FrameDiff`（変化を測る）と `recognize.DetectRegion`（盤の矩形を探す）だけ。
// ⚠️ **ffmpeg は外部のコマンドとして呼ぶ**（cgo で繋がない。PureGo の方針）。
package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/recognize"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ikkyoku-trim:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("ikkyoku-trim", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "使い方: ikkyoku-trim [フラグ] <動画ファイルかディレクトリ>...")
		fmt.Fprintln(fs.Output(), "  ディレクトリはその中の動画ファイルを名前の順に、並べたファイルは渡した順に、1 本の続いた録画として読む")
		fs.PrintDefaults()
	}
	var o options
	fs.StringVar(&o.out, "o", "", "書き出す動画（既定は最初の入力の隣の <名前>-trim.mp4。区間の JSON は同じ名前の .json）")
	fs.DurationVar(&o.before, "before", 10*time.Second, "変化の前に残す長さ")
	fs.DurationVar(&o.after, "after", 10*time.Second, "変化の後に残す長さ")
	fs.Float64Var(&o.fps, "fps", 4, "変化を探すときに 1 秒あたり何コマ見るか")
	fs.Float64Var(&o.threshold, "threshold", defaultThreshold, "盤の矩形の中で、明るさが変わった点の割合がこれ以上なら変化")
	fs.Float64Var(&o.scene, "scene", defaultScene, "画面全体か対局の盤の中で、明るさが変わった点の割合がこれ以上なら、対局の盤がまだ映っているかを確かめる")
	fs.DurationVar(&o.switchKeep, "switch", 2*time.Second, "対局の盤が映らなくなった・戻ったときに残す長さ（戻ったあとは -after）")
	fs.StringVar(&o.region, "region", "", "対局の盤の矩形を動画の画素で x,y,w,h と指定する（既定は最初に見つけた盤）")
	fs.StringVar(&o.segments, "segments", "", "変化を探さず、この JSON の区間で切ってつなぐ（1 段目の JSON を手で直したとき）")
	fs.BoolVar(&o.dryRun, "dry-run", false, "変化を探して JSON を書くだけ（動画は書き出さない）")
	fs.BoolVar(&o.force, "force", false, "書き出す先が既にあっても上書きする")
	fs.IntVar(&o.crf, "crf", 12, "書き出す動画の画質（libx264 の CRF。小さいほど元に近い）")
	fs.StringVar(&o.ffmpeg, "ffmpeg", "ffmpeg", "ffmpeg のパス")
	fs.StringVar(&o.ffprobe, "ffprobe", "ffprobe", "ffprobe のパス")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	o.detect = recognize.DetectRegion
	if o.fps <= 0 {
		return fmt.Errorf("-fps は 0 より大きくしてください")
	}
	if o.region != "" {
		r, err := ikkyoku.ParseRegion(o.region)
		if err != nil {
			return fmt.Errorf("-region を読めません: %w", err)
		}
		o.fixed = r.Rect()
	}

	var plan Plan
	if o.segments != "" {
		p, err := readPlan(o.segments)
		if err != nil {
			return err
		}
		plan = p
		if o.out == "" {
			o.out = strings.TrimSuffix(o.segments, filepath.Ext(o.segments)) + ".mp4"
		}
	} else {
		if fs.NArg() == 0 {
			fs.Usage()
			return fmt.Errorf("動画ファイルかディレクトリを渡してください")
		}
		files, err := expandInputs(fs.Args())
		if err != nil {
			return err
		}
		if o.out == "" {
			o.out = defaultOut(fs.Args()[0], files[0])
		}
		inputs, err := probeAll(o.ffprobe, files)
		if err != nil {
			return err
		}
		o.boardImage = strings.TrimSuffix(o.out, filepath.Ext(o.out)) + "-board.png"
		p, err := scan(o, inputs)
		if err != nil {
			return err
		}
		plan = p
		jsonPath := strings.TrimSuffix(o.out, filepath.Ext(o.out)) + ".json"
		if err := writePlan(jsonPath, plan); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "区間を書きました: %s（変化 %d 回・区間 %d・残す長さ %s / 元 %s）\n",
			jsonPath, len(plan.Changes), len(plan.Segments), seconds(plan.Kept), seconds(plan.Total))
	}
	if o.dryRun {
		return nil
	}
	if !o.force {
		if _, err := os.Stat(o.out); err == nil {
			return fmt.Errorf("%s は既にあります（上書きするなら -force）", o.out)
		}
	}
	if err := cut(o, plan); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "書き出しました: %s\n", o.out)
	return nil
}

// options はフラグをまとめたもの。
type options struct {
	out           string
	before, after time.Duration
	fps           float64
	threshold     float64
	scene         float64
	region        string
	segments      string
	dryRun        bool
	force         bool
	crf           int
	ffmpeg        string
	ffprobe       string
	switchKeep    time.Duration
	// fixed は -region で指定した対局の盤の矩形（空なら最初に見つけた盤）。
	fixed image.Rectangle
	// boardImage は対局の盤を決めたコマを切り出して残す先（空なら残さない）。
	boardImage string
	// detect は盤を探す関数（既定は recognize.DetectRegion。テストで差し替える）。
	detect detector
}

// seconds は秒数を「1h02m03s」の形にする。
func seconds(s float64) string {
	return time.Duration(s * float64(time.Second)).Round(time.Second).String()
}

// savePNG は img を path に PNG で書く（対局の盤を決めたコマ。大盤を選んでいないか目で確かめるため）。
func savePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
