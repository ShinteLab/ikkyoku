package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku"
)

// guideBorderPx はフロント側(frontend/src/main.ts)で描くガイド枠の太さ(CSS px)。
// キャプチャ領域は「クライアント領域をこの太さぶん内側にオフセットした矩形」に決める
// (ガイド枠自体が写り込まないための決定論的な方式。撮る直前に枠を消すタイミング依存の
// 方式は採らない、という TODO.md / 依頼の指示による)。
// フロント側の値と必ず一致させること。ずれると枠が写り込む、または枠の内側が余る。
const guideBorderPx = 2

// CaptureService は Wails にバインドする、GUI からのキャプチャ操作。
// ロジックは持たず、ikkyoku ルートパッケージ(Capture / SavePNG / DefaultOutDir)を
// 呼ぶだけに徹する(ikkyoku/CLAUDE.md: 将棋のロジックを書かない、状態を持たない)。
type CaptureService struct {
	app    *application.App
	window *application.WebviewWindow
	logger *slog.Logger
}

func NewCaptureService(logger *slog.Logger) *CaptureService {
	return &CaptureService{logger: logger}
}

// bind は main() から起動シーケンスの中で呼ぶ。ServiceStartup は使わない
// (window は Service 登録より後にしか作れないため。wails3 skill pitfalls.md 5 の
// 「main() から手動で初期化する」パターンに準拠)。
func (s *CaptureService) bind(app *application.App, window *application.WebviewWindow) {
	s.app = app
	s.window = window
}

// CaptureResult はフロントに返すキャプチャ結果。
type CaptureResult struct {
	Path      string `json:"path"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Thumbnail string `json:"thumbnail"` // data:image/png;base64,... のサムネイル(等倍)
}

// Capture はガイド枠の内側を撮って PNG 保存し、保存先パスとサムネイルを返す。
// フロントの「撮る」ボタンとグローバルホットキーの両方から呼ばれる。
// 1 回のキャプチャは他のキャプチャと完全に独立している(状態を持たない)。
func (s *CaptureService) Capture() (CaptureResult, error) {
	region, err := s.captureRegion()
	if err != nil {
		return CaptureResult{}, err
	}

	img, err := ikkyoku.Capture(region)
	if err != nil {
		return CaptureResult{}, err
	}

	dir, err := ikkyoku.DefaultOutDir()
	if err != nil {
		return CaptureResult{}, err
	}
	path, err := ikkyoku.SavePNG(img, dir)
	if err != nil {
		return CaptureResult{}, err
	}

	var buf bytes.Buffer
	thumb := ""
	if err := png.Encode(&buf, img); err != nil {
		s.logger.Warn("サムネイル用のPNGエンコードに失敗しました", "error", err)
	} else {
		thumb = "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	}

	b := img.Bounds()
	result := CaptureResult{Path: path, Width: b.Dx(), Height: b.Dy(), Thumbnail: thumb}
	s.logger.Info("キャプチャしました", "path", path, "width", b.Dx(), "height", b.Dy())
	if s.app != nil {
		s.app.Event.Emit("capture:done", result)
	}
	return result, nil
}

// captureRegion はネイティブウィンドウハンドル(HWND)からクライアント領域を
// 物理ピクセルのスクリーン座標で求め、ガイド枠の太さぶん内側にオフセットする。
//
// Wails の Window.Position()/Size() は DIP(論理ピクセル)を返すため、
// マルチモニタでスケーリング(150%等)が混在する環境ではそのまま使うと領域がずれる。
// HWND から Windows API を直接呼べば物理ピクセルで確実に一致する
// (clientrect_windows.go。DPI 変換もタイトルバー・枠の厚みの計算も不要になる)。
func (s *CaptureService) captureRegion() (ikkyoku.Region, error) {
	if s.window == nil {
		return ikkyoku.Region{}, fmt.Errorf("ikkyoku-app: ウィンドウが初期化されていません")
	}
	hwnd := s.window.NativeWindow()
	if hwnd == nil {
		return ikkyoku.Region{}, fmt.Errorf("ikkyoku-app: ネイティブウィンドウハンドルを取得できませんでした")
	}

	rect, scale, err := clientRectPhysical(hwnd)
	if err != nil {
		return ikkyoku.Region{}, err
	}

	inset := int(float64(guideBorderPx)*scale + 0.5)
	region := ikkyoku.Region{
		X:      rect.X + inset,
		Y:      rect.Y + inset,
		Width:  rect.Width - inset*2,
		Height: rect.Height - inset*2,
	}
	if !region.Valid() {
		return ikkyoku.Region{}, fmt.Errorf("ikkyoku-app: ウィンドウが小さすぎます(ガイド枠より小さい領域になります)")
	}
	return region, nil
}
