// ikkyoku コマンドは ikkyoku の Wails3 GUI。
//
// ウィンドウは 2 枚。
//
//   - 枠ウィンドウ … 画面に重ねる Frameless の透過ウィンドウ。「どこを撮るか」の定義そのもの。
//     上部のツールバーから撮れる。閉じても破棄せず隠すだけ(領域の定義は生かしたまま)。
//   - メイン画面   … アプリ本体。撮った画像・認識結果・設定を置く。起動時は非表示で、
//     最初のキャプチャで現れる。**閉じるとアプリが終了する。**
//
// 「メイン」は枠ではなくメイン画面。枠は位置合わせが済めば用済みになりうる道具で、
// アプリの寿命を握る画面ではない。ただし**キャプチャ領域の基準は枠のまま**で、これは
// 移せない(領域は枠のクライアント矩形そのもの。captureservice.go 参照)。
//
// 将棋のロジック(SFEN・盤面・駒)はまだ持たない。撮って保存するだけ。
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

// appWindows はウィンドウ 2 枚と、その位置・サイズの追跡をまとめたもの。
type appWindows struct {
	// frame は枠ウィンドウ。**キャプチャ領域はこのウィンドウのクライアント矩形そのもの。**
	frame *application.WebviewWindow
	// main はメイン画面。閉じるとアプリが終了する。
	main *application.WebviewWindow

	frameGeom *geometryTracker
	mainGeom  *geometryTracker

	// mainHasSavedPos は前回終了時の位置を復元したか。復元しているなら初回表示で
	// 枠の外へ動かさない(ユーザーが決めた位置を上書きしないため)。
	mainHasSavedPos bool
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	captureSvc := NewCaptureService(logger, loadConfig(logger).SutemeDataDir)

	app := application.New(application.Options{
		Name:        "ikkyoku",
		Description: "ikkyoku - shogi broadcast region capture",
		Logger:      logger,
		Services: []application.Service{
			application.NewService(captureSvc),
		},
		Assets: assetOptions(),
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	state := loadAppState()
	frame := newFrameWindow(app, state.Frame)
	main, mainHasSavedPos := newMainWindow(app, state.Main)

	wins := &appWindows{
		frame:           frame,
		main:            main,
		frameGeom:       newGeometryTracker("frame", state.Frame, logger),
		mainGeom:        newGeometryTracker("main", state.Main, logger),
		mainHasSavedPos: mainHasSavedPos,
	}
	wins.frameGeom.attach(frame)
	wins.mainGeom.attach(main)
	captureSvc.bind(app, wins)

	registerFrameHooks(wins, state.Frame, captureSvc)
	registerMainHooks(app, wins, state.Main, logger)
	registerHotkey(app, captureSvc, logger)

	// 駒種推論器(suteme)を先に用意しておく。3.5MB の学習データを読むので、
	// 最初のキャプチャのときに読み始めると撮った瞬間に待たされる。
	captureSvc.ReloadRecognizer()

	if err := app.Run(); err != nil {
		logger.Error("アプリが異常終了しました", "error", err)
		os.Exit(1)
	}
}

// newFrameWindow は盤に重ねる Frameless の透過ウィンドウを作る。
func newFrameWindow(app *application.App, st windowState) *application.WebviewWindow {
	w, h := safeFallback(st, defaultFrameWidth, defaultFrameHeight)

	opts := application.WebviewWindowOptions{
		Title:     "ikkyoku",
		Width:     w,
		Height:    h,
		MinWidth:  minFrameWidth,
		MinHeight: minFrameHeight,
		// Frameless。OS のタイトルバーの代わりに、フロント側(frame.ts)が上部に
		// 自前のツールバーを描く。撮る操作をここに置くことで、メイン画面を見なくても
		// 枠だけで撮り続けられる。ツールバーはキャプチャ領域の外(上)にあるので
		// 写り込まない(領域の算出は captureservice.go の captureRegion)。
		//
		// Frameless にするとウィンドウの移動・リサイズが OS 任せでなくなる。移動は
		// ツールバーの `--wails-draggable: drag`、リサイズは Wails ランタイムによる
		// ウィンドウ端の検出で行う。どちらも frontend/src/main.ts の
		// `import "@wailsio/runtime"` が前提。
		Frameless:        true,
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL:              "/?window=frame",
		// 中継の再生ウィンドウの「上」に重ねて盤面に合わせる道具なので、最前面は必須。
		// これが無いと枠が中継ウィンドウの後ろに回り、位置合わせができなくなる(実測)。
		AlwaysOnTop: true,
		Windows: application.WindowsWindow{
			// Frameless の既定では DWM のフレームをクライアント領域に延ばして
			// 影・角丸を残す(Wails の framelessWithDecorations)。この枠は中継映像の
			// 上に重ねる透過ウィンドウなので、影が盤の周囲に落ちるのも角が丸まるのも
			// 邪魔になるうえ、透過領域に DWM が何か描く余地を残したくない。
			DisableFramelessWindowDecorations: true,
		},
	}
	applyPosition(&opts, st)
	return app.Window.NewWithOptions(opts)
}

// newMainWindow はアプリ本体の画面を作る。起動時は非表示。
// 2 つ目の戻り値は「保存された位置を持っているか」で、初回だけ枠の外へ逃がす判断に使う。
func newMainWindow(app *application.App, st windowState) (*application.WebviewWindow, bool) {
	w, h := safeFallback(st, defaultMainWidth, defaultMainHeight)

	opts := application.WebviewWindowOptions{
		Title:     "ikkyoku",
		Width:     w,
		Height:    h,
		MinWidth:  minMainWidth,
		MinHeight: minMainHeight,
		URL:       "/?window=main",
		// 起動直後は枠だけを見せる。最初のキャプチャで現れる(revealMain)。
		Hidden: true,
		// AlwaysOnTop は付けない。中継を観ながら使う画面なので、最前面に居座ると
		// 中継そのものを覆ってしまう。最前面が要るのは位置合わせをする枠だけ。
	}
	hasSavedPos := applyPosition(&opts, st)
	return app.Window.NewWithOptions(opts), hasSavedPos
}

// applyPosition は保存位置があればそれを、無ければ中央表示を設定する。
// 戻り値は保存位置を適用したかどうか。
func applyPosition(opts *application.WebviewWindowOptions, st windowState) bool {
	if st.X == unsetPosition && st.Y == unsetPosition {
		// 初回起動(保存された位置が無い)は中央表示にフォールバックする。
		// センチネル値をそのまま X/Y に渡すと画面外に出てしまうため。
		opts.InitialPosition = application.WindowCentered
		return false
	}
	opts.X, opts.Y = st.X, st.Y
	opts.InitialPosition = application.WindowXY
	return true
}

func registerFrameHooks(wins *appWindows, st windowState, svc *CaptureService) {
	frame := wins.frame
	// WindowRuntimeReady はウィンドウ単位で発火し、この時点なら ScreenNearestDipPoint も
	// SetSize/SetPosition も確実に効く(ApplicationStarted では不確実)。
	frame.RegisterHook(events.Common.WindowRuntimeReady, func(e *application.WindowEvent) {
		if st.X != unsetPosition || st.Y != unsetPosition {
			x, y, w, h := clampToScreen(st, defaultFrameWidth, defaultFrameHeight)
			frame.SetSize(w, h)
			frame.SetPosition(x, y)
		}
		// 初回起動(中央表示)でも実際の座標を記録しておく。動かさずに終了しても
		// 次回同じ場所に出るようにするため。
		wins.frameGeom.record(frame)
	})

	// 枠は閉じずに隠す。Alt+F4・タスクバーから閉じる・OS シャットダウンはこの経路を通る
	// (ツールバーの自前✕は通らないので、そちらは JS から HideFrame を直接呼んでいる。
	// wails3 skill pitfalls.md「Frameless ウィンドウの×ボタンは通常 WindowClosing を通らない」)。
	//
	// **OnWindowEvent(listener)ではなく RegisterHook(hook)を使うこと。**
	// デフォルトの破棄用 listener は並列 goroutine で走るため、listener 側で Cancel() しても
	// 破棄とレースして止まらないことがある。hook は listener より前に同期実行される
	// (wails3 skill tray-hotkey.md 2)。
	frame.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		svc.HideFrame()
	})
}

func registerMainHooks(app *application.App, wins *appWindows, st windowState, logger *slog.Logger) {
	main := wins.main
	if st.X != unsetPosition || st.Y != unsetPosition {
		main.RegisterHook(events.Common.WindowRuntimeReady, func(e *application.WindowEvent) {
			x, y, w, h := clampToScreen(st, defaultMainWidth, defaultMainHeight)
			main.SetSize(w, h)
			main.SetPosition(x, y)
		})
	}

	// メイン画面を閉じる = アプリ終了。枠は隠れているだけで生き続けるため、
	// ここで明示的に Quit しないとプロセスが残る。
	//
	// ここで Cancel() して確認ダイアログを出すことはしない。WindowClosing は OS の
	// シャットダウンでも発火し、通常のクローズと区別できないため、Cancel するとシャット
	// ダウンをブロックしてしまう(wails3 skill pitfalls.md 4)。保存だけを同期で済ませる。
	//
	// **保存する座標はここで Position() を読んで得たものではない。** この時点では
	// 破棄が進行中で不正な値が返るため、動いたときに記録しておいた値を使う(geometry.go)。
	main.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		st := appState{
			Frame: wins.frameGeom.snapshot(),
			Main:  wins.mainGeom.snapshot(),
		}
		if err := saveAppState(st); err != nil {
			logger.Error("ウィンドウ状態の保存に失敗しました", "error", err)
		} else {
			logger.Info("ウィンドウ状態を保存しました",
				"frame", st.Frame, "main", st.Main)
		}
		app.Quit()
	})
}

// registerHotkey はグローバルホットキー(既定 alt+s)を登録する。
// ウィンドウが非アクティブでも効くことが重要(中継の再生画面にフォーカスがある状態で
// 撮るのが普通の使い方のため)。
func registerHotkey(app *application.App, svc *CaptureService, logger *slog.Logger) {
	accel, err := hotkeyAccelerator(resolveHotkey())
	if err != nil {
		logger.Error("ホットキーの解決に失敗しました", "error", err)
		return
	}
	if err := app.GlobalShortcut.Register(accel, func() {
		if _, err := svc.Capture(); err != nil {
			logger.Error("ホットキーからのキャプチャに失敗しました", "error", err)
			app.Event.Emit("capture:failed", err.Error())
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
}
