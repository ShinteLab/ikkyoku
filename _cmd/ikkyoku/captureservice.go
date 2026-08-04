package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"log/slog"
	"math"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku"
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

// GuideLayout は枠ウィンドウの描画寸法(CSS px)をフロントに渡すための型。
type GuideLayout struct {
	BorderPx  int `json:"borderPx"`
	ToolbarPx int `json:"toolbarPx"`
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

	mu sync.Mutex
	// mainShown はメイン画面を一度でも出したか。2 回目以降のキャプチャで
	// 前面に出し直さないための記録(観戦中にフォーカスを奪わない)。
	mainShown bool
}

func NewCaptureService(logger *slog.Logger) *CaptureService {
	return &CaptureService{logger: logger}
}

// bind は main() から起動シーケンスの中で呼ぶ。ServiceStartup は使わない
// (ウィンドウは Service 登録より後にしか作れないため。wails3 skill pitfalls.md 5 の
// 「main() から手動で初期化する」パターンに準拠)。
func (s *CaptureService) bind(app *application.App, wins *appWindows) {
	s.app = app
	s.wins = wins
}

// revealMain はメイン画面をまだ出していなければ出す。
//
// 起動直後は枠だけを見せ、最初のキャプチャでメイン画面が現れる、という導線のため。
// 2 回目以降は何もしない(中継を観ている最中にフォーカスを奪わない)。
// ホットキー経由のキャプチャは別 goroutine から来るのでロックで保護する
// (Show 自体は Wails が内部で InvokeSync するのでスレッドは問わない)。
func (s *CaptureService) revealMain() {
	if s.wins == nil || s.wins.main == nil {
		return
	}
	s.mu.Lock()
	if s.mainShown {
		s.mu.Unlock()
		return
	}
	s.mainShown = true
	s.mu.Unlock()

	s.placeMainBesideFrame()
	s.wins.main.Show()
	// 表示されて初めて位置が確定するので、ここで記録しておく
	// (非表示のあいだの座標は当てにならない。geometry.go 参照)。
	s.wins.mainGeom.record(s.wins.main)
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
	// 撮れたらメイン画面を出す(初回のみ)。イベントを先に出しておくことで、
	// 表示された時点でメイン画面が最新の結果を持っている状態になる。
	s.revealMain()
	return result, nil
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
func (s *CaptureService) captureRegion() (ikkyoku.Region, error) {
	if s.wins == nil || s.wins.frame == nil {
		return ikkyoku.Region{}, fmt.Errorf("ikkyoku-app: ウィンドウが初期化されていません")
	}
	hwnd := s.wins.frame.NativeWindow()
	if hwnd == nil {
		return ikkyoku.Region{}, fmt.Errorf("ikkyoku-app: ネイティブウィンドウハンドルを取得できませんでした")
	}

	rect, scale, err := clientRectPhysical(hwnd)
	if err != nil {
		return ikkyoku.Region{}, err
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
		return ikkyoku.Region{}, fmt.Errorf("ikkyoku-app: ウィンドウが小さすぎます(ツールバーとガイド枠で領域が無くなります)")
	}
	return region, nil
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
