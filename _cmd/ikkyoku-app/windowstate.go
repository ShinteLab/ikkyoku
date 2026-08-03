package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// unsetPosition はウィンドウ位置が未保存であることを表すセンチネル値。
// マルチモニタでは左/上にモニタがあると座標が負になりうるため、0 や負値では
// 「未設定」を判定できない(wails3 skill window-state.md)。
const unsetPosition = -32000

const (
	defaultWindowWidth  = 480
	defaultWindowHeight = 420
	minWindowWidth      = 240
	minWindowHeight     = 200
	maxReasonableSize   = 4000

	// 操作パネル(main.go)のサイズと、ガイド枠ウィンドウとの間隔。
	// パネルは位置・サイズを永続化しない(最小限の実装。毎回ガイド枠の近くに出す)。
	panelWidth  = 300
	panelHeight = 150
	panelGap    = 8
)

// windowState は永続化するウィンドウの位置・サイズ。
//
// ikkyoku ルートパッケージの Config(CLI のキャプチャ設定)とはあえて分けてある。
// ルートパッケージに Wails 依存(application パッケージ)を持ち込まないための分離で、
// ウィンドウ状態は GUI アプリ固有の関心事なのでこちらに閉じる。
type windowState struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func defaultWindowState() windowState {
	return windowState{X: unsetPosition, Y: unsetPosition, Width: defaultWindowWidth, Height: defaultWindowHeight}
}

// windowStatePath は os.UserConfigDir()/ikkyoku/app-window.json を返す。
func windowStatePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("ikkyoku-app: 設定ディレクトリの取得に失敗しました: %w", err)
	}
	return filepath.Join(dir, "ikkyoku", "app-window.json"), nil
}

// loadWindowState はウィンドウ状態を読み込む。ファイルが無い・壊れている場合は
// エラーを無視して既定値を返す(初回起動やファイル破損でアプリが起動できなくなるのを避ける)。
func loadWindowState() windowState {
	def := defaultWindowState()
	path, err := windowStatePath()
	if err != nil {
		return def
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return def
	}
	var st windowState
	if err := json.Unmarshal(b, &st); err != nil {
		return def
	}
	if st.Width <= 0 || st.Height <= 0 {
		return def
	}
	return st
}

func saveWindowState(st windowState) error {
	path, err := windowStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ikkyoku-app: 設定ディレクトリの作成に失敗しました: %w", err)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("ikkyoku-app: ウィンドウ状態のエンコードに失敗しました: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("ikkyoku-app: ウィンドウ状態の書き込みに失敗しました: %w", err)
	}
	return nil
}

// safeFallback は Run() 前(スクリーン情報が使えない段階)での簡易な安全策。
// 保存値が異常でも極端なサイズで開かないようにするだけで、マルチモニタのクランプはしない
// (それは Run() 後の clampToScreen が担当する。wails3 skill window-state.md)。
func safeFallback(state windowState) (x, y, w, h int) {
	w, h = state.Width, state.Height
	if w <= 0 || w > maxReasonableSize {
		w = defaultWindowWidth
	}
	if h <= 0 || h > maxReasonableSize {
		h = defaultWindowHeight
	}
	return state.X, state.Y, w, h
}

// clampToScreen は WindowRuntimeReady 時点(Run() 後)で呼ぶ。
// 保存位置が存在しないモニタ・解像度変更後などでウィンドウが画面外に飛ぶのを防ぐ。
func clampToScreen(state windowState) (x, y, w, h int) {
	_, _, w, h = safeFallback(state)
	x, y = state.X, state.Y

	cx, cy := x+w/2, y+h/2
	screen := application.ScreenNearestDipPoint(application.Point{X: cx, Y: cy})
	if screen == nil {
		return x, y, w, h
	}

	area := screen.WorkArea
	if w > area.Width {
		w = area.Width
	}
	if h > area.Height {
		h = area.Height
	}
	if x < area.X {
		x = area.X
	}
	if y < area.Y {
		y = area.Y
	}
	if x+w > area.X+area.Width {
		x = area.X + area.Width - w
	}
	if y+h > area.Y+area.Height {
		y = area.Y + area.Height - h
	}
	return x, y, w, h
}
