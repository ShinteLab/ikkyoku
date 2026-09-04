// ikkyoku コマンドは ikkyoku の Wails3 GUI。
//
// ウィンドウは 2 枚。
//
//   - メイン画面   … アプリ本体。**起動するとこれが出る**(入力タブ)。
//     **閉じるとアプリが終了する。**
//   - 枠ウィンドウ … 画面に重ねる Frameless の透過ウィンドウ。「どこを撮るか」の定義そのもの。
//     上部のツールバーから撮れる。**起動時は出さない**——タイトルバーの「枠を表示」を
//     押すまで隠れている。閉じても破棄せず隠すだけ(領域の定義は生かしたまま)。
//
// ⚠️ **起動時に出るのは枠ではなくメイン画面**(2026-08-10 に入れ替えた)。以前は
// 「枠だけが出て、最初のキャプチャでメイン画面が現れる」だったが、**入力の口が
// 画面キャプチャだけではなくなる**(SFEN / KIF / 画像ファイル)ので、
// 撮ることを前提にした導線をやめた。枠は「撮るときだけ使う道具」の位置づけになる。
//
// 「メイン」は枠ではなくメイン画面。枠は位置合わせが済めば用済みになりうる道具で、
// アプリの寿命を握る画面ではない。ただし**キャプチャ領域の基準は枠のまま**で、これは
// 移せない(領域は枠のクライアント矩形そのもの。captureservice.go 参照)。
//
// 実処理は親パッケージ github.com/ShinteLab/ikkyoku をそのまま呼ぶ。Wails 依存のコードは
// このディレクトリ(_cmd/ikkyoku)にのみ置く(wails3 skill の architecture 方針)。
package main

import (
	"embed"
	"log/slog"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	// ⚠️ **別名にしてあるのは、この関数の中の `app`（application.App）と
	// パッケージ名がぶつかるから。**
	ikkyokuapp "github.com/ShinteLab/ikkyoku/app"
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

	settingsSvc := NewSettingsService(logger)
	cfg := settingsSvc.config()
	captureSvc := NewCaptureService(logger, cfg.SutemeDataDir, cfg.SutemeSourceOr())
	positionSvc := NewPositionService(logger)
	trainingSvc := NewTrainingService(logger, settingsSvc)
	// 「駒の字」（設定タブ）。端末に入っているフォントから駒の字を焼く。
	// **盤に当てるのはフロント**で、ここが返すのは family 名と data URL まで。
	fontSvc := NewFontService(logger, settingsSvc)
	// 局面を持つ Service は 2 つあり、**別のものを持っている**（混同しないこと）。
	//
	//   positionSvc … 訂正タブ。認識の誤りを直す面。未決・不正でよい
	//   studySvc    … 解析タブ。確定した局面。**訂正タブから写しを採る**
	//
	// 受け渡しは studySvc.Adopt の 1 か所だけ（訂正タブの「この局面を解析する」）。
	studySvc := NewStudyService(logger, positionSvc)
	// 解析は**確定した局面**にだけかかる。局面を持っているのは studySvc なので、
	// フロントから SFEN を渡してもらうのではなく、あちらから読む。
	analyzeSvc := NewAnalyzeService(logger, studySvc, settingsSvc)
	// 棋譜データベース（棚）。**実装は kicho のままで、ikkyoku は利用する側**。
	// ⚠️ **開けなくてもアプリは動く**（設計原則3）——「解析する」の行き先を持つので
	// studySvc のあとに作り、開くのは下（失敗しても起動を止めない）。
	kifuSvc := NewKifuService(logger, studySvc)
	// フロントが生きているかの計測だけを持つ Service(diagservice.go)。
	// 局面にもキャプチャにも関与しない。
	diagSvc := ikkyokuapp.NewDiagService(logger)

	app := application.New(application.Options{
		Name:        "ikkyoku",
		Description: "ikkyoku - shogi broadcast region capture",
		Logger:      logger,
		Services: []application.Service{
			application.NewService(captureSvc),
			application.NewService(settingsSvc),
			application.NewService(positionSvc),
			application.NewService(studySvc),
			application.NewService(trainingSvc),
			application.NewService(fontSvc),
			application.NewService(analyzeSvc),
			application.NewService(kifuSvc),
			application.NewService(diagSvc),
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
	settingsSvc.bind(app)
	// 棚を開く。⚠️ **失敗してもここで止めない** —— 理由は KifuService が抱えて
	// 設定タブに出す（撮った 1 局面と貼った棋譜の解析は棚に依らない。設計原則3）。
	if dbPath, err := cfg.KifuDB(); err != nil {
		logger.Warn("棋譜データベースの場所を決められませんでした", "error", err)
	} else {
		kifuSvc.open(dbPath)
	}
	// 設定タブで場所を変えたらその場で開き直す（認識器の読み込み元と同じ扱い）。
	settingsSvc.onKifuDBPath = kifuSvc.open
	// 枠の素通し（設定「枠の内側で後ろの画面を操作する」）。
	// **枠の HWND を触るのは CaptureService** なので、設定タブからの切り替えは
	// ここで繋いだこのフックを通る（SettingsService はウィンドウを持っていない）。
	settingsSvc.onClickThrough = captureSvc.applyClickThrough
	settingsSvc.onSutemeSource = captureSvc.applyRecognizerSource
	settingsSvc.onSutemeDataDir = captureSvc.applyRecognizerDir
	captureSvc.applyClickThrough(cfg.ClickThrough)
	analyzeSvc.bind(app)

	// 終了の入口は 2 つ（メイン画面を閉じる / 枠のメニューの「終了」）。
	// **後始末はこの 1 本に寄せる**（保存の経路を 1 本にしてあるのと同じ理由）。
	quit := func() {
		saveWindowState(wins, logger)
		analyzeSvc.close()
		kifuSvc.close()
	}
	captureSvc.beforeQuit = quit

	registerFrameHooks(app, wins, state.Frame, captureSvc, cfg.FitOnStartup, logger)
	registerMainHooks(app, wins, state.Main, quit)
	registerHotkey(app, captureSvc, logger)
	registerVisibilityLog(wins, logger)
	diagSvc.Watch()

	// 駒種推論器(suteme)を先に用意しておく。3.5MB の学習データを読むので、
	// 最初のキャプチャのときに読み始めると撮った瞬間に待たされる。
	captureSvc.ReloadRecognizer()

	if err := app.Run(); err != nil {
		logger.Error("アプリが異常終了しました", "error", err)
		os.Exit(1)
	}
}

// newFrameWindow は盤に重ねる Frameless の透過ウィンドウを作る。
//
// ⚠️ **常に隠した状態で作る**(2026-08-10)。枠は「撮るときだけ使う道具」なので、
// タイトルバーの「枠を表示」を押すまで出さない。出すのは 2 経路だけ:
//
//   - `CaptureService.ShowFrame`（タイトルバーのトグル・枠のメニュー経由）
//   - `startupFit`（設定「起動時に盤面を探す」。**合わせ終えてから**出す）
//
// **隠していても HWND は生きている**ので、出し直せば前と同じ領域に戻る。ただし
// ⚠️ **隠しているあいだは撮れない**(`CaptureService.Capture` の `requireFrame`)。
// 「今どこを撮るのか」が画面に出ていないまま撮れるのは事故のもとなので、
// **枠が見えていることを撮れる条件にしてある。**
func newFrameWindow(app *application.App, st windowState) *application.WebviewWindow {
	w, h := safeFallback(st, defaultFrameWidth, defaultFrameHeight)

	opts := application.WebviewWindowOptions{
		Hidden:    true,
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

// newMainWindow はアプリ本体の画面を作る。
//
// ⚠️ **`Hidden: true` で作るが、起動時にすぐ出す**(`registerFrameHooks` の
// `WindowRuntimeReady` → `revealMain`)。**最初から `Hidden: false` にしないこと** ——
// 位置決め(`placeMainBesideFrame`)も座標の記録も `revealMain` が面倒を見ており、
// そこを通らないと初回のキャプチャで**ウィンドウが突然動く**。
//
// ⚠️ **メイン画面も Frameless**(2026-08-12)。OS のタイトルバーを外し、**その場所に
// タブの行を上げてある**(mainscreen.ts の `.main-toolbar`)。狙いは**縦の領域**で、
// 「タイトルバー + タブの行」で 2 段使っていたところが 1 段になる。解析タブの盤は
// `100vh` から引いて決まる(style.css の `--board-size`)ので、**そのぶん盤が大きくなる**。
//
// 引き換えに OS が面倒を見ていたものを自前で持つ:
//
//   - 移動   … `.main-toolbar` の `--wails-draggable: drag`(タブとボタンは `no-drag`)
//   - リサイズ … Wails ランタイムのウィンドウ端の検出。**`DisableResize` を付けないこと**
//   - 最小化 / 最大化 / 閉じる … ツールバー右端の自前のボタン
//
// ⚠️ **自前の✕は `WindowClosing` を通らない**(wails3 skill pitfalls.md)。そのため
// フロントは `CaptureService.Quit` を呼ぶ(位置・サイズの保存を含む終了の入口)。
// `registerMainHooks` の `WindowClosing` フックは Alt+F4・OS シャットダウン用に残す。
//
// ⚠️ **枠と違って `DisableFramelessWindowDecorations` は付けない。** あちらは中継映像に
// 重ねる透過ウィンドウなので影も角丸も邪魔だったが、こちらは普通のアプリの窓で、
// 影と角丸が無いと**どこまでがウィンドウか分からなくなる**。
//
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
		// 上の ⚠️ を読むこと。タイトルバーのぶんを画面に返すための Frameless。
		Frameless: true,
		// 出すのは revealMain。上の ⚠️ を読むこと。
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

// startupFitDelay は起動時の自動フィットを始めるまでの待ち時間。
//
// 直前に `SetSize`/`SetPosition` で枠を復元位置へ動かしているので、その結果が
// HWND に反映されるのを待つ(**枠の内側を探す一手目がその矩形を基準にする**)。
// 枠は隠したままなので「描かれ切るのを待つ」必要は無く、短くてよい。
// **ここを伸ばすと、起動してから枠が出るまでの無言の時間がそのまま伸びる。**
const startupFitDelay = 200 * time.Millisecond

func registerFrameHooks(app *application.App, wins *appWindows, st windowState, svc *CaptureService, fitOnStartup bool, logger *slog.Logger) {
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

		// ⚠️ **起動時にメイン画面を出すのはここ。**「枠の座標が確定したあと」でないと
		// `placeMainBesideFrame` が保存前のセンチネル値(-32000)を基準に置いてしまう
		// (初回起動でメイン画面が画面外へ飛ぶ)。**枠のフックに置いてあるのはこの順序を
		// 保証するためで、メイン画面側のフックへ移さないこと**(2 つのウィンドウの
		// WindowRuntimeReady はどちらが先か決まっていない)。
		if fitOnStartup {
			// 探しているあいだは枠もメイン画面も画面に無い状態にしておく
			// (自分のウィンドウが 1 枚も写らないので、塗り潰しも要らない)。
			// 出すのは合わせ終えてから。
			startupFit(app, wins, svc, logger)
			return
		}
		// **枠は出さない。** タイトルバーの「枠を表示」を押すまで隠れたまま。
		svc.revealMain()
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

// startupFit は起動時に一度だけ盤を探し、**合わせ終えてから枠を出す**(設定 fitOnStartup)。
//
// 枠は隠した状態で作ってある(newFrameWindow)。**見えている枠を後から動かすのではなく、
// 最初から合った位置に出す**のが狙い。ボタンを押したときと違って、枠を隠して撮る
// 手順(captureWithoutSelf)も走らない — 最初から出ていないので隠す必要が無い。
// メイン画面もまだ出していない(下記)ので、塗り潰し(maskWindows)も要らない。
// **起動時は画面に自分のウィンドウが 1 枚も無い**状態で探せる。
//
// ⚠️ **この設定のときだけ枠が自動で出る。** 「枠を表示を押すまで出さない」の唯一の
// 例外だが、**この設定の目的がまさに「最初から盤に合った枠が出ていること」**なので、
// ここで隠したままにすると設定の意味が無くなる。既定はオフ。
//
// 枠そのものは先に作る必要がある。キャプチャ領域は枠のクライアント矩形そのもの
// (captureservice.go)で、座標の逆算もその矩形との差分で出しているため。
//
// 別 goroutine で走らせるのは、探すのに数秒かかるため。ここで待つと
// WindowRuntimeReady のフックが返らず、起動が止まる。
//
// **どの経路を通っても最後に必ず枠とメイン画面を出す。** 盤が見つからなくても、
// 探索が失敗しても、どちらも出ないままではアプリが操作できない。
// ⚠️ **メイン画面は枠のあと**(`revealMain` が枠の位置を基準に置くため。
// 合わせたあとの枠に対して逃がさないと、キャプチャ領域に重なりうる)。
func startupFit(app *application.App, wins *appWindows, svc *CaptureService, logger *slog.Logger) {
	go func() {
		defer func() {
			wins.frame.Show()
			// ⚠️ **出したことをメイン画面へ知らせる**(タイトルバーのトグル)。
			// この経路だけ `ShowFrame` を通らないので、落とすと
			// **枠が出ているのにトグルが「枠を表示」のまま**になる。
			svc.emitFrameVisible()
			svc.revealMain()
		}()

		time.Sleep(startupFitDelay)
		started := time.Now()

		result, err := svc.FitFrame()
		if err != nil {
			logger.Warn("起動時の自動フィットに失敗しました", "error", err)
			app.Event.Emit("fit:done", FitResult{Message: "盤を探せませんでした: " + err.Error()})
			return
		}
		logger.Info("起動時の自動フィット",
			"fitted", result.Fitted, "message", result.Message, "elapsed", time.Since(started))
		app.Event.Emit("fit:done", result)
	}()
}

func registerMainHooks(app *application.App, wins *appWindows, st windowState, quit func()) {
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
		quit()
		app.Quit()
	})
}

// registerVisibilityLog は表示状態の変化(表示・非表示・最小化・復帰)をログに出す。
//
// ⚠️ **debug で出す**(2026-08-11)。枠を動かしたりリサイズしたりするだけで大量に出て
// (「戻す」が連続する)、**他のログがこれに埋もれる**ため。アプリのログレベルは info の
// ままなので、既定では出ない。**消さないこと** —— 下記のとおり現象の再現待ちで要る
// 記録なので、出すのをやめるのではなく普段は黙らせているだけ。追うときは
// この 1 行を Info に戻すか、ハンドラの Level を debug にする。
//
// **これだけは推測ではなく記録が要る。** メイン画面が真っ黒になって触れなくなる現象は、
// Wails が最小化のときに WebView2 を不可視にし、復帰の分岐でしか戻さない作りに
// 由来すると見ている(diagservice.go / CaptureService.revealMain)。だとすると
// **「最小化 → 復帰の並びのどこで戻し損ねたか」がログに出ていないと追えない。**
// 起きたときの手掛かりが `[WebView2] Focus failed` の 4 行しか無かったのが前回の反省。
//
// 位置・サイズの追跡(geometry.go)とは別物なので混ぜないこと。あちらは保存のため、
// こちらは現象の再現待ち。**listener で十分**(記録するだけで、破棄とレースしない)。
func registerVisibilityLog(wins *appWindows, logger *slog.Logger) {
	watch := func(name string, w *application.WebviewWindow) {
		for label, id := range map[string]events.WindowEventType{
			"表示":  events.Common.WindowShow,
			"非表示": events.Common.WindowHide,
			"最小化": events.Common.WindowMinimise,
			"復帰":  events.Common.WindowUnMinimise,
			"戻す":  events.Common.WindowRestore,
		} {
			w.OnWindowEvent(id, func(*application.WindowEvent) {
				logger.Debug("ウィンドウの表示状態", "window", name, "event", label)
			})
		}
	}
	watch("frame", wins.frame)
	watch("main", wins.main)
}

// saveWindowState は枠とメイン画面の位置・サイズを保存する。
//
// 終了の入口が 2 つある(メイン画面を閉じる / 枠のメニューの「閉じる」)ので、
// **保存はこの 1 本に寄せる。** どちらから終了しても同じものが残る。
//
// **ここで Position()/Size() を読まない。** メイン画面を閉じる経路では既に破棄が
// 進行中で不正な値が返る(geometry.go)。動いたときに記録しておいた値を使う。
func saveWindowState(wins *appWindows, logger *slog.Logger) {
	st := appState{
		Frame: wins.frameGeom.snapshot(),
		Main:  wins.mainGeom.snapshot(),
	}
	if err := saveAppState(st); err != nil {
		logger.Error("ウィンドウ状態の保存に失敗しました", "error", err)
		return
	}
	logger.Info("ウィンドウ状態を保存しました", "frame", st.Frame, "main", st.Main)
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
