package recognize_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
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

// writePack は学習ディレクトリの中身を、suteme の配布用の書き出し（-export -gzip）と同じ形にする。
// date が空なら書き出しの記録（export.json）を置かない。
func writePack(t *testing.T, src string, date string, withStrip bool) string {
	t.Helper()
	dir := t.TempDir()
	gz := func(name string) {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		w.Write(b)
		w.Close()
		if err := os.WriteFile(filepath.Join(dir, name+".gz"), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gz(suteme.DefaultDataFile)
	if withStrip {
		gz(suteme.DefaultStripFile)
	}
	if date != "" {
		info := `{"date": "` + date + `T10:00:00+09:00", "samples": 2}`
		if err := os.WriteFile(filepath.Join(dir, recognize.ExportInfoFile), []byte(info), 0o644); err != nil {
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

// 配布セット（suteme の配布用の書き出しそのもの）の歯止め。
//
//   - **置かれていないのは ErrNoPack**（読めないのとは別。既定の置き場所が空なのは普通の状態で、
//     ⚠ に出すことではない）。**フォルダが無いのも同じ**
//   - **圧縮した書き出しも、しない書き出しも読む**（-gzip の有無）
//   - **版が違う書き出しは ErrNoPack ではなく、版が合わないと言って断る**（下の段へ落とし、⚠ に出す）
//   - **壊れていたら読めずにエラー**
//   - **書き出しの記録から日時を読む**（焼き込みより古いモデルを使わない判断に使う）
func TestLoadPack(t *testing.T) {
	if _, err := recognize.LoadPack(t.TempDir()); !errors.Is(err, recognize.ErrNoPack) {
		t.Fatalf("空のディレクトリで %v（ErrNoPack であること）", err)
	}
	if _, err := recognize.LoadPack(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, recognize.ErrNoPack) {
		t.Fatalf("無いディレクトリで %v（ErrNoPack であること）", err)
	}

	train := writeTrainingDir(t, true)
	dir := writePack(t, train, "2026-10-01", true)
	s, err := recognize.LoadPack(dir)
	if err != nil {
		t.Fatalf("読めませんでした: %v", err)
	}
	if s.Source != dir || s.StripSamples != 2 || s.Samples != 2 {
		t.Fatalf("組 = %+v", s)
	}
	if got := s.Date.Format("2006-01-02"); got != "2026-10-01" {
		t.Fatalf("日時 = %s", got)
	}

	// 圧縮しない書き出し（suteme の既定の dist/）もそのまま読める。記録が無ければ日時はゼロ。
	s, err = recognize.LoadPack(train)
	if err != nil || s.StripSamples != 2 || !s.Date.IsZero() {
		t.Fatalf("圧縮しない書き出し: s=%+v err=%v", s, err)
	}

	// 判定器が無くても組として読める。
	s, err = recognize.LoadPack(writePack(t, train, "", false))
	if err != nil || s.StripErr == nil {
		t.Fatalf("判定器なし: s=%+v err=%v", s, err)
	}

	// 版が違う（この exe の suteme が知らない名前しか無い）。
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "training_data_v99.bin.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = recognize.LoadPack(other)
	if err == nil || errors.Is(err, recognize.ErrNoPack) || !strings.Contains(err.Error(), "版") {
		t.Fatalf("版違いで %v", err)
	}

	// 壊れている（gzip でない）なら ErrNoPack ではないエラー。
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, suteme.DefaultDataFile+".gz"), []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := recognize.LoadPack(broken); err == nil || errors.Is(err, recognize.ErrNoPack) {
		t.Fatalf("壊れた配布セットで %v", err)
	}
}

// 焼き込み（2026-10-04 から _cmd/ikkyoku が埋め込んで SetEmbedded で渡す）は、
// **置いた配布モデルと同じ読み方**をすること。渡されていなければ焼き込みは無い。
func TestEmbedded(t *testing.T) {
	t.Cleanup(func() { recognize.SetEmbedded(nil) })
	recognize.SetEmbedded(nil)
	if recognize.EmbeddedAvailable() {
		t.Fatal("渡していないのに焼き込みがあることになっている")
	}
	if _, err := recognize.LoadEmbedded(); err == nil {
		t.Fatal("渡していないのに読めた")
	}

	recognize.SetEmbedded(os.DirFS(writePack(t, writeTrainingDir(t, true), "2026-10-02", true)))
	if !recognize.EmbeddedAvailable() {
		t.Fatal("渡したのに焼き込みが無いことになっている")
	}
	s, err := recognize.LoadEmbedded()
	if err != nil || s.StripSamples != 2 || !strings.HasPrefix(s.Source, "焼き込み") {
		t.Fatalf("焼き込み: s=%+v err=%v", s, err)
	}
	if !strings.Contains(recognize.EmbeddedSource(), "2026-10-02") {
		t.Fatalf("出所に書き出しの日時が無い: %q", recognize.EmbeddedSource())
	}
}

// 日時が読めないものは古いと見なさない（比べられないものを捨てない）。
func TestSetOlderThan(t *testing.T) {
	train := writeTrainingDir(t, false)
	load := func(date string) *recognize.Set {
		s, err := recognize.LoadPack(writePack(t, train, date, false))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	old, newer, undated := load("2026-09-01"), load("2026-10-01"), load("")
	if !old.OlderThan(newer) || newer.OlderThan(old) {
		t.Fatal("日時の比較が逆")
	}
	if undated.OlderThan(newer) || old.OlderThan(undated) {
		t.Fatal("日時の無いものと比べて古いと言った")
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
