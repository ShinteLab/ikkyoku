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

// CaptureKeepDays は撮った画像を置いておく日数（`RemoveOldCaptures`。2026-10-11）。
// ⚠️ **ログの日数（`log.KeepDays`）とは別**（2026-11-08 にログだけ 90 日へ延ばした。PNG は圧縮が効かないうえ、
// suteme へ送れば役目を終える）。
const CaptureKeepDays = 7

// timestampLayout は撮った画像のファイル名の時刻の形（古いものを消すときもこれで読む）。
const timestampLayout = "20060102-150405"

// timestampName はキャプチャ時刻からファイル名を生成する（例 20260804-193045.png）。
// 時刻を引数で受け取ることでテスト可能にしてある。
func timestampName(t time.Time) string {
	return t.Format(timestampLayout) + ".png"
}

// DefaultOutDir は撮った画像の既定の保存先を返す（一時ディレクトリの ikkyoku/capture。2026-10-11）。
//
// 撮った画像は suteme へ学習データとして送れば役目を終えるので、残し続けない（`RemoveOldCaptures`）。
// それまでは os.UserConfigDir()/ikkyoku/captures で、消す仕組みが無く 2 か月で 400 枚ほど溜まっていた。
// ⚠️ **中継の録画（ログのディレクトリの follow）とは別のディレクトリにすること**（撮った画像は人が撮る瞬間を
// 決めたもの、録画は追従のデバッグの記録）。ログ・録画と同じ一時ディレクトリの ikkyoku の下。
func DefaultOutDir() (string, error) {
	return filepath.Join(os.TempDir(), "ikkyoku", "capture"), nil
}

// RemoveOldCaptures は dir の撮った画像のうち、名前の時刻が keepDays 日より前のものを消す（2026-10-11）。
// ⚠️ **名前が撮った時刻として読める PNG だけ消す**（ほかのものは置いた人のもの）。消せなくても何もしない。
func RemoveOldCaptures(dir string, now time.Time, keepDays int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	limit := now.AddDate(0, 0, -keepDays)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".png") {
			continue
		}
		t, err := time.ParseInLocation(timestampLayout, strings.TrimSuffix(name, ".png"), now.Location())
		if err != nil || !t.Before(limit) {
			continue
		}
		os.Remove(filepath.Join(dir, name))
	}
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
