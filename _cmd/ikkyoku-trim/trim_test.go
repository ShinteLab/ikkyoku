package main

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
	changes := []Change{{At: 5}, {At: 5.5}, {At: 9.5}, {At: 19.9}}
	segs, kept := buildSegments(inputs, changes, 1, 1, 0.25)
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
