// Package app はフロントに公開する Service を持つ。
//
// ⚠️ **wails3 に依存しないこと。** Wails 依存は `_cmd/ikkyoku` に閉じ込める、
// というのがこのプロジェクトの線引きで、ここはその外側。
// **イベントの発火やダイアログが要る Service は `_cmd` 側に残し、
// ロジックだけをこちら（あるいは ikkyoku の他のパッケージ）へ出す。**
//
// ここに置くのは「Wails の口が要らない Service」だけ。
// `application.NewService()` は任意のパッケージの値を取れるので、
// 登録は `_cmd/ikkyoku/main.go` が行う。
package app

import (
	"sync"
	"time"

	"github.com/ShinteLab/ikkyoku/log"
)

// DiagService は「画面が真っ黒になって何も触れなくなる」現象を切り分けるための計測。
//
// 2026-08-08 に実機で 1 回起きた。そのとき手元に残っていたのは
// `[WebView2] Focus failed: The group or resource is not in the correct state`
// が 4 行だけで、**Go 側は無傷だった**(黒くなったあとのホットキーで PNG が保存できている)。
//
// 疑っているのは Wails beta.3 の最小化まわり。`WM_SIZE / SIZE_MINIMIZED` で
// Wails は `chromium.Hide()`(= WebView2 の `PutIsVisible(false)`)を呼び、
// **元に戻すのは復帰時の 1 か所だけ**(`webview_window_windows.go` の
// SIZE_RESTORED / SIZE_MAXIMIZED 分岐。しかも条件付き)。ここを取りこぼすと
// コントローラは不可視のまま戻らず、次の状態になる:
//
//   - 真っ黒(不可視なので何も合成されない)
//   - 入力を受け付けない(不可視のコントローラは入力を取らない)
//   - `MoveFocus` が「状態が正しくない」で失敗する(= 観測されたログ)
//   - **フロントの JS は動き続けている**
//
// 最後の 1 点が決め手になる。**心拍が続いたまま画面が黒いなら「見えなくされている」、
// 心拍が止まるならレンダラが死んでいる。** この 2 つは手当てが正反対
// (前者は表示し直せば戻る = CaptureService.RepairMain、後者は読み込み直すしかない)
// なので、次に起きた 1 回で判別が付くようにしておく。
//
// **2026-08-13 に 2 回目が起き、この計測が答えを出した。**
// **main と frame の心拍が同じ秒に止まり**(両方 hidden=false・heap は 11/12MB)、
// 以後 `Eval failed` が通らなくなった。**最小化説はウィンドウ単位の話なので 2 枚同時は
// 説明できず、`Eval` が通らない = ページではなく WebView2 の側が居ない**
// (ページさえ生きていれば ExecuteScript は通る)。2 枚は同じ WebView2 環境に
// ぶら下がっているので、ブラウザプロセスが落ちればまとめて黒くなる。
// **= 下の「心拍が止まる」側で、RepairMain は効かない**(戻す相手が居ない)。
// 詳細と次の一手は AGENTS.md の同名の節。
//
// **状態は持つが、局面やキャプチャには一切関与しない。** 計測だけの Service。
type DiagService struct {
	mu    sync.Mutex
	beats map[string]*heartbeatState
}

// heartbeatState はウィンドウ 1 枚ぶんの心拍の記録。
type heartbeatState struct {
	last time.Time
	// heapMB は直近の JS ヒープ(MB)。performance.memory が無いブラウザでは 0。
	// **メモリ枯渇の線は 10〜20 枚のキャプチャでは薄い**と分かっているが、
	// 途切れる直前の値が残っていれば、次に疑う順番を決められる。
	heapMB float64
	// hidden は直近の心拍時点で画面が隠れていたか(document.hidden)。
	// **下の閾値の理由そのもの**なので、途切れたときは必ず一緒に出す。
	hidden bool
	// stale は「途切れた」を報告済みか。5 秒ごとに同じ警告を吐き続けないための記録で、
	// 戻ってきたときに 1 行出すためのフラグでもある。
	stale bool
}

// heartbeatInterval はフロントが心拍を打つ間隔(frontend/src/main.ts と揃えること)。
const heartbeatInterval = 5 * time.Second

// heartbeatStaleAfter はこれだけ間が空いたら「途切れた」とみなす閾値。
//
// ⚠️ **間隔の 3 回ぶんでは足りない。** Chromium は隠れている・最小化されている
// ウィンドウのタイマーを間引き、`setInterval` は最悪 1 分に 1 回まで落ちる。
// **そして今追っている現象はまさに最小化まわりで起きる**ので、短い閾値だと
// 「調べたい状況で必ず誤検知する」ことになる。1 分の間引きを 1 回見送れる幅を取る。
//
// 遅くなるが困らない。**この計測はその場で気づくためではなく、後からログを読んで
// 「心拍は続いていたのか、止まったのか」を判定するためのもの。**
const heartbeatStaleAfter = 90 * time.Second

func NewDiagService() *DiagService {
	return &DiagService{beats: map[string]*heartbeatState{}}
}

// Heartbeat はフロントから定期的に呼ばれる。window は "frame" / "main"。
// hidden は document.hidden(隠れている窓はタイマーを間引かれる。上の閾値を参照)。
//
// **戻り値を持たない。** フロントは結果を見ないし、返す意味のあるものも無い。
func (s *DiagService) Heartbeat(window string, heapMB float64, hidden bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.beats[window]
	if !ok {
		st = &heartbeatState{}
		s.beats[window] = st
		log.Info("フロントエンドの心拍を検知しました", "window", window)
	}
	if st.stale {
		// 止まっていたものが戻った。**これが出るかどうかで手当てが変わる**
		// (戻るなら描き直しで復帰しうる。戻らないなら読み込み直すしかない)。
		log.Info("フロントエンドの心拍が戻りました",
			"window", window, "断絶", time.Since(st.last).Round(time.Second))
		st.stale = false
	}
	st.last = time.Now()
	st.heapMB = heapMB
	st.hidden = hidden
}

// ReportError はフロントで拾えなかった例外を Go 側のログに流す。
//
// **今までフロントの例外はどこにも出ていなかった**(webview の DevTools を
// 開いていない限り消える)。黒くなった件では何も出ていないが、
// 「出ていない」ことを根拠にするには、まず出る経路が要る。
func (s *DiagService) ReportError(window, kind, message, stack string) {
	log.Warn("フロントエンドで例外が発生しました",
		"window", window, "kind", kind, "message", message, "stack", stack)
}

// Watch は心拍の途絶を監視する。main() から 1 回だけ起こす。
//
// **一度も心拍を受け取っていないウィンドウは監視しない**(起動直後や、
// まだ一度も表示していないメイン画面を「途切れた」と言わないため)。
func (s *DiagService) Watch() {
	go func() {
		t := time.NewTicker(heartbeatInterval)
		defer t.Stop()
		for range t.C {
			s.mu.Lock()
			for name, st := range s.beats {
				if st.stale || time.Since(st.last) <= heartbeatStaleAfter {
					continue
				}
				st.stale = true
				log.Warn("フロントエンドの心拍が途切れました",
					"window", name,
					"最後の心拍", st.last.Format(time.TimeOnly),
					"heapMB", st.heapMB,
					// 隠れていたなら、間引きで止まって見えているだけの可能性が残る
					// (見えている窓が止まったのなら、そちらは本物)。
					"隠れていた", st.hidden)
			}
			s.mu.Unlock()
		}
	}()
}
