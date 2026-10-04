package recognize_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/ShinteLab/ikkyoku/recognize"
	"github.com/ShinteLab/suteme"
)

// stripSamples は帯の教師データを 2 本だけ作る。
// 中身の良し悪しは問わない（ここで測るのは配線であって判定の精度ではない）。
func stripSamples(t *testing.T) []suteme.StripSample {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 200, 40))
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 251), uint8(y * 6), 128, 255})
		}
	}
	in := suteme.StripInput(img, image.Rect(0, 0, 180, 18), false)
	out := suteme.StripInput(img, image.Rect(10, 20, 190, 38), false)
	if in == nil || out == nil {
		t.Fatal("StripInput が帯を作れませんでした")
	}
	return []suteme.StripSample{{Input: in, Board: true}, {Input: out, Board: false}}
}

// writeTrainingDir は suteme の学習ディレクトリの形（生の .bin）を作る。withStrip で判定器も置く。
func writeTrainingDir(t *testing.T, withStrip bool) string {
	t.Helper()
	dir := t.TempDir()
	// 入力の長さは suteme の特徴量の長さに合わせる（違うと読み込みが断る）。中身は問わない。
	n := len(suteme.CellToInput(image.NewRGBA(image.Rect(0, 0, 24, 24))))
	a, b := make([]float64, n), make([]float64, n)
	for i := range b {
		b[i] = 1
	}
	data := &suteme.TrainingData{Samples: []suteme.TrainingSample{
		{Input: a, Label: 1},
		{Input: b, Label: 2},
	}}
	if err := suteme.SaveTrainingData(filepath.Join(dir, suteme.DefaultDataFile), data); err != nil {
		t.Fatal(err)
	}
	if withStrip {
		if err := suteme.SaveStripData(filepath.Join(dir, suteme.DefaultStripFile), stripSamples(t)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// writePack は学習ディレクトリの中身を gzip して、配布セットの形にする（copy-model.ps1 と同じ）。
func writePack(t *testing.T, src string, source string, withStrip bool) string {
	t.Helper()
	dir := t.TempDir()
	gz := func(from, to string) {
		b, err := os.ReadFile(filepath.Join(src, from))
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		w.Write(b)
		w.Close()
		if err := os.WriteFile(filepath.Join(dir, to), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gz(suteme.DefaultDataFile, recognize.PackPredictorFile)
	if withStrip {
		gz(suteme.DefaultStripFile, recognize.PackStripFile)
	}
	if source != "" {
		// copy-model.ps1 は BOM を付ける（Windows PowerShell の Set-Content -Encoding utf8）。
		if err := os.WriteFile(filepath.Join(dir, recognize.PackSourceFile), []byte("\ufeff"+source+"\r\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// 学習ディレクトリから 1 組を読めること。**判定器が無くても組としては返す**
// （認識はできる。盤の位置が 1 マス滑ることがあるだけ）が、理由は StripErr に残すこと
// —— 「認識器: OK」とだけ出ると、判定器を置き忘れていることに気づけない（2026-08-22）。
func TestLoadDir(t *testing.T) {
	s, err := recognize.LoadDir(writeTrainingDir(t, true))
	if err != nil {
		t.Fatalf("読めませんでした: %v", err)
	}
	if s.StripSamples != 2 || s.StripErr != nil {
		t.Fatalf("判定器: samples=%d err=%v", s.StripSamples, s.StripErr)
	}

	s, err = recognize.LoadDir(writeTrainingDir(t, false))
	if err != nil {
		t.Fatalf("判定器が無いだけで読めなくなった: %v", err)
	}
	if s.StripErr == nil || s.StripSamples != 0 {
		t.Fatalf("判定器が無いのに StripErr が空: %+v", s)
	}

	if _, err := recognize.LoadDir(t.TempDir()); err == nil {
		t.Fatal("空のディレクトリで読めてしまった")
	}
}

// 配布セットの歯止め。
//
//   - **置かれていないのは ErrNoPack**（読めないのとは別。既定の置き場所が空なのは普通の状態で、
//     ⚠ に出すことではない）
//   - **壊れていたら読めずにエラー**（下の段へ落とすため）
//   - **source.txt の BOM を落とし、日付を読むこと**（焼き込みより古いモデルを使わない判断に使う）
func TestLoadPack(t *testing.T) {
	if _, err := recognize.LoadPack(t.TempDir()); !errors.Is(err, recognize.ErrNoPack) {
		t.Fatalf("空のディレクトリで %v（ErrNoPack であること）", err)
	}

	train := writeTrainingDir(t, true)
	dir := writePack(t, train, "suteme/dist training_data_v8.bin + strip_data_v1.bin (2026-10-01)", true)
	s, err := recognize.LoadPack(dir)
	if err != nil {
		t.Fatalf("読めませんでした: %v", err)
	}
	if s.Source != dir || s.StripSamples != 2 {
		t.Fatalf("組 = %+v", s)
	}
	if got := s.Date.Format("2006-01-02"); got != "2026-10-01" {
		t.Fatalf("日付 = %s", got)
	}

	// 判定器が無くても組として読める。
	s, err = recognize.LoadPack(writePack(t, train, "", false))
	if err != nil || s.StripErr == nil || !s.Date.IsZero() {
		t.Fatalf("判定器なし: s=%+v err=%v", s, err)
	}

	// 壊れている（gzip でない）なら ErrNoPack ではないエラー。
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, recognize.PackPredictorFile), []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := recognize.LoadPack(broken); err == nil || errors.Is(err, recognize.ErrNoPack) {
		t.Fatalf("壊れた配布セットで %v", err)
	}
}

// 日付が読めないものは古いと見なさない（比べられないものを捨てない）。
func TestSetOlderThan(t *testing.T) {
	train := writeTrainingDir(t, false)
	load := func(source string) *recognize.Set {
		s, err := recognize.LoadPack(writePack(t, train, source, false))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	old, newer, undated := load("x (2026-09-01)"), load("x (2026-10-01)"), load("x")
	if !old.OlderThan(newer) || newer.OlderThan(old) {
		t.Fatal("日付の比較が逆")
	}
	if undated.OlderThan(newer) || old.OlderThan(undated) {
		t.Fatal("日付の無いものと比べて古いと言った")
	}
}

// **差し替えていなければ suteme を呼ばない**（既定の探索に落ちない）こと、
// **Clear で外したら断る**こと。
func TestReadyGate(t *testing.T) {
	t.Cleanup(recognize.Clear)
	recognize.Clear()
	img := image.NewRGBA(image.Rect(0, 0, 90, 90))
	if _, err := recognize.FromImage(img); !errors.Is(err, recognize.ErrNoRecognizer) {
		t.Fatalf("認識器なしで %v（ErrNoRecognizer であること）", err)
	}

	s, err := recognize.LoadDir(writeTrainingDir(t, true))
	if err != nil {
		t.Fatal(err)
	}
	s.Use()
	if !recognize.Ready() {
		t.Fatal("Use したのに Ready でない")
	}
	if _, err := recognize.FromImage(img); errors.Is(err, recognize.ErrNoRecognizer) {
		t.Fatal("Use したのに認識器なしで断った")
	}
	recognize.Clear()
	if recognize.Ready() {
		t.Fatal("Clear したのに Ready のまま")
	}
}
