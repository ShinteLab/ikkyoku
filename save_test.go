package ikkyoku

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTimestampName(t *testing.T) {
	tm := time.Date(2026, 8, 4, 19, 30, 45, 0, time.UTC)
	if got, want := timestampName(tm), "20260804-193045.png"; got != want {
		t.Errorf("timestampName() = %q, want %q", got, want)
	}
}

func TestSavePNGAt(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "captures") // 未作成ディレクトリでも作られることを確認する

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})

	tm := time.Date(2026, 8, 4, 19, 30, 45, 0, time.UTC)
	path, err := savePNGAt(img, outDir, tm)
	if err != nil {
		t.Fatalf("savePNGAt() error = %v", err)
	}
	want := filepath.Join(outDir, "20260804-193045.png")
	if path != want {
		t.Errorf("savePNGAt() path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("saved file not found: %v", err)
	}
}

func TestDefaultOutDir(t *testing.T) {
	dir, err := DefaultOutDir()
	if err != nil {
		t.Fatalf("DefaultOutDir() error = %v", err)
	}
	if filepath.Base(dir) != "captures" {
		t.Errorf("DefaultOutDir() = %q, want basename %q", dir, "captures")
	}
}

// ⚠️ **名前を呼び出し側が決められること**（中継の追従の「録画」）。
// `SavePNG` のタイムスタンプ名は**秒単位なので同じ秒に 2 枚撮ると上書きされる**し、
// **どの手の画像なのかが名前から読めない**（誤認識を追うのが目的なので致命的）。
func TestSavePNGAs(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "follow", "20260915-140000")
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})

	path, err := SavePNGAs(img, outDir, "042-7g7f.png")
	if err != nil {
		t.Fatalf("SavePNGAs() error = %v", err)
	}
	if want := filepath.Join(outDir, "042-7g7f.png"); path != want {
		t.Errorf("SavePNGAs() path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("保存したファイルが見つかりません: %v", err)
	}
	// ⚠️ **同じ秒に 2 枚でも上書きされないこと**（名前が違えば別のファイル）。
	if _, err := SavePNGAs(img, outDir, "043-3c3d.png"); err != nil {
		t.Fatalf("2 枚目: %v", err)
	}
	ents, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Errorf("保存されたのは %d 枚（2 枚のはず）", len(ents))
	}
}

// ⚠️ **保存先の外へ書けないこと。** 名前を組み立てるのは呼び出し側なので、
// そこを間違えたときに**黙って別のディレクトリへ書く**のが一番たちが悪い。
func TestSavePNGAsRejectsPath(t *testing.T) {
	outDir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	for _, name := range []string{"", ".", "..", "../out.png", `..\out.png`, "sub/out.png"} {
		if _, err := SavePNGAs(img, outDir, name); err == nil {
			t.Errorf("SavePNGAs(%q) が通りました", name)
		}
	}
}
