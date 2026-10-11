package main

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ShinteLab/ikkyoku/recognize"
)

// ⚠️ **ディレクトリは中の動画ファイルを名前の順に並べ、書き出した動画（-trim）・動画でないもの・サブディレクトリは拾わないこと**。
// 並べたファイルは渡した順のまま（2026-10-11。録画ソフトが 1 時間を超えると分けて録るため）。
func TestExpandInputs(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"2026-10-11 10-00-00.mkv", "2026-10-11 09-00-00.mp4", "rec-trim.mp4", "memo.txt"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	os.Mkdir(filepath.Join(dir, "sub.mp4"), 0o755)

	got, err := expandInputs([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "2026-10-11 09-00-00.mp4"), filepath.Join(dir, "2026-10-11 10-00-00.mkv")}
	if !slices.Equal(got, want) {
		t.Errorf("expandInputs(dir) = %v, want %v", got, want)
	}

	files := []string{filepath.Join(dir, "2026-10-11 10-00-00.mkv"), filepath.Join(dir, "2026-10-11 09-00-00.mp4")}
	if got, _ := expandInputs(files); !slices.Equal(got, files) {
		t.Errorf("並べたファイルの順が変わりました: %v", got)
	}
	if _, err := expandInputs([]string{t.TempDir()}); err == nil {
		t.Errorf("動画の無いディレクトリでエラーになりません")
	}
}

// ⚠️ **変化の前後を残し、重なる区間はまとめ、つないだ録画の外は切り、動画の境目で分けること**。
func TestBuildSegments(t *testing.T) {
	inputs := []Input{{File: "a", Duration: 10, Offset: 0}, {File: "b", Duration: 10, Offset: 10}}
	changes := []Change{{At: 5, Before: 1, After: 1}, {At: 5.5, Before: 1, After: 1}, {At: 9.5, Before: 1, After: 1}, {At: 19.9, Before: 1, After: 1}}
	segs, kept := buildSegments(inputs, changes, 0.25)
	want := []Segment{
		{File: "a", Start: 3.75, End: 6.5}, // 5 と 5.5 はまとまる
		{File: "a", Start: 8.25, End: 10},  // 9.5 は境目をまたぐ
		{File: "b", Start: 0, End: 0.5},
		{File: "b", Start: 8.65, End: 10}, // 19.9 は録画の終わりで切る
	}
	if len(segs) != len(want) {
		t.Fatalf("segments = %+v, want %+v", segs, want)
	}
	for i := range want {
		if segs[i].File != want[i].File || math.Abs(segs[i].Start-want[i].Start) > 1e-9 || math.Abs(segs[i].End-want[i].End) > 1e-9 {
			t.Errorf("segments[%d] = %+v, want %+v", i, segs[i], want[i])
		}
	}
	if math.Abs(kept-(2.75+2.25+1.35)) > 1e-9 {
		t.Errorf("kept = %v", kept)
	}
}

func TestParseProbe(t *testing.T) {
	b := []byte(`{"streams":[{"width":1920,"height":1080,"avg_frame_rate":"30000/1001","r_frame_rate":"30/1"}],
		"format":{"duration":"3600.5","start_time":"1.4"}}`)
	in, err := parseProbe("x.ts", b)
	if err != nil {
		t.Fatal(err)
	}
	if in.Width != 1920 || in.Height != 1080 || in.Duration != 3600.5 || in.StartTime != 1.4 || math.Abs(in.FrameRate-29.97) > 0.01 {
		t.Errorf("parseProbe = %+v", in)
	}
	if _, err := parseProbe("y", []byte(`{"streams":[],"format":{"duration":"1"}}`)); err == nil {
		t.Errorf("映像の無い動画でエラーになりません")
	}
}

// ⚠️ **変化ごとの前後の長さが違っても、始まりの順に並べてまとめること**（後の変化の区間が前から始まりうる。
// 対局の盤が戻ったときは前を長めに残す）。
func TestBuildSegmentsSortsSpans(t *testing.T) {
	inputs := []Input{{File: "a", Duration: 20}}
	changes := []Change{{At: 5, Before: 1, After: 1}, {At: 9, Before: 6, After: 1}}
	segs, _ := buildSegments(inputs, changes, 0.25)
	if len(segs) != 1 || math.Abs(segs[0].Start-2.75) > 1e-9 || math.Abs(segs[0].End-10) > 1e-9 {
		t.Errorf("segments = %+v, want [{a 2.75 10}]", segs)
	}
}

// ⚠️ **区間の時刻に動画の最初の時刻を足し、大きさの違う動画は最初の動画に合わせてつなぐこと**。
func TestCutArgs(t *testing.T) {
	p := Plan{
		Inputs: []Input{
			{File: "a.mp4", Width: 1920, Height: 1080, FrameRate: 30},
			{File: "skip.mp4", Width: 1920, Height: 1080, FrameRate: 30},
			{File: "b.ts", Width: 1280, Height: 720, FrameRate: 60, StartTime: 1.5},
		},
		Segments: []Segment{{File: "a.mp4", Start: 1, End: 2}, {File: "a.mp4", Start: 5, End: 6}, {File: "b.ts", Start: 0, End: 3}},
	}
	script, args, err := cutArgs(p, "out.mp4", 12)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{
		"[0:v]select='between(t,1.000,2.000)+between(t,5.000,6.000)',setpts=N/(30)/TB[v0]",
		"[1:v]select='between(t,1.500,4.500)',setpts=N/(60)/TB,scale=1920:1080,setsar=1[v1]",
		"[v0][v1]concat=n=2:v=1:a=0[out]",
	} {
		if !strings.Contains(script, e) {
			t.Errorf("フィルタに %q がありません:\n%s", e, script)
		}
	}
	if slices.Contains(args, "skip.mp4") {
		t.Errorf("区間の無い動画を読んでいます: %v", args)
	}
	if args[len(args)-1] != "out.mp4" {
		t.Errorf("最後が書き出す先ではありません: %v", args)
	}
}

// ⚠️ **分けて録った 2 本を 1 本の続いた録画として詰め、変化の前後だけの 1 本を書き出すこと**（ffmpeg が無ければ飛ばす）。
//
// 1 本目は 3 秒で黒い四角が出る。2 本目は最初から黒い四角があり（境目で盤は変わらない）、2 秒で白い四角が出る。
// つないだ録画の 3 秒と 8 秒が変化で、前後 1 秒ずつ残す。
func TestTrimEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg がありません")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe がありません")
	}
	dir := t.TempDir()
	box := "drawbox=x=40:y=40:w=60:h=60:color=black:t=fill"
	make := func(name, vf string) {
		cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=gray:s=320x240:r=10:d=6",
			"-vf", vf, "-c:v", "libx264", "-pix_fmt", "yuv420p", filepath.Join(dir, name))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v\n%s", err, out)
		}
	}
	make("2026-10-11 09-00-00.mp4", box+":enable='gte(t,3)'")
	make("2026-10-11 10-00-00.mp4", box+",drawbox=x=200:y=120:w=60:h=60:color=white:t=fill:enable='gte(t,2)'")

	out := filepath.Join(t.TempDir(), "trim.mp4")
	if err := run([]string{"-region", "0,0,320,240", "-before", "1s", "-after", "1s", "-o", out, dir}); err != nil {
		t.Fatalf("run: %v", err)
	}
	p, err := readPlan(strings.TrimSuffix(out, ".mp4") + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var at []float64
	for _, c := range p.Changes {
		at = append(at, c.At)
	}
	if len(at) != 2 || math.Abs(at[0]-3) > 0.3 || math.Abs(at[1]-8) > 0.3 {
		t.Fatalf("変化 = %v, want [3 8] 付近", at)
	}
	if len(p.Segments) != 2 || p.Segments[0].File == p.Segments[1].File {
		t.Errorf("2 本それぞれから 1 区間ずつのはず: %+v", p.Segments)
	}
	in, err := probe("ffprobe", out)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(in.Duration-p.Kept) > 0.5 {
		t.Errorf("書き出した長さ %.2f 秒が、残す長さ %.2f 秒と違います", in.Duration, p.Kept)
	}
}

// colorBox は、img の中で色 (r, g, b) に近い画素を囲む矩形（無ければ空）。
func colorBox(img image.Image, want [3]uint8) image.Rectangle {
	var box image.Rectangle
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			c := [3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8)}
			near := true
			for i := range c {
				if d := int(c[i]) - int(want[i]); d > 60 || d < -60 {
					near = false
				}
			}
			if near {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return box
}

// boxDetector は「青い四角を対局の盤、赤い四角を大盤」と見なす盤の検出（テスト用）。青が無ければ赤を返す。
func boxDetector(img image.Image) (recognize.BoardRegion, error) {
	if r := colorBox(img, [3]uint8{0, 0, 255}); !r.Empty() {
		return recognize.BoardRegion{Rect: r, Confidence: 1}, nil
	}
	if r := colorBox(img, [3]uint8{255, 0, 0}); !r.Empty() {
		return recognize.BoardRegion{Rect: r, Confidence: 1}, nil
	}
	return recognize.BoardRegion{}, errors.New("盤がありません")
}

// ⚠️ **最初に見つけた盤を対局の盤にし、大盤が映っているあいだの駒の動きは残さないこと**（2026-10-11。ユーザーの判断）。
// 対局の盤が映らなくなった瞬間と戻った瞬間だけを残し、戻ったあとの対局の盤の変化は今までどおり残す（ffmpeg が無ければ飛ばす）。
//
// 青い四角（対局の盤）が 0〜4 秒と 8 秒から、赤い四角（大盤）が 4〜8 秒。大盤の上では 5 秒と 6 秒に駒が動き、
// 対局の盤には大盤のあいだに 1 つ（戻ったときに見える）、10 秒にもう 1 つ駒が来る。
func TestScanKeepsOnlyGameBoard(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg がありません")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe がありません")
	}
	file := filepath.Join(t.TempDir(), "rec.mp4")
	vf := strings.Join([]string{
		"drawbox=x=20:y=20:w=120:h=120:color=blue:t=fill:enable='lt(t,4)+gte(t,8)'",
		"drawbox=x=150:y=50:w=150:h=150:color=red:t=fill:enable='gte(t,4)*lt(t,8)'",
		"drawbox=x=170:y=70:w=20:h=20:color=black:t=fill:enable='gte(t,5)*lt(t,8)'",
		"drawbox=x=230:y=120:w=20:h=20:color=black:t=fill:enable='gte(t,6)*lt(t,8)'",
		"drawbox=x=40:y=40:w=20:h=20:color=black:t=fill:enable='gte(t,8)'",
		"drawbox=x=90:y=90:w=20:h=20:color=white:t=fill:enable='gte(t,10)'",
	}, ",")
	cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=gray:s=320x240:r=10:d=12",
		"-vf", vf, "-c:v", "libx264", "-pix_fmt", "yuv420p", file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	inputs, err := probeAll("ffprobe", []string{file})
	if err != nil {
		t.Fatal(err)
	}
	o := options{fps: 4, threshold: defaultThreshold, scene: defaultScene, before: time.Second, after: time.Second,
		switchKeep: time.Second, ffmpeg: "ffmpeg", detect: boxDetector}
	p, err := scan(o, inputs)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range p.Changes {
		got = append(got, fmt.Sprintf("%.2f %s", c.At, c.Why))
	}
	want := []string{"4.00 " + whyHidden, "8.00 " + whyBack, "10.00 " + whyBoard}
	if !slices.Equal(got, want) {
		t.Errorf("変化 = %q, want %q", got, want)
	}
	if len(p.Boards) != 1 || p.Boards[0].Rect.Dx() < 110 || p.Boards[0].Rect.Dx() > 130 {
		t.Errorf("対局の盤は最初に見つけた青い四角のはず: %+v", p.Boards)
	}
}

// ⚠️ **盤が見つからないコマでは、最後に対局の盤が映っていたときの盤の部分と見比べること**（盤の検出が苦手な画面で、
// 一度「映っていない」になったら二度と戻らない、を防ぐ）。
func TestGameBoardShowsWithoutDetection(t *testing.T) {
	board := image.Rect(10, 10, 110, 110)
	noBoard := func(image.Image) (recognize.BoardRegion, error) { return recognize.BoardRegion{}, errors.New("無い") }
	g := gameBoard{detect: noBoard, scene: defaultScene, sig: recognize.Signature{Frame: image.Rect(0, 0, 200, 200), Board: board}}
	paint := func(c color.RGBA, r image.Rectangle) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 200, 200))
		draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{128, 128, 128, 255}}, image.Point{}, draw.Src)
		draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Src)
		return img
	}
	shown := paint(color.RGBA{200, 180, 120, 255}, board)
	if g.shows(shown) {
		t.Errorf("まだ一度も映っていないのに、映っていると言いました")
	}
	g.remember(shown)
	if !g.shows(paint(color.RGBA{200, 180, 120, 255}, board)) {
		t.Errorf("同じ盤の部分なのに、映っていないと言いました")
	}
	if g.shows(paint(color.RGBA{20, 20, 20, 255}, board)) {
		t.Errorf("盤の部分が丸ごと変わったのに、映っていると言いました")
	}
}
