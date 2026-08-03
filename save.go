package ikkyoku

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
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
func SavePNG(img image.Image, outDir string) (string, error) {
	return savePNGAt(img, outDir, time.Now())
}

func savePNGAt(img image.Image, outDir string, t time.Time) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("ikkyoku: 保存先ディレクトリの作成に失敗しました: %w", err)
	}
	path := filepath.Join(outDir, timestampName(t))
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
