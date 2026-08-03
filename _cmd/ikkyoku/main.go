// ikkyoku コマンドは ikkyoku の Wails3 GUI。
//
// 画面に「透過した枠」を重ね、その枠の中身をキャプチャして PNG に保存するだけのツール。
// 将棋のロジック(SFEN・盤面・駒)は一切持たない。撮って保存するだけ。
// 実処理は親パッケージ github.com/ShinteLab/ikkyoku をそのまま呼ぶ。Wails 依存のコードは
// このディレクトリ(_cmd/ikkyoku)にのみ置く(wails3 skill の architecture 方針)。
package main

import (
	"embed"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	captureSvc := NewCaptureService(logger)

	app := application.New(application.Options{
		Name:        "ikkyoku",
		Description: "ikkyoku - shogi broadcast region capture",
		Logger:      logger,
		Services: []application.Service{
			application.NewService(captureSvc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	state := loadWindowState()
	// Phase 1(Run() 前): スクリーン情報を使わない簡易な安全策のみ行う。
	// マルチモニタのクランプは Phase 2(WindowRuntimeReady)で行う
	// (ScreenNearestDipPoint は Run() 前は nil を返すため。wails3 skill window-state.md)。
	_, _, w, h := safeFallback(state)

	opts := application.WebviewWindowOptions{
		Title:     "ikkyoku",
		Width:     w,
		Height:    h,
		MinWidth:  minWindowWidth,
		MinHeight: minWindowHeight,
		// Frameless にはしない(タイトルバー・枠を持つ通常ウィンドウにする、というユーザー要望)。
		// クライアント領域だけを透過させる。
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL:              "/?window=frame",
		// 中継の再生ウィンドウの「上」に重ねて盤面に合わせる道具なので、最前面は必須。
		// これが無いと枠が中継ウィンドウの後ろに回り、位置合わせができなくなる(実測)。
		AlwaysOnTop: true,
	}
	if state.X == unsetPosition && state.Y == unsetPosition {
		// 初回起動(保存された位置が無い)は中央表示にフォールバックする。
		// センチネル値をそのまま X/Y に渡すと画面外に出てしまうため。
		opts.InitialPosition = application.WindowCentered
	} else {
		opts.X, opts.Y = state.X, state.Y
		opts.InitialPosition = application.WindowXY
	}

	window := app.Window.NewWithOptions(opts)
	captureSvc.bind(app, window)

	// Phase 2(Run() 後): WindowRuntimeReady はウィンドウ単位で発火し、この時点なら
	// ScreenNearestDipPoint も SetSize/SetPosition も確実に効く(ApplicationStarted では不確実)。
	if state.X != unsetPosition || state.Y != unsetPosition {
		window.RegisterHook(events.Common.WindowRuntimeReady, func(e *application.WindowEvent) {
			x, y, cw, ch := clampToScreen(state)
			window.SetSize(cw, ch)
			window.SetPosition(x, y)
		})
	}

	// 終了前に位置・サイズを保存する。
	// WindowClosing はデフォルトの破棄用リスナーが並列 goroutine で走るため、
	// OnWindowEvent(listener) だと破棄と保存処理がレースしうる。RegisterHook(hook) は
	// listener より前に同期実行されるため、破棄が始まる前に確実に Position()/Size() を読める
	// (wails3 skill tray-hotkey.md の hook/listener 順序の説明、window-state.md の
	// 「WindowClosing 時点では座標が不正になりうる」を踏まえた対策)。
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		ww, wh := window.Size()
		wx, wy := window.Position()
		if err := saveWindowState(windowState{X: wx, Y: wy, Width: ww, Height: wh}); err != nil {
			logger.Error("ウィンドウ状態の保存に失敗しました", "error", err)
		}
	})

	// 操作パネル(「撮る」ボタン・保存先・サムネイル)は別ウィンドウにする。
	// ガイド枠ウィンドウの中には一切 UI を置かない(置いた要素はそのままキャプチャに
	// 写り込むため。依頼の「ボタンはキャプチャ領域に入らない場所に置く」の実現方法)。
	// 同じフロントバンドルを URL クエリで出し分ける(wails3 skill advanced.md 参照)。
	panelX, panelY := unsetPosition, unsetPosition
	if state.X != unsetPosition && state.Y != unsetPosition {
		panelX, panelY = state.X, state.Y-panelHeight-panelGap
		if panelY < 0 {
			panelY = state.Y + h + panelGap // 上に置く余白が無ければ枠の下に置く
		}
	}
	panelOpts := application.WebviewWindowOptions{
		Title:       "ikkyoku - 操作パネル",
		Width:       panelWidth,
		Height:      panelHeight,
		AlwaysOnTop: true,
		URL:         "/?window=panel",
	}
	if panelX == unsetPosition {
		panelOpts.InitialPosition = application.WindowCentered
	} else {
		panelOpts.X, panelOpts.Y = panelX, panelY
		panelOpts.InitialPosition = application.WindowXY
	}
	panel := app.Window.NewWithOptions(panelOpts)

	// パネルがガイド枠に重なっているとパネルごと写り込む。
	// 初回起動時は枠・パネルとも WindowCentered になり必ず重なるため、
	// 枠の配置が確定した時点(WindowRuntimeReady)でパネルを枠の外へ退避させる。
	// 座標はどちらも Wails の DIP なので、ここで DPI 換算は不要
	// (物理ピクセルが要るのはキャプチャ領域の算出だけ。captureservice.go 参照)。
	window.RegisterHook(events.Common.WindowRuntimeReady, func(e *application.WindowEvent) {
		fx, fy := window.Position()
		_, fh := window.Size()
		py := fy - panelHeight - panelGap
		if py < 0 {
			py = fy + fh + panelGap // 上に余白が無ければ枠の下へ
		}
		panel.SetPosition(fx, py)
	})

	// グローバルホットキー(既定 alt+s)。ウィンドウが非アクティブでも効くことが重要
	// (中継の再生画面にフォーカスがある状態で撮るのが普通の使い方のため)。
	accel, err := hotkeyAccelerator(resolveHotkey())
	if err != nil {
		logger.Error("ホットキーの解決に失敗しました", "error", err)
	} else if err := app.GlobalShortcut.Register(accel, func() {
		if _, err := captureSvc.Capture(); err != nil {
			logger.Error("ホットキーからのキャプチャに失敗しました", "error", err)
			app.Event.Emit("capture:failed", err.Error())
			return
		}
	}); err != nil {
		// 登録失敗は致命的にしない(他アプリと競合している環境がありうる)。
		// 「撮る」ボタンは引き続き使えるので、UI からエラーが分かるようにだけ通知する。
		logger.Warn("グローバルホットキーの登録に失敗しました", "hotkey", accel, "error", err)
		app.Event.Emit("hotkey:register-failed", map[string]any{
			"hotkey": accel,
			"error":  err.Error(),
		})
	}

	if err := app.Run(); err != nil {
		logger.Error("アプリが異常終了しました", "error", err)
		os.Exit(1)
	}
}
