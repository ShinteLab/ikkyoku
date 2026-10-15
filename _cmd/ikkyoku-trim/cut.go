package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// cut は区間を ffmpeg の select に渡し、1 本の動画に書き出す（2 段目）。
//
// ⚠️ **ffmpeg が流しながら処理する**（動画ごとに 1 回だけ頭から読み、残す区間のコマだけを通す）。
// フィルタは長くなる（区間が何百もある）ので、コマンドラインではなくファイルで渡す（Windows のコマンドラインの長さの上限）。
func cut(o options, p Plan) error {
	script, args, err := cutArgs(p, o.out, o.crf)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "ikkyoku-trim-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// 進み具合は ffmpeg の -progress（書き出した時刻を key=value で 1 行ずつ）を受けて、残す長さに対する割合で出す。
	full := append([]string{"-v", "error", "-nostats", "-progress", "pipe:1", "-nostdin", "-y"}, args...)
	full = append(full[:len(full)-1], "-/filter_complex", f.Name(), full[len(full)-1])
	cmd := exec.Command(o.ffmpeg, full...)
	cmd.Stderr = o.stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg を起動できません（ffmpeg はありますか）: %w", err)
	}
	kept := keptSeconds(p.Segments)
	written := 0.0
	prog := newProgress(o.stderr, "2 段目（切ってつなぐ）", kept, func() string {
		return fmt.Sprintf("（%s / %s）", seconds(written), seconds(kept))
	})
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if v, ok := progressSeconds(sc.Text()); ok {
			written = v
			prog.set(v)
		}
	}
	if err := cmd.Wait(); err != nil {
		prog.breakLine()
		return fmt.Errorf("ffmpeg で書き出せません: %w", err)
	}
	prog.finish()
	return nil
}

// keptSeconds は残す区間の長さの合計（秒）。手で直した JSON でも合うように、区間から数える。
func keptSeconds(segs []Segment) float64 {
	sum := 0.0
	for _, s := range segs {
		sum += max(0, s.End-s.Start)
	}
	return sum
}

// progressSeconds は ffmpeg の -progress の 1 行から、書き出した時刻（秒）を読む（`out_time_us=…`。
// `out_time_ms` も中身はマイクロ秒）。読めない行・まだ時刻の無い行（`N/A`）は ok が偽。
func progressSeconds(line string) (float64, bool) {
	k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok || (k != "out_time_us" && k != "out_time_ms") {
		return 0, false
	}
	us, err := strconv.ParseFloat(v, 64)
	if err != nil || us < 0 {
		return 0, false
	}
	return us / 1e6, true
}

// cutArgs は 2 段目の ffmpeg のフィルタと引数（フィルタのファイルを除く。最後が書き出す先）を組む。
//
//   - 動画ごとに `select` で残す区間のコマだけを通し、`setpts` で時刻を詰めて振り直す
//   - ⚠️ **区間の時刻に動画の最初の時刻（`StartTime`）を足す**（mpeg-ts などは 0 から始まらない）
//   - ⚠️ **画面の大きさが違う動画が混ざったら、最初の動画の大きさに合わせる**（つなぐには揃っている必要がある）。
//     画素が変わるので、黙らずに知らせる
//   - 音は入れない（盤の変化を見るための動画なので要らない）
func cutArgs(p Plan, out string, crf int) (script string, args []string, err error) {
	byFile := map[string][]Segment{}
	for _, s := range p.Segments {
		byFile[s.File] = append(byFile[s.File], s)
	}
	var used []Input
	for _, in := range p.Inputs {
		if len(byFile[in.File]) > 0 {
			used = append(used, in)
		}
	}
	if len(used) == 0 {
		return "", nil, fmt.Errorf("残す区間がありません")
	}
	w, h := used[0].Width, used[0].Height
	var b strings.Builder
	var labels []string
	for i, in := range used {
		args = append(args, "-i", in.File)
		var terms []string
		for _, s := range byFile[in.File] {
			terms = append(terms, fmt.Sprintf("between(t,%s,%s)", num(s.Start+in.StartTime), num(s.End+in.StartTime)))
		}
		fmt.Fprintf(&b, "[%d:v]select='%s',setpts=N/(%s)/TB", i, strings.Join(terms, "+"), strconv.FormatFloat(in.FrameRate, 'f', -1, 64))
		if in.Width != w || in.Height != h {
			fmt.Fprintf(os.Stderr, "⚠ %s は %dx%d なので %dx%d に合わせます（画素が変わります）\n", in.File, in.Width, in.Height, w, h)
			fmt.Fprintf(&b, ",scale=%d:%d,setsar=1", w, h)
		}
		label := fmt.Sprintf("v%d", i)
		fmt.Fprintf(&b, "[%s];\n", label)
		labels = append(labels, "["+label+"]")
	}
	if len(used) == 1 {
		fmt.Fprintf(&b, "%snull[out]\n", labels[0])
	} else {
		fmt.Fprintf(&b, "%sconcat=n=%d:v=1:a=0[out]\n", strings.Join(labels, ""), len(used))
	}
	args = append(args, "-map", "[out]", "-an", "-c:v", "libx264", "-crf", strconv.Itoa(crf),
		"-preset", "medium", "-pix_fmt", "yuv420p", "-movflags", "+faststart", out)
	return b.String(), args, nil
}

// num は秒数を ffmpeg に渡す形にする（指数表記にしない）。
func num(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
