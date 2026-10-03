package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/ShinteLab/ikkyoku/guide"
	"github.com/ShinteLab/ikkyoku/log"
)

// ウィンドウの位置・サイズ。**追跡と永続化**の 2 つが入っている。
//
//	① 永続化 … app-window.json の読み書き・既定値・画面内へのクランプ
//	② 追跡   … 動くたびに記録しておく（終了時には読めないため）
//
// ⚠️ **`ikkyoku` ルートの Config(config.json) とはあえて分けてある。**
// ルートパッケージに Wails 依存(`application` パッケージ)を持ち込まないための分離で、
// ウィンドウ状態は GUI アプリ固有の関心事なのでこちらに閉じる。
//
// ⚠️ **位置・サイズの型（`guide.Window`）は `ikkyoku/guide`。** 幾何の計算
// （自動フィット）と同じ型を使うためで、**JSON タグごとあちらにある**
// （保存形式はこの移動で変わっていない）。

// ---- ① 永続化 -------------------------------------------------------------

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

	// 評価値グラフの窓（切り離したとき。2026-09-08）。**横に長いほうが読める**
	// ——150 手を横軸に並べる面なので、正方形に近い形にすると点が潰れる。
	defaultGraphWidth  = 720
	defaultGraphHeight = 260
	minGraphWidth      = 320
	minGraphHeight     = 140

	// 候補手の窓（切り離したとき。2026-09-08）。**縦に長いほうが読める**
	// ——エンジンのカードが縦に積まれる面なので、横に広げても余るだけ。
	defaultSideWidth  = 420
	defaultSideHeight = 720
	minSideWidth      = 300
	minSideHeight     = 240

	// 手順の窓（切り離したとき。2026-09-12）。**候補手の窓と同じ既定にしてある**
	// ——どちらも縦に積まれる面で、片方だけ別の形で出てくる理由が無い。
	// ⚠️ **下限は候補手より低くてよい**（連続解析のボタン 1 行と手順のリストだけ）。
	defaultMovesWidth  = 420
	defaultMovesHeight = 720
	minMovesWidth      = 260
	minMovesHeight     = 200

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
	// Side は切り離した**候補手の面**の窓（2026-09-08）。⚠️ **Graph とは別** ——
	// 片方だけ切り離す使い方が普通なので、位置も別に覚える。
	Side guide.Window `json:"side"`
	// Moves は切り離した**手順の面**の窓（2026-09-12）。⚠️ **Side とも別** ——
	// 同上（候補手だけ／手順だけを外に出す使い方のどちらもある）。
	Moves guide.Window `json:"moves"`
	// Graph は切り離した評価値グラフの窓（2026-09-08）。
	//
	// ⚠️ **「切り離しているか」はここには無い**（`config.json` の
	// `evalGraphDetached`）。ここが持つのは**位置と大きさだけ** —— 窓の座標は
	// GUI 固有の関心事だが、画面の組み方は設定なので、置き場所を分けてある。
	Graph guide.Window `json:"graph"`
}

func defaultAppState() appState {
	return appState{
		Frame: guide.Window{X: unsetPosition, Y: unsetPosition, Width: defaultFrameWidth, Height: defaultFrameHeight},
		Main:  guide.Window{X: unsetPosition, Y: unsetPosition, Width: defaultMainWidth, Height: defaultMainHeight},
		Graph: guide.Window{X: unsetPosition, Y: unsetPosition, Width: defaultGraphWidth, Height: defaultGraphHeight},
		Side:  guide.Window{X: unsetPosition, Y: unsetPosition, Width: defaultSideWidth, Height: defaultSideHeight},
		Moves: guide.Window{X: unsetPosition, Y: unsetPosition, Width: defaultMovesWidth, Height: defaultMovesHeight},
	}
}

// appStatePath は os.UserConfigDir()/ikkyoku/app-window.json を返す。
func appStatePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("ikkyoku: 設定ディレクトリの取得に失敗しました: %w", err)
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
	// ⚠️ **古い app-window.json には graph が無い**（0 で読まれる）ので、
	// ここで既定に倒すこと。倒さないと**大きさ 0 の窓**が出る。
	if st.Graph.Width <= 0 || st.Graph.Height <= 0 {
		st.Graph = def.Graph
	}
	if st.Side.Width <= 0 || st.Side.Height <= 0 {
		st.Side = def.Side
	}
	if st.Moves.Width <= 0 || st.Moves.Height <= 0 {
		st.Moves = def.Moves
	}
	return st
}

func saveAppState(st appState) error {
	path, err := appStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ikkyoku: 設定ディレクトリの作成に失敗しました: %w", err)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("ikkyoku: ウィンドウ状態のエンコードに失敗しました: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("ikkyoku: ウィンドウ状態の書き込みに失敗しました: %w", err)
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

// ---- ② 追跡 ---------------------------------------------------------------

// geometryTracker はウィンドウの位置・サイズを、動くたびに記録しておくもの。
//
// **終了時に Position()/Size() を読む方式は使えない。** WindowClosing の時点では
// ウィンドウの破棄が進行しており、不正な値が返ることがある(wails3 skill
// window-state.md「WindowClosing 時に Position()/Size() が不正な値を返すことがある。
// 特に Frameless ウィンドウで発生しやすい」)。実際、終了時に読んだ座標が実測位置と
// 食い違うことをこのアプリでも確認している。
//
// スキルが挙げている回避策(フロントの✕から Go の Quit() を呼び、破棄前に読む)は、
// 自前の✕しか無い Frameless ウィンドウ向けのもの。**メイン画面はネイティブの
// タイトルバーを持ち、Alt+F4 や OS シャットダウンからも閉じられる**ため、
// 「閉じる操作を全部自前の経路に通す」ことができない。そこで、動いたときに
// 記録しておき、終了時にはその記録を保存する方式にしている。
type geometryTracker struct {
	name string

	mu    sync.Mutex
	state guide.Window
}

// newGeometryTracker は初期値(前回終了時に復元した値)で追跡を始める。
// 一度も動かさずに終了した場合は、この初期値がそのまま保存される。
func newGeometryTracker(name string, initial guide.Window) *geometryTracker {
	return &geometryTracker{name: name, state: initial}
}

// attach は移動・リサイズのイベントを購読して記録を更新する。
// 記録するだけでクローズを妨げないので、hook ではなく listener でよい。
func (t *geometryTracker) attach(win *application.WebviewWindow) {
	update := func(e *application.WindowEvent) { t.record(win) }
	win.OnWindowEvent(events.Common.WindowDidMove, update)
	win.OnWindowEvent(events.Common.WindowDidResize, update)
}

// record は今のウィンドウの位置・サイズを読んで記録する。
// 非表示のウィンドウや破棄途中のウィンドウでは不正な値が返りうるので、
// サイズが正の値のときだけ採用する(不正な値で上書きしない)。
func (t *geometryTracker) record(win *application.WebviewWindow) {
	x, y := win.Position()
	w, h := win.Size()
	if w <= 0 || h <= 0 {
		log.Debug("ウィンドウ位置の記録を見送りました", "window", t.name, "width", w, "height", h)
		return
	}
	t.mu.Lock()
	t.state = guide.Window{X: x, Y: y, Width: w, Height: h}
	t.mu.Unlock()
}

// snapshot は保存に使う最後の正常値を返す。
func (t *geometryTracker) snapshot() guide.Window {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}
