package main

import (
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
	full := append([]string{"-v", "error", "-stats", "-nostdin", "-y"}, args...)
	full = append(full[:len(full)-1], "-/filter_complex", f.Name(), full[len(full)-1])
	cmd := exec.Command(o.ffmpeg, full...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg で書き出せません（ffmpeg はありますか）: %w", err)
	}
	return nil
}

// cutArgs は 2 段目の ffmpeg のフィルタと引数（フィルタのファイルを除く。最後が書き出す先）を組む。
//
//   - 動画ごとに `select` で残す区間のコマだけを通し、`setpts` で時刻を詰めて振り直す
//   - ⚠️ **区間の時刻に動画の最初の時刻（`StartTime`）を足す**（mpeg-ts などは 0 から始まらない）
//   - ⚠️ **画面の大きさが違う動画が混ざったら、最初の動画の大きさに合わせる**（つなぐには揃っている必要がある）。
//     画素が変わるので、黙らずに知らせる
//   - 音は入れない（追従には要らない）
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
