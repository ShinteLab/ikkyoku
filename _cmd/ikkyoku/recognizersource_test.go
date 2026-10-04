package main

import (
	"bytes"
	"compress/gzip"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/recognize"
	"github.com/ShinteLab/suteme"
)

// 認識器の 3 段（学習データ → 配布モデル → 焼き込み。2026-10-04）の見る順。
//
//   - **auto は学習データから**（焼き込みへ勝手に倒れると、学習データを更新しても
//     反映されないという最も気づきにくい事故になる）
//   - 見始めた段から下へ、**そのあと上の段へも回る**（焼き込みの無いビルドで
//     「焼き込み」にしていても動くように）
func TestRecognizerOrder(t *testing.T) {
	d, m, e := ikkyoku.SutemeSourceDir, ikkyoku.SutemeSourceModel, ikkyoku.SutemeSourceEmbed
	tests := []struct {
		pref string
		want []string
	}{
		{ikkyoku.SutemeSourceAuto, []string{d, m, e}},
		{d, []string{d, m, e}},
		{m, []string{m, e, d}},
		{e, []string{e, d, m}},
	}
	for _, tt := range tests {
		if got := recognizerOrder(tt.pref); !slices.Equal(got, tt.want) {
			t.Errorf("recognizerOrder(%q) = %v, want %v", tt.pref, got, tt.want)
		}
	}
}

// trainingDir は suteme の学習ディレクトリの形（生の .bin）を作る。
func trainingDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	n := len(suteme.CellToInput(image.NewRGBA(image.Rect(0, 0, 24, 24))))
	a, b := make([]float64, n), make([]float64, n)
	for i := range b {
		b[i] = 1
	}
	data := &suteme.TrainingData{Samples: []suteme.TrainingSample{{Input: a, Label: 1}, {Input: b, Label: 2}}}
	if err := suteme.SaveTrainingData(filepath.Join(dir, suteme.DefaultDataFile), data); err != nil {
		t.Fatal(err)
	}
	return dir
}

// packDir は学習ディレクトリを配布セットの形（suteme の -export -gzip と同じ）にする。
func packDir(t *testing.T, train, date string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(train, suteme.DefaultDataFile))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	w.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, suteme.DefaultDataFile+".gz"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	info := `{"date": "` + date + `T10:00:00+09:00", "samples": 2}`
	if err := os.WriteFile(filepath.Join(dir, recognize.ExportInfoFile), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// 段の落ち方の歯止め。
//
//   - **置いてあるのに読めない段は Skipped に残して下へ落ちる**（⚠ に出る）
//   - **置かれていない段（既定の置き場所が空）は Skipped に入れない**（普通の状態）
//   - **場所を指定したのに置かれていないときは Skipped に入れる**（指定は意思表示）
//   - **どれも読めなければ認識器を外す**（suteme 既定の探索に落とさない）
func TestLoadRecognizerFallsThrough(t *testing.T) {
	t.Cleanup(recognize.Clear)
	if recognize.EmbeddedAvailable() {
		t.Skip("焼き込みのあるビルドでは最後の段が埋まるので、落ち方を確かめられない")
	}
	broken := t.TempDir() // 学習データとして指定したが、中身が無い
	pack := packDir(t, trainingDir(t), "2026-10-01")

	s := NewCaptureService(broken, pack, ikkyoku.SutemeSourceAuto)
	st := s.loadRecognizer()
	if !st.Ready || st.Mode != ikkyoku.SutemeSourceModel || st.Source != pack {
		t.Fatalf("配布モデルへ落ちていない: %+v", st)
	}
	if len(st.Skipped) != 1 || !strings.Contains(st.Skipped[0], "学習データ") {
		t.Fatalf("Skipped = %v（読めなかった学習データが残ること）", st.Skipped)
	}

	// 学習データを指定せず、配布モデルの場所も既定（ここでは空のディレクトリを指定して
	// 置かれていない状態を作る）。指定した場所に無いので Skipped に入り、認識器は外れる。
	s = NewCaptureService("", t.TempDir(), ikkyoku.SutemeSourceAuto)
	st = s.loadRecognizer()
	if st.Ready || st.Error == "" || recognize.Ready() {
		t.Fatalf("どれも無いのに読めたことになっている: %+v ready=%v", st, recognize.Ready())
	}
	if len(st.Skipped) != 1 || !strings.Contains(st.Skipped[0], "置かれていません") {
		t.Fatalf("指定した場所に無いことが Skipped に無い: %v", st.Skipped)
	}

	// 学習データを指定すれば、それが使われる（配布モデルより上の段）。
	s = NewCaptureService(trainingDir(t), pack, ikkyoku.SutemeSourceAuto)
	if st = s.loadRecognizer(); st.Mode != ikkyoku.SutemeSourceDir || len(st.Skipped) != 0 {
		t.Fatalf("学習データが使われていない: %+v", st)
	}

	// 「配布モデルから」にすれば、学習データがあっても配布モデル。
	s = NewCaptureService(trainingDir(t), pack, ikkyoku.SutemeSourceModel)
	if st = s.loadRecognizer(); st.Mode != ikkyoku.SutemeSourceModel {
		t.Fatalf("配布モデルから見始めていない: %+v", st)
	}
}
