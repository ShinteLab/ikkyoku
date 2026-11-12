package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"  // 画像ファイルの読み込み（loadImage）
	_ "image/jpeg" // 同上
	"image/png"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/config"
	"github.com/ShinteLab/ikkyoku/followcam"
	"github.com/ShinteLab/ikkyoku/guide"
	"github.com/ShinteLab/ikkyoku/log"
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
	// Source は実際に読んだ出所(学習データ・配布モデルはディレクトリのパス、焼き込みは
	// 書き出しの日時と件数)。読めていなければ空。
	Source string `json:"source"`
	Ready  bool   `json:"ready"`
	// Error は**どの段も読めなかった**ときの理由(このとき盤面は読めない)。
	Error string `json:"error"`

	// Mode は実際にどの段から読んだか(`config.SutemeSourceDir` / `SutemeSourceModel` /
	// `SutemeSourceEmbed` / 空＝どれも読めていない)。**設定の値そのものではない** ——
	// 設定は「どの段から見始めるか」で、読めなかった段は飛ばされる。**画面にはこちらを出すこと。**
	Mode string `json:"mode"`
	// Skipped は**置いてあるのに使えなかった段**とその理由(2026-10-04)。読めた段より
	// 上にあったものが入る。**置かれていない段は入れない**(既定の置き場所が空なのは普通)。
	// 空でなければ画面の ⚠ に「指定した認識器を使えない」と出る(issues.go)。
	Skipped []string `json:"skipped"`
	// ModelDir は配布モデルを探した場所(設定が空なら既定の置き場所)。設定タブに出す。
	ModelDir string `json:"modelDir"`
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
// 呼ぶだけに徹する(ikkyoku/AGENTS.md: 将棋のロジックを書かない、状態を持たない)。
type CaptureService struct {
	app *application.App
	// wins は枠とメイン画面。**キャプチャ領域は枠のクライアント矩形そのもの**なので、
	// メイン画面がどちらであっても撮る基準は枠のまま。枠は隠されていても HWND が
	// 生きているため領域の定義自体は有効だが、**撮るのは枠が出ているときだけ**
	// (Capture の requireFrame)。
	wins *appWindows

	// beforeQuit は枠のメニューの「終了」から呼ぶ後始末（ウィンドウ位置の保存・
	// エンジンとの接続の close）。**終了の入口が 2 つある**ので、中身は
	// メイン画面を閉じる経路と同じものを main.go で 1 本にしてある。
	beforeQuit func()

	// recognizerDir は学習データの置き場所(config.Config の SutemeDataDir)。
	// 空ならこの段は無い(suteme 既定の探索には任せない。loadRecognizer)。
	recognizerDir string
	// recognizerModelDir は配布モデルの置き場所の設定(config.Config の SutemeModelDir)。
	// **空なら既定の置き場所**(解決は loadRecognizer が config.Config.ModelDir で毎回する)。
	recognizerModelDir string
	// recognizerSource は読み込み元の設定(config.Config の SutemeSource)。
	// **設定タブから変えられる**ので、mu で守る(applyRecognizerSource)。
	recognizerSource string

	mu sync.Mutex
	// mainShown はメイン画面を一度でも出したか。**初回だけやること**
	// (枠の外への配置・表示位置の記録)を 2 回目以降に繰り返さないための記録。
	// 前面に出す(Show/Focus)のは毎回。revealMain 参照。
	mainShown bool
	// frameReady は枠ウィンドウの WindowRuntimeReady が来たか（2026-10-04）。
	// **起動時にメイン画面を出す合図はこれ**なので、来ないとメイン画面が 1 枚も出ない
	// まま動き続ける。`revealIfStuck` が待つのはこれ。
	frameReady bool
	// recognizerStatus は直近の読み込み結果。表示のためだけに 3.5MB を
	// 読み直さなくて済むよう覚えておく。
	recognizerStatus RecognizerStatus
	// onRecognizer は認識器を読み直すたびに呼ぶ（画面の ⚠ を合わせるため。main.go が差し込む）。
	// ⚠️ **起動時だけでなく、設定タブで指し直したときも呼ぶこと** —— 直ったのに ⚠ が残る。
	onRecognizer func(RecognizerStatus)

	// lastShot は**最後に画面から撮った 1 枚**の盤の見た目（2026-10-09。`noteShot`）。
	// ⚠️ **入れるのは `Capture`（画面から撮った 1 枚）だけ** —— 画像ファイルの 1 枚は
	// 画面と座標系が違う（記述子は画像の中の座標）。
	lastShot followcam.ShotAnchor
	// followShot は**解析タブの根を採った（訂正した）1 枚**（2026-10-09。`SetFollowShot`）。
	//
	// **訂正してから追うときのマスタはこれ**（`followcam.Camera.AnchorBoard`。差し込み口 `Corrected` から渡す）。押した瞬間の 1 枚は使わない ——
	// 押したときに棋士の手が盤に被っていて、初手から「別の盤」になった（実機で踏んだ）。
	// 訂正した 1 枚は人が盤を確かめた 1 枚なので、マスタとしてこれより確かなものは無い。
	// ⚠️ **今の根を採った 1 枚のときだけ使う**（`Session` を `studySession` と突き合わせる。
	// 棋譜や新規対局で根を入れ替えたあとに、前の 1 枚の盤を追わない）。
	followShot followcam.ShotAnchor
	// studySession は解析タブの今の検討の id を返す（`main` が `StudyService.Session` を入れる）。
	// ⚠️ **nil なら訂正した 1 枚は使わない**（押した瞬間の 1 枚で決める。今までどおり）。
	studySession func() string
	// onHideRefused は**追っているあいだに枠を隠そうとして断った**ことを中継セッションへ渡す（2026-10-11。
	// `StudyService.FollowHideRefused`。帯に出す）。nil なら知らせない（設計原則3）。
	onHideRefused func(message string)

	// followFirst は設定「中継を追うあいだは盤の読みを優先する」（`enginepriority.go`。2026-10-07）。
	followFirst bool
	// enginesLowered は外部エンジンの優先度を下げているか（戻し忘れないため）。
	enginesLowered bool
	// engineNames は登録してある外部エンジンの実行ファイルのパス（`main` が差し込む。設定から読む）。
	engineNames func() []string

	// clickThrough は設定「枠の内側で後ろの画面を操作する」（`config.Config.ClickThrough`）。
	clickThrough bool
	// clickStop は素通しの見張り（watchCursor）を止めるチャネル。
	// **入のあいだだけ goroutine が居る。**
	clickStop chan struct{}
	// mouseThrough は今 WS_EX_TRANSPARENT を立てているか。
	// **設定そのものではない**（設定が入でも、カーソルがツールバーの上に居るあいだは false）。
	mouseThrough bool

	// ---- 切り離した窓の表示（2026-09-12）----------------------------------
	//
	// ⚠️ **「切り離しているか」と「今出ているか」は別**。前者は設定
	// （`config.json`）、後者は**解析タブを見ているか**との掛け合わせ。
	// 切り離した 3 つはどれも**解析タブの中身**なので、他のタブに居るあいだ
	// 出しっぱなしにしても読む相手が居ない（起動直後も同じ）。
	//
	// ⚠️ **設定のほうを書き換えて隠さないこと。** それは「ドックに戻す」であって、
	// **タブに戻ったときに窓へ出し直せなくなる**（人が畳んだのか、タブを離れた
	// だけなのかが区別できなくなる）。ここは `Show`/`Hide` だけ。

	// paneDetached は切り離しの設定（graph / side / moves）。
	paneDetached struct{ graph, side, moves bool }
	// studyTabActive は解析タブを見ているか（フロントが `SetStudyTabActive` で伝える）。
	studyTabActive bool
	// onPaneShowFailed は窓を出せなかったときにドックへ戻すための口
	// （実体は `SettingsService.Set*Detached(false)`。main.go が差し込む）。
	//
	// ⚠️ **落とさないこと**（設計原則3）。出せないまま設定が「切り離し」で残ると、
	// **中身がどこにも無いうえ戻す入口も無い**（戻す口はその窓の中にある）。
	onPaneShowFailed func(kind string)
	// follow は**中継を追うときの撮る側**（`followcam.Camera`。撮る・ふるい・速い経路・録画と、追う 1 回の持ち物。
	// 2026-11-13 にここから `ikkyoku/followcam` へ移した）。`main` が `StudyService.Camera` に入れる。
	// ⚠️ **追従の状態をここ（`CaptureService`）に足さない**（あちらの `captureFollow` へ）。
	// ⚠️ **`mu` を持ったままあちらを呼ばない**（あちらは自分のロックを持ち、差し込み口でこちらへ戻る）。
	// `&CaptureService{}` で作ったとき（テスト）は nil。
	follow *followcam.Camera
}

func NewCaptureService(recognizerDir, recognizerModelDir, recognizerSource string) *CaptureService {
	s := &CaptureService{
		recognizerDir:      recognizerDir,
		recognizerModelDir: recognizerModelDir,
		recognizerSource:   recognizerSource,
	}
	// 撮る側のうち、ウィンドウと設定が要るものだけをこちらから差し込む（どれも撮る側のロックの外で呼ばれる）。
	s.follow = &followcam.Camera{
		Region: s.followRegion,
		Corrected: func() followcam.ShotAnchor {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.followShot
		},
		Session: func() string {
			if s.studySession == nil {
				return ""
			}
			return s.studySession()
		},
		// 外部エンジンの優先度（`enginepriority.go`）。追っているあいだに起きたエンジンも下げ、止めたら戻す。
		BeforeRead:  s.syncEnginePriority,
		AfterAnchor: s.syncEnginePriority,
		AfterEnd:    s.syncEnginePriority,
	}
	return s
}

// followRegion は中継を追うときの撮影範囲（`followcam.Camera.Region`）。
// ⚠️ **枠の門番は掛けたまま**（`Capture` と同じ）。「どこを撮るのかが画面に見えていること」は追従でも変わらない。
func (s *CaptureService) followRegion() (ikkyoku.Region, error) {
	if err := s.requireFrame(); err != nil {
		return ikkyoku.Region{}, err
	}
	region, _, err := s.captureRegion()
	return region, err
}

// applyRecognizerDir は学習データの置き場所の変更をその場で効かせる
// (SettingsService.onSutemeDataDir から呼ばれる)。
func (s *CaptureService) applyRecognizerDir(dir string) {
	s.mu.Lock()
	s.recognizerDir = dir
	s.mu.Unlock()
	s.ReloadRecognizer()
}

// applyRecognizerModelDir は配布モデルの置き場所の変更をその場で効かせる
// (SettingsService.OnSutemeModelDir から呼ばれる。2026-10-04)。
// **設定の値（空なら既定の置き場所）をそのまま持つ** —— 解決は loadRecognizer が毎回する。
func (s *CaptureService) applyRecognizerModelDir(dir string) {
	s.mu.Lock()
	s.recognizerModelDir = dir
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
	hook := s.onRecognizer
	s.mu.Unlock()
	if hook != nil {
		hook(st)
	}
	return st
}

// Recognizer は直近の読み込み結果を返す。**読み込み直さない。**
// フロントが起動時に状態を表示するためだけに 3.5MB を読み直すのを避ける。
func (s *CaptureService) Recognizer() RecognizerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recognizerStatus
}

// loadRecognizer は設定に従って認識器を 1 組読み、差し替える（2026-10-04 に 3 段にした）。
//
//  1. 学習データ（SutemeDataDir）  2. 配布モデル（SutemeModelDir）  3. 焼き込み
//
// **見る順は recognizerOrder**（設定 SutemeSource は「どの段から見始めるか」）。
// **置かれていない段は黙って飛ばし、置いてあるのに読めなかった段は Skipped に残して
// 下へ落とす**（画面の ⚠ に出る。issues.go）。どの段も読めなければ認識器を外す
// （recognize.Clear。⚠️ suteme 既定の探索には落とさない）。
//
// ⚠️ **駒種の推論器と盤の縁の判定器は同じ段から 1 組で差し替える**（recognize.Set.Use）。
// 段ごとに別々に読むと「駒種は最新の学習データ、盤の位置合わせは学習前」になる。
//
// ⚠️ **自動（auto / dir）のときは、焼き込みより古い配布モデルを使わない**（export.json の
// 日付で比べる）。exe を更新したのに、昔置いたモデルが優先されて精度が下がるのを防ぐ。
// 配布モデルから見始める設定（model）にしているときは、古くても使う（意思表示なので）。
func (s *CaptureService) loadRecognizer() RecognizerStatus {
	s.mu.Lock()
	dir, modelSetting, pref := s.recognizerDir, s.recognizerModelDir, s.recognizerSource
	s.mu.Unlock()

	modelDir, err := config.Config{SutemeModelDir: modelSetting}.ModelDir()
	if err != nil {
		log.Warn("配布モデルの置き場所を決められませんでした", "error", err)
	}
	st := RecognizerStatus{EmbedAvailable: recognize.EmbeddedAvailable(), ModelDir: modelDir}

	// 焼き込みは配布モデルとの新旧の比較でも読むので、読んだら使い回す。
	var embedded *recognize.Set
	var embeddedErr error
	embeddedLoaded := false
	loadEmbedded := func() (*recognize.Set, error) {
		if !embeddedLoaded {
			embedded, embeddedErr = recognize.LoadEmbedded()
			embeddedLoaded = true
		}
		return embedded, embeddedErr
	}

	for _, mode := range recognizerOrder(pref) {
		var set *recognize.Set
		switch mode {
		case config.SutemeSourceDir:
			if dir == "" {
				continue
			}
			if set, err = recognize.LoadDir(dir); err != nil {
				st.Skipped = append(st.Skipped, fmt.Sprintf("学習データ（%s）を読めません: %v", dir, err))
				log.Warn("学習データを読めませんでした", "dir", dir, "error", err)
				continue
			}
		case config.SutemeSourceModel:
			if modelDir == "" {
				continue
			}
			set, err = recognize.LoadPack(modelDir)
			if errors.Is(err, recognize.ErrNoPack) {
				// 既定の置き場所が空なのは普通の状態。**場所を指定したのに無いときだけ**残す。
				if modelSetting != "" {
					st.Skipped = append(st.Skipped, fmt.Sprintf("配布モデル（%s）が置かれていません", modelDir))
				}
				continue
			}
			if err != nil {
				st.Skipped = append(st.Skipped, fmt.Sprintf("配布モデル（%s）を読めません: %v", modelDir, err))
				log.Warn("配布モデルを読めませんでした", "dir", modelDir, "error", err)
				continue
			}
			if pref != config.SutemeSourceModel && recognize.EmbeddedAvailable() {
				if emb, err := loadEmbedded(); err == nil && set.OlderThan(emb) {
					st.Skipped = append(st.Skipped, fmt.Sprintf("配布モデル（%s）は焼き込み（%s）より古いので使っていません",
						set.Date.Format("2006-01-02"), emb.Date.Format("2006-01-02")))
					log.Info("配布モデルは焼き込みより古いので使いません", "model", set.Date, "embed", emb.Date)
					continue
				}
			}
		case config.SutemeSourceEmbed:
			if !recognize.EmbeddedAvailable() {
				continue
			}
			if set, err = loadEmbedded(); err != nil {
				st.Skipped = append(st.Skipped, fmt.Sprintf("焼き込んだ認識器を読めません: %v", err))
				log.Warn("焼き込んだ認識器を読めませんでした", "error", err)
				continue
			}
		}

		set.Use()
		st.Mode, st.Source, st.Ready = mode, set.Source, true
		st.StripSamples = set.StripSamples
		if set.StripErr != nil {
			st.StripError = set.StripErr.Error()
			log.Warn("盤の縁の帯の判定器を読み込めませんでした(盤の位置が 1マス滑ることがあります)",
				"mode", mode, "error", set.StripErr)
		}
		// NOTICE（常に残す。2026-10-11）: どの認識器で読んだかが分からないと、読み違いを追えない。
		log.Notice("認識器を読み込みました", "mode", mode, "source", set.Source, "strip", set.StripSamples)
		return st
	}

	recognize.Clear()
	st.Error = "学習データ・配布モデル・焼き込みのどれも読めていません"
	log.Warn("認識器がありません（撮った画像から盤面を読めません）", "skipped", st.Skipped)
	return st
}

// recognizerOrder は認識器の段を見る順を返す（2026-10-04）。
//
// 設定は「どの段から見始めるか」で、**見始めた段から下へ、そのあと上の段**の順。
// 上の段へも回るのは、**焼き込みの無いビルドで「焼き込み」にしていても動くように**
// （設定ファイルは `wails3 dev` と配る exe で共用なので、普通に起きる）。
//
// ⚠️ **auto を「焼き込み優先」にしないこと。** 焼き込みは配布用に固定したデータ、
// 学習データは育て続けるデータ。開発中（学習データを指している状態）に焼き込みへ
// 倒れると、**学習データを更新しても反映されない**という最も気づきにくい事故になる。
// 置いたもの・指定したもののほうがユーザーの意思表示なので、上の段を先に見る。
func recognizerOrder(pref string) []string {
	all := []string{config.SutemeSourceDir, config.SutemeSourceModel, config.SutemeSourceEmbed}
	start := 0
	switch pref {
	case config.SutemeSourceModel:
		start = 1
	case config.SutemeSourceEmbed:
		start = 2
	}
	order := make([]string, 0, len(all))
	order = append(order, all[start:]...)
	return append(order, all[:start]...)
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

	// Source はこの 1 枚がどこから来たか（`ImageSourceScreen` / `ImageSourceFile`）。
	//
	// ⚠️ **振る舞いを分けるためのものではない**（2026-09-12）。認識も訂正も
	// 「画像から起こした 1 局面」に対する操作で、出どころで変わるものは無い。
	// **変わるのは言い回しだけ** ——「撮りました」と言えないのと、
	// **枠のシャッターの合図を出さないこと**（押していない操作に合図が出る）。
	Source string `json:"source"`
}

// ImageSourceScreen / ImageSourceFile は 1 枚の出どころ（`CaptureShot.Source`）。
const (
	ImageSourceScreen = "screen"
	ImageSourceFile   = "file"
)

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
	// Source は出どころ（`CaptureShot.Source` と同じ値）。
	Source string `json:"source"`

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

	result := s.deliver(img, path, ImageSourceScreen)
	s.noteShot(img, region, path, result.Debug)
	return result, nil
}

// noteShot は**画面から撮った 1 枚の盤の見た目**を控える（2026-10-09。`lastShot`）。
//
// 使うのは訂正してから追うときだけ（`SetFollowShot` → `AnchorBoard`）で、
// **撮ることそのものはこれを読まない**（1 回のキャプチャは他のキャプチャから独立したまま）。
// ⚠️ **盤が取れなかった 1 枚でも入れ替える**（パスだけ持たせる）—— その 1 枚を訂正して採っても、
// 前の 1 枚の盤をマスタにしない。
func (s *CaptureService) noteShot(img image.Image, region ikkyoku.Region, path string, d *recognize.Debug) {
	shot := followcam.ShotAnchor{Path: path}
	if sig, ok := recognize.SignatureOf(d); ok {
		shot.Sig, shot.Region, shot.Img = sig, region, img
	}
	s.mu.Lock()
	s.lastShot = shot
	s.mu.Unlock()
}

// SetFollowShot は**解析タブの根を採った 1 枚**を、中継を追う盤（マスタ）として覚える（2026-10-09）。
//
// フロントが「この局面を解析する」で根を採った直後に、訂正タブの画像のパスを渡す
// （画像の無い局面・画像ファイルの局面なら空か、撮った 1 枚と違うパスになる）。
// ⚠️ **最後に画面から撮った 1 枚と同じパスで、盤が取れていたときだけ覚える**。それ以外は忘れる
// （押した瞬間の 1 枚で決める。今までどおり）。
func (s *CaptureService) SetFollowShot(path string) {
	session := ""
	if s.studySession != nil {
		session = s.studySession()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	shot := s.lastShot
	if path == "" || shot.Path != path || !shot.OK() || session == "" {
		s.followShot = followcam.ShotAnchor{}
		return
	}
	shot.Session = session
	s.followShot = shot
	log.Follow.Info("訂正した 1 枚を、中継を追う盤として覚えました", "path", path, "board", shot.Sig.Board)
}

// applyFollowDebug は設定「中継の録画とログを残す（デバッグ）」が切り替わったときに呼ばれる（`SettingsService.OnFollowDebug`）。
// 追従のログは次の行から、録画は次に「追う」を押したときから（切ったときは次の 1 枚から残さない）。
func (s *CaptureService) applyFollowDebug(v bool) {
	if s.follow != nil {
		s.follow.SetDebug(v)
	}
	log.SetFollow(v)
	// 起動時（設定を読んだあと）と切り替えたときに 1 行（ログの最初の行は設定を読む前に書くので、そちらには出せない）。
	log.Notice("中継の録画とログ（デバッグ）を設定しました", "残す", v)
}

// OpenImage は画像ファイルを選んで、撮った 1 枚と同じ経路に載せる（入力タブの
// 「画像ファイルを読み込む」）。
//
// ⚠️ **撮る経路と分けないこと。** 認識も訂正も「画像から起こした 1 局面」に対する
// 操作で、どこから来た画像かで変わるものが無い（変わるのは画面に出す言い回しだけ）。
// 分けると `capture:shot` / `capture:done` を受ける側が 2 系統になり、
// **訂正タブへ渡すまでの約束を 2 か所で守ることになる。**
//
// ⚠️ **枠が出ているかは見ない**（`requireFrame` を掛けない）。あれは「どこを撮るのかが
// 画面に見えていること」を撮る条件にする門番で、**ファイルにはその話が無い。**
//
// **取り消しはエラーではない。** パスが空で返るので、そのまま何もせずに返す
// （イベントも出ないので、画面は 1 つも変わらない）。
func (s *CaptureService) OpenImage() (CaptureResult, error) {
	if s.app == nil {
		return CaptureResult{}, fmt.Errorf("ダイアログを開けません")
	}
	dlg := s.app.Dialog.OpenFile()
	dlg.SetTitle("盤面の画像を選ぶ")
	dlg.CanChooseFiles(true)
	dlg.CanChooseDirectories(false)
	dlg.AddFilter("画像 (*.png, *.jpg, *.gif)", "*.png;*.jpg;*.jpeg;*.gif")
	// 撮った PNG の置き場所から開く。**撮ったものを撮り直しに使う**のが一番多い
	// （認識を試し直す・学習に回す）ので、そこが既定の入口として素直。
	if dir, err := ikkyoku.DefaultOutDir(); err == nil {
		dlg.SetDirectory(dir)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil {
		return CaptureResult{}, err
	}
	if path == "" {
		return CaptureResult{}, nil // 取り消し
	}
	return s.loadImage(path)
}

// loadImage は画像ファイルを読んで認識に掛ける（`OpenImage` の中身）。
// **ドラッグ＆ドロップを足すときもここに合流させる**（パスを渡す口を増やすだけ）。
//
// ⚠️ **PNG 以外は PNG にして控えること。** この先は `Path` を「撮った PNG」として
// 扱う口が 2 つある（学習への還元 `TrainingService.Send` と画像のコピー
// `CopyImage`）。JPEG のパスをそのまま流すと、**suteme には .png という名前で
// JPEG が届く。** 変換したものは撮った 1 枚と同じ置き場所に置く。
func (s *CaptureService) loadImage(path string) (CaptureResult, error) {
	if path == "" {
		return CaptureResult{}, fmt.Errorf("画像のパスがありません")
	}
	f, err := os.Open(path)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("画像を開けません: %w", err)
	}
	defer f.Close()
	img, format, err := image.Decode(f)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("画像を読めません（png / jpeg / gif のみ）: %w", err)
	}
	if format != "png" {
		dir, err := ikkyoku.DefaultOutDir()
		if err != nil {
			return CaptureResult{}, err
		}
		saved, err := ikkyoku.SavePNG(img, dir)
		if err != nil {
			return CaptureResult{}, err
		}
		log.Info("読み込んだ画像を PNG にして控えました", "from", path, "path", saved, "format", format)
		path = saved
	}
	log.Info("画像を読み込みました", "path", path, "format", format,
		"width", img.Bounds().Dx(), "height", img.Bounds().Dy())
	return s.deliver(img, path, ImageSourceFile), nil
}

// deliver は 1 枚の画像を「撮った 1 枚」として画面へ届ける。
// **撮る（`Capture`）とファイルを読む（`loadImage`）の合流点**で、ここから先は
// どちらから来たかで振る舞いを変えない（`source` は画面の言い回しのためだけ）。
func (s *CaptureService) deliver(img image.Image, path, source string) CaptureResult {
	var buf bytes.Buffer
	thumb := ""
	if err := png.Encode(&buf, img); err != nil {
		log.Warn("サムネイル用のPNGエンコードに失敗しました", "error", err)
	} else {
		thumb = "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	}

	b := img.Bounds()
	result := CaptureResult{
		Path:      path,
		Width:     b.Dx(),
		Height:    b.Dy(),
		Thumbnail: thumb,
		Source:    source,
		Warnings:  []string{},
		HandTotal: map[string]int{},
	}
	log.Info("キャプチャしました", "path", path, "width", b.Dx(), "height", b.Dy(), "source", source)

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
			Source:    source,
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
		log.Warn("盤面を認識できませんでした", "path", path, "error", err)
	} else {
		result.SFEN = board.SFEN
		result.Confidence = board.Confidence
		result.Warnings = board.Warnings
		result.HandTotal = board.HandTotal
		result.Debug = board.Debug
		log.Info("盤面を認識しました",
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
	return result
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
		log.Info("検出した盤が小さすぎるので合わせませんでした",
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

	log.Info("ガイド枠を盤に合わせました",
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
			log.Debug("枠の内側には盤がありませんでした", "confidence", det.Confidence)
		case s.looksLikePartOfBoard(b, region, det.Confidence):
			// 縦横とも半分に縮む候補は「盤の一部」を掴んでいる可能性が高い。
			// 採らずに画面全体の探索へ回す(そちらで本来の盤が見つかることがある)。
		default:
			log.Info("枠の内側で盤を見つけました",
				"rect", b.String(), "confidence", det.Confidence)
			return b, det.Confidence, nil
		}
	} else {
		log.Warn("枠の内側を撮れませんでした", "error", err)
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
		log.Info("画面に盤が見つかりませんでした",
			"display", disp.String(), "confidence", det.Confidence, "error", err)
		return image.Rectangle{}, det.Confidence, nil
	}
	b := det.Rect.Sub(img.Bounds().Min).Add(image.Pt(disp.X, disp.Y))
	log.Info("画面の中に盤を見つけました", "rect", b.String(), "confidence", det.Confidence)
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
	log.Info("枠の内側で見つけた盤が小さすぎるので採りませんでした",
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
	log.Warn("枠がどのディスプレイにも属していません。プライマリを探します", "region", region.String())
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

// CopyImage は保存済みの PNG をクリップボードへ入れる。デバッグタブから呼ばれる。
//
// **撮った画像をメモリに抱えず、保存したファイルを読み直す。** 1 回のキャプチャは
// 他のキャプチャと独立という方針(ikkyoku/AGENTS.md)に沿って「直近の画像」を
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
		log.Warn("画像を開けませんでした", "path", path, "error", err)
		return err
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		log.Warn("画像を読めませんでした", "path", path, "error", err)
		return err
	}
	if err := copyImageToClipboard(img); err != nil {
		log.Warn("画像をクリップボードに入れられませんでした", "path", path, "error", err)
		return err
	}
	log.Info("画像をクリップボードに入れました", "path", path)
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
//	├──────────────────────────┤
//	│ 帯(frameFooterPx。0 もある)│ ← 中継の追従の知らせ(SetFrameFooter)
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

	// ⚠️ **帯の高さと矩形は同じロックの中で読むこと**（`SetFrameFooter`。伸びた矩形と古い高さを
	// 組み合わせると、帯が撮る範囲に入る）。
	s.wins.frameFooterMu.Lock()
	footer := s.wins.frameFooterPx
	rect, scale, err := clientRectPhysical(hwnd)
	s.wins.frameFooterMu.Unlock()
	if err != nil {
		return ikkyoku.Region{}, 0, err
	}

	border := guide.ScaleUp(guide.BorderPx, scale)
	top := guide.ScaleUp(guide.ToolbarHeightPx, scale) + border
	bottom := border + guide.FramelessBottomPaddingPx + guide.ScaleUp(footer, scale)
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
