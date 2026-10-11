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

// defaultThreshold は対局の盤の矩形の中で「変わった」と見なす点の割合の既定（`ikkyoku.FrameDiff`）。
//
// ⚠️ **迷ったら敏感な側に倒すこと** —— 余分に拾っても詰めた動画が数秒長くなるだけだが、見落とした変化は
// 詰めた動画から消えて戻らない（駒が少し動いた・手が盤に掛かったような小さな変化こそ残したい）。
// ⚠️ **当て推量**（2026-10-11）。配信の圧縮の揺らぎで毎コマ変化になるなら上げる。JSON の `quiet`
// （変化と見なさなかったコマの分布）を見て詰める（README.md の「線を詰める」）。
const defaultThreshold = 0.0005

// defaultScene は「画面が切り替わったかもしれない」と見なす割合の既定（画面全体か、対局の盤の矩形の中）。
// 越えたら盤を探し直して、対局の盤がまだ映っているかを確かめる。画面全体には持ち時間の表示や字幕があるので、
// 小さな変化では確かめない。⚠️ **当て推量**（2026-10-11）。
const defaultScene = 0.3

// redetectEvery は、対局の盤が映っていないあいだ（と、まだ見つけていないあいだ）何秒ごとに盤を探すか。
const redetectEvery = 2.0

// 変化の理由（`Change.Why`）。
const (
	whyBoard  = "盤"
	whyHidden = "対局の盤が映らなくなった"
	whyBack   = "対局の盤が戻った"
)

// scan は動画を順に流し読みして、変化と見なしたコマを集め、残す区間を組む（1 段目）。
//
// **対局の盤は `-region`、無ければ最初に見つけた盤**（2026-10-11。ユーザーの判断）。⚠️ **それ以外の盤（大盤など）は測らない**
// —— 詰めた動画で大事なのは対局の盤面の流れで、大盤の解説の動きは要らない。対局の盤が映らなくなった瞬間と戻った瞬間だけを
// 残す（戻ったときは、映っていないあいだに指された手で盤が変わっているので、戻ったあとを `after` ぶん残す）。
// ⚠️ **最初に見つけた盤を対局の盤にするので、録画の頭が大盤だと誤る**（`-region` で渡す。決めた盤は画像に切り出して残す）。
//
// ⚠️ **持つのは 1 つ前のコマと今のコマだけ**（動画を丸ごと展開しない）。
// ⚠️ **1 つ前のコマとの比べは動画の境目をまたいで続ける**（分けて録ったあいだに盤が変わっていれば、それも変化）。
func scan(o options, inputs []Input) (Plan, error) {
	p := Plan{Inputs: inputs, Before: o.before.Seconds(), After: o.after.Seconds(), FPS: o.fps,
		Threshold: o.threshold, Scene: o.scene, Switch: o.switchKeep.Seconds(), Quiet: newHistogram()}
	step := 1 / o.fps
	sw := o.switchKeep.Seconds()
	var prev *image.RGBA
	// g は対局の盤（見つけるまで空）。visible は今それが映っているか。
	g := gameBoard{detect: o.detect, scene: o.scene}
	game := &g.sig
	visible := false
	if !o.fixed.Empty() && len(inputs) > 0 {
		*game = recognize.Signature{Frame: image.Rect(0, 0, inputs[0].Width, inputs[0].Height), Board: o.fixed}
		visible = true
		p.Boards = append(p.Boards, BoardNote{File: inputs[0].File, Rect: o.fixed, How: "region"})
	}
	for _, in := range inputs {
		frame := image.Rect(0, 0, in.Width, in.Height)
		// ⚠️ **画面の大きさが違う動画に変わったら、対局の盤を大きさの比で引き伸ばす**（探し直すと大盤を拾いうる）。
		if !game.Board.Empty() && !game.Frame.Eq(frame) {
			*game = recognize.Signature{Frame: frame, Board: scaleRect(game.Board, game.Frame, frame)}
			g.seen = nil // 前の大きさの画面の盤は比べられない
			p.Boards = append(p.Boards, BoardNote{File: in.File, Rect: game.Board, How: "scaled"})
		}
		lastTry := -redetectEvery
		fmt.Fprintf(os.Stderr, "%s を読みます（%dx%d・%s）\n", in.File, in.Width, in.Height, seconds(in.Duration))
		nextReport := 600.0
		err := decodeFrames(o.ffmpeg, in, o.fps, func(cur *image.RGBA, t float64) error {
			defer func() {
				prev = cur // ⚠️ バッファは decodeFrames が入れ替えて使い回す
				if visible {
					g.remember(cur)
				}
			}()
			add := func(c Change, before, after float64) {
				c.File, c.Time, c.At, c.Before, c.After = in.File, t, in.Offset+t, before, after
				p.Changes = append(p.Changes, c)
			}
			if t >= nextReport {
				fmt.Fprintf(os.Stderr, "  %s / %s（変化 %d 回）\n", seconds(t), seconds(in.Duration), len(p.Changes))
				nextReport += 600
			}
			// まだ対局の盤を決めていない: 見つけたらそれにする（それまでは何も残さない）。
			if game.Board.Empty() {
				if t-lastTry < redetectEvery {
					return nil
				}
				lastTry = t
				if r, err := o.detect(cur); err == nil {
					*game, visible = recognize.Signature{Frame: frame, Board: r.Rect}, true
					p.Boards = append(p.Boards, BoardNote{File: in.File, Time: t, Rect: r.Rect, Confidence: r.Confidence, How: "detect"})
					fmt.Fprintf(os.Stderr, "  %s: 対局の盤を決めました %v（信頼度 %.2f）\n", seconds(t), r.Rect, r.Confidence)
					if o.boardImage != "" {
						if err := savePNG(o.boardImage, cur.SubImage(r.Rect.Inset(-8).Intersect(frame))); err != nil {
							fmt.Fprintf(os.Stderr, "  ⚠ 対局の盤の画像を残せません: %v\n", err)
						}
					}
				}
				return nil
			}
			// 対局の盤が映っていない: 探して、戻ったら残す。
			if !visible {
				if t-lastTry < redetectEvery {
					return nil
				}
				lastTry = t
				if g.shows(cur) {
					visible = true
					// 戻ったのは前に探したときより後なので、前は探す間隔ぶん余分に残す。
					add(Change{Board: -1, Scene: -1, Why: whyBack}, sw+redetectEvery, p.After)
				}
				return nil
			}
			if prev == nil {
				return nil
			}
			c := Change{Board: -1, Scene: -1}
			s, ok := ikkyoku.FrameDiff(prev, cur, frame)
			if ok {
				c.Scene = s
			}
			if d, ok := ikkyoku.FrameDiff(prev, cur, game.Board); ok {
				c.Board = d
			}
			// 大きく変わったら（比べられないときも）、対局の盤がまだ映っているかを確かめる。
			if !ok || c.Scene >= o.scene || c.Board >= o.scene {
				if !g.shows(cur) {
					visible, lastTry = false, t
					c.Why = whyHidden
					add(c, sw, sw)
					return nil
				}
			}
			if c.Board < 0 || c.Board >= o.threshold {
				c.Why = whyBoard
				add(c, p.Before, p.After)
				return nil
			}
			p.Quiet.add(c.Board)
			return nil
		})
		if err != nil {
			return Plan{}, err
		}
	}
	if game.Board.Empty() {
		fmt.Fprintln(os.Stderr, "⚠ 盤が一度も見つかりませんでした（-region で盤の位置を渡してください）")
	}
	p.Segments, p.Kept = buildSegments(inputs, p.Changes, step)
	last := inputs[len(inputs)-1]
	p.Total = last.Offset + last.Duration
	return p, nil
}

// gameBoard は対局の盤と、それが映っているかの見分け方。
type gameBoard struct {
	detect detector
	scene  float64
	sig    recognize.Signature
	// seen は最後に対局の盤が映っていたコマの、盤の矩形の部分の写し（盤を探せないときの比べる相手）。
	seen *image.RGBA
}

// shows は、今のコマに対局の盤が映っているかを返す。
//
//   - 盤が見つかれば、対局の盤と同じ大きさ・位置か（ikkyoku の追従と同じ `recognize.Signature.Matches`。地色は見ない）。
//     **違う盤（大盤など）なら映っていない**
//   - ⚠️ **盤が見つからなければ、最後に映っていたときの盤の部分と見比べる**（`seen`）。盤の検出が苦手な画面
//     （だから `-region` を渡した）で、一度「映っていない」になったら二度と戻らない、を防ぐ。見比べて大きく違わなければ映っている
func (g *gameBoard) shows(img *image.RGBA) bool {
	if r, err := g.detect(img); err == nil {
		same, _ := g.sig.Matches(recognize.Signature{Frame: img.Bounds(), Board: r.Rect})
		return same
	}
	if g.seen == nil {
		return false
	}
	cur, ok := img.SubImage(g.sig.Board).(*image.RGBA)
	if !ok {
		return false
	}
	d, ok := ikkyoku.FrameDiff(g.seen, cur, g.sig.Board)
	return ok && d < g.scene
}

// remember は対局の盤が映っているコマの、盤の矩形の部分を写して持つ（バッファは使い回す）。
func (g *gameBoard) remember(img *image.RGBA) {
	r := g.sig.Board.Intersect(img.Bounds())
	if r.Empty() {
		return
	}
	if g.seen == nil || !g.seen.Rect.Eq(r) {
		g.seen = image.NewRGBA(r)
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := img.Pix[img.PixOffset(r.Min.X, y):img.PixOffset(r.Max.X, y)]
		copy(g.seen.Pix[g.seen.PixOffset(r.Min.X, y):], src)
	}
}

// detector は盤を探す関数（既定は `recognize.DetectRegion`。テストで差し替える）。
type detector func(img image.Image) (recognize.BoardRegion, error)

// scaleRect は from の大きさの画面での矩形 r を、to の大きさの画面へ比で写す。
func scaleRect(r, from, to image.Rectangle) image.Rectangle {
	if from.Dx() <= 0 || from.Dy() <= 0 {
		return r
	}
	sx := float64(to.Dx()) / float64(from.Dx())
	sy := float64(to.Dy()) / float64(from.Dy())
	return image.Rect(int(float64(r.Min.X)*sx), int(float64(r.Min.Y)*sy), int(float64(r.Max.X)*sx), int(float64(r.Max.Y)*sy))
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
