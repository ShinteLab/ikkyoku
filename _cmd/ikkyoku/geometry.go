package main

import (
	"log/slog"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

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
	name   string
	logger *slog.Logger

	mu    sync.Mutex
	state windowState
}

// newGeometryTracker は初期値(前回終了時に復元した値)で追跡を始める。
// 一度も動かさずに終了した場合は、この初期値がそのまま保存される。
func newGeometryTracker(name string, initial windowState, logger *slog.Logger) *geometryTracker {
	return &geometryTracker{name: name, logger: logger, state: initial}
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
		t.logger.Debug("ウィンドウ位置の記録を見送りました", "window", t.name, "width", w, "height", h)
		return
	}
	t.mu.Lock()
	t.state = windowState{X: x, Y: y, Width: w, Height: h}
	t.mu.Unlock()
}

// snapshot は保存に使う最後の正常値を返す。
func (t *geometryTracker) snapshot() windowState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}
