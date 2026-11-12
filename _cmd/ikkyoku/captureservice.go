package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"  // 画像ファイルの読み込み（loadImage）
	_ "image/jpeg" // 同上
	"image/png"
	"os"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/followcam"
	"github.com/ShinteLab/ikkyoku/guide"
	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/recognize"
)

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

	// mu は 1 つだけ（役割ごとのファイルに分けても、ロックは分けない）。
	mu sync.Mutex

	// ---- 認識器（capture_recognizer.go）-----------------------------------
	// recognizerDir は学習データの置き場所(config.Config の SutemeDataDir)。
	// 空ならこの段は無い(suteme 既定の探索には任せない。recognize.LoadSources)。
	recognizerDir string
	// recognizerModelDir は配布モデルの置き場所の設定(config.Config の SutemeModelDir)。
	// **空なら既定の置き場所**(解決は recognize.LoadSources が config.Config.ModelDir で毎回する)。
	recognizerModelDir string
	// recognizerSource は読み込み元の設定(config.Config の SutemeSource)。
	// **設定タブから変えられる**ので、mu で守る(applyRecognizerSource)。
	recognizerSource string
	// recognizerStatus は直近の読み込み結果。表示のためだけに 3.5MB を
	// 読み直さなくて済むよう覚えておく。
	recognizerStatus RecognizerStatus
	// onRecognizer は認識器を読み直すたびに呼ぶ（画面の ⚠ を合わせるため。main.go が差し込む）。
	// ⚠️ **起動時だけでなく、設定タブで指し直したときも呼ぶこと** —— 直ったのに ⚠ が残る。
	onRecognizer func(RecognizerStatus)

	// ---- ウィンドウ（capture_windows.go）-----------------------------------
	// mainShown はメイン画面を一度でも出したか。**初回だけやること**
	// (枠の外への配置・表示位置の記録)を 2 回目以降に繰り返さないための記録。
	// 前面に出す(Show/Focus)のは毎回。revealMain 参照。
	mainShown bool
	// frameReady は枠ウィンドウの WindowRuntimeReady が来たか（2026-10-04）。
	// **起動時にメイン画面を出す合図はこれ**なので、来ないとメイン画面が 1 枚も出ない
	// まま動き続ける。`revealIfStuck` が待つのはこれ。
	frameReady bool
	// onHideRefused は**追っているあいだに枠を隠そうとして断った**ことを中継セッションへ渡す（2026-10-11。
	// `StudyService.FollowHideRefused`。帯に出す）。nil なら知らせない（設計原則3）。
	onHideRefused func(message string)
	// clickThrough は設定「枠の内側で後ろの画面を操作する」（`config.Config.ClickThrough`）。
	clickThrough bool
	// clickStop は素通しの見張り（watchCursor）を止めるチャネル。
	// **入のあいだだけ goroutine が居る。**
	clickStop chan struct{}
	// mouseThrough は今 WS_EX_TRANSPARENT を立てているか。
	// **設定そのものではない**（設定が入でも、カーソルがツールバーの上に居るあいだは false）。
	mouseThrough bool

	// ---- 撮る・中継を追う（captureservice.go / enginepriority.go）-----------
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
	// follow は**中継を追うときの撮る側**（`followcam.Camera`。撮る・ふるい・速い経路・録画と、追う 1 回の持ち物。
	// 2026-11-13 にここから `ikkyoku/followcam` へ移した）。`main` が `StudyService.Camera` に入れる。
	// ⚠️ **追従の状態をここ（`CaptureService`）に足さない**（あちらの `captureFollow` へ）。
	// ⚠️ **`mu` を持ったままあちらを呼ばない**（あちらは自分のロックを持ち、差し込み口でこちらへ戻る）。
	// `&CaptureService{}` で作ったとき（テスト）は nil。
	follow *followcam.Camera
	// followFirst は設定「中継を追うあいだは盤の読みを優先する」（`enginepriority.go`。2026-10-07）。
	followFirst bool
	// enginesLowered は外部エンジンの優先度を下げているか（戻し忘れないため）。
	enginesLowered bool
	// engineNames は登録してある外部エンジンの実行ファイルのパス（`main` が差し込む。設定から読む）。
	engineNames func() []string

	// ---- 切り離した窓の表示（capture_panes.go。2026-09-12）-------------------
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
