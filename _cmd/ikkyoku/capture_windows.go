package main

// CaptureService のうち、**ウィンドウ**（枠とメイン画面を出す・隠す・素通し・枠の下の帯）（2026-11-13 に captureservice.go から分けた）。
//
// 制約は `_docs/cmd-windows.md`（出す・隠す・起動）と `_docs/cmd-frame.md`（素通し・帯）。

import (
	"fmt"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku/guide"
	"github.com/ShinteLab/ikkyoku/log"
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
	revealed.Store(true)

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

// markFrameReady は枠ウィンドウの WindowRuntimeReady が来たことを記録する
// （registerFrameHooks のフックの頭で呼ぶ）。
func (s *CaptureService) markFrameReady() {
	s.mu.Lock()
	s.frameReady = true
	s.mu.Unlock()
}

// revealIfStuck は**枠の準備が来ないままメイン画面が出ていなければ**、メイン画面を出す
// （2026-10-04。起動から mainRevealTimeout 後に 1 回だけ呼ぶ）。出したら true。
//
// 起動時にメイン画面を出す合図は**枠の** WindowRuntimeReady（registerFrameHooks）で、
// これは枠の WebView がページを読み込んでランタイムを起こしたときに来る。**枠の側で
// 何かが失敗すると合図が来ず、プロセスは生きているのに窓が 1 枚も出ない**
// （タスクバーにも出ないので、利用者からは「起動しない」にしか見えない）。
//
// ⚠️ **枠の準備が来ていたら何もしないこと。** 「起動時に盤面を探す」は探し終えてから
// 出す（数秒かかる）ので、時間だけで判断すると探している最中に割り込む。
// ⚠️ **枠の横へは置かない**（placeMainBesideFrame を通らない）。枠の位置は合図の中で
// 記録するので、合図が来ていないと保存前のセンチネル値（-32000）を基準に置いてしまう。
// ここで出すのは前回の位置か、それが無ければ画面の中央（newMainWindow の既定）。
func (s *CaptureService) revealIfStuck() bool {
	if s.wins == nil || s.wins.main == nil {
		return false
	}
	s.mu.Lock()
	if s.frameReady || s.mainShown {
		s.mu.Unlock()
		return false
	}
	s.mainShown = true
	s.mu.Unlock()
	revealed.Store(true)

	s.wins.main.Show()
	s.wins.main.Focus()
	s.wins.mainGeom.record(s.wins.main)
	return true
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
//
// ⚠️ **中継を追っているあいだは隠さない**（2026-10-10）。隠すと次の 1 周で撮れずに追跡がエラーで止まり、
// しかも理由が見えない場所（手順の下）に出ていた。断ったことは中継セッションへ渡し（`onHideRefused` →
// `StudyService.FollowHideRefused`）、枠の下の帯に出す（入口が 3 つあり、Alt+F4 は Go からしか返せないので、知らせは 1 本にする）。
func (s *CaptureService) HideFrame() {
	if s.wins == nil || s.wins.frame == nil {
		return
	}
	if s.follow != nil && s.follow.Following() {
		log.Follow.Info("中継を追っているので枠を隠しません")
		if s.onHideRefused != nil {
			s.onHideRefused(hideRefusedMessage)
		}
		return
	}
	s.revealMain()
	s.wins.frame.Hide()
	s.emitFrameVisible()
}

// hideRefusedMessage は追跡中に枠を隠そうとしたときの文（`HideFrame`）。
const hideRefusedMessage = "中継の追跡中は枠を隠せません。追跡をキャンセルしてください"

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
	// NOTICE（常に残す。2026-10-11）: 起動の 1 行と対にする（どこまで動いていたかの区切り）。
	log.Notice("終了します")
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
	log.Info("メイン画面を描き直します")
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
		log.Info("枠の内側を素通しにします")
		go s.watchCursor(ch)
		return
	}
	s.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	s.setMouseThrough(false)
	log.Info("枠の素通しをやめます")
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
		log.Warn("枠の素通しを切り替えられませんでした", "on", on, "error", err)
		return
	}
	s.mouseThrough = on
}

// SetFrameFooter は枠の下の帯の高さ(CSS px)を決め、**ウィンドウをそのぶん下へ伸ばす／縮める**（2026-10-07）。
//
// 帯は中継の追従の知らせ（見失った・止めた理由など）を出す場所で、文が出ているあいだだけある。
// 以前は解析タブの手順の下に出していたが、**追っているあいだ見ているのは中継**なので、
// 枠の近くに無いと届かない（枠のツールバーの札と同じ理由）。
//
// ⚠️ **上へは伸ばさない。** 上端・左右は動かさないので、**撮る範囲は 1 画素も動かない**
// （動くと追っている盤を取り直すことになる）。
// ⚠️ **伸び縮みと高さの記録を同じロックの中でやること**（`captureRegion` も同じロックで読む）。
// 追従は裏で回り続けているので、伸ばしたのに高さが古いまま読まれると、帯が撮る範囲に入る。
// ⚠️ **帯の中身（文字）を描く順番はフロントが守る** —— 伸ばしてから描く・消してから縮める
// （逆だと、一瞬ガイド枠の線が撮る範囲の内側に描かれる）。
//
// px は 0〜`guide.FooterMaxPx` に丸め、**丸めた結果を返す**（帯はその高さで描くこと。
// ⚠️ **フロントで上限を持たないこと** —— 伸ばしたより高く描くと、帯が撮る範囲に食い込む）。
func (s *CaptureService) SetFrameFooter(px int) (int, error) {
	if s.wins == nil || s.wins.frame == nil {
		return 0, fmt.Errorf("ikkyoku: 枠がありません")
	}
	px = max(0, min(px, guide.FooterMaxPx))
	w := s.wins
	w.frameFooterMu.Lock()
	defer w.frameFooterMu.Unlock()
	delta := px - w.frameFooterPx
	if delta == 0 {
		return px, nil
	}
	fw, fh := w.frame.Size()
	if fh+delta <= 0 {
		return w.frameFooterPx, fmt.Errorf("ikkyoku: 枠の高さが足りません")
	}
	w.frame.SetSize(fw, fh+delta)
	w.frameFooterPx = px
	return px, nil
}

// Layout は枠ウィンドウが描くべき寸法を返す。フロントは起動時にこれを呼び、
// CSS 変数に反映してからガイド枠を描く(定数の二重管理を避けるため)。
func (s *CaptureService) Layout() guide.Layout {
	return guide.Layout{BorderPx: guide.BorderPx, ToolbarPx: guide.ToolbarHeightPx}
}
