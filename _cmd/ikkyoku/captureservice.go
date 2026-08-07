package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// 枠ウィンドウのレイアウト寸法(CSS px)。**ここが唯一のソース。**
//
// フロント側は起動時に CaptureService.Layout() を呼んでこの値を受け取り、CSS 変数に
// 流し込んで描画する(frame.ts)。以前はフロントにも同じ定数を置いて「両方を必ず
// 一致させること」という運用にしていたが、ずれると枠やツールバーが写り込むという
// 直接的な不具合になるうえ、ツールバーの追加で同期対象が 2 つに増えるため、
// キャプチャ矩形を実際に計算する Go 側に一本化した。
//
// キャプチャ領域は「クライアント領域から、ツールバーとガイド枠を除いた内側の矩形」に
// 決める(決定論的な方式。撮る直前に枠を消すタイミング依存の方式は採らない)。
const (
	// guideBorderPx はガイド枠の線の太さ。
	guideBorderPx = 2
	// toolbarHeightPx は枠ウィンドウ上部のツールバーの高さ。
	// Frameless なので、これが OS のタイトルバーの代わりになる。
	toolbarHeightPx = 32
)

// framelessBottomPaddingPx は Wails が Frameless ウィンドウの下端に入れる余白(物理px)。
//
// Wails は WM_NCCALCSIZE で `rgrc.Bottom += 1` と `setPadding(edge.Rect{Bottom: 1})` を
// 行っている(リサイズ時のちらつき回避。webview_window_windows.go の該当箇所にコメントあり)。
// 結果として **クライアント領域の最下 1 行には WebView が描画されず**、ガイド枠の下辺は
// GetClientRect の下端より 1px 上に来る。これを引かないと、撮った画像の最下行に
// 赤いガイド枠が 1px 写り込む(実測で確認済み)。
//
// DPI ではなく物理ピクセル単位の固定値なので、スケール変換はしない。
// なお最大化・全画面のときは Wails 側がこの余白を入れないため 1px 余分に内側を撮る
// ことになるが、盤が 1px 欠けるだけで実害は無い(枠が写り込む方を避ける)。
const framelessBottomPaddingPx = 1

// fitMinBoardPx は自動フィットで受け入れる盤の最小の一辺(物理px)。
//
// 9 マスに割ると 1 マス 10px。これ以下の矩形に枠を合わせると、盤ではない何かを
// 掴んでいたときに枠が潰れて操作できなくなる。信頼度の判定(MinRegionConfidence)を
// 通ったあとの最後の歯止め。
const fitMinBoardPx = 90

// fitMarginCellRatio は自動フィットで盤の外側に残す余白(マス 1 つの何割か)。
// fitMinMarginPx はその下限(物理px)。
//
// **盤にぴったり合わせると、次に撮った画像が認識しづらくなる。** 盤の外枠の線が
// 画像の端に来てしまい、検出(DetectBoard)が格子として掴めなくなるため。撮り溜めた
// PNG を「検出した矩形ぴったり」と「余白つき」で切り出して Recognize に流すと、
// ぴったり側だけが落ちる(実測: 0.63←0.90 / 0.79←0.99 / 0.86←0.98 / 0.88←1.00 / 0.91←1.00。
// 悪いものは検出そのものが失敗する)。**フィットの目的は認識を良くすることなので、
// ここで余白を取らないと機能として本末転倒になる。**
//
// 余白はマスの大きさに比例させる(盤の見かけの大きさは中継によって 2 倍以上違う)。
// 同じ実測で +2〜+8px(マス 70〜100px に対して 2〜11%)がどれも安定していたので、
// その真ん中を取っている。**大きくしすぎないこと**(余白に写った中継の UI が
// 盤の格子と競合しうる)。
//
// **この余白は「もう合っている」の判定にもそのまま使う**(fitGeometry の slop)。
// 検出結果は毎回 数px 揺れるので、余白より小さいずれまで直しにいくと押すたびに
// 枠が動く。余白より小さいずれは**盤が枠に収まっているかどうかを変えない**ので、
// 直す必要が無い。
const (
	fitMarginCellRatio = 0.1
	fitMinMarginPx     = 2
)

// fitMinShrinkRatio は自動フィットで許す縮小の下限(今のキャプチャ領域に対する一辺の比)。
//
// **9x9 のグリッド検出には「半分の周期」で 1 校 100 点が出る当たり方がある。**
// マス 2 つぶんを 1 マスとみなすと格子線が 1 本おきに一致し、盤の内側は
// どこを切り取っても色が均一なので、信頼度(ValidateBoard)は 1.00 のまま
// **盤の 1/4 の領域**が返る。撮り溜めた 62 枚のうち 3 枚で実際に起きた
// (例: 651x700 の盤に対して (10,312) 309x334 で信頼度 1.00)。
//
// この当たり方は**縦横の両方がちょうど半分**になるのが特徴なので、両辺ともこの比を
// 下回る候補は採らない。片辺だけ小さいのは「枠の縦横比が盤と違う」という普通の状態で、
// これは弾かない。信頼度では区別が付かないため、大きさで見るしかない。
//
// 副作用として「盤が枠の半分以下しか占めていない」ときもフィットしなくなるが、
// これは**枠の中から盤を探す**この機能の想定(盤より少し大きめに枠を置いてから押す)の
// 外側なので、枠を近づけてから押し直せばよい。
//
// **検出そのものを直すのは suteme の仕事。** ここでやっているのは
// 「アプリとして、ユーザーが手で合わせた枠をどこまで信じて動かすか」の線引き。
const fitMinShrinkRatio = 0.6

// GuideLayout は枠ウィンドウの描画寸法(CSS px)をフロントに渡すための型。
type GuideLayout struct {
	BorderPx  int `json:"borderPx"`
	ToolbarPx int `json:"toolbarPx"`
}

// RecognizerStatus は駒種推論器(suteme)の読み込み状況。
type RecognizerStatus struct {
	// Source は読み込み元。設定で指定していなければ空(suteme 既定の探索に任せる)。
	Source string `json:"source"`
	Ready  bool   `json:"ready"`
	Error  string `json:"error"`
}

// CaptureService は Wails にバインドする、GUI からのキャプチャ操作。
// ロジックは持たず、ikkyoku ルートパッケージ(Capture / SavePNG / DefaultOutDir)を
// 呼ぶだけに徹する(ikkyoku/CLAUDE.md: 将棋のロジックを書かない、状態を持たない)。
type CaptureService struct {
	app *application.App
	// wins は枠とメイン画面。**キャプチャ領域は枠のクライアント矩形そのもの**なので、
	// メイン画面がどちらであっても撮る基準は枠のまま。枠は隠されていても HWND が
	// 生きているため、非表示でも領域の定義は有効(そのまま撮れる)。
	wins   *appWindows
	logger *slog.Logger

	// beforeQuit は枠のメニューの「終了」から呼ぶ後始末（ウィンドウ位置の保存・
	// エンジンとの接続の close）。**終了の入口が 2 つある**ので、中身は
	// メイン画面を閉じる経路と同じものを main.go で 1 本にしてある。
	beforeQuit func()

	// recognizerDir は駒種推論器の学習データの置き場所(ikkyoku.Config の SutemeDataDir)。
	// 空なら suteme 既定の探索(カレントディレクトリ → 実行ファイルのディレクトリ)に任せる。
	recognizerDir string

	mu sync.Mutex
	// mainShown はメイン画面を一度でも出したか。**初回だけやること**
	// (枠の外への配置・表示位置の記録)を 2 回目以降に繰り返さないための記録。
	// 前面に出す(Show/Focus)のは毎回。revealMain 参照。
	mainShown bool
	// recognizerStatus は直近の読み込み結果。表示のためだけに 3.5MB を
	// 読み直さなくて済むよう覚えておく。
	recognizerStatus RecognizerStatus
}

func NewCaptureService(logger *slog.Logger, recognizerDir string) *CaptureService {
	return &CaptureService{logger: logger, recognizerDir: recognizerDir}
}

// ReloadRecognizer は駒種推論器を読み込み直し、その結果を返す。起動時にも呼ぶ。
//
// **再読み込みの入口を用意しているのは、学習データを育てながら使うため。**
// suteme は一度読み込んだ推論器をキャッシュするので、学習データを更新しても
// これを呼ぶまで(あるいは再起動するまで)反映されない。
// 訂正 → 学習データ更新 → 撮り直す、というループを回すのにアプリの再起動を
// 挟みたくない。
func (s *CaptureService) ReloadRecognizer() RecognizerStatus {
	st := s.loadRecognizer()
	s.mu.Lock()
	s.recognizerStatus = st
	s.mu.Unlock()
	return st
}

// Recognizer は直近の読み込み結果を返す。**読み込み直さない。**
// フロントが起動時に状態を表示するためだけに 3.5MB を読み直すのを避ける。
func (s *CaptureService) Recognizer() RecognizerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recognizerStatus
}

func (s *CaptureService) loadRecognizer() RecognizerStatus {
	if s.recognizerDir == "" {
		// suteme 既定の探索に任せる。ここではキャッシュを捨てるだけで、
		// 実際に読めるかどうかは最初のキャプチャのときに分かる。
		recognize.UseDefaultPredictor()
		s.logger.Info("駒種推論器は suteme の既定探索に任せます")
		return RecognizerStatus{}
	}

	st := RecognizerStatus{Source: s.recognizerDir}
	if err := recognize.UsePredictorFrom(s.recognizerDir); err != nil {
		st.Error = err.Error()
		s.logger.Warn("駒種推論器を読み込めませんでした", "dir", s.recognizerDir, "error", err)
		return st
	}
	st.Ready = true
	s.logger.Info("駒種推論器を読み込みました", "dir", s.recognizerDir)
	return st
}

// bind は main() から起動シーケンスの中で呼ぶ。ServiceStartup は使わない
// (ウィンドウは Service 登録より後にしか作れないため。wails3 skill pitfalls.md 5 の
// 「main() から手動で初期化する」パターンに準拠)。
func (s *CaptureService) bind(app *application.App, wins *appWindows) {
	s.app = app
	s.wins = wins
}

// revealMain はメイン画面を出し、前面に持ってくる(アクティブにする)。
//
// 起動直後は枠だけを見せ、最初のキャプチャでメイン画面が現れる、という導線。
// **撮るたびに毎回アクティブにする。** 撮った結果(盤・SFEN・警告)を見るのが
// 撮った直後にやることなので、最小化していても背面にいても出てくるのが速い。
// 引き換えに中継の再生画面からフォーカスが外れる。動画の再生は続くが、
// 中継側のキーボード操作(シークなど)はメイン画面をクリックし直すまで効かなくなる
// (Alt+S はグローバルホットキーなので、フォーカスがどこにあっても撮れる)。
//
// ⚠️ **前面に出るのは撮り終えた後**。Capture() の最後で呼んでいるので、
// メイン画面が枠に重なっていてもその 1 枚には写らない。ただし重なったまま
// 次を撮ると写り込むので、初回の配置(placeMainBesideFrame)で枠の外へ逃がしている。
//
// ホットキー経由のキャプチャは別 goroutine から来るのでロックで保護する
// (Show/Focus 自体は Wails が内部で InvokeSync するのでスレッドは問わない)。
func (s *CaptureService) revealMain() {
	if s.wins == nil || s.wins.main == nil {
		return
	}
	s.mu.Lock()
	first := !s.mainShown
	s.mainShown = true
	s.mu.Unlock()

	if first {
		s.placeMainBesideFrame()
	}
	s.wins.main.Show()
	// 最小化されていると Show() だけでは畳まれたまま。Focus() の前に戻す。
	if s.wins.main.IsMinimised() {
		s.wins.main.UnMinimise()
	}
	s.wins.main.Focus()
	if first {
		// 表示されて初めて位置が確定するので、ここで記録しておく
		// (非表示のあいだの座標は当てにならない。geometry.go 参照)。
		s.wins.mainGeom.record(s.wins.main)
	}
}

// placeMainBesideFrame はメイン画面を枠に重ならない位置へ置く。
// 重なったまま撮るとメイン画面ごと写り込むため(画面の合成結果を撮るので z 順では避けられない)。
// 座標はどちらも Wails の DIP なので DPI 換算は不要(物理ピクセルが要るのは
// キャプチャ領域の算出だけ。captureRegion 参照)。
//
// 前回終了時の位置を復元している場合は何もしない。ユーザーが自分で決めた位置を
// 毎回上書きしてしまうため(初回起動時だけの安全策)。
func (s *CaptureService) placeMainBesideFrame() {
	if s.wins.mainHasSavedPos {
		return
	}
	// 枠の位置は、動くたびに記録してある値を使う(終了時に限らず、
	// Position() の値が当てにならない場面があるため。geometry.go 参照)。
	f := s.wins.frameGeom.snapshot()
	_, mh := s.wins.main.Size()

	y := f.Y - mh - windowGap
	if y < 0 {
		y = f.Y + f.Height + windowGap // 上に置く余白が無ければ枠の下へ
	}
	s.wins.main.SetPosition(f.X, y)
}

// HideFrame は枠を隠す。フロント(ツールバーの✕)と Alt+F4 の両方から呼ばれる。
//
// 閉じずに隠すだけなのは、枠が「見せるための UI」ではなく「撮る領域の定義」だから。
// 隠しても HWND は生きているので、そのまま Alt+S で同じ領域を撮り続けられる。
// むしろ隠したほうが、ツールバーやガイド枠が写り込む余地が原理的に無くなる。
//
// 枠を隠した結果として可視ウィンドウが 1 枚も無くなると、アプリが動いているのに
// 操作できない状態になる。それを避けるため、メイン画面がまだ出ていなければ出す。
func (s *CaptureService) HideFrame() {
	if s.wins == nil || s.wins.frame == nil {
		return
	}
	s.revealMain()
	s.wins.frame.Hide()
}

// ShowMain はメイン画面を出して前面に持ってくる。枠のメニューから呼ばれる。
//
// **枠からメイン画面を出す手段が「撮る」か「枠を✕で隠す」しか無かった。** どちらも
// 副作用が目的とずれている(撮りたくないのに撮る / 位置合わせに使う枠が消える)ので、
// メイン画面を見たいときの入口をメニューに作った。
//
// tab は開いてほしいタブ("board" / "debug" / "settings")。空なら今のタブのまま。
// **イベントは Show の前に出す**(Capture と同じ理由。前面に来た時点で目的のタブが
// 開いている状態にする)。メイン画面は隠れていてもフロントは動いているので、
// 非表示のあいだに出したイベントも受け取れる。
func (s *CaptureService) ShowMain(tab string) {
	if s.app != nil && tab != "" {
		s.app.Event.Emit("main:tab", tab)
	}
	s.revealMain()
}

// Quit はアプリを終了する。枠のツールバーのメニュー(▼ → 閉じる)から呼ばれる。
//
// **枠の✕は「隠す」であって「終了」ではない**(HideFrame。領域の定義を生かすため)。
// そのため枠しか出ていない状態では終了する手段が無く、メイン画面を一度出してから
// 閉じるしかなかった。枠だけで使っているときの終了の入口がこれ。
//
// メイン画面を閉じたときと同じものを残す必要があるので、**Quit の前に位置・サイズを
// 保存する**(app.Quit() が WindowClosing のフックを通す保証は無い)。二重に保存されても
// 同じ記録から書くので害は無い。
func (s *CaptureService) Quit() {
	if s.app == nil {
		return
	}
	if s.beforeQuit != nil {
		s.beforeQuit()
	}
	s.logger.Info("枠のメニューから終了します")
	s.app.Quit()
}

// ShowFrame は隠した枠を出し直す。メイン画面のボタンから呼ばれる。
func (s *CaptureService) ShowFrame() {
	if s.wins == nil || s.wins.frame == nil {
		return
	}
	s.wins.frame.Show()
	s.wins.frame.Focus()
}

// Layout は枠ウィンドウが描くべき寸法を返す。フロントは起動時にこれを呼び、
// CSS 変数に反映してからガイド枠を描く(定数の二重管理を避けるため)。
func (s *CaptureService) Layout() GuideLayout {
	return GuideLayout{BorderPx: guideBorderPx, ToolbarPx: toolbarHeightPx}
}

// CaptureResult はフロントに返すキャプチャ結果。
//
// 盤面の認識結果も含むが、**認識できなくてもキャプチャは成功**として返す
// (設計原則3「段階的に劣化すること」。撮った 1 局面が残ることのほうが大事で、
// 認識はその上に乗るもの)。認識だけが失敗したときは SFEN が空になり、
// RecognizeError に理由が入る。
type CaptureResult struct {
	Path      string `json:"path"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Thumbnail string `json:"thumbnail"` // data:image/png;base64,... のサムネイル(等倍)

	SFEN       string         `json:"sfen"`       // 盤面部分のみ。認識できなければ空
	Confidence float64        `json:"confidence"` // 盤面検出の信頼度(0.0〜1.0)
	Warnings   []string       `json:"warnings"`   // 局面として成立していない点
	HandTotal  map[string]int `json:"handTotal"`  // 駒台の推定枚数(先後不明)

	// RecognizeError は「撮れたが認識できなかった」ときの理由。
	// キャプチャ自体の失敗はこれではなく Capture のエラーで表す。
	RecognizeError string `json:"recognizeError"`

	// Debug は認識の観測情報(盤面と判定した矩形・マス割り・使った推論器)。
	// **デバッグタブ専用。** 撮った画像にこの矩形を重ねることで、認識が外れたときに
	// 「座標がずれているのか、駒種を外しているのか」を切り分けられる。
	//
	// 画像を Go 側で描いて返さないのは、サムネイルが既に等倍 PNG の base64 で、
	// 描き込んだ 2 枚目を積むとイベントのペイロードが倍になるため。矩形の座標だけを
	// 渡してフロントで重ねれば軽く、マスごとの確信度をホバーで出すこともできる。
	Debug *recognize.Debug `json:"debug,omitempty"`
}

// Capture はガイド枠の内側を撮って PNG 保存し、保存先パスとサムネイルを返す。
// フロントの「撮る」ボタンとグローバルホットキーの両方から呼ばれる。
// 1 回のキャプチャは他のキャプチャと完全に独立している(状態を持たない)。
func (s *CaptureService) Capture() (CaptureResult, error) {
	region, _, err := s.captureRegion()
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
	result := CaptureResult{
		Path:      path,
		Width:     b.Dx(),
		Height:    b.Dy(),
		Thumbnail: thumb,
		Warnings:  []string{},
		HandTotal: map[string]int{},
	}
	s.logger.Info("キャプチャしました", "path", path, "width", b.Dx(), "height", b.Dy())

	// 盤面の認識。**ここで失敗してもキャプチャは成功として返す。**
	// PNG は既に保存できており、撮った 1 局面を失わないことのほうが大事
	// (設計原則3。認識失敗はキャプチャの失敗ではない)。
	//
	// **オプションは付けない(suteme の既定 = 全部調べるがエラーにはしない)。**
	// 駒数が合わない・玉が無いといった盤面でも、撮った 1 局面は解析させたい。
	// 「おかしい」は Warnings として UI に出し、直すのは訂正 UI(Phase 5)の仕事。
	// エラーになるのは盤そのものが取れなかったときだけになる。
	if board, err := recognize.FromImage(img); err != nil {
		result.RecognizeError = err.Error()
		s.logger.Warn("盤面を認識できませんでした", "path", path, "error", err)
	} else {
		result.SFEN = board.SFEN
		result.Confidence = board.Confidence
		result.Warnings = board.Warnings
		result.HandTotal = board.HandTotal
		result.Debug = board.Debug
		s.logger.Info("盤面を認識しました",
			"sfen", board.SFEN, "confidence", board.Confidence, "warnings", len(board.Warnings),
			// 盤面と判定した矩形・その決め方・使った推論器の 1 行要約。
			// 撮り溜めたログから「いつから外し始めたか」を追えるようにしておく。
			"detail", board.Debug.String())
	}
	if s.app != nil {
		s.app.Event.Emit("capture:done", result)
	}
	// 撮れたらメイン画面を出してアクティブにする。イベントを先に出しておくことで、
	// 前面に来た時点でメイン画面が最新の結果を持っている状態になる。
	s.revealMain()
	return result, nil
}

// FitResult はガイド枠の自動フィットの結果。
//
// **見つからなかったことはエラーではない。** 盤が映っていない画面に枠を置いている
// ことも、認識が外すこともある(設計原則3)。そのときは Fitted=false と理由を返し、
// 枠は 1px も動かさない。error になるのはキャプチャ自体ができなかったときだけ。
type FitResult struct {
	Fitted     bool    `json:"fitted"`
	Confidence float64 `json:"confidence"`
	Message    string  `json:"message"`
}

// FitFrame は**画面に出ている盤**を探して、そこへガイド枠を合わせる。
//
// **枠を盤に合わせること自体が認識の精度に効く。** suteme は「盤だけが写っている画像」
// なら素直に解けるので、枠が盤に合っているほど当たりが良くなる。位置合わせを楽にする
// だけの機能ではない。
//
// 探すのは**枠の内側 → 枠がいるディスプレイ全体**の順(findBoard)。「枠の内側だけ」を
// やめたのは、枠を先に盤へ近づけておく必要があり、**それができるなら手で合わせるのと
// あまり変わらない**ため。今は「その画面に今出ている盤に合わせる」という機能になっている。
//
// 画面全体を撮るときは**自分のウィンドウを画面から消してから撮る**:
//
//   - 枠は Hide() して撮り、撮り終えたら戻す。赤いガイド枠が写ると格子の検出を汚す
//   - メイン画面は消さず、撮った画像の上でその矩形を塗り潰す(maskWindows)。
//     **メイン画面には `<shogi-board>` が本物の盤を描いている**ので、放っておくと
//     中継の盤より綺麗なそちらが選ばれる
//
// 隠して撮り直すぶん、押してから結果が出るまでに一呼吸ある(枠が一瞬消える)。
// **撮る操作(Capture)にこの方式を持ち込まないこと。** あちらはタイミングに依存しない
// 決定論的な領域算出が要点で、こちらはユーザーが押した 1 回きりの操作なので待てる。
//
// 枠の位置はユーザーが手で合わせたものなので、**怪しい検出結果では動かさない**。
// 判断は recognize.DetectRegion の信頼度・最小サイズ・「枠の中で半分に縮む候補は採らない」
// の 3 つ。
func (s *CaptureService) FitFrame() (FitResult, error) {
	region, scale, err := s.captureRegion()
	if err != nil {
		return FitResult{}, err
	}
	b, conf, err := s.findBoard(region)
	if err != nil {
		return FitResult{}, err
	}
	if b.Empty() {
		return FitResult{
			Confidence: conf,
			Message:    "盤が見つかりませんでした",
		}, nil
	}
	if b.Dx() < fitMinBoardPx || b.Dy() < fitMinBoardPx {
		s.logger.Info("検出した盤が小さすぎるので合わせませんでした",
			"width", b.Dx(), "height", b.Dy(), "confidence", conf)
		return FitResult{
			Confidence: conf,
			Message:    "検出した盤が小さすぎます",
		}, nil
	}
	// 位置・サイズは記録(frameGeom)ではなく今の値を読む。記録は「終了時に読めない」
	// 問題への対策で、動作中の値は正しい(geometry.go)。
	x, y := s.wins.frame.Position()
	w, h := s.wins.frame.Size()
	cur := windowState{X: x, Y: y, Width: w, Height: h}
	// **盤ぴったりではなく、少し外側に合わせる**(fitMarginCellRatio 参照)。
	next, moved := fitGeometry(cur, region, withFitMargin(b), fitMargin(b), scale)
	if !moved {
		return FitResult{
			Fitted:     true,
			Confidence: conf,
			Message:    fmt.Sprintf("既に合っています(信頼度 %.2f)", conf),
		}, nil
	}
	nx, ny, nw, nh := next.X, next.Y, next.Width, next.Height

	s.wins.frame.SetSize(nw, nh)
	s.wins.frame.SetPosition(nx, ny)
	// 移動・リサイズのイベントは飛ぶはずだが、保存される値がこの操作を取りこぼすと
	// 次回起動で元の位置に戻る。ここで明示的に記録しておく。
	s.wins.frameGeom.record(s.wins.frame)

	s.logger.Info("ガイド枠を盤に合わせました",
		"confidence", conf,
		"from", fmt.Sprintf("%d,%d,%dx%d", x, y, w, h),
		"to", fmt.Sprintf("%d,%d,%dx%d", nx, ny, nw, nh))
	return FitResult{
		Fitted:     true,
		Confidence: conf,
		Message:    fmt.Sprintf("盤に合わせました(信頼度 %.2f)", conf),
	}, nil
}

// findBoard は盤を探し、**スクリーン座標・物理ピクセル**の矩形と信頼度を返す。
// 見つからなければ空の矩形を返す(error はキャプチャ自体に失敗したときだけ)。
//
// **枠の内側 → 画面全体、の順に探す。**
//
// 画面全体だけにしないのは、**小さく切り出した画像のほうが確実に当たる**ため。
// 実測(デスクトップのスクリーンショットに盤を合成して検出)では、盤だけを切り出せば
// ほぼ 1.00 で当たるものが、2560x1440 の画面全体では 12 回中 3 回見つからず、
// 1 回は「半分の周期」の誤検出になった。**枠が既に盤を囲んでいるなら、
// その中で探すほうが速くて確実。**
//
// 逆に画面全体を見ないと、枠を先に盤へ近づけておく必要があり、それができるなら
// 手で合わせるのと変わらない。両方やるのはそのため。
func (s *CaptureService) findBoard(region ikkyoku.Region) (image.Rectangle, float64, error) {
	// 1) 枠の内側。ここは枠を隠す必要が無い(ガイド枠もツールバーも領域の外)。
	if img, err := ikkyoku.Capture(region); err == nil {
		det, err := recognize.DetectRegion(img)
		b := det.Rect.Sub(img.Bounds().Min).Add(image.Pt(region.X, region.Y))
		switch {
		case err != nil:
			s.logger.Debug("枠の内側には盤がありませんでした", "confidence", det.Confidence)
		case s.looksLikePartOfBoard(b, region, det.Confidence):
			// 縦横とも半分に縮む候補は「盤の一部」を掴んでいる可能性が高い。
			// 採らずに画面全体の探索へ回す(そちらで本来の盤が見つかることがある)。
		default:
			s.logger.Info("枠の内側で盤を見つけました",
				"rect", b.String(), "confidence", det.Confidence)
			return b, det.Confidence, nil
		}
	} else {
		s.logger.Warn("枠の内側を撮れませんでした", "error", err)
	}

	// 2) 画面全体(枠がいるディスプレイ 1 枚)。
	disp, err := s.frameDisplay(region)
	if err != nil {
		return image.Rectangle{}, 0, err
	}
	img, err := s.captureWithoutSelf(disp)
	if err != nil {
		return image.Rectangle{}, 0, err
	}
	det, err := recognize.DetectRegion(img)
	if err != nil {
		s.logger.Info("画面に盤が見つかりませんでした",
			"display", disp.String(), "confidence", det.Confidence, "error", err)
		return image.Rectangle{}, det.Confidence, nil
	}
	b := det.Rect.Sub(img.Bounds().Min).Add(image.Pt(disp.X, disp.Y))
	s.logger.Info("画面の中に盤を見つけました", "rect", b.String(), "confidence", det.Confidence)
	return b, det.Confidence, nil
}

// looksLikePartOfBoard は「枠が囲んでいる盤の一部」を掴んだ疑いがあるかを返す。
//
// 9x9 のグリッド検出には**マス 2 つぶんを 1 マスとみなす**当たり方があり、
// 格子線が 1 本おきに一致するうえ盤の内側は色が均一なので、信頼度 1.00 のまま
// 盤の 1/4 が返る(fitMinShrinkRatio 参照)。縦横の**両方**がちょうど半分になるのが
// 特徴なので、大きさで見分ける。片辺だけ小さいのは「枠の縦横比が盤と違う」という
// 普通の状態なので弾かない。
//
// **枠の内側を探すときだけの判定。** 画面全体から探すときは、見つけた盤が
// 今の枠と無関係な場所にあるので比べる意味が無い。
func (s *CaptureService) looksLikePartOfBoard(board image.Rectangle, region ikkyoku.Region, conf float64) bool {
	if float64(board.Dx()) >= float64(region.Width)*fitMinShrinkRatio ||
		float64(board.Dy()) >= float64(region.Height)*fitMinShrinkRatio {
		return false
	}
	s.logger.Info("枠の内側で見つけた盤が小さすぎるので採りませんでした",
		"board", fmt.Sprintf("%dx%d", board.Dx(), board.Dy()),
		"region", fmt.Sprintf("%dx%d", region.Width, region.Height),
		"confidence", conf)
	return true
}

// frameDisplay は枠がいるディスプレイ全体の領域を返す。
//
// **探すのは 1 枚だけ。** 全モニタをまとめて撮ると、ディスプレイごとに DPI が違う
// 環境で「見つけた盤のあるモニタ」と「枠のいるモニタ」の換算係数が食い違う
// (scale は枠の HWND から取っているため)。中継とガイド枠は同じ画面にあるのが自然なので、
// 枠のいるディスプレイに絞る。
func (s *CaptureService) frameDisplay(region ikkyoku.Region) (ikkyoku.Region, error) {
	center := image.Pt(region.X+region.Width/2, region.Y+region.Height/2)
	for _, d := range ikkyoku.ListDisplays() {
		if center.In(d.Bounds) {
			return d.Region(), nil
		}
	}
	// モニタ構成の隙間などで中心がどこにも入らないとき。撮れないよりはましなので
	// プライマリに落とす(見つからなければ「盤が見つかりません」になるだけ)。
	s.logger.Warn("枠がどのディスプレイにも属していません。プライマリを探します", "region", region.String())
	return ikkyoku.PrimaryRegion()
}

// fitHideSettle は枠を隠してから撮るまでの待ち時間。
//
// Hide() が返った時点では画面の合成結果に反映されているとは限らない。
// 60Hz で 3 フレームぶん見ておけば、下にある中継が塗り直される時間としては十分。
// **短くしすぎると、消したはずの赤い枠が写る。**
const fitHideSettle = 50 * time.Millisecond

// captureWithoutSelf は自分のウィンドウを取り除いた画面を撮る。
//
// 枠は隠して撮り、すぐ戻す。メイン画面は隠さず、撮った画像の上で塗り潰す
// (maskWindows)。**隠すと z 順やフォーカスが動くので、消す必要があるだけの
// メイン画面にはやらない。** 枠だけ隠すのは、ガイド枠が「探す対象の上に重なる線」
// そのもので、塗り潰すと盤まで消えてしまうため。
func (s *CaptureService) captureWithoutSelf(disp ikkyoku.Region) (image.Image, error) {
	frame := s.wins.frame
	hidden := false
	if frame.IsVisible() {
		frame.Hide()
		hidden = true
		time.Sleep(fitHideSettle)
	}

	img, err := ikkyoku.Capture(disp)

	if hidden {
		frame.Show()
	}
	if err != nil {
		return nil, err
	}
	s.maskWindows(img, disp)
	return img, nil
}

// maskWindows は撮った画像から、まだ写っている自分のウィンドウを塗り潰す。
//
// **メイン画面には `<shogi-board>` が本物の将棋盤を描いている。** 中継の盤より
// 綺麗な格子なので、放っておくと検出はそちらを選ぶ。塗り潰しはウィンドウ全体
// (タイトルバー込み)で、消し残しを作らない。
//
// 画像が *image.RGBA でない(将来キャプチャの実装が変わった)場合は何もしない。
// 塗り潰せないこと自体は致命的ではなく、検出が外れるだけで枠は動かない。
func (s *CaptureService) maskWindows(img image.Image, disp ikkyoku.Region) {
	rgba, ok := img.(*image.RGBA)
	if !ok {
		s.logger.Warn("撮った画像を塗り潰せません(*image.RGBA ではありません)")
		return
	}
	main := s.wins.main
	if main == nil || !main.IsVisible() || main.IsMinimised() {
		return
	}
	hwnd := main.NativeWindow()
	if hwnd == nil {
		return
	}
	r, err := windowRectPhysical(hwnd)
	if err != nil {
		s.logger.Warn("メイン画面の矩形を取得できませんでした", "error", err)
		return
	}

	// スクリーン座標 → 撮った画像の座標。
	off := img.Bounds().Min.Sub(image.Pt(disp.X, disp.Y))
	rect := image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height).Add(off).Intersect(rgba.Bounds())
	if rect.Empty() {
		return // 別のディスプレイにいる
	}
	draw.Draw(rgba, rect, image.NewUniform(color.Black), image.Point{}, draw.Src)
	s.logger.Debug("メイン画面を塗り潰しました", "rect", rect.String())
}

// fitMargin は盤の外側に残す余白(物理px)。マスの大きさに比例する。
func fitMargin(board image.Rectangle) int {
	cell := float64(board.Dx()) / 9
	if h := float64(board.Dy()) / 9; h < cell {
		cell = h // 縦横で違う場合は狭いほうに合わせる(余白が過剰にならないように)
	}
	m := int(math.Round(cell * fitMarginCellRatio))
	if m < fitMinMarginPx {
		m = fitMinMarginPx
	}
	return m
}

// withFitMargin は盤の矩形を余白のぶんだけ広げる。
//
// **画像の外にはみ出しても切り詰めない。** 枠は今より大きくなってよく、はみ出した
// ぶんには画面の続きが写るだけ。ここで image.Bounds() に丸めると、盤が枠の端に
// 接している(= まさに余白が要る)ときに限って余白が消える。
func withFitMargin(board image.Rectangle) image.Rectangle {
	return board.Inset(-fitMargin(board))
}

// fitGeometry は「盤がスクリーンのどこにあるか」から、枠ウィンドウの新しい
// 位置・サイズ(DIP)を求める。moved が false なら動かす必要は無い。
//
// board は**スクリーン座標・物理ピクセル**の矩形(撮った画像の座標系ではない。
// 画面全体から探すので、画像の原点とキャプチャ領域の原点が一致しないため)。
// region は今のキャプチャ領域で、これもスクリーン座標・物理ピクセル。
//
// キャプチャ領域はガイド枠の内側そのものなので、**region と board のずれをそのまま
// ウィンドウに足せば**枠の内側が盤に重なる。ツールバーやガイド枠の太さは
// 位置とサイズの両方に同じだけ乗っているので、差分にすると消える(足し引き不要)。
//
// scale は CSS px → 物理 px の係数で、ウィンドウの座標系(DIP)へ割り戻すのに使う。
// slop は「もう合っている」とみなすずれ(物理px。呼び出し側は余白と同じ値を渡す)。
//
// **サイズは外側に倒す**(scaleUp と同じ理由の裏返し。丸めで縮むと盤の端が欠ける。
// 1px 広いぶんには盤の外周が少し余分に写るだけで実害が無い)。
//
// GUI から切り離してあるのは、符号を 1 つ間違えると枠が逆へ飛ぶのに、
// 実機で気づくしかなくなるため(fitGeometry のテストがある)。
func fitGeometry(cur windowState, region ikkyoku.Region, board image.Rectangle, slop int, scale float64) (windowState, bool) {
	dx := board.Min.X - region.X
	dy := board.Min.Y - region.Y
	dw := board.Dx() - region.Width
	dh := board.Dy() - region.Height
	if abs(dx) <= slop && abs(dy) <= slop && abs(dw) <= slop && abs(dh) <= slop {
		return cur, false
	}
	return windowState{
		X:      cur.X + int(math.Round(float64(dx)/scale)),
		Y:      cur.Y + int(math.Round(float64(dy)/scale)),
		Width:  cur.Width + int(math.Ceil(float64(dw)/scale)),
		Height: cur.Height + int(math.Ceil(float64(dh)/scale)),
	}, true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// CopyImage は保存済みの PNG をクリップボードへ入れる。デバッグタブから呼ばれる。
//
// **撮った画像をメモリに抱えず、保存したファイルを読み直す。** 1 回のキャプチャは
// 他のキャプチャと独立という方針(ikkyoku/CLAUDE.md)に沿って「直近の画像」を
// 持たずに済むし、後から一覧を作ってどの 1 枚でもコピーできるようにするときも
// そのまま使える。読み直しの費用は数 MB の PNG のデコード 1 回だけ。
//
// パスの持ち主はフロント(直前の CaptureResult.Path)。テキストのコピーは Wails
// ランタイムの Clipboard.SetText で完結するのでフロント側にあり、画像だけがここに来る
// (画像はランタイムに口が無く、Win32 を直接叩く必要があるため。clipboard_windows.go)。
func (s *CaptureService) CopyImage(path string) error {
	if path == "" {
		return fmt.Errorf("ikkyoku-app: コピーする画像がありません")
	}
	f, err := os.Open(path)
	if err != nil {
		s.logger.Warn("画像を開けませんでした", "path", path, "error", err)
		return err
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		s.logger.Warn("画像を読めませんでした", "path", path, "error", err)
		return err
	}
	if err := copyImageToClipboard(img); err != nil {
		s.logger.Warn("画像をクリップボードに入れられませんでした", "path", path, "error", err)
		return err
	}
	s.logger.Info("画像をクリップボードに入れました", "path", path)
	return nil
}

// captureRegion はネイティブウィンドウハンドル(HWND)からクライアント領域を
// 物理ピクセルのスクリーン座標で求め、ツールバーとガイド枠を除いた内側を返す。
//
// 枠ウィンドウのクライアント領域は、上から順に次のように積まれている(frame.ts):
//
//	┌──────────────────────────┐
//	│ ツールバー(toolbarHeightPx)│ ← 撮る/隠す。ドラッグ移動もここ
//	├──────────────────────────┤
//	│ ┌──────────────────────┐ │ ← ガイド枠(guideBorderPx)
//	│ │   ここを撮る(透過)    │ │
//	│ └──────────────────────┘ │
//	└──────────────────────────┘
//
// Frameless なのでクライアント領域はウィンドウ全体と一致する(Wails が WM_NCCALCSIZE で
// 標準フレームを外すため)。タイトルバー・枠の厚みを別途足し引きする必要はない。
//
// Wails の Window.Position()/Size() は DIP(論理ピクセル)を返すため、
// マルチモニタでスケーリング(150%等)が混在する環境ではそのまま使うと領域がずれる。
// HWND から Windows API を直接呼べば物理ピクセルで確実に一致する
// (clientrect_windows.go)。
//
// scale(CSS px → 物理 px)も返す。**キャプチャ領域から枠の寸法を逆算する側
// (FitFrame)が同じ係数を要る**ためで、取り直すと DPI が変わった瞬間に
// 撮った領域と戻す先が食い違う。
func (s *CaptureService) captureRegion() (ikkyoku.Region, float64, error) {
	if s.wins == nil || s.wins.frame == nil {
		return ikkyoku.Region{}, 0, fmt.Errorf("ikkyoku-app: ウィンドウが初期化されていません")
	}
	hwnd := s.wins.frame.NativeWindow()
	if hwnd == nil {
		return ikkyoku.Region{}, 0, fmt.Errorf("ikkyoku-app: ネイティブウィンドウハンドルを取得できませんでした")
	}

	rect, scale, err := clientRectPhysical(hwnd)
	if err != nil {
		return ikkyoku.Region{}, 0, err
	}

	border := scaleUp(guideBorderPx, scale)
	top := scaleUp(toolbarHeightPx, scale) + border
	bottom := border + framelessBottomPaddingPx
	region := ikkyoku.Region{
		X:      rect.X + border,
		Y:      rect.Y + top,
		Width:  rect.Width - border*2,
		Height: rect.Height - top - bottom,
	}
	if !region.Valid() {
		return ikkyoku.Region{}, 0, fmt.Errorf("ikkyoku-app: ウィンドウが小さすぎます(ツールバーとガイド枠で領域が無くなります)")
	}
	return region, scale, nil
}

// scaleUp は CSS px を物理ピクセルに変換する。**切り上げる。**
//
// ブラウザ側の丸めと 1px ずれることがあるため、どちらに倒すかを決める必要がある。
// 内側に 1px 多く食い込む(盤が 1px 欠ける)のは実害が無いが、外側に 1px はみ出すと
// 赤いガイド枠やツールバーがキャプチャに写り込み、認識(Phase 2)のノイズになる。
// したがって常に内側に倒す。
func scaleUp(cssPx int, scale float64) int {
	return int(math.Ceil(float64(cssPx) * scale))
}
