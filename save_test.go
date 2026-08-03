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
