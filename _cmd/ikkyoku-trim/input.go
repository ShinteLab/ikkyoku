package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// videoExts は、ディレクトリを渡したときに動画として拾う拡張子（小文字）。
var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".mov": true, ".flv": true, ".ts": true,
	".webm": true, ".avi": true, ".m4v": true,
}

// trimSuffix は書き出した動画の名前の終わり。⚠️ **ディレクトリを読み直したとき、前に書き出した動画を入力に混ぜない**。
const trimSuffix = "-trim"

// expandInputs は引数（動画ファイルかディレクトリ）を、読む順の動画ファイルの並びにする。
//
// ディレクトリはその中の動画ファイルを**名前の順**に並べる（OBS の名前は録り始めの日時なので、名前の順が録った順になる）。
// 並べたファイルは渡した順のまま。⚠️ **サブディレクトリは見ない**・⚠️ **書き出した動画（`-trim`）は拾わない**。
func expandInputs(args []string) ([]string, error) {
	var out []string
	for _, a := range args {
		st, err := os.Stat(a)
		if err != nil {
			return nil, fmt.Errorf("%s を読めません: %w", a, err)
		}
		if !st.IsDir() {
			out = append(out, a)
			continue
		}
		entries, err := os.ReadDir(a)
		if err != nil {
			return nil, fmt.Errorf("%s を読めません: %w", a, err)
		}
		var names []string
		for _, e := range entries {
			name := e.Name()
			ext := strings.ToLower(filepath.Ext(name))
			if e.IsDir() || !videoExts[ext] || strings.HasSuffix(strings.TrimSuffix(name, filepath.Ext(name)), trimSuffix) {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return nil, fmt.Errorf("%s に動画ファイルがありません", a)
		}
		for _, n := range names {
			out = append(out, filepath.Join(a, n))
		}
	}
	return out, nil
}

// defaultOut は書き出す動画の既定の場所。最初の引数がディレクトリならその中の <ディレクトリ名>-trim.mp4、
// ファイルならその隣の <名前>-trim.mp4。
func defaultOut(firstArg, firstFile string) string {
	if st, err := os.Stat(firstArg); err == nil && st.IsDir() {
		abs, err := filepath.Abs(firstArg)
		if err != nil {
			abs = firstArg
		}
		return filepath.Join(firstArg, filepath.Base(abs)+trimSuffix+".mp4")
	}
	return strings.TrimSuffix(firstFile, filepath.Ext(firstFile)) + trimSuffix + ".mp4"
}

// Input は読む動画 1 本。
type Input struct {
	File     string  `json:"file"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Duration float64 `json:"duration"`
	// FrameRate は 1 秒あたりのコマ数（2 段目で切ったあとの時刻を振り直すのに使う）。
	FrameRate float64 `json:"frameRate"`
	// StartTime は動画の最初の時刻（mpeg-ts などは 0 から始まらない。2 段目の select の時刻に足す）。
	StartTime float64 `json:"startTime"`
	// Offset はつないだ録画の中での、この動画の始まりの秒。
	Offset float64 `json:"offset"`
}

// probeAll は ffprobe で各動画の大きさ・長さ・コマ数を調べ、つないだ録画の中の始まり（Offset）を振る。
func probeAll(ffprobe string, files []string) ([]Input, error) {
	var out []Input
	offset := 0.0
	for _, f := range files {
		in, err := probe(ffprobe, f)
		if err != nil {
			return nil, err
		}
		in.Offset = offset
		offset += in.Duration
		out = append(out, in)
	}
	return out, nil
}

func probe(ffprobe, file string) (Input, error) {
	cmd := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,avg_frame_rate,r_frame_rate:format=duration,start_time", "-of", "json", file)
	b, err := cmd.Output()
	if err != nil {
		return Input{}, fmt.Errorf("%s を ffprobe で読めません（ffprobe はありますか）: %w", file, err)
	}
	return parseProbe(file, b)
}

// parseProbe は ffprobe の JSON を読む（テストのために分けてある）。
func parseProbe(file string, b []byte) (Input, error) {
	var p struct {
		Streams []struct {
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			AvgFrameRate string `json:"avg_frame_rate"`
			RFrameRate   string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration  string `json:"duration"`
			StartTime string `json:"start_time"`
		} `json:"format"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return Input{}, fmt.Errorf("%s の ffprobe の答えを読めません: %w", file, err)
	}
	if len(p.Streams) == 0 || p.Streams[0].Width <= 0 || p.Streams[0].Height <= 0 {
		return Input{}, fmt.Errorf("%s に映像がありません", file)
	}
	d, err := strconv.ParseFloat(p.Format.Duration, 64)
	if err != nil || d <= 0 {
		return Input{}, fmt.Errorf("%s の長さが分かりません（%q）", file, p.Format.Duration)
	}
	fr := parseRate(p.Streams[0].AvgFrameRate)
	if fr <= 0 {
		fr = parseRate(p.Streams[0].RFrameRate)
	}
	if fr <= 0 {
		return Input{}, fmt.Errorf("%s のコマ数が分かりません", file)
	}
	st, _ := strconv.ParseFloat(p.Format.StartTime, 64) // 無ければ 0
	return Input{File: file, Width: p.Streams[0].Width, Height: p.Streams[0].Height, Duration: d, FrameRate: fr, StartTime: st}, nil
}

// parseRate は ffprobe の「30000/1001」の形を読む（読めなければ 0）。
func parseRate(s string) float64 {
	num, den, ok := strings.Cut(s, "/")
	n, err1 := strconv.ParseFloat(num, 64)
	if !ok {
		if err1 != nil {
			return 0
		}
		return n
	}
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return 0
	}
	return n / d
}
