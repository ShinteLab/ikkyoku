package recognize_test

import (
	"image"
	"image/color"
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

// TestUseStripJudgeFrom は SutemeDataDir から帯の判定器を読めることを確かめる。
//
// **これが配線されていないと、盤の外枠線が画像の外に出ているキャプチャで
// 盤の位置が 1マス滑ったまま信頼度 1.00 で返る。** しかも suteme が
// カレントディレクトリを探すので、開発機では「suteme のリポジトリで動かすと直る」
// という再現しづらい食い違いになる。
func TestUseStripJudgeFrom(t *testing.T) {
	dir := t.TempDir()
	if err := suteme.SaveStripData(filepath.Join(dir, suteme.DefaultStripFile), stripSamples(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(recognize.UseDefaultStripJudge)

	n, err := recognize.UseStripJudgeFrom(dir)
	if err != nil {
		t.Fatalf("読み込めませんでした: %v", err)
	}
	if n != 2 {
		t.Errorf("サンプル数 = %d, want 2", n)
	}
}

// TestUseStripJudgeFromMissingKeepsDefault は、読めなかったときに
// **自動探索を止めてしまわない**ことを確かめる。
//
// suteme.SetStripJudge(nil) は「判定器を使わない」の意味で、
// SetPredictor(nil) のように既定探索へ戻るのとは逆。エラー時にうっかり nil を
// 渡すと、実行ファイルの隣に置いたデータまで拾わなくなる。
func TestUseStripJudgeFromMissingKeepsDefault(t *testing.T) {
	t.Cleanup(recognize.UseDefaultStripJudge)
	recognize.UseDefaultStripJudge()

	if _, err := recognize.UseStripJudgeFrom(t.TempDir()); err == nil {
		t.Fatal("ファイルが無いのにエラーになりませんでした")
	}

	// 読めるディレクトリを指せば、その後も普通に読める（＝止まっていない）。
	dir := t.TempDir()
	if err := suteme.SaveStripData(filepath.Join(dir, suteme.DefaultStripFile), stripSamples(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := recognize.UseStripJudgeFrom(dir); err != nil {
		t.Fatalf("読み込めませんでした: %v", err)
	}
}
