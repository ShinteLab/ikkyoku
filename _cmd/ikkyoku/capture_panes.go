package main

// CaptureService のうち、**切り離した窓**（評価値グラフ・候補手・手順）の出し入れ（2026-11-13 に captureservice.go から分けた）。
//
// ⚠️ **「切り離しているか」（設定）と「今出ているか」（解析タブを見ているか）は別**（構造体の paneDetached のコメント）。
// 制約は `_docs/ui/detach.md`。

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/log"
)

// applyEvalGraphDetached は評価値グラフの切り離しを窓に反映する（2026-09-08）。
//
// 呼ばれるのは 3 経路 —— 起動時（設定の値）・解析タブのトグル・グラフ窓を閉じたとき。
// **どれも `SettingsService.SetEvalGraphDetached` を通る**（起動時だけは直接）ので、
// **設定と窓の状態が食い違わない。**
//
// ⚠️ **メイン画面へ知らせること**（`graph:detached`）。窓を閉じて戻したときに
// これが無いと、**メイン画面のペインが出てこない**（グラフがどこにも無くなる）。
func (s *CaptureService) applyEvalGraphDetached(detached bool) {
	s.paneDetached.graph = detached
	s.syncPaneWindows()
	// ⚠️ **前に出すのは人が押したときだけ**（`syncPaneWindows` では呼ばない）。
	// タブを移っただけでフォーカスを奪うと、**解析タブに戻るたびに
	// キーボードが別の窓へ行く**。
	if detached && s.studyTabActive && s.wins != nil && s.wins.graph != nil {
		s.wins.graph.Focus()
	}
	if s.app != nil {
		s.app.Event.Emit("graph:detached", detached)
	}
	log.Info("評価値グラフの置き場所を変えました", "detached", detached)
}

// initPaneDetached は起動時の切り離しの設定を控える（2026-09-12）。
//
// ⚠️ **ここでは出さない。** `app.Run()` の前の `Show()` は何も起きないし、
// そもそも起動直後に出るのは入力タブ。**出すのはフロントが解析タブを開いたとき**
// （`SetStudyTabActive`）。
//
// ⚠️ **控えること自体が要点。** これが無いと、設定タブで触るまで
// `applyXxxDetached` が呼ばれないので、**切り離したまま終了して起動し直すと、
// 解析タブを開いても窓が出てこない**（設定は「切り離し」のままなので
// メイン画面の列も出ない ＝ 中身がどこにも無い）。
func (s *CaptureService) initPaneDetached(graph, side, moves bool) {
	s.paneDetached.graph = graph
	s.paneDetached.side = side
	s.paneDetached.moves = moves
}

// SetStudyTabActive は**解析タブを見ているか**を受け取る（2026-09-12）。
//
// 切り離した 3 つの窓（評価値グラフ・候補手・手順）は**どれも解析タブの中身**なので、
// 他のタブに居るあいだ出しておいても読む相手が居ない。**起動直後も同じ**
// （最初に出るのは入力タブ）。
//
// ⚠️ **設定は触らない。** ここでやるのは `Show`/`Hide` だけで、「切り離しているか」は
// `config.json` のまま —— **タブに戻ったら同じ形で出し直す**ためにそうする。
// ⚠️ **`*:detached` のイベントもここでは出さない** —— あれは設定が変わった知らせで、
// フロントはあれを見て**ドック側のペインを出し入れする**。タブを移るたびに流すと、
// **メイン画面の右の列が勝手に戻ってくる。**
func (s *CaptureService) SetStudyTabActive(on bool) {
	if s.studyTabActive == on {
		return
	}
	s.studyTabActive = on
	s.syncPaneWindows()
}

// syncPaneWindows は「切り離しの設定 × 解析タブを見ているか」で 3 つの窓を出し入れする。
//
// ⚠️ **破棄しないこと**（`Hide` だけ）。破棄すると次に出すときに作り直しになり、
// **位置も大きさも失う**（枠と同じ扱い）。
// ⚠️ **中身は隠れていても動いている**ので、出し直しに待ち時間は無い
// （`studyscreen.ts` / `graphscreen.ts` の「切り離していない窓も動いている」）。
func (s *CaptureService) syncPaneWindows() {
	if s.wins == nil {
		return
	}
	for _, w := range []struct {
		kind     string
		name     string
		win      *application.WebviewWindow
		detached bool
	}{
		{"graph", "評価値グラフの窓", s.wins.graph, s.paneDetached.graph},
		{"side", "候補手の窓", s.wins.side, s.paneDetached.side},
		{"moves", "手順の窓", s.wins.moves, s.paneDetached.moves},
	} {
		if w.win == nil {
			continue
		}
		want := w.detached && s.studyTabActive
		if !want {
			if w.win.IsVisible() {
				w.win.Hide()
			}
			continue
		}
		if w.win.IsVisible() {
			continue
		}
		w.win.Show()
		// ⚠️ **出せなかったらドックへ戻すこと**（設計原則3）。設定だけが
		// 「切り離し」で残ると、**中身がどこにも無いうえ戻す入口も無い。**
		if !w.win.IsVisible() && s.onPaneShowFailed != nil {
			log.Warn("窓を出せませんでした。ドックに戻します", "window", w.name)
			s.onPaneShowFailed(w.kind)
		}
	}
}

// applyStudyPaneDetached は**候補手の面**の切り離しを窓に反映する（2026-09-08。
// 2026-09-12 に手順を分けたので、右の列まるごとではない）。
//
// **`applyEvalGraphDetached` と同じ形。揃えておくこと** —— 2 つの窓で作法が違うと、
// どちらがどうだったかを覚えることになる。
func (s *CaptureService) applyStudyPaneDetached(detached bool) {
	s.paneDetached.side = detached
	s.syncPaneWindows()
	if detached && s.studyTabActive && s.wins != nil && s.wins.side != nil {
		s.wins.side.Focus()
	}
	if s.app != nil {
		s.app.Event.Emit("side:detached", detached)
	}
	log.Info("候補手の置き場所を変えました", "detached", detached)
}

// applyMovePaneDetached は**手順の面**の切り離しを窓に反映する（2026-09-12）。
//
// **`applyStudyPaneDetached` と同じ形。揃えておくこと** —— 3 つの窓で作法が違うと、
// どれがどうだったかを覚えることになる。
//
// ⚠️ **メイン画面へ知らせること**（`moves:detached`）。窓を閉じて戻したときに
// これが無いと、**メイン画面の手順が出てこない**（手順がどこにも無くなる）。
func (s *CaptureService) applyMovePaneDetached(detached bool) {
	s.paneDetached.moves = detached
	s.syncPaneWindows()
	if detached && s.studyTabActive && s.wins != nil && s.wins.moves != nil {
		s.wins.moves.Focus()
	}
	if s.app != nil {
		s.app.Event.Emit("moves:detached", detached)
	}
	log.Info("手順の置き場所を変えました", "detached", detached)
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
		log.Warn("撮った画像を塗り潰せません(*image.RGBA ではありません)")
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
			log.Warn("ウィンドウの矩形を取得できませんでした", "window", w.name, "error", err)
			continue
		}
		rect := image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height).Add(off).Intersect(rgba.Bounds())
		if rect.Empty() {
			continue // 別のディスプレイにいる
		}
		draw.Draw(rgba, rect, image.NewUniform(color.Black), image.Point{}, draw.Src)
		log.Debug("ウィンドウを塗り潰しました", "window", w.name, "rect", rect.String())
	}
}
