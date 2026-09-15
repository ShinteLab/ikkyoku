package ikkyoku

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// timestampName はキャプチャ時刻からファイル名を生成する（例 20260804-193045.png）。
// 時刻を引数で受け取ることでテスト可能にしてある。
func timestampName(t time.Time) string {
	return t.Format("20060102-150405") + ".png"
}

// DefaultOutDir は既定の保存先ディレクトリを返す（os.UserConfigDir()/ikkyoku/captures）。
func DefaultOutDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("ikkyoku: 設定ディレクトリの取得に失敗しました: %w", err)
	}
	return filepath.Join(dir, "ikkyoku", "captures"), nil
}

// SavePNG は img を outDir にタイムスタンプ名で PNG 保存し、保存先パスを返す。
// outDir が無ければ作成する。
//
// ⚠️ **名前が秒単位なので、同じ秒に 2 枚撮ると黙って上書きされる。**
// 呼び出し側が名前を決められるなら `SavePNGAs` を使うこと。
func SavePNG(img image.Image, outDir string) (string, error) {
	return savePNGAt(img, outDir, time.Now())
}

// SavePNGAs は img を **呼び出し側が決めた名前**で PNG 保存する（2026-09-15）。
//
// **中継の追従で「採用した 1 枚」を手数と指し手の名前で残すために足した。**
// `SavePNG` のタイムスタンプ名は**秒単位なので同じ秒に 2 枚撮ると上書きされる**し、
// そもそも**どの手の画像なのかが名前から読めない**（誤認識を追うのが目的なので、
// 名前が手と結び付いていないと使い物にならない）。
//
// ⚠️ **name にパス区切りを入れさせないこと。** 名前を組み立てるのは呼び出し側
// なので、そこを間違えると**保存先の外へ書ける**。
func SavePNGAs(img image.Image, outDir, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("ikkyoku: 保存する名前が空です")
	}
	if name != filepath.Base(name) || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("ikkyoku: 保存する名前にパスは使えません: %q", name)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("ikkyoku: 保存先ディレクトリの作成に失敗しました: %w", err)
	}
	return writePNG(img, filepath.Join(outDir, name))
}

func savePNGAt(img image.Image, outDir string, t time.Time) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("ikkyoku: 保存先ディレクトリの作成に失敗しました: %w", err)
	}
	return writePNG(img, filepath.Join(outDir, timestampName(t)))
}

func writePNG(img image.Image, path string) (string, error) {
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("ikkyoku: ファイルの作成に失敗しました: %w", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return "", fmt.Errorf("ikkyoku: PNG エンコードに失敗しました: %w", err)
	}
	return path, nil
}
