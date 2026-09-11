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
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/guide"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// guide.ClickThroughInsetPx は素通しにする範囲を、撮る領域から内側へ詰める幅(CSS px)。
// clickThroughPoll はカーソルの位置を見に行く間隔。
//
// ⚠️ **撮る領域をそのまま素通しにしないこと。** Wails のリサイズ判定は
// 「クライアント領域の端 5px(角は +10px)」で、**ガイド枠(2px)より内側まで食い込む**。
// そこを素通しにすると、**左右と下の縁から枠をリサイズできなくなる**。
// 詰めたぶんは「押しても後ろへ抜けない細い縁」になるだけで、実害が無い。
//
// 間隔は 50ms。素通しのあいだ枠はマウスの動きを受け取れない(イベントが来ない)ので、
// **戻すきっかけを作れるのはポーリングだけ**。設定が入のときしか回さない。
// ⚠️ **詰め幅（guide.ClickThroughInsetPx）は ikkyoku/guide にある。**
// あちらは幾何の話で、ここにあるのは見張りの間隔だけ。
const clickThroughPoll = 50 * time.Millisecond

// RecognizerStatus は suteme のデータ(SutemeDataDir)の読み込み状況。
//
// **駒種推論器と帯の判定器を別々に持つ。** 前者は無ければ駒種が読めない(致命的)が、
// 後者は無くても検出は動く(1マス滑りを直せなくなるだけ)。ひとつの Ready / Error に
// まとめると、帯データを置き忘れているのに「認識器: OK」と出て気づけない。
type RecognizerStatus struct {
	// Source は読み込み元。設定で指定していなければ空(suteme 既定の探索に任せる)。
	Source string `json:"source"`
	Ready  bool   `json:"ready"`
	Error  string `json:"error"`

	// Mode は実際にどこから読んだか(`ikkyoku.SutemeSourceDir` / `SutemeSourceEmbed` /
	// 空＝suteme 既定の探索)。**設定の値そのものではない** ——
	// 設定が auto のときはここで初めてどちらかに決まるし、"embed" にしていても
	// 焼き込みの無いビルドでは dir へ落ちる。**画面にはこちらを出すこと。**
	Mode string `json:"mode"`
	// EmbedAvailable はこのビルドに認識器が焼き込まれているか
	// (`-tags embedmodel`)。設定タブが「焼き込み」を選べるかの判断に使う。
	EmbedAvailable bool `json:"embedAvailable"`

	// StripSamples は盤の縁の帯の判定器のサンプル数。0 なら読めていない。
	StripSamples int `json:"stripSamples"`
	// StripError は帯の判定器が読めなかった理由。**Error とは別**で、
	// これが埋まっていても認識自体は動く(盤の位置が 1マス滑ることがある)。
	StripError string `json:"stripError"`
}

// CaptureService は Wails にバインドする、GUI からのキャプチャ操作。
// ロジックは持たず、ikkyoku ルートパッケージ(Capture / SavePNG / DefaultOutDir)を
// 呼ぶだけに徹する(ikkyoku/CLAUDE.md: 将棋のロジックを書かない、状態を持たない)。
type CaptureService struct {
	app *application.App
	// wins は枠とメイン画面。**キャプチャ領域は枠のクライアント矩形そのもの**なので、
	// メイン画面がどちらであっても撮る基準は枠のまま。枠は隠されていても HWND が
	// 生きているため領域の定義自体は有効だが、**撮るのは枠が出ているときだけ**
	// (Capture の requireFrame)。
	wins   *appWindows
	logger *slog.Logger

	// beforeQuit は枠のメニューの「終了」から呼ぶ後始末（ウィンドウ位置の保存・
	// エンジンとの接続の close）。**終了の入口が 2 つある**ので、中身は
	// メイン画面を閉じる経路と同じものを main.go で 1 本にしてある。
	beforeQuit func()

	// recognizerDir は駒種推論器の学習データの置き場所(ikkyoku.Config の SutemeDataDir)。
	// 空なら suteme 既定の探索(カレントディレクトリ → 実行ファイルのディレクトリ)に任せる。
	recognizerDir string
	// recognizerSource は読み込み元の設定(ikkyoku.Config の SutemeSource)。
	// **設定タブから変えられる**ので、mu で守る(applyRecognizerSource)。
	recognizerSource string

	mu sync.Mutex
	// mainShown はメイン画面を一度でも出したか。**初回だけやること**
	// (枠の外への配置・表示位置の記録)を 2 回目以降に繰り返さないための記録。
	// 前面に出す(Show/Focus)のは毎回。revealMain 参照。
	mainShown bool
	// recognizerStatus は直近の読み込み結果。表示のためだけに 3.5MB を
	// 読み直さなくて済むよう覚えておく。
	recognizerStatus RecognizerStatus

	// clickThrough は設定「枠の内側で後ろの画面を操作する」（`ikkyoku.Config.ClickThrough`）。
	clickThrough bool
	// clickStop は素通しの見張り（watchCursor）を止めるチャネル。
	// **入のあいだだけ goroutine が居る。**
	clickStop chan struct{}
	// mouseThrough は今 WS_EX_TRANSPARENT を立てているか。
	// **設定そのものではない**（設定が入でも、カーソルがツールバーの上に居るあいだは false）。
	mouseThrough bool
}

func NewCaptureService(logger *slog.Logger, recognizerDir, recognizerSource string) *CaptureService {
	return &CaptureService{
		logger:           logger,
		recognizerDir:    recognizerDir,
		recognizerSource: recognizerSource,
	}
}

// applyRecognizerDir は学習データの置き場所の変更をその場で効かせる
// (SettingsService.onSutemeDataDir から呼ばれる)。
func (s *CaptureService) applyRecognizerDir(dir string) {
	s.mu.Lock()
	s.recognizerDir = dir
	s.mu.Unlock()
	s.ReloadRecognizer()
}

// applyRecognizerSource は設定「認識器の読み込み元」の変更をその場で効かせる
// (SettingsService.onSutemeSource から呼ばれる)。
//
// **再起動を待たせないのは、切り替えた結果を見るための設定だから** ——
// 焼き込みに切り替えて認識が落ちるなら、その場で戻せたほうがよい。
func (s *CaptureService) applyRecognizerSource(source string) {
	s.mu.Lock()
	s.recognizerSource = source
	s.mu.Unlock()
	s.ReloadRecognizer()
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

// loadRecognizer は設定に従って suteme のデータを読む。
//
// ⚠️ **読むものは 2 つ**(駒種推論器と盤の縁の帯の判定器)。片方だけ配線すると、
// 駒種は最新の学習データなのに盤の位置合わせは学習前、というちぐはぐな状態になる。
// recognize/predictor.go の先頭の注意書きも参照。
//
// **読み込み元は 3 通り**(`ikkyoku.Config.SutemeSource`)。どれを使うかの解決は
// resolveRecognizerSource が一手に引き受け、ここから先は分岐しない。
func (s *CaptureService) loadRecognizer() RecognizerStatus {
	s.mu.Lock()
	dir, pref := s.recognizerDir, s.recognizerSource
	s.mu.Unlock()

	mode := resolveRecognizerSource(pref, dir)
	st := RecognizerStatus{Mode: mode, EmbedAvailable: recognize.EmbeddedAvailable()}

	switch mode {
	case ikkyoku.SutemeSourceEmbed:
		st.Source = embeddedSourceLabel()
		// 帯の判定器は**推論器が読めなくても読む**(下の dir と同じ理由)。
		if n, err := recognize.UseStripJudgeEmbedded(); err != nil {
			st.StripError = err.Error()
			s.logger.Warn("焼き込んだ帯の判定器を読み込めませんでした(盤の位置が 1マス滑ることがあります)", "error", err)
		} else {
			st.StripSamples = n
			s.logger.Info("焼き込んだ帯の判定器を読み込みました", "samples", n)
		}
		if err := recognize.UsePredictorEmbedded(); err != nil {
			st.Error = err.Error()
			s.logger.Warn("焼き込んだ駒種推論器を読み込めませんでした", "error", err)
			return st
		}
		st.Ready = true
		s.logger.Info("焼き込んだ駒種推論器を読み込みました", "source", st.Source)
		return st

	case ikkyoku.SutemeSourceDir:
		st.Source = dir
		// 帯の判定器は**推論器が読めなくても読む**。盤の位置を合わせるだけなら
		// 駒種推論器は要らない(ガイド枠の自動フィット recognize.DetectRegion がそれ)。
		if n, err := recognize.UseStripJudgeFrom(dir); err != nil {
			st.StripError = err.Error()
			s.logger.Warn("盤の縁の帯の判定器を読み込めませんでした(盤の位置が 1マス滑ることがあります)",
				"dir", dir, "error", err)
		} else {
			st.StripSamples = n
			s.logger.Info("盤の縁の帯の判定器を読み込みました", "dir", dir, "samples", n)
		}
		if err := recognize.UsePredictorFrom(dir); err != nil {
			st.Error = err.Error()
			s.logger.Warn("駒種推論器を読み込めませんでした", "dir", dir, "error", err)
			return st
		}
		st.Ready = true
		s.logger.Info("駒種推論器を読み込みました", "dir", dir)
		return st

	default:
		// suteme 既定の探索に任せる。ここではキャッシュを捨てるだけで、
		// 実際に読めるかどうかは最初のキャプチャのときに分かる。
		recognize.UseDefaultPredictor()
		recognize.UseDefaultStripJudge()
		s.logger.Info("suteme のデータは既定探索に任せます")
		return st
	}
}

// resolveRecognizerSource は「設定の値」から「実際にどこから読むか」を決める。
//
// ⚠️ **auto を「焼き込み優先」にしていない。** 焼き込みは配るときのための固定した
// データで、ディレクトリは育て続けるデータ。開発中(＝ディレクトリを指している状態)に
// 焼き込みへ勝手に倒れると、**学習データを更新しても反映されない**という
// 最も気づきにくい事故になる。指定してあるほうがユーザーの意思表示なのでそちらを採る。
//
// **"embed" を選んでいても、焼き込みの無いビルドでは dir へ落ちる。**
// 設定ファイルは配布ビルドと開発ビルドで共用される(同じ `config.json`)ので、
// 「焼き込みで動かす設定のまま `wails3 dev` を動かす」は普通に起きる。
func resolveRecognizerSource(pref, dir string) string {
	switch pref {
	case ikkyoku.SutemeSourceEmbed:
		if recognize.EmbeddedAvailable() {
			return ikkyoku.SutemeSourceEmbed
		}
		if dir != "" {
			return ikkyoku.SutemeSourceDir
		}
		return ""
	case ikkyoku.SutemeSourceDir:
		if dir != "" {
			return ikkyoku.SutemeSourceDir
		}
		return ""
	default: // auto
		if dir != "" {
			return ikkyoku.SutemeSourceDir
		}
		if recognize.EmbeddedAvailable() {
			return ikkyoku.SutemeSourceEmbed
		}
		return ""
	}
}

// embeddedSourceLabel は焼き込んだデータの出所(画面とログに出す)。
func embeddedSourceLabel() string {
	if src := recognize.EmbeddedSource(); src != "" {
		return "焼き込み (" + src + ")"
	}
	return "焼き込み"
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
// **起動時に一度呼ばれ**(main.go の registerFrameHooks)、以後は撮るたび・
// 枠のメニューから呼ばれる。⚠️ **初回だけやること**(枠の外への配置・座標の記録)が
// ここに入っているので、**ウィンドウを Show する経路をここ以外に作らないこと。**
//
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
//
// ⚠️ **最小化されているときは先に戻す。畳まれたままの窓に Show() を当てない。**
// Wails は `WM_SIZE / SIZE_MINIMIZED` で WebView2 のコントローラを不可視にし
// (`chromium.Hide()` = `PutIsVisible(false)`)、**復帰の分岐でしか戻さない**。
// 畳まれたままの窓に Show() を当てると、その最小化⇄復帰の並びに
// `PutIsVisible` が横から差し込まれる。**戻し損ねるとメイン画面は真っ黒のまま
// 入力も受け付けなくなり、以後 Focus() は「状態が正しくない」で失敗し続ける**
// (2026-08-08 に実機で 1 回起きた。diagservice.go / RepairMain 参照)。
//
// **UnMinimise() は内部で Focus() まで済ませる**(Wails の restore())ので、
// そちらを通ったときに重ねて Focus() を呼ばない。
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
	if s.wins.main.IsMinimised() {
		s.wins.main.UnMinimise()
		s.wins.main.Show()
	} else {
		s.wins.main.Show()
		s.wins.main.Focus()
	}
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

// HideFrame は枠を隠す。**入口は 3 つ**（枠のツールバーの ✕ / Alt+F4 /
// **メイン画面のタイトルバーのトグル**）。⚠️ どれを通っても `frame:visible` で
// 知らせるので、**トグルの表示が食い違わない**。
//
// 閉じずに隠すだけなのは、枠が「見せるための UI」ではなく「撮る領域の定義」だから。
// 隠しても HWND は生きているので、**出し直せば前と同じ領域に戻る**（位置を覚えている）。
//
// ⚠️ **隠しているあいだは撮れない**（`Capture` の `requireFrame`）。技術的には
// 撮れてしまうが、**「今どこを撮るのか」が画面に出ていないまま撮れるのは事故のもと**
// なので、見えていることを条件にしてある。
//
// 枠を隠した結果として可視ウィンドウが 1 枚も無くなると、アプリが動いているのに
// 操作できない状態になる。それを避けるため、メイン画面がまだ出ていなければ出す
// (起動時に出しているので普通は既に出ているが、**保険は外さないこと**)。
func (s *CaptureService) HideFrame() {
	if s.wins == nil || s.wins.frame == nil {
		return
	}
	s.revealMain()
	s.wins.frame.Hide()
	s.emitFrameVisible()
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

// Quit はアプリを終了する。**フロントの自前の✕はどちらもここへ来る**
// (枠のツールバーのメニュー ▼ → 終了 / メイン画面のタイトルバーの ✕)。
//
// ⚠️ **メイン画面も Frameless にしたので、あちらの✕も `WindowClosing` を通らない**
// (自前のボタンなので OS の WM_CLOSE が飛ばない。wails3 skill pitfalls.md)。
// 終了時にやることを 2 か所に書かないよう、**入口はこの 1 本に寄せる**。
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
	s.logger.Info("終了します")
	s.app.Quit()
}

// RepairMain はメイン画面を隠して出し直す。枠のメニューの「メイン画面を描き直す」から呼ばれる。
//
// **メイン画面が真っ黒になって何も触れなくなったときの復帰手段。**
// Wails は Hide()/Show() でそれぞれ WebView2 の `PutIsVisible(false)`/`(true)` を呼ぶので、
// 最小化の復帰で不可視のまま取り残されたコントローラを表示状態に戻せる
// (現象と経緯は diagservice.go)。
//
// **入口を枠のメニューに置いたのが要点。** 黒くなるのはメイン画面なので、
// メイン画面の中にボタンを置いても押せない。枠は別ウィンドウなので生きている。
//
// これで絵が戻るなら「見えなくされていた」、戻らないならレンダラ側が死んでいる、
// という切り分けにもなる(心拍のログと合わせて読むこと)。
func (s *CaptureService) RepairMain() {
	if s.wins == nil || s.wins.main == nil {
		return
	}
	s.logger.Info("メイン画面を描き直します")
	s.wins.main.Hide()
	s.revealMain()
}

// requireFrame は枠が出ていなければ理由を返す。**撮る前の唯一の門番。**
//
// ⚠️ **`FitFrame` には掛けないこと。** あちらは枠を隠して撮り直す手順を内側に
// 持っており(`captureWithoutSelf`)、起動時の自動フィットに至っては枠を出す前に
// 走る。掛けると自分で自分を止める。
func (s *CaptureService) requireFrame() error {
	if s.wins == nil || s.wins.frame == nil {
		return fmt.Errorf("ikkyoku: ウィンドウが初期化されていません")
	}
	if !s.wins.frame.IsVisible() {
		return fmt.Errorf("ガイド枠が出ていません。タイトルバーの「枠を表示」から出して、撮りたい盤面に合わせてください")
	}
	return nil
}

// ShowFrame は隠した枠を出し直す。メイン画面のタイトルバーのボタンから呼ばれる。
func (s *CaptureService) ShowFrame() {
	if s.wins == nil || s.wins.frame == nil {
		return
	}
	s.wins.frame.Show()
	s.wins.frame.Focus()
	s.emitFrameVisible()
}

// FrameVisible は枠が今出ているか。**タイトルバーのトグルの初期状態**に使う
// (起動した時点で出ていることがある —— 設定「起動時に盤面を探す」)。
func (s *CaptureService) FrameVisible() bool {
	return s.wins != nil && s.wins.frame != nil && s.wins.frame.IsVisible()
}

// emitFrameVisible は枠の出入りをメイン画面へ知らせる(`frame:visible`)。
//
// **枠を隠す口はメイン画面の外にある**(枠のツールバーの ✕)ので、押した結果を
// メイン画面が自分では知れない。知らせないと、**枠が隠れているのにトグルが
// 「出ています」のまま**になり、押せないボタンだけが残る。
//
// ⚠️ **`captureWithoutSelf` からは呼ばない。** あちらは探すあいだ枠を一瞬隠して
// すぐ戻すだけで、**ユーザーから見た状態は変わっていない**(知らせるとトグルが明滅する)。
func (s *CaptureService) emitFrameVisible() {
	if s.app == nil {
		return
	}
	s.app.Event.Emit("frame:visible", s.FrameVisible())
}

// applyClickThrough は設定「枠の内側で後ろの画面を操作する」を反映する。
// **起動時（main.go）と、設定タブで切り替えたとき（SettingsService.SetClickThrough）**
// の 2 か所から呼ばれる。
//
// 入なら見張りの goroutine を 1 つ起こし、切ったら止めて素通しを解く。
// ⚠️ **切ったときに必ず解くこと。** 解き忘れると、**設定を切ったのに枠が
// 押せないまま**になり、画面からは理由が分からない。
func (s *CaptureService) applyClickThrough(on bool) {
	s.mu.Lock()
	if s.clickThrough == on {
		s.mu.Unlock()
		return
	}
	s.clickThrough = on
	stop := s.clickStop
	s.clickStop = nil
	if on {
		ch := make(chan struct{})
		s.clickStop = ch
		s.mu.Unlock()
		s.logger.Info("枠の内側を素通しにします")
		go s.watchCursor(ch)
		return
	}
	s.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	s.setMouseThrough(false)
	s.logger.Info("枠の素通しをやめます")
}

// watchCursor はカーソルが「撮る範囲の内側」に居るあいだだけ枠を素通しにする。
//
// **枠ごと素通しにしない**のがこの見張りの理由そのもの。WS_EX_TRANSPARENT は
// ウィンドウ単位でしか付けられないので、ツールバー（撮る・□・✕・ドラッグ移動）を
// 押せるまま残すには、**カーソルがどこに居るかで付け外しするしかない**。
//
// ⚠️ **素通しにしているあいだ、枠にはマウスのイベントが 1 つも来ない。**
// だから「カーソルがツールバーへ戻ってきた」を知る手段がポーリング以外に無い。
// 回すのは設定が入のあいだだけ（50ms）。
func (s *CaptureService) watchCursor(stop chan struct{}) {
	t := time.NewTicker(clickThroughPoll)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.stepCursor()
		}
	}
}

// stepCursor は 1 回ぶんの判定。
func (s *CaptureService) stepCursor() {
	if s.wins == nil || s.wins.frame == nil || !s.wins.frame.IsVisible() {
		// 枠が出ていないあいだは素通しにする相手が居ない。**必ず解いておく**
		// （出し直したときに、カーソルがツールバーの上でも押せない状態から始まらないように）。
		s.setMouseThrough(false)
		return
	}
	// ⚠️ **押している最中は切り替えない。** ドラッグ移動もリサイズもボタンを
	// 押したまま動かす操作なので、途中で素通しになると掴んだまま外れる。
	if mouseButtonDown() {
		return
	}
	x, y, ok := cursorPos()
	if !ok {
		return
	}
	region, scale, err := s.captureRegion()
	if err != nil {
		s.setMouseThrough(false)
		return
	}
	s.setMouseThrough(guide.InsideClickThrough(region, scale, x, y))
}

// setMouseThrough は枠の WS_EX_TRANSPARENT を実際に付け外しする。
// **変化したときだけ Win32 を呼ぶ**（50ms ごとに叩かないため）。
func (s *CaptureService) setMouseThrough(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on && !s.clickThrough {
		// 切った直後に見張りの最後の 1 回が滑り込むことがある。**入れ直さない。**
		return
	}
	if s.mouseThrough == on {
		return
	}
	if s.wins == nil || s.wins.frame == nil {
		return
	}
	hwnd := s.wins.frame.NativeWindow()
	if hwnd == nil {
		return
	}
	if err := setMouseTransparent(hwnd, on); err != nil {
		s.logger.Warn("枠の素通しを切り替えられませんでした", "on", on, "error", err)
		return
	}
	s.mouseThrough = on
}

// Layout は枠ウィンドウが描くべき寸法を返す。フロントは起動時にこれを呼び、
// CSS 変数に反映してからガイド枠を描く(定数の二重管理を避けるため)。
func (s *CaptureService) Layout() guide.Layout {
	return guide.Layout{BorderPx: guide.BorderPx, ToolbarPx: guide.ToolbarHeightPx}
}

// CaptureShot は「撮れた」ことだけを伝えるイベントのペイロード（`capture:shot`）。
//
// ⚠️ **撮ることと認識することは別の話**（2026-08-18 に分けた）。認識は数秒かかるので、
// 撮り終えてから `capture:done` まで待つと、その間ずっと枠に「撮影中…」が出たままになり、
// **いつ撮れた 1 枚なのかが画面から読めない**（撮り直したのか、まだ撮っていないのかも
// 分からない）。撮れた時点でこれを流し、枠は**そこでシャッターの合図を出し**、
// メイン画面は**撮った画像を出して「認識中…」に変わる**。
//
// 認識結果は載せない（まだ無い）。**中身は CaptureResult の「撮れた」ぶんだけ**で、
// 続きは `capture:done` が丸ごと持ってくる。
type CaptureShot struct {
	Path      string `json:"path"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Thumbnail string `json:"thumbnail"` // data:image/png;base64,... のサムネイル(等倍)
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
//
// ⚠️ **枠が出ていなければ撮らない**(2026-08-10 決定)。技術的には隠れていても
// HWND は生きているので撮れてしまうが、**「今どこを撮るのか」が画面に出ていない
// まま撮れるのは事故のもと**。枠は「どこを撮るか」の定義そのものなので、
// **見えていることを撮れる条件にする。**
func (s *CaptureService) Capture() (CaptureResult, error) {
	if err := s.requireFrame(); err != nil {
		return CaptureResult{}, err
	}

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

	// **撮れたことを先に知らせる。** 認識(下)は数秒かかるので、ここで一度切らないと
	// 枠は「撮影中…」のまま止まって見え、メイン画面は前の 1 枚を出したままになる。
	// 撮った時点で合図とメイン画面の表示を済ませ、認識は「認識中…」として続きを待たせる。
	//
	// ⚠️ **メイン画面を出すのもここ**(以前は認識まで終えてから出していた)。
	// 撮った直後に前に出るので、**今どの 1 枚の話をしているか**が画面から読める。
	// 撮影そのものは既に終わっているので、前に出たメイン画面が写り込むことはない。
	if s.app != nil {
		s.app.Event.Emit("capture:shot", CaptureShot{
			Path:      path,
			Width:     b.Dx(),
			Height:    b.Dy(),
			Thumbnail: thumb,
		})
	}
	s.revealMain()

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
	// 認識まで含めた結果。**メイン画面は既に出ている**(上の capture:shot)ので、
	// ここでやることは「認識中…」を結果で置き換えることだけ。
	if s.app != nil {
		s.app.Event.Emit("capture:done", result)
	}
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
	if b.Dx() < guide.MinBoardPx || b.Dy() < guide.MinBoardPx {
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
	cur := guide.Window{X: x, Y: y, Width: w, Height: h}
	// **盤ぴったりではなく、少し外側に合わせる**(fitMarginCellRatio 参照)。
	next, moved := guide.Geometry(cur, region, guide.WithMargin(b), guide.Slop(b), scale)
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
// 盤の 1/4 が返る(guide.MinShrinkRatio 参照)。縦横の**両方**がちょうど半分になるのが
// 特徴なので、大きさで見分ける。片辺だけ小さいのは「枠の縦横比が盤と違う」という
// 普通の状態なので弾かない。
//
// **枠の内側を探すときだけの判定。** 画面全体から探すときは、見つけた盤が
// 今の枠と無関係な場所にあるので比べる意味が無い。
func (s *CaptureService) looksLikePartOfBoard(board image.Rectangle, region ikkyoku.Region, conf float64) bool {
	// **大きさの線引きは ikkyoku/guide**（この節の理由もあちらのコメントにある）。
	// ⚠️ **ログはここに残すこと** —— 採るかどうかは幾何の話だが、
	// 「採らなかった」と知らせるのは GUI の仕事。
	if !guide.TooSmall(board, region) {
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

// applyEvalGraphDetached は評価値グラフの切り離しを窓に反映する（2026-09-08）。
//
// 呼ばれるのは 3 経路 —— 起動時（設定の値）・解析タブのトグル・グラフ窓を閉じたとき。
// **どれも `SettingsService.SetEvalGraphDetached` を通る**（起動時だけは直接）ので、
// **設定と窓の状態が食い違わない。**
//
// ⚠️ **メイン画面へ知らせること**（`graph:detached`）。窓を閉じて戻したときに
// これが無いと、**メイン画面のペインが出てこない**（グラフがどこにも無くなる）。
func (s *CaptureService) applyEvalGraphDetached(detached bool) {
	if s.wins == nil || s.wins.graph == nil {
		return
	}
	if detached {
		s.wins.graph.Show()
		s.wins.graph.Focus()
	} else {
		s.wins.graph.Hide()
	}
	if s.app != nil {
		s.app.Event.Emit("graph:detached", detached)
	}
	s.logger.Info("評価値グラフの置き場所を変えました", "detached", detached)
}

// applyStudyPaneDetached は**候補手の面**の切り離しを窓に反映する（2026-09-08。
// 2026-09-12 に手順を分けたので、右の列まるごとではない）。
//
// **`applyEvalGraphDetached` と同じ形。揃えておくこと** —— 2 つの窓で作法が違うと、
// どちらがどうだったかを覚えることになる。
func (s *CaptureService) applyStudyPaneDetached(detached bool) {
	if s.wins == nil || s.wins.side == nil {
		return
	}
	if detached {
		s.wins.side.Show()
		s.wins.side.Focus()
	} else {
		s.wins.side.Hide()
	}
	if s.app != nil {
		s.app.Event.Emit("side:detached", detached)
	}
	s.logger.Info("候補手の置き場所を変えました", "detached", detached)
}

// applyMovePaneDetached は**手順の面**の切り離しを窓に反映する（2026-09-12）。
//
// **`applyStudyPaneDetached` と同じ形。揃えておくこと** —— 3 つの窓で作法が違うと、
// どれがどうだったかを覚えることになる。
//
// ⚠️ **メイン画面へ知らせること**（`moves:detached`）。窓を閉じて戻したときに
// これが無いと、**メイン画面の手順が出てこない**（手順がどこにも無くなる）。
func (s *CaptureService) applyMovePaneDetached(detached bool) {
	if s.wins == nil || s.wins.moves == nil {
		return
	}
	if detached {
		s.wins.moves.Show()
		s.wins.moves.Focus()
	} else {
		s.wins.moves.Hide()
	}
	if s.app != nil {
		s.app.Event.Emit("moves:detached", detached)
	}
	s.logger.Info("手順の置き場所を変えました", "detached", detached)
}

// maskWindows は撮った画像から、まだ写っている自分のウィンドウを塗り潰す。
//
// **メイン画面には `<shogi-board>` が本物の将棋盤を描いている。** 中継の盤より
// 綺麗な格子なので、放っておくと検出はそちらを選ぶ。塗り潰しはウィンドウ全体
// (タイトルバー込み)で、消し残しを作らない。
//
// ⚠️ **自分の窓が増えたらここにも足すこと**（2026-09-08 に評価値グラフの窓を足した）。
// あちらに盤は無いが、**折れ線と目盛りは格子に似た直線の集まり**で、
// 検出を汚す余地がある。**「盤が描かれていないから要らない」と決めないこと。**
//
// 画像が *image.RGBA でない(将来キャプチャの実装が変わった)場合は何もしない。
// 塗り潰せないこと自体は致命的ではなく、検出が外れるだけで枠は動かない。
func (s *CaptureService) maskWindows(img image.Image, disp ikkyoku.Region) {
	rgba, ok := img.(*image.RGBA)
	if !ok {
		s.logger.Warn("撮った画像を塗り潰せません(*image.RGBA ではありません)")
		return
	}
	// スクリーン座標 → 撮った画像の座標。
	off := img.Bounds().Min.Sub(image.Pt(disp.X, disp.Y))
	for _, w := range []struct {
		name string
		win  *application.WebviewWindow
	}{
		{"メイン画面", s.wins.main},
		{"評価値グラフの窓", s.wins.graph},
		{"候補手の窓", s.wins.side},
		{"手順の窓", s.wins.moves},
	} {
		if w.win == nil || !w.win.IsVisible() || w.win.IsMinimised() {
			continue
		}
		hwnd := w.win.NativeWindow()
		if hwnd == nil {
			continue
		}
		r, err := windowRectPhysical(hwnd)
		if err != nil {
			s.logger.Warn("ウィンドウの矩形を取得できませんでした", "window", w.name, "error", err)
			continue
		}
		rect := image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height).Add(off).Intersect(rgba.Bounds())
		if rect.Empty() {
			continue // 別のディスプレイにいる
		}
		draw.Draw(rgba, rect, image.NewUniform(color.Black), image.Point{}, draw.Src)
		s.logger.Debug("ウィンドウを塗り潰しました", "window", w.name, "rect", rect.String())
	}
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
		return fmt.Errorf("ikkyoku: コピーする画像がありません")
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
//	│ ツールバー(guide.ToolbarHeightPx)│ ← 撮る/隠す。ドラッグ移動もここ
//	├──────────────────────────┤
//	│ ┌──────────────────────┐ │ ← ガイド枠(guide.BorderPx)
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
		return ikkyoku.Region{}, 0, fmt.Errorf("ikkyoku: ウィンドウが初期化されていません")
	}
	hwnd := s.wins.frame.NativeWindow()
	if hwnd == nil {
		return ikkyoku.Region{}, 0, fmt.Errorf("ikkyoku: ネイティブウィンドウハンドルを取得できませんでした")
	}

	rect, scale, err := clientRectPhysical(hwnd)
	if err != nil {
		return ikkyoku.Region{}, 0, err
	}

	border := guide.ScaleUp(guide.BorderPx, scale)
	top := guide.ScaleUp(guide.ToolbarHeightPx, scale) + border
	bottom := border + guide.FramelessBottomPaddingPx
	region := ikkyoku.Region{
		X:      rect.X + border,
		Y:      rect.Y + top,
		Width:  rect.Width - border*2,
		Height: rect.Height - top - bottom,
	}
	if !region.Valid() {
		return ikkyoku.Region{}, 0, fmt.Errorf("ikkyoku: ウィンドウが小さすぎます(ツールバーとガイド枠で領域が無くなります)")
	}
	return region, scale, nil
}
