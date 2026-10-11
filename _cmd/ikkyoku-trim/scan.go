package main

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"strconv"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// defaultThreshold は盤の矩形の中で「変わった」と見なす点の割合の既定（`ikkyoku.FrameDiff`）。
//
// ⚠️ **迷ったら敏感な側に倒すこと** —— 余分に拾っても詰めた動画が数秒長くなるだけだが、見落とした変化は
// 詰めた動画から消えて戻らない（駒が少し動いた・手が盤に掛かったような小さな変化こそ残したい）。
// ⚠️ **当て推量**（2026-10-11）。配信の圧縮の揺らぎで毎コマ変化になるなら上げる。JSON の `quiet`
// （変化と見なさなかったコマの分布）を見て詰める（README.md の「線を詰める」）。
const defaultThreshold = 0.0005

// defaultScene は画面全体で「切り替わった」と見なす割合の既定（大盤・解説者の画面・CM への切り替え）。
// 画面全体には持ち時間の表示や字幕があるので、小さな変化は拾わない。⚠️ **当て推量**（2026-10-11）。
const defaultScene = 0.3

// redetectEvery は盤が見つかっていないあいだ、何秒ごとに探し直すか。
const redetectEvery = 2.0

// scan は動画を順に流し読みして、変化と見なしたコマを集め、残す区間を組む（1 段目）。
//
// ⚠️ **持つのは 1 つ前のコマと今のコマだけ**（動画を丸ごと展開しない）。
// ⚠️ **1 つ前のコマとの比べは動画の境目をまたいで続ける**（分けて録ったあいだに盤が変わっていれば、それも変化）。
func scan(o options, inputs []Input) (Plan, error) {
	p := Plan{Inputs: inputs, Before: o.before.Seconds(), After: o.after.Seconds(), FPS: o.fps,
		Threshold: o.threshold, Scene: o.scene, Quiet: newHistogram()}
	step := 1 / o.fps
	var prev *image.RGBA
	var board image.Rectangle
	for _, in := range inputs {
		frame := image.Rect(0, 0, in.Width, in.Height)
		// 盤の矩形: -region があればそれ。⚠️ **画面の大きさが違う動画に変わったら探し直す**（前の矩形を黙って使わない）。
		switch {
		case !o.fixed.Empty():
			board = o.fixed.Intersect(frame)
			p.Boards = append(p.Boards, BoardNote{File: in.File, Rect: board, How: "region"})
		case prev == nil || !prev.Bounds().Eq(frame):
			board = image.Rectangle{}
		}
		redetect := o.fixed.Empty() && board.Empty()
		lastTry := -redetectEvery
		fmt.Fprintf(os.Stderr, "%s を読みます（%dx%d・%s）\n", in.File, in.Width, in.Height, seconds(in.Duration))
		nextReport := 600.0
		err := decodeFrames(o.ffmpeg, in, o.fps, func(cur *image.RGBA, t float64) error {
			if redetect && t-lastTry >= redetectEvery {
				lastTry = t
				if r, err := recognize.DetectRegion(cur); err == nil {
					redetect = false
					if r.Rect != board {
						board = r.Rect
						p.Boards = append(p.Boards, BoardNote{File: in.File, Time: t, Rect: r.Rect, Confidence: r.Confidence, How: "detect"})
						fmt.Fprintf(os.Stderr, "  %s: 盤を見つけました %v（信頼度 %.2f）\n", seconds(t), r.Rect, r.Confidence)
					}
				}
			}
			if prev != nil {
				c, changed := judge(prev, cur, board, o.threshold, o.scene)
				if changed {
					c.File, c.Time, c.At = in.File, t, in.Offset+t
					p.Changes = append(p.Changes, c)
					// 画面が切り替わったら盤の位置も変わりうるので探し直す（-region のときは探さない）。
					// ⚠️ **見つかるまで前の矩形で測り続ける**（捨てると、探せないあいだの手が詰めた動画から消える）。
					if c.Scene >= o.scene && o.fixed.Empty() && !redetect {
						redetect, lastTry = true, t-redetectEvery
					}
				} else if c.Board >= 0 {
					p.Quiet.add(c.Board)
				}
			}
			if t >= nextReport {
				fmt.Fprintf(os.Stderr, "  %s / %s（変化 %d 回）\n", seconds(t), seconds(in.Duration), len(p.Changes))
				nextReport += 600
			}
			// ⚠️ 今のコマを 1 つ前のコマにする。バッファは decodeFrames が入れ替えて使い回す。
			prev = cur
			return nil
		})
		if err != nil {
			return Plan{}, err
		}
	}
	p.Segments, p.Kept = buildSegments(inputs, p.Changes, p.Before, p.After, step)
	last := inputs[len(inputs)-1]
	p.Total = last.Offset + last.Duration
	return p, nil
}

// judge は 1 つ前のコマ a と今のコマ b を比べて、変化と見なすかを返す。board が空なら盤は見ない。
//
// ⚠️ **比べられないとき（大きさが違う）は変化に倒す**（`ikkyoku.FrameDiff` と同じ考え。見落とすより残すほうが軽い）。
func judge(a, b *image.RGBA, board image.Rectangle, threshold, scene float64) (Change, bool) {
	c := Change{Board: -1, Scene: -1}
	s, ok := ikkyoku.FrameDiff(a, b, b.Bounds())
	if !ok {
		c.Why = "画面の大きさが変わった"
		return c, true
	}
	c.Scene = s
	if !board.Empty() {
		if d, ok := ikkyoku.FrameDiff(a, b, board); ok {
			c.Board = d
		}
	}
	switch {
	case c.Board >= threshold:
		c.Why = "盤"
		return c, true
	case s >= scene:
		c.Why = "画面"
		return c, true
	}
	return c, false
}

// decodeFrames は ffmpeg に動画をデコードさせ、1 秒 fps コマの RGBA を順に fn へ渡す（t はその動画の中の秒）。
//
// ⚠️ **バッファは 2 つだけ使い回す**（fn には呼ぶたびに入れ替えて渡すので、fn が 1 つ前のコマを持っていても壊れない）。
func decodeFrames(ffmpeg string, in Input, fps float64, fn func(img *image.RGBA, t float64) error) error {
	cmd := exec.Command(ffmpeg, "-v", "error", "-nostdin", "-i", in.File, "-map", "0:v:0",
		"-vf", "fps="+strconv.FormatFloat(fps, 'f', -1, 64), "-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1")
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg を起動できません（ffmpeg はありますか）: %w", err)
	}
	r := bufio.NewReaderSize(out, 1<<20)
	size := in.Width * in.Height * 4
	bufs := [2]*image.RGBA{
		{Pix: make([]byte, size), Stride: in.Width * 4, Rect: image.Rect(0, 0, in.Width, in.Height)},
		{Pix: make([]byte, size), Stride: in.Width * 4, Rect: image.Rect(0, 0, in.Width, in.Height)},
	}
	var ferr error
	for k := 0; ; k++ {
		img := bufs[k%2]
		if _, err := io.ReadFull(r, img.Pix); err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				ferr = err
			}
			break
		}
		if ferr = fn(img, float64(k)/fps); ferr != nil {
			break
		}
	}
	if ferr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return ferr
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s を ffmpeg で読めません: %w", in.File, err)
	}
	return nil
}
