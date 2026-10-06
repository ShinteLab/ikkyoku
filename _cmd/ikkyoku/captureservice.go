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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku"
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

	// Mode は実際にどの段から読んだか(`ikkyoku.SutemeSourceDir` / `SutemeSourceModel` /
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

	// recognizerDir は学習データの置き場所(ikkyoku.Config の SutemeDataDir)。
	// 空ならこの段は無い(suteme 既定の探索には任せない。loadRecognizer)。
	recognizerDir string
	// recognizerModelDir は配布モデルの置き場所の設定(ikkyoku.Config の SutemeModelDir)。
	// **空なら既定の置き場所**(解決は loadRecognizer が ikkyoku.Config.ModelDir で毎回する)。
	recognizerModelDir string
	// recognizerSource は読み込み元の設定(ikkyoku.Config の SutemeSource)。
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

	// boardAnchor は**追いかけている盤の見た目**（2026-09-15。中継の追従）。
	//
	// ⚠️ **中継には大盤（解説用）が映る。** あちらは将棋の局面としては矛盾しない
	// ので、**盤面だけを見ていては弾けない** —— 解説が本譜から 1 手の変化を
	// 並べていたら、そのまま棋譜に足してしまう。
	//
	// ⚠️ **覚えるのは人が「追う」と言ったときだけ**（`AnchorBoard`）。
	// 撮るたびに覚え直すと、**大盤に切り替わった 1 枚でマスタがそちらへ移る。**
	// ⚠️ **ゼロ値は「決めていない」** —— そのときは何も落とさない（設計原則3）。
	boardAnchor recognize.Signature
	// anchorRegion は**マスタを取ったときの撮影範囲**（2026-09-15）。
	//
	// ⚠️ **記述子は画像の中の座標なので、枠を動かすと必ず食い違う** ——
	// 盤は同じなのに「盤の位置が違います」になり、**そこから全部見送られる**
	// （実機で踏んだ。「盤が映っていないのかも」と枠をずらしたら止まった）。
	// **枠を動かすのは狙いを直す操作**であって、追う盤を変える操作ではない。
	anchorRegion ikkyoku.Region
	// quietOutcome は直近の `CaptureQuiet` の結果の種類（`board` / `none` / `off`）。
	//
	// ⚠️ **変わり目だけログに出すため**（2026-09-15）。追従は 1 秒ごとに回るので
	// 毎回出すと読めないが、**何も出さないと「正常に見送っている」のか
	// 「止まっている」のかがログから分からない**（実機で聞かれた）。
	quietOutcome string

	// followDir は**追跡中の録画**の保存先（空なら残さない）。
	//
	// ⚠️ **残すのは「マスタ」と「手を決めた 1 枚」だけ**（2026-09-15）。
	// 中継には**棋士の手が映り込む**ので、誤認識したときに
	// **「手が被ったのか、認識器が弱いのか」を切り分ける手掛かり**が要る
	// （実機で出た話）。⚠️ **見送ったフレームは残さないこと** ——
	// 1 秒ごとに撮るので、全部残すとディスクが埋まるうえ**大半は空振り**。
	followDir string
	// lastQuiet は**直近に黙って撮った 1 枚**（採用が決まってから残すため）。
	//
	// ⚠️ **撮った時点ではまだ「その手を決めた画像」かどうか分からない** ——
	// 決めるのは解析タブ側（`FollowAuto`）なので、**答えが返るまで持っておく**。
	lastQuiet image.Image
	// cellBase は**速い経路の比べる相手**（本譜の先端とぴったり合っていた 1 枚。`celldiff.go`。2026-10-07）。
	// ⚠️ **入れるのは `SetCellBase` だけ**（解析タブ側が `FollowAuto.AtFrame` で「合っている」と言った 1 枚）。
	cellBase image.Image
	// cellRects / cellRegion は**最後に 81 マスを読んだときのマス割り**（速い経路はこれでマスを切る）。
	// cellBounds はそのときの画像の大きさ（違えばマス割りは使えない）。
	cellRects  []image.Rectangle
	cellRegion image.Rectangle
	cellBounds image.Rectangle
	// gateRead は**ふるいが読む側へ倒した理由**（`gateReadSettled` など。速い経路を使ってよいかの判断）。
	gateRead string
	// lastShot は**直近に手で撮った（読み込んだ）1 枚**。
	//
	// ⚠️ **`lastQuiet`（追従が黙って撮る 1 枚）とは別物。** 混ぜると、
	// **追従が回っているあいだに手で繋ぐと中継のフレームが残る**
	// （人が見ていたのは訂正タブの盤なのに、証拠だけ別の画像になる）。
	lastShot image.Image
	// followMisses は**繋げなかった周を残した枚数**（2026-09-15）。
	//
	// ⚠️ **見送りを全部残さない方針の例外。** 1 秒ごとの空振りは残さないが、
	// **「繋ごうとして繋げなかった」周だけは残す** —— 実機で追従が止まったとき、
	// **止まった瞬間の画像が無いせいで原因が分からなかった**（採用した画像しか
	// 残していないので、**壊れた場面の証拠だけが消える**という一番まずい形）。
	// ⚠️ **上限を置くこと**（`followMissMax`）—— 追いつけない状態は何十分も続くので、
	// 上限が無いとディスクが埋まる。**知りたいのは崩れ始めの数枚**。
	followMisses int
	// lastMissKind は直近に残した「繋がらなかった理由」。
	//
	// ⚠️ **変わり目だけ残すため**（2026-09-15）。同じ状態は何十周も続くので、
	// 毎周残すとディスクが埋まるうえ**同じ絵が並ぶだけで読めない**。
	// **知りたいのは「何が起きて止まったか」**なので、種類が変わった 1 枚でよい。
	lastMissKind string

	// ---- 画素差分のふるい（2026-09-18）--------------------------------------
	//
	// ⚠️ **認識 1 枚 2.1 秒**が追従の速さを決めている。**ikkyoku 側で 1 枚を
	// 速くする手は無い**（重いのは 81 マスの推論で、それは `suteme` の話）ので、
	// できるのは**読む枚数を減らすこと**だけ。長考中も CM 中も画素は動いていない
	// ので、**前の 1 枚と変わっていなければ認識を呼ばない。**
	//
	// ⚠️ **状態はここに置く**（`ikkyoku.FrameDiff` は測るだけ）。追従の状態は
	// 既にここに集まっている（マスタ・録画）ので、**もう 1 か所に散らさない。**

	// gateFrame は前の周に撮った 1 枚（比べる相手）。
	gateFrame image.Image
	// gateDirty は「変化を見たが、まだ認識していない」。
	//
	// ⚠️ **変化した周ではなく、その次の「止まった」周で認識すること**（静止判定）。
	// 中継には**棋士の手が映り込む**ので、動いている最中の 1 枚を読むと
	// **2.1 秒かけてゴミを取る**（駒が隠れた盤が「繋がらない」で捨てられる）。
	gateDirty bool
	// gateMoving は変化が続いた周の数（`gateMovingMax` の保険用）。
	gateMoving int
	// gateSkipped は**「変わっていない」で省き続けた周の数**（`gateSkipMax` の保険用）。
	//
	// ⚠️ **これが無いと、ふるいが外したときに永久に止まる**（2026-09-18 に
	// 実機で踏んだ。**指したのに「変わっていません」と出続けた**）。
	// **しきい値をどう詰めても、外したら動かない作りにはしないこと。**
	gateSkipped int
	// gateMaxSkip は省いた周で見た**いちばん大きかった差**。
	//
	// ⚠️ **しきい値を実測で詰めるために要る。** 「しきい値を超えられなかった
	// 最大値」が分かれば、**どれだけ足りなかったのか**が数字で読める。
	gateMaxSkip float64
	// detectMisses は**盤の有無のふるい**で見送り続けた周の数（`detectMissMax`）。
	//
	// ⚠️ **数えているのは「弱い判断で落とした」周。** 検出だけを根拠に
	// 落とし続けると**黙って何も起きない**ので、**続いたら 1 枚は読んで確かめる。**
	detectMisses int
	// gateShots / gateReads は撮った枚数と認識した枚数。
	//
	// ⚠️ **測るために持っている。** ふるいがどれくらい効いたかは**実機の中継で
	// しか分からない**ので、追跡を止めたときにログへ出す（当て推量の定数を
	// あとで実測で詰めるための材料）。
	gateShots, gateReads int

	// clickThrough は設定「枠の内側で後ろの画面を操作する」（`ikkyoku.Config.ClickThrough`）。
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
}

func NewCaptureService(recognizerDir, recognizerModelDir, recognizerSource string) *CaptureService {
	return &CaptureService{
		recognizerDir:      recognizerDir,
		recognizerModelDir: recognizerModelDir,
		recognizerSource:   recognizerSource,
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

	modelDir, err := ikkyoku.Config{SutemeModelDir: modelSetting}.ModelDir()
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
		case ikkyoku.SutemeSourceDir:
			if dir == "" {
				continue
			}
			if set, err = recognize.LoadDir(dir); err != nil {
				st.Skipped = append(st.Skipped, fmt.Sprintf("学習データ（%s）を読めません: %v", dir, err))
				log.Warn("学習データを読めませんでした", "dir", dir, "error", err)
				continue
			}
		case ikkyoku.SutemeSourceModel:
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
			if pref != ikkyoku.SutemeSourceModel && recognize.EmbeddedAvailable() {
				if emb, err := loadEmbedded(); err == nil && set.OlderThan(emb) {
					st.Skipped = append(st.Skipped, fmt.Sprintf("配布モデル（%s）は焼き込み（%s）より古いので使っていません",
						set.Date.Format("2006-01-02"), emb.Date.Format("2006-01-02")))
					log.Info("配布モデルは焼き込みより古いので使いません", "model", set.Date, "embed", emb.Date)
					continue
				}
			}
		case ikkyoku.SutemeSourceEmbed:
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
		log.Info("認識器を読み込みました", "mode", mode, "source", set.Source, "strip", set.StripSamples)
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
	all := []string{ikkyoku.SutemeSourceDir, ikkyoku.SutemeSourceModel, ikkyoku.SutemeSourceEmbed}
	start := 0
	switch pref {
	case ikkyoku.SutemeSourceModel:
		start = 1
	case ikkyoku.SutemeSourceEmbed:
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
	log.Info("終了します")
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

	// OffBoard は**追いかけている盤とは別の盤が映っている**（中継の大盤など）。
	//
	// ⚠️ **「盤が映っていない」とは別物。** あちらは一致度が落とすもので、
	// こちらは**盤としては読めているが、追っている盤ではない**。
	// **次にすることが違う**（待つ / 撮り直す）。
	OffBoard bool `json:"offBoard"`
	// OffBoardReason はその理由（同じ盤なら空）。
	OffBoardReason string `json:"offBoardReason"`

	// Skipped は**認識を省いた**理由（省いていなければ空。2026-09-18）。
	//
	// `unchanged`（前の 1 枚と変わっていない）/ `moving`（まだ動いている）。
	//
	// ⚠️ **「盤が映っていない」（SFEN が空）と混ぜないこと。** あちらは
	// **2.1 秒かけて読んだうえで盤が無かった**で、こちらは**読んでいない**。
	// 呼ぶ側は**黙って次の周へ行くだけ**（見送りとして数えない・録画も残さない）。
	Skipped string `json:"skipped"`
	// Cells は**速い経路で変わったマス**（撮った画像の向きの 81 マスの番号。2026-10-07）。
	// 空でなければ**81 マスは読んでいない**（SFEN は空）。受ける側は `StudyService.FollowCells` に渡し、
	// 手が決まらなければ `RecognizeQuiet` で同じ 1 枚を読む。⚠️ **「盤が映っていない」と混ぜないこと。**
	Cells []int `json:"cells"`
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

	return s.deliver(img, path, ImageSourceScreen), nil
}

// CaptureQuiet は**知らせずに撮る**（中継の追従。2026-09-15）。
//
// ⚠️ **`Capture` との違いは「撮ったあと何もしない」こと。** イベントを出さず、
// メイン画面も前に出さず、タブも動かさない。**追従中に前に出られたら中継が見えない**し、
// **訂正タブへ飛ばすのは「訂正するかどうかをこちらが決める」ことになる**
// （それは人が決めること）。
//
// ⚠️ **PNG も残さない。** 追従は 1 秒ごとに撮るので、残すと**ディスクが埋まる**
// うえ、**大半は「盤が映っていない」で捨てるフレーム**。
// 残すなら**採用した 1 枚だけ**で、それは別の話（Phase 6 の「録画」）。
//
// ⚠️ **枠の門番は掛けたまま**（`Capture` と同じ）。「どこを撮るのかが画面に
// 見えていること」は追従でも変わらない。
func (s *CaptureService) CaptureQuiet() (CaptureResult, error) {
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
	// ⚠️ **採用が決まるまで控えておく**（`SaveFollowFrame`）。手を決めたかどうかは
	// **解析タブ側が返事をするまで分からない**ので、ここでは残すか決められない。
	s.mu.Lock()
	s.lastQuiet = img
	s.mu.Unlock()

	b := img.Bounds()
	result := CaptureResult{
		Width: b.Dx(), Height: b.Dy(), Source: ImageSourceScreen,
		Warnings: []string{}, HandTotal: map[string]int{},
	}
	// ⚠️ **読まずに済むなら読まない**（2026-09-18）。認識は 2.1 秒かかるので、
	// **変わっていない 1 枚に払うと、そのぶんだけ次の手に気づくのが遅れる。**
	if skip := s.gate(img); skip != "" {
		result.Skipped = skip
		s.noteQuiet(result)
		return result, nil
	}
	// ⚠️ **変わったマスだけで済むなら 81 マスを読まない**（速い経路。2026-10-07。`celldiff.go`）。
	if cells, ok := s.fastCells(img); ok {
		if len(cells) == 0 {
			// 比べる相手から何も変わっていない（露出の揺れでふるいが動いただけ）。
			result.Skipped = cellSkipSame
		} else {
			result.Cells = cells
		}
		s.noteQuiet(result)
		return result, nil
	}
	return s.readQuiet(img, region, result), nil
}

// RecognizeQuiet は**直前に `CaptureQuiet` で撮った 1 枚を、81 マスで読む**（2026-10-07）。
//
// 速い経路（`CaptureResult.Cells`）で手が決まらなかったときに、フロントが呼ぶ。撮り直さないのは、
// **変わったマスを測った 1 枚と、読む 1 枚を同じにするため**（間に次の手が指されると話が食い違う）。
func (s *CaptureService) RecognizeQuiet() (CaptureResult, error) {
	s.mu.Lock()
	img := s.lastQuiet
	s.mu.Unlock()
	if img == nil {
		return CaptureResult{}, errors.New("読み直す 1 枚がありません")
	}
	region, _, err := s.captureRegion()
	if err != nil {
		return CaptureResult{}, err
	}
	b := img.Bounds()
	result := CaptureResult{
		Width: b.Dx(), Height: b.Dy(), Source: ImageSourceScreen,
		Warnings: []string{}, HandTotal: map[string]int{},
	}
	return s.readQuiet(img, region, result), nil
}

// readQuiet は**盤の有無を見て、81 マスを読み、追っている盤か確かめる**（`CaptureQuiet` の後段）。
func (s *CaptureService) readQuiet(img image.Image, region ikkyoku.Region, result CaptureResult) CaptureResult {
	// ⚠️ **盤の有無だけなら安い**（2026-09-18）。**大盤（解説用）が映っている
	// あいだは画素が動き続ける**ので、上のふるいは素通りする ——
	// そこを 2.1 秒かけて読んでから捨てていた。
	//
	// ⚠️ **ここで落としても `OffBoard` として返すこと**（`Skipped` にしない）——
	// 受ける側から見れば**「別の盤が映っている」に変わりは無い**ので、
	// 安くなっただけで画面の振る舞いを変えない。
	if kind, why := s.detectGate(img, region); kind != "" {
		// **盤が見つからない**ときは SFEN が空のまま（＝ 今までどおり
		// 「盤が映っていません」）。**別の盤**のときだけ `OffBoard` を立てる。
		if kind == detectOff {
			result.OffBoard, result.OffBoardReason = true, why
		}
		s.noteQuiet(result)
		return result
	}
	s.noteRead()
	// ⚠️ **認識に失敗しても成功として返す**（設計原則3）。呼び出し側は
	// 盤面が空なら見送るだけで、**追従そのものは続く。**
	board, err := recognize.FromImage(img)
	if err != nil {
		result.RecognizeError = err.Error()
		return result
	}
	result.SFEN = board.SFEN
	result.Confidence = board.Confidence
	if w := board.Warnings; w != nil {
		result.Warnings = w
	}
	if h := board.HandTotal; h != nil {
		result.HandTotal = h
	}
	result.Debug = board.Debug
	// 速い経路のマス割りを控える（`celldiff.go`）。
	s.noteCellRects(img, board.Debug)

	// ⚠️ **追っている盤かどうかを見る**（2026-09-15）。中継には**大盤**（解説用）が
	// 映り、あちらは**将棋の局面としては矛盾しない**ので盤面だけでは弾けない ——
	// 解説が本譜から 1 手の変化を並べていたら**そのまま棋譜に足してしまう**。
	s.mu.Lock()
	anchor, has := s.boardAnchor, s.boardAnchor.Board.Dx() > 0
	moved := has && s.anchorRegion != region
	s.mu.Unlock()
	if has {
		if got, ok := recognize.SignatureOf(board.Debug); ok {
			// ⚠️ **枠を動かしたらマスタを取り直す**（2026-09-15 に実機で踏んだ）。
			// 記述子は**画像の中の座標**なので、枠が動けば盤は同じでも必ず食い違い、
			// **そこから全部見送られる**。**枠を動かすのは狙いを直す操作。**
			if moved {
				s.mu.Lock()
				s.boardAnchor, s.anchorRegion = got, region
				// ⚠️ **比べる相手も捨てること**（画像の中の座標が変わったので、もう比べられない）。
				s.cellBase = nil
				s.mu.Unlock()
				log.Info("枠が動いたので追う盤を取り直しました", "board", got.Board)
			} else if same, why := anchor.Matches(got); !same {
				result.OffBoard, result.OffBoardReason = true, why
				log.Info("別の盤と判断しました", "reason", why,
					"master", anchor.Board, "got", got.Board,
					"colorGap", anchor.ColorGap(got))
			}
		}
	}
	s.noteQuiet(result)
	return result
}

// ふるいの判断（2026-09-18）。⚠️ **どれも実測で決めた数ではない。**
// 実機の中継で当たり具合を見て詰めること（追跡を止めたときに枚数を出している）。
const (
	// gateChangedRatio は「変化した」と見なす点の割合。
	//
	// **1 マスは盤の 1/81 ≈ 1.2%** で、指し手は動いた先と元の 2 マスが変わる。
	// ⚠️ **上げすぎないこと** —— **ふるいで落とした手は二度と戻らない**
	// （認識を 1 枚余分に読むほうがずっと軽い）。
	gateChangedRatio = 0.0015
	// gateMovingMax は静止を待つ周の上限。
	//
	// ⚠️ **待ち続けないための保険。** 盤の矩形の中に**動き続けるもの**
	// （寄りのカメラ・盤上に重なるテロップ）が入ると、静止判定だけでは
	// **永久に認識しない**という壊れ方をする。**止まらなくても、いずれ読む。**
	gateMovingMax = 8
	// gateSkipMax は**「変わっていない」で省き続けてよい周の数**（約 10 秒）。
	//
	// ⚠️ **これを外さないこと**（2026-09-18 に実機で踏んで足した）。
	// **指したのに「変わっていません」と出続けて、1 手も進まなかった** ——
	// 盤の駒と地色は明るさが近いので、**ふるいが外すことが実際にある。**
	//
	// ⚠️ **しきい値を詰めることで代えないこと。** どんな数にしても
	// **外したら永久に止まる**という作りのほうが問題で、
	// これは**外しても 10 秒で戻る**ことを保証する側。
	//
	// ⚠️ **長考中の無駄にはならない。** 10 秒に 1 枚 2.1 秒を払うだけで、
	// **省く前（毎周 2.1 秒）とは桁が違う。**
	gateSkipMax = 40

	// 省いた理由（`CaptureResult.Skipped`）。
	gateSkipUnchanged = "unchanged"
	gateSkipMoving    = "moving"

	// ふるいが読む側へ倒した理由（`gateRead`。2026-10-07）。**速い経路を使ってよいのは
	// `gateReadSettled`（変化して止まった）だけ** —— 保険の 1 枚は 81 マスを読んで答え合わせを
	// する役で、止まらないまま読む 1 枚は手や頭が動いている最中かもしれない。
	gateReadFirst     = "first"
	gateReadSettled   = "settled"
	gateReadRestless  = "restless"
	gateReadInsurance = "insurance"
)

// gate は**この 1 枚を認識するか**を決める（空なら認識する）。
//
// ⚠️ **判断できないときは認識するほうへ倒すこと**（追う盤が決まっていない・
// 比べる相手が無い・大きさが違う）。**見落とすより 1 枚余分に読むほうが軽い。**
//
// ⚠️ **比べるのは盤の矩形の中だけ**（マスタが持っている）。画面全体で比べると、
// **消費時間の秒読みやテロップで毎周「変化あり」になり、ふるいが素通しになる。**
//
// ⚠️ **枠を動かしたときはここでは直さない。** 動かしている最中は画素が動くので
// `moving` が続き、`gateMovingMax` で認識に落ちて、そこでマスタが取り直される
// （`CaptureQuiet` の後段）。**同じ仕事を 2 か所でしない。**
func (s *CaptureService) gate(img image.Image) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.gateShots++
	anchor, prev := s.boardAnchor, s.gateFrame
	s.gateFrame = img

	// ⚠️ **ここで数えないこと**（2026-09-18）。この先に**盤の有無のふるい**
	// （`detectGate`）があり、そこでも落ちる。**数えるのは実際に認識した 1 枚だけ**
	// （`noteRead`）で、そうでないと省いた割合が実態より悪く見える。
	read := func(why string) string {
		s.gateDirty, s.gateMoving = false, 0
		s.gateSkipped, s.gateMaxSkip = 0, 0
		s.gateRead = why
		return ""
	}
	if anchor.Board.Dx() <= 0 || prev == nil {
		return read(gateReadFirst)
	}
	ratio, ok := ikkyoku.FrameDiff(prev, img, anchor.Board)
	if !ok {
		return read(gateReadFirst)
	}
	if ratio >= gateChangedRatio {
		s.gateDirty = true
		s.gateMoving++
		if s.gateMoving < gateMovingMax {
			return gateSkipMoving
		}
		return read(gateReadRestless) // 止まらないので、待つのをやめて読む
	}
	if !s.gateDirty {
		s.gateSkipped++
		if ratio > s.gateMaxSkip {
			s.gateMaxSkip = ratio
		}
		if s.gateSkipped < gateSkipMax {
			return gateSkipUnchanged
		}
		// ⚠️ **ここが「黙って止まらない」ための保険**（2026-09-18）。
		// **ふるいが外していても、いずれ読む。**
		log.Info("変化が無いまま続いたので 1 枚読みます（ふるいの取りこぼしの確認）",
			"省いた周", s.gateSkipped,
			"いちばん大きかった差", fmt.Sprintf("%.5f", s.gateMaxSkip),
			"しきい値", gateChangedRatio)
		return read(gateReadInsurance)
	}
	return read(gateReadSettled) // 変化したあと静止した ＝ 読むならこの 1 枚
}

// detectMissMax は**盤の有無のふるいで見送り続けてよい周の数**。
//
// ⚠️ **保険を外さないこと。** 検出だけでは判断の材料が少なく、
// **こちらだけで落とし続けると「黙って何も起きない」**になる
// （この追従で何度もやった壊れ方）。**見送りが続いたら 1 枚は必ず読む。**
const detectMissMax = 8

// detectGate は**81 マスの推論に入る前に、盤の有無だけで落とす**（2026-09-18）。
//
// **大盤（解説用）が映っているあいだ、画素は動き続ける**ので画素差分のふるいは
// 素通りする。そこを 2.1 秒かけて読んでから「別の盤」と捨てていた ——
// **盤の矩形を探すだけなら 0.15 秒程度**（認識 2.1 秒のうち検出は 15%）なので、
// **矩形がマスタと合わないなら推論に入らない。**
//
// ⚠️ **`suteme` に矩形を渡す案とは別物**（あちらは**却下済み**。録画中に画面の
// 大きさが変わったときの振る舞いが難しくなる）。**こちらは渡さない**
// —— 自分で探して、自分の持っているマスタと突き合わせるだけ。
//
// ⚠️ **見つからなかったときに落とさないこと。** `DetectRegion` は
// `suteme.Recognize` と違って**画像全体へのフォールバックを持たない**ので、
// **枠を盤にぴったり合わせている人ほど厳しく出る**おそれがある。
// **判断できないときは読むほうへ倒す**（このふるい全体の作法と同じ）。
//
// 戻り値は**落とす種類**（空なら読む）と理由。
//
// ⚠️ **「盤が見つからない」と「別の盤」を分けること。** ユーザがすることが違う
// （待つ / 枠を直す）し、**画面にも別の顔で出る**。
func (s *CaptureService) detectGate(img image.Image, region ikkyoku.Region) (string, string) {
	s.mu.Lock()
	anchor, moved := s.boardAnchor, s.anchorRegion != region
	s.mu.Unlock()
	// ⚠️ **枠を動かしたら通すこと。** マスタは**画像の中の座標**なので、
	// 枠が動けば盤は同じでも必ず食い違う —— ここで落とすと
	// **マスタを取り直す後段（`CaptureQuiet`）に永久に辿り着かない**
	// （2026-09-15 に「枠をずらしたら死ぬ」を踏んだのと同じ形）。
	if anchor.Board.Dx() <= 0 || moved {
		return "", ""
	}

	got, err := recognize.DetectRegion(img)
	if err != nil {
		// **盤が見つからない**（CM・解説・寄り）。⚠️ **ここは弱い判断**なので、
		// 見送り続けずに時々は読む（`detectMissMax`）。
		return s.detectMiss(detectNone, "")
	}
	sig := recognize.Signature{Frame: img.Bounds(), Board: got.Rect}
	if same, why := anchor.Matches(sig); !same {
		return s.detectMiss(detectOff, why)
	}
	s.mu.Lock()
	s.detectMisses = 0
	s.mu.Unlock()
	return "", ""
}

// 盤の有無のふるいが落とした種類。
const (
	// detectNone は**盤が見つからない**（CM・解説・寄り）。
	detectNone = "none"
	// detectOff は**追っている盤ではない**（大盤など）。
	detectOff = "off"
)

// detectMiss は見送りを数え、**続きすぎたら通す**（保険）。
func (s *CaptureService) detectMiss(kind, why string) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detectMisses++
	if s.detectMisses >= detectMissMax {
		s.detectMisses = 0
		return "", "" // 落とし続けない（読んで確かめる）
	}
	return kind, why
}

// noteRead は**実際に認識した 1 枚**を数える。
func (s *CaptureService) noteRead() {
	s.mu.Lock()
	s.gateReads++
	s.mu.Unlock()
}

// resetGate はふるいを白紙に戻す（追跡の始まりと終わり）。
//
// ⚠️ **枚数も 0 に戻すこと** —— 数えているのは**この回の追跡**で、
// 前の対局と混ぜると何を測ったのか分からなくなる。
func (s *CaptureService) resetGate() {
	s.gateFrame, s.gateDirty, s.gateMoving, s.detectMisses = nil, false, 0, 0
	s.gateSkipped, s.gateMaxSkip = 0, 0
	s.gateShots, s.gateReads = 0, 0
}

// noteQuiet は**結果の種類が変わったときだけ**ログに出す。
//
// ⚠️ **毎回出さないこと**（1 秒ごとに回るので読めなくなる）。
// ⚠️ **何も出さないのも駄目** —— 盤が映っていないあいだ Go 側が無言になると、
// **正常に見送っているのか止まっているのかがログから分からない**（実機で聞かれた）。
func (s *CaptureService) noteQuiet(r CaptureResult) {
	kind, msg := "board", "盤を見つけました（追跡）"
	switch {
	// ⚠️ **読んでいない周を「盤が映っていません」と言わないこと**（2026-09-18）。
	// **原因が全く違うのに同じ顔で出る**のが、この追従で何度もやった壊れ方
	// （`TODO.md` 4 の ③）。省いたのは**変わっていないから**で、正常そのもの。
	case r.Skipped != "":
		kind, msg = "skip:"+r.Skipped, "変わっていないので認識を省いています（追跡）"
		switch r.Skipped {
		case gateSkipMoving:
			msg = "動いているので止まるのを待っています（追跡）"
		case cellSkipSame:
			msg = "先端と合っていた 1 枚から、どのマスも変わっていません（追跡）"
		}
	case len(r.Cells) > 0:
		kind, msg = "cells", "変わったマスだけで手を割り出します（追跡）"
	// ⚠️ **`OffBoard` を先に見ること**（2026-09-18）。盤の有無のふるいで落ちた周は
	// **読んでいないので SFEN が空**で、順番が逆だと**別の盤を「盤が映っていません」**
	// と言う（**原因が違うのに同じ顔で出る**、この追従で何度もやった壊れ方）。
	case r.OffBoard:
		kind, msg = "off", "別の盤が映っています（追跡・見送り）"
	case r.SFEN == "":
		kind, msg = "none", "盤が映っていません（追跡・見送り）"
	}
	s.mu.Lock()
	changed := s.quietOutcome != kind
	s.quietOutcome = kind
	s.mu.Unlock()
	if changed {
		log.Info(msg, "reason", r.OffBoardReason, "confidence", r.Confidence)
	}
}

// AnchorBoard は**今映っている盤を「これから追う盤」として覚える**（2026-09-15）。
//
// ⚠️ **記述子だけを覚える**（局面は覚えない）。手が進めば駒は動くので、
// **同じ盤かどうかの判断には使えない。**
//
// ⚠️ **覚えるのは人が「追う」と言ったときだけ。** 撮るたびに覚え直すと、
// **大盤に切り替わった 1 枚でマスタがそちらへ移ってしまう**（そこから先は
// ずっと大盤を追う）。
func (s *CaptureService) AnchorBoard() (CaptureResult, error) {
	s.mu.Lock()
	s.boardAnchor, s.anchorRegion, s.quietOutcome = recognize.Signature{}, ikkyoku.Region{}, ""
	// ⚠️ **ふるいも白紙に戻すこと**（2026-09-18）。この 1 枚は
	// **これから追う盤を決めるための 1 枚**なので、**必ず読む**。
	s.resetGate()
	s.mu.Unlock()

	r, err := s.CaptureQuiet()
	if err != nil {
		return r, err
	}
	sig, ok := recognize.SignatureOf(r.Debug)
	if !ok {
		return r, fmt.Errorf("盤が映っていないので、追う盤を決められません")
	}
	dir := s.startFollowDir()
	region, _, rerr := s.captureRegion()
	if rerr != nil {
		return r, rerr
	}
	s.mu.Lock()
	s.boardAnchor, s.anchorRegion = sig, region
	s.mu.Unlock()
	log.Info("追う盤を決めました", "board", sig.Board, "color", sig.Color, "dir", dir)
	return r, nil
}

// startFollowDir は録画の保存先を作り、**マスタ画像**を残す（2026-09-15）。
//
// ⚠️ **マスタを残すのが要点。** 追いかける基準そのものなので、
// **これが無いと「なぜ別の盤だと判断したか」を後から確かめられない**
// （`recognize.Signature` は矩形と色しか持っていない）。
//
// ⚠️ **失敗しても追跡を止めないこと**（設計原則3）。録画はおまけで、
// **残らなくても中継は追える。**
func (s *CaptureService) startFollowDir() string {
	base, err := ikkyoku.DefaultOutDir()
	if err != nil {
		log.Warn("録画の保存先が決められません", "error", err)
		return ""
	}
	// ⚠️ **今撮った 1 枚をそのまま使うこと**（`CaptureQuiet` が控えている）。
	// 撮り直すと**マスタと記述子が別のフレームになる** —— 数えるのは同じ盤でも、
	// 手が被った瞬間などに**画像と判断がずれる**。
	s.mu.Lock()
	img := s.lastQuiet
	s.mu.Unlock()
	dir := filepath.Join(base, "follow", time.Now().Format("20060102-150405"))
	if img != nil {
		if _, err := ikkyoku.SavePNGAs(img, dir, "master.png"); err != nil {
			log.Warn("マスタ画像を残せません", "error", err)
			return ""
		}
	}
	s.mu.Lock()
	s.followDir, s.followMisses, s.lastMissKind = dir, 0, ""
	s.mu.Unlock()
	return dir
}

// SaveFollowFrame は**手を決めた 1 枚**を録画として残す（2026-09-15）。
//
// number は最初の手の手数（棋譜の数え方）、moves はその 1 枚で足した手（USI）。
// ⚠️ **guess（推測で足したか）を名前に入れること** —— **後から見たいのはそれ**で、
// 「手が映り込んで誤認識した」はまず推測の側に出る。
//
// ⚠️ **戻り値でエラーを返さないこと。** 呼ぶのは追跡のループの中なので、
// **残せなかっただけで追跡が止まるのは割に合わない**（設計原則3）。
// 理由はログに出す。
func (s *CaptureService) SaveFollowFrame(number int, moves []string, guess bool) string {
	s.mu.Lock()
	dir, img := s.followDir, s.lastQuiet
	s.mu.Unlock()
	if dir == "" || img == nil {
		return ""
	}
	// ⚠️ **足せたら数え直すこと。** 残したいのは**崩れ始め**なので、
	// 追いついているあいだに使い切っていては意味が無い。
	s.mu.Lock()
	s.followMisses, s.lastMissKind = 0, ""
	s.mu.Unlock()

	name := followFrameName(number, moves, guess)
	path, err := ikkyoku.SavePNGAs(img, dir, name)
	if err != nil {
		log.Warn("採用した画像を残せません", "name", name, "error", err)
		return ""
	}
	return path
}

// SaveConnectFrame は**手で繋いで決まった手**を、その画像ごと残す（2026-09-18）。
//
// ⚠️ **追従の録画と揃えるためのもの。** 決まった手の根拠が画像で残っていないと、
// **「なぜその駒になったのか」を後から確かめられない**（実機で
// **6九歩打と読んだが正しくは6九桂打**という誤認識が出て、**手で繋いだぶんだけ
// 証拠が無かった**）。
//
// ⚠️ **置き場所は追従とは別**（`<captures>/connect/<日付>/`）。あちらは
// **1 局を 1 つのディレクトリに揃える**ためのもので、手で繋ぐのは散発的に起きる
// —— 混ぜると**どちらの経路で決まった手か**が読めなくなる。
//
// ⚠️ **認識を覆したかを名前に入れること**（`-fixed`）。**後から見たいのはそちら。**
//
// ⚠️ **エラーを返さないこと**（設計原則3）。残せなくても繋ぐのは成立する。
func (s *CaptureService) SaveConnectFrame(number int, moves []string, fixed bool) string {
	s.mu.Lock()
	img := s.lastShot
	s.mu.Unlock()
	if img == nil || len(moves) == 0 {
		return ""
	}
	base, err := ikkyoku.DefaultOutDir()
	if err != nil {
		log.Warn("繋いだ手の画像を残せません", "error", err)
		return ""
	}
	dir := filepath.Join(base, "connect", time.Now().Format("20060102"))
	name := connectFrameName(number, moves, fixed)
	path, err := ikkyoku.SavePNGAs(img, dir, name)
	if err != nil {
		log.Warn("繋いだ手の画像を残せません", "name", name, "error", err)
		return ""
	}
	log.Info("繋いだ手の画像を残しました", "path", path, "moves", strings.Join(moves, " "))
	return path
}

// connectFrameName は `042-6i6h_...[-fixed].png`。
//
// ⚠️ **手数は 0 詰め 3 桁**（追従と同じ。並びが手順の順になる）。
// ⚠️ **同じ手数で 2 回繋ぐことがある**ので、**上書きしないよう時刻を足す** ——
// 手で繋ぐのは**やり直しが普通**（繋いで、違うと思って戻して、撮り直す）。
func connectFrameName(number int, moves []string, fixed bool) string {
	name := strings.TrimSuffix(followFrameName(number, moves, false), ".png")
	if fixed {
		name += "-fixed"
	}
	return name + "-" + time.Now().Format("150405") + ".png"
}

// followMissMax は**繋げなかった周を残す枚数の上限**。
//
// ⚠️ **知りたいのは崩れ始めの数枚**（そこから先は同じ状態が続くだけ）。
const followMissMax = 12

// SaveFollowMiss は**繋げなかった周**の画像を残す（2026-09-15）。
//
// ⚠️ **採用した画像だけでは原因が分からない。** 実機で追従が 4 手目から
// 90 手ぶん止まったとき、**止まった瞬間の画像が 1 枚も残っていなかった**ので、
// 「認識が外したのか」「順番が決まらなかったのか」を切り分けられなかった。
//
// ⚠️ **上限を超えたら黙って捨てること**（設計原則3）。追いつけない状態は
// 何十分も続くので、全部残すとディスクが埋まる。
func (s *CaptureService) SaveFollowMiss(number int, kind string) string {
	s.mu.Lock()
	dir, img, n := s.followDir, s.lastQuiet, s.followMisses
	// ⚠️ **同じ理由が続いているあいだは残さない**（変わり目だけ）。
	// 「別の盤」も「変わっていない」も何十周も続くので、毎周残すと
	// **同じ絵で上限を使い切り、そのあとの本当の変わり目が残らない。**
	same := kind == s.lastMissKind
	if dir != "" && img != nil && n < followMissMax && !same {
		s.followMisses, s.lastMissKind = n+1, kind
	}
	s.mu.Unlock()
	if dir == "" || img == nil || n >= followMissMax || same {
		return ""
	}
	name := followMissName(number, kind, n)
	path, err := ikkyoku.SavePNGAs(img, dir, name)
	if err != nil {
		log.Warn("繋げなかった画像を残せません", "name", name, "error", err)
		return ""
	}
	log.Info("繋げなかった周を残しました", "name", name)
	return path
}

// followMissName は「x042-choices-1.png」のような名前を組み立てる。
//
// ⚠️ **頭に x を付けること** —— 採用した画像（`042-7g7f.png`）と並ぶので、
// **どれが成立した手でどれが失敗かが名前で分かる**必要がある。
// ⚠️ **連番を付けること** —— 同じ手数で何周も失敗するので、
// 付けないと**崩れ始めの 1 枚が後の周に上書きされる**（一番見たい 1 枚が消える）。
func followMissName(number int, kind string, seq int) string {
	if number < 0 {
		number = 0
	}
	k := strings.Map(safeNameRune, kind)
	if k == "" {
		k = "miss"
	}
	return fmt.Sprintf("x%03d-%s-%d.png", number, k, seq+1)
}

// followFrameName は「042-7g7f-guess.png」のような名前を組み立てる。
//
// ⚠️ **手数を 0 詰めにすること** —— そうしないとファイルの並びが手順の順に
// ならない（9 手目の次に 10 手目ではなく 100 手目が並ぶ）。
// ⚠️ **パス区切りを作らないこと**（`SavePNGAs` が弾くが、ここで作らないのが本筋）。
func followFrameName(number int, moves []string, guess bool) string {
	if number < 0 {
		number = 0
	}
	parts := make([]string, 0, len(moves))
	for _, mv := range moves {
		if mv = strings.Map(safeNameRune, mv); mv != "" {
			parts = append(parts, mv)
		}
	}
	name := fmt.Sprintf("%03d", number)
	if len(parts) > 0 {
		name += "-" + strings.Join(parts, "_")
	}
	if guess {
		name += "-guess"
	}
	return name + ".png"
}

// safeNameRune はファイル名に使ってよい字だけを通す（USI は英数字なので普通は素通り）。
//
// ⚠️ **打つ手の `*` は `@` にすること**（2026-10-05 に実機で踏んだ）。`*` は Windows の
// ファイル名に使えないので、`P*4a` の画像が「ファイルの作成に失敗しました」で残らず、
// **誤った打つ手の証拠が 1 枚も無かった。**
func safeNameRune(r rune) rune {
	switch {
	case r == '*':
		return '@'
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '+':
		return r
	default:
		return -1
	}
}

// ClearBoardAnchor は追う盤を忘れる（追跡を止めたとき）。
func (s *CaptureService) ClearBoardAnchor() {
	s.mu.Lock()
	// ⚠️ **録画も畳むこと。** 次に「追う」を押したら**別の対局**かもしれないので、
	// 前の回の続きに書き足すと**1 つのディレクトリに 2 局が混ざる**。
	s.boardAnchor, s.anchorRegion, s.quietOutcome = recognize.Signature{}, ikkyoku.Region{}, ""
	s.followDir, s.lastQuiet, s.followMisses, s.lastMissKind = "", nil, 0, ""
	s.cellBase, s.cellRects = nil, nil
	// ⚠️ **ふるいがどれくらい効いたかを出す**（2026-09-18）。定数（変化のしきい値・
	// 待つ周の上限・撮る間隔）は**どれも当て推量**なので、
	// **実機の中継で詰めるための材料**が要る。
	shots, reads := s.gateShots, s.gateReads
	s.resetGate()
	s.mu.Unlock()
	if shots > 0 {
		log.Info("追跡を止めました", "撮った枚数", shots, "認識した枚数", reads,
			"省いた割合", fmt.Sprintf("%.0f%%", 100*float64(shots-reads)/float64(shots)))
	}
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

	// ⚠️ **手で撮った 1 枚も控えておくこと**（2026-09-18）。訂正タブの
	// **「本譜に繋ぐ」で決まった手**を、その画像ごと残すため（`SaveConnectFrame`）。
	// **決まった手の根拠が画像で残っていないと、誤認識を追えない** ——
	// 追従では残していたのに、**手で繋いだときだけ残っていなかった**（実機で出た）。
	s.mu.Lock()
	s.lastShot = img
	s.mu.Unlock()

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
