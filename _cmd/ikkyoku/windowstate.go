package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku/guide"
)

// unsetPosition はウィンドウ位置が未保存であることを表すセンチネル値。
// マルチモニタでは左/上にモニタがあると座標が負になりうるため、0 や負値では
// 「未設定」を判定できない(wails3 skill window-state.md)。
const unsetPosition = -32000

const (
	// 枠ウィンドウ。盤に重ねる道具なので小さめ。
	defaultFrameWidth  = 480
	defaultFrameHeight = 420
	minFrameWidth      = 240
	minFrameHeight     = 200

	// メイン画面。撮った画像・認識結果・設定を並べるアプリ本体の画面なので広く取る。
	defaultMainWidth  = 720
	defaultMainHeight = 520
	minMainWidth      = 360
	minMainHeight     = 280

	maxReasonableSize = 4000

	// 初回にメイン画面を枠の外へ逃がすときの間隔。重なったまま撮ると
	// メイン画面ごとキャプチャに写り込む(画面の合成結果を撮るため、
	// z 順を変えても避けられない)。
	windowGap = 8
)

// appState は永続化するウィンドウ状態の全体。
//
// ikkyoku ルートパッケージの Config(config.json)とはあえて分けてある。
// ルートパッケージに Wails 依存(application パッケージ)を持ち込まないための分離で、
// ウィンドウ状態は GUI アプリ固有の関心事なのでこちらに閉じる。
//
// 以前は枠ウィンドウ 1 枚ぶんをフラットな JSON で持っていた。互換は取っていない。
// 枠に高さ toolbarHeightPx のツールバーが増えたことで、同じウィンドウサイズでも
// 撮れる領域が変わっており、**旧い座標をそのまま復元しても位置合わせはやり直しになる**ため。
type appState struct {
	Frame guide.Window `json:"frame"`
	Main  guide.Window `json:"main"`
}

func defaultAppState() appState {
	return appState{
		Frame: guide.Window{X: unsetPosition, Y: unsetPosition, Width: defaultFrameWidth, Height: defaultFrameHeight},
		Main:  guide.Window{X: unsetPosition, Y: unsetPosition, Width: defaultMainWidth, Height: defaultMainHeight},
	}
}

// appStatePath は os.UserConfigDir()/ikkyoku/app-window.json を返す。
func appStatePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("ikkyoku-app: 設定ディレクトリの取得に失敗しました: %w", err)
	}
	return filepath.Join(dir, "ikkyoku", "app-window.json"), nil
}

// loadAppState はウィンドウ状態を読み込む。ファイルが無い・壊れている場合は
// エラーを無視して既定値を返す(初回起動やファイル破損でアプリが起動できなくなるのを避ける)。
func loadAppState() appState {
	def := defaultAppState()
	path, err := appStatePath()
	if err != nil {
		return def
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return def
	}
	var st appState
	if err := json.Unmarshal(b, &st); err != nil {
		return def
	}
	if st.Frame.Width <= 0 || st.Frame.Height <= 0 {
		st.Frame = def.Frame
	}
	if st.Main.Width <= 0 || st.Main.Height <= 0 {
		st.Main = def.Main
	}
	return st
}

func saveAppState(st appState) error {
	path, err := appStatePath()
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
func safeFallback(state guide.Window, defW, defH int) (w, h int) {
	w, h = state.Width, state.Height
	if w <= 0 || w > maxReasonableSize {
		w = defW
	}
	if h <= 0 || h > maxReasonableSize {
		h = defH
	}
	return w, h
}

// clampToScreen は WindowRuntimeReady 時点(Run() 後)で呼ぶ。
// 保存位置が存在しないモニタ・解像度変更後などでウィンドウが画面外に飛ぶのを防ぐ。
func clampToScreen(state guide.Window, defW, defH int) (x, y, w, h int) {
	w, h = safeFallback(state, defW, defH)
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
