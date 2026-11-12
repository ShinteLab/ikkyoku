// Package followcam は**中継を追う 1 回の撮る側**（撮る・ふるう・変わったマスを測る・81 マスを読む・録画）。
//
// 1 周の段取りを回すのは `app` の中継セッション（`followsession.go`）で、ここは `app.FollowCamera` として
// 差し込まれる（`Camera`）。⚠️ **局面を知らない層なので、「合っているか」「動いていないか」を判断しない**
// （決めるのは解析タブ側。ここは言われた組を覚えるだけ）。
// ⚠️ **Wails に依存しない**（2026-11-13 に `_cmd/ikkyoku` の `CaptureService` から移した）。ウィンドウが要るもの
// （枠の矩形・訂正した 1 枚・外部エンジンの優先度）は `Camera` の差し込み口から入れる。
// 制約は `_docs/cmd-capture.md`、理由は `_docs/design-follow.md`。
package followcam

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// Shot は撮る側が返す 1 枚（`AnchorBoard` / `CaptureQuiet` / `RecognizeQuiet`）。中継の追従が使うものだけを持つ。
//
// 盤面の認識結果も含むが、**認識できなくても撮れたものとして返す**（設計原則3）。認識だけが失敗したときは
// SFEN が空になり、RecognizeError に理由が入る。
type Shot struct {
	// Skipped は**認識を省いた**理由（省いていなければ空。2026-09-18）。
	//
	// `unchanged`（前の 1 枚と変わっていない）/ `moving`（まだ動いている）/ `same`（比べる相手から、どのマスも変わっていない）。
	//
	// ⚠️ **「盤が映っていない」（SFEN が空）と混ぜないこと。** あちらは
	// **読んだうえで盤が無かった**で、こちらは**読んでいない**。
	// 呼ぶ側は**黙って次の周へ行くだけ**（見送りとして数えない・録画も残さない）。
	Skipped string
	// Fast は**速い経路で扱う 1 枚か**（真なら**81 マスは読んでいない**。SFEN は空）。受ける側は
	// `StudyService.FollowCells(Cells)` に渡し、手が決まらなければ `RecognizeQuiet` で同じ 1 枚を読む。
	// ⚠️ **「盤が映っていない」と混ぜないこと。**
	Fast bool
	// Cells は**比べる相手の 1 枚から変わったマス**（撮った画像の向きの 81 マスの番号。2026-10-07）。
	// 比べる相手が無い・比べられないときは nil。81 マスを読んだ周でも入る —— 受ける側は読んだ結果のあと
	// `StudyService.FrameFits` に渡し、合えば比べる相手を今の 1 枚に替える（`SetCellBase`）。
	// ⚠️ **nil は「測っていない」・空は「何も変わっていない」**（`FrameFits` は測った 1 枚なら空でも呼ぶ。潰さないこと）。
	Cells []int
	// SFEN は読んだ盤（盤面部分のみ。読んでいない・盤が無ければ空）。
	SFEN string
	// Confidence は盤面検出の信頼度（0.0〜1.0）。
	Confidence float64
	// OffBoard は**追いかけている盤とは別の盤が映っている**（中継の大盤など）。
	//
	// ⚠️ **「盤が映っていない」とは別物。** あちらは一致度が落とすもので、
	// こちらは**盤としては読めているが、追っている盤ではない**。
	// **次にすることが違う**（待つ / 撮り直す）。
	OffBoard bool
	// OffBoardReason はその理由（同じ盤なら空）。
	OffBoardReason string
	// RecognizeError は「撮れたが認識できなかった」ときの理由。
	// 撮ること自体の失敗はこれではなくメソッドのエラーで表す。
	RecognizeError string
	// Debug は認識の観測情報（盤面と判定した矩形・マス割り）。追う盤（マスタ）を決めるのに使う（`AnchorBoard`）。
	Debug *recognize.Debug
	// CellConfidence は**マスごとの読みの確かさ**（81 個・行優先。`recognize.CellConfidence`。2026-10-11）。
	// 中継を追うときの費用表の元で、中継セッションがそのまま `StudyService.FollowAuto` に渡す。
	// ⚠️ **空きのマスも被覆率から確かさを持つ**（以前は 0 で、下限の 0.15 で数えていた）。読めなければ nil。
	CellConfidence []float64
	// CellHidden は**見えないマス**（手や頭が被っている。`recognize.CellHidden`）。`FollowAuto` にそのまま渡す。
	CellHidden []bool
}

// CellPiece は**マス 1 つの読み**（`Camera.ReadCells` が返し、`StudyService.FollowCells` が受ける。2026-10-07）。
// Cell は撮った画像の向きの 81 マスの番号、Piece は撮った画像の向きの SFEN の駒（手前側が大文字。
// 空は ""、読めなければ "?"）。Side は駒の向き（`recognize.SideUp` = 手前向き / `SideDown` = 向こう向き /
// "" = 空き・切り出せない）。⚠️ **Piece が "?" でも Side は入っていることがある**（成駒は種類が読めないのが
// 普通だが、向きは読める。`recognize.CellRead`）。
type CellPiece struct {
	Cell  int    `json:"cell"`
	Piece string `json:"piece"`
	Side  string `json:"side"`
}

// CellPair は**動いた駒の、動く前のマスと動いた先のマス**（撮った画像の向きの 81 マスの番号。2026-10-07）。
// From は速い経路の比べる相手の 1 枚（`cellBase`）、To は今の 1 枚で見る。
// Same が真なら**どちらも今の 1 枚で見る**（2026-10-09。打った駒と盤上の駒の比べ。`followDropPairs`）。
type CellPair struct {
	From int  `json:"from"`
	To   int  `json:"to"`
	Same bool `json:"same,omitempty"`
}

// Camera は**中継セッションの撮る側**（`app.FollowCamera` を満たす）。ゼロ値で使える（差し込み口はどれも nil でよい）。
//
// ⚠️ **差し込み口は `mu` を持たずに呼ぶこと**（先で `CaptureService` のロックを取る。持ったまま呼ぶと、
// あちらがロックを持ってこちらを呼ぶ経路と向きが逆になる）。差し込み口は追い始める前に入れる（あとから替えない）。
type Camera struct {
	// Region は**今の撮影範囲**を返す（枠が出ていなければエラー。`_cmd/ikkyoku` の `requireFrame` + `captureRegion`）。
	// nil なら撮れない。
	Region func() (ikkyoku.Region, error)
	// Corrected は**解析タブの根を採った（訂正した）1 枚**を返す（`CaptureService.followShot`。無ければゼロ値）。
	Corrected func() ShotAnchor
	// Session は解析タブの今の検討の id を返す（`StudyService.Session`）。
	// ⚠️ **nil なら訂正した 1 枚は使わない**（押した瞬間の 1 枚で決める）。
	Session func() string
	// BeforeRead は 81 マスを読む直前に呼ぶ（外部エンジンの優先度を合わせる。`enginepriority.go`）。
	BeforeRead func()
	// AfterAnchor は追う盤を決めたあとに別のゴルーチンで呼ぶ（同上）。
	AfterAnchor func()
	// AfterEnd は撮る側の持ち物を捨てたあとに呼ぶ（外部エンジンの優先度を戻す）。
	AfterEnd func()

	mu sync.Mutex
	// debug は設定「中継の録画とログを残す（デバッグ）」（2026-10-11。`SetDebug`）。偽なら録画を残さない。
	// ⚠️ **追う前から入る設定なので `fol` に入れない**（止めても消えない）。
	debug bool
	// fol は**中継を追う 1 回の撮る側の持ち物**（`captureFollow`。2026-10-11）。
	// ⚠️ **追い始め（`BeginFollow`）で作り直し、止めたとき（`EndFollow`）に捨てる**。`mu` で守る。
	fol captureFollow
}

// region は今の撮影範囲を取る（差し込み口が無ければエラー）。⚠️ **`mu` を持たずに呼ぶこと。**
func (s *Camera) region() (ikkyoku.Region, error) {
	if s.Region == nil {
		return ikkyoku.Region{}, errors.New("撮影範囲が決まっていません")
	}
	return s.Region()
}

// Following は**中継を追っているか**（`BeginFollow` から `EndFollow` まで）。追っているあいだは枠を隠させない（`HideFrame`）。
func (s *Camera) Following() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fol.following
}

// Anchored は**追う盤（マスタ）を決めてあるか**（外部エンジンの優先度を下げるかの判断。`enginepriority.go`）。
func (s *Camera) Anchored() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fol.boardAnchor.Board.Dx() > 0
}

// SetDebug は設定「中継の録画とログを残す（デバッグ）」を入れる（起動時と切り替えたとき）。
// 録画は次に「追う」を押したときから（切ったときは次の 1 枚から残さない）。
func (s *Camera) SetDebug(v bool) {
	s.mu.Lock()
	s.debug = v
	s.mu.Unlock()
}

// 中継を追う 1 回の、撮る側の持ち物（2026-10-11。追従の作り直し。理由は `_docs/design-follow.md`）。
//
// 以前は追う盤（マスタ）・録画・比べる相手の 1 枚・ふるいの数えが `CaptureService` のフィールドに散らばり、
// 捨てる場所も `AnchorBoard`・`ClearBoardAnchor`（今は無い）・`resetGate` に分かれていた（観点 3「どの状態がいつ捨てられるかを
// 1 か所で見渡せない」）。**追い始め（`BeginFollow`）で作り直し、止めたとき（`EndFollow`）に丸ごと捨てる**。
// 呼ぶのは中継セッション（`app` の `followsession.go`。`app.FollowCamera` として差し込まれる）。
//
// ⚠️ **追う前から入ってくるものはここに入れない**（訂正した 1 枚 `followShot`・普通に撮った 1 枚 `lastShot`・
// 設定 `followFirst`・フック。どれも `_cmd/ikkyoku` の `CaptureService` が持つ。設定 `debug` と差し込み口は `Camera` の側）。
// ⚠️ **外部エンジンの優先度を下げたか（`enginesLowered`）も入れない** —— OS の状態なので、
// 捨てても戻らない（`EndFollow` が捨ててから `AfterEnd` で戻させる）。

// captureFollow は中継を追う 1 回の撮る側の持ち物（`Camera.fol`。`mu` で守る）。ゼロ値は「追っていない」。
type captureFollow struct {
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
	// following は**中継を追っているか**（2026-10-10。中継セッションが `BeginFollow` / `EndFollow` で入り切りする）。
	// 追っているあいだは枠を隠させない（`HideFrame`）。⚠️ **追う盤（`boardAnchor`）で代わりにしないこと** ——
	// 盤が映っていなくても追い始められる（空のまま追っている）うえ、エラーで止まったときは忘れない。
	following bool
	// gateRead は**ふるいが読む側へ倒した理由**（`gateReadSettled` など。速い経路を使ってよいかの判断）。
	gateRead string
	// cellGen は**比べる相手（`cellBase`）の版**。替えるたびに進める（`setCellBaseLocked`）。
	// 違う相手と比べた変わったマスの組どうしは比べられないので、組に版を添えて持つ（`frameCells`）。
	cellGen int
	// gateCells は**前の周に撮った 1 枚の、比べる相手から変わったマス**（2026-10-07）。
	// ふるいが「変わったマスの組が 2 枚続けて同じ ＝ 止まった」と見なすのに使う（`gate`）。毎周入れ替える。
	gateCells frameCells
	// cellsFailed は**速い経路で決まらなかった、変わったマスの組**（2026-10-07。`markCellsFailed`）。
	// ⚠️ **この組が続いても「止まった」と見なさない** —— 駒を掴んだまま行き先が光り続けると同じ組が続き、
	// 見なすと 0.5 秒ごとに 81 マスを読み直す。比べる相手を替えたら忘れる。
	cellsFailed frameCells
	// cellsUnmoved は**81 マスを読んだら何も指していなかった、変わったマスの組**（2026-10-08。`CellsUnmoved`）。
	// 駒を掴んで行き先が光っているだけの組。⚠️ **この組のあいだは「変わっていない」に数える**（`gate`）——
	// 81 マスを読まず、組が変われば（指した）すぐ速い経路に入る。保険（`gateSkipMax`）は残る。比べる相手を替えたら忘れる。
	cellsUnmoved frameCells
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
	// ⚠️ **認識 1 枚（約 0.1 秒）**が追従の速さを決めている。ここでやるのは
	// **読む枚数を減らすこと**。長考中も CM 中も画素は動いていない
	// ので、**前の 1 枚と変わっていなければ認識を呼ばない。**
	//
	// ⚠️ **状態はここに置く**（`ikkyoku.FrameDiff` は測るだけ）。追従の状態は
	// この構造体に集めてある（マスタ・録画・速い経路）ので、**もう 1 か所に散らさない。**

	// gateFrame は前の周に撮った 1 枚（比べる相手）。
	gateFrame image.Image
	// gateDirty は「変化を見たが、まだ認識していない」。
	//
	// ⚠️ **変化した周ではなく、その次の「止まった」周で認識すること**（静止判定）。
	// 中継には**棋士の手が映り込む**ので、動いている最中の 1 枚を読むと
	// **認識 1 枚ぶんかけてゴミを取る**（駒が隠れた盤が「繋がらない」で捨てられる）。
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
}

// BeginFollow は**中継を追い始める**（撮る側の持ち物を作り直す）。中継セッションが `StartFollow` で呼ぶ。
// 追う盤はまだ決めない（`AnchorBoard`）。
func (s *Camera) BeginFollow() {
	s.mu.Lock()
	s.fol = captureFollow{following: true}
	s.mu.Unlock()
}

// EndFollow は**中継の追跡を終える**（撮る側の持ち物を丸ごと捨てる）。中継セッションがどの止まり方でも呼ぶ。
//
// ⚠️ **録画も畳むこと。** 次に「追う」を押したら**別の対局**かもしれないので、前の回の続きに書き足すと
// **1 つのディレクトリに 2 局が混ざる**。⚠️ **エンジンの優先度を戻すこと**（下げていれば）。
func (s *Camera) EndFollow() {
	s.mu.Lock()
	// anchored は追う盤を決めていたか（本体のログに出すのは追っていたときの 1 回だけ）。
	anchored := s.fol.boardAnchor.Board.Dx() > 0
	// ⚠️ **ふるいがどれくらい効いたかを出す**（2026-09-18）。定数はどれも当て推量なので、実機で詰める材料が要る。
	shots, reads := s.fol.gateShots, s.fol.gateReads
	s.fol = captureFollow{}
	s.mu.Unlock()
	if s.AfterEnd != nil {
		s.AfterEnd()
	}
	if shots > 0 {
		log.Follow.Info("追跡を止めました", "撮った枚数", shots, "認識した枚数", reads,
			"省いた割合", fmt.Sprintf("%.0f%%", 100*float64(shots-reads)/float64(shots)))
	}
	if anchored {
		log.Info("中継の追跡を止めました", "撮った枚数", shots, "認識した枚数", reads)
	}
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
func (s *Camera) CaptureQuiet() (Shot, error) {
	region, err := s.region()
	if err != nil {
		return Shot{}, err
	}
	img, err := ikkyoku.Capture(region)
	if err != nil {
		return Shot{}, err
	}
	// ⚠️ **採用が決まるまで控えておく**（`SaveFollowFrame`）。手を決めたかどうかは
	// **解析タブ側が返事をするまで分からない**ので、ここでは残すか決められない。
	s.mu.Lock()
	s.fol.lastQuiet = img
	s.mu.Unlock()

	result := Shot{}
	// ⚠️ **読まずに済むなら読まない**（2026-09-18）。認識は約 0.1 秒かかるので、
	// **変わっていない 1 枚に払うと、そのぶんだけ次の手に気づくのが遅れる。**
	// 比べる相手から変わったマスは 1 枚につき 1 回だけ測る（ふるいと速い経路の両方が使う。数ミリ秒）。
	fc := s.cellsOf(img)
	if skip := s.gate(img, fc); skip != "" {
		result.Skipped = skip
		s.noteQuiet(result)
		return result, nil
	}
	// ⚠️ **変わったマスだけで済むなら 81 マスを読まない**（速い経路。2026-10-07。`celldiff.go`）。
	if fc.ok {
		result.Cells = fc.cells
	}
	if s.fastCells(fc) {
		if len(fc.cells) == 0 {
			// 比べる相手から何も変わっていない（露出の揺れでふるいが動いただけ）。
			result.Skipped = cellSkipSame
		} else {
			result.Fast = true
		}
		s.noteQuiet(result)
		return result, nil
	}
	return s.readQuiet(img, region, result), nil
}

// RecognizeQuiet は**直前に `CaptureQuiet` で撮った 1 枚を、81 マスで読む**（2026-10-07）。
//
// 速い経路（`Shot.Cells`）で手が決まらなかったときに、中継セッションが呼ぶ。撮り直さないのは、
// **変わったマスを測った 1 枚と、読む 1 枚を同じにするため**（間に次の手が指されると話が食い違う）。
func (s *Camera) RecognizeQuiet() (Shot, error) {
	s.mu.Lock()
	img := s.fol.lastQuiet
	s.mu.Unlock()
	if img == nil {
		return Shot{}, errors.New("読み直す 1 枚がありません")
	}
	region, err := s.region()
	if err != nil {
		return Shot{}, err
	}
	result := Shot{}
	// 比べる相手からの変わったマスは添えたまま（読んだ結果のあと `FrameFits` に使う）。
	// ⚠️ **ここへ来た組は「速い経路で決まらなかった組」として覚える**（`cellsFailed`。2026-10-07）。
	// 同じ組が続いても、ふるいはもう「止まった」と見なさない（毎周 81 マスを読み直さない）。
	if fc := s.cellsOf(img); fc.ok {
		result.Cells = fc.cells
		s.markCellsFailed(fc)
	}
	return s.readQuiet(img, region, result), nil
}

// readQuiet は**盤の有無を見て、81 マスを読み、追っている盤か確かめる**（`CaptureQuiet` の後段）。
func (s *Camera) readQuiet(img image.Image, region ikkyoku.Region, result Shot) Shot {
	// ⚠️ **盤の有無で落とす**（2026-09-18）。**大盤（解説用）が映っている
	// あいだは画素が動き続ける**ので、上のふるいは素通りする ——
	// そのままでは別の盤を読んでから捨てることになる。
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
	// 設定が有効なら、読む前にエンジンの優先度を合わせる（追っているあいだに起きたエンジンも下げる）。
	if s.BeforeRead != nil {
		s.BeforeRead()
	}
	// ⚠️ **認識に失敗しても成功として返す**（設計原則3）。呼び出し側は
	// 盤面が空なら見送るだけで、**追従そのものは続く。**
	started := time.Now()
	board, err := recognize.FromImage(img)
	// **かかった時間を毎回出す**（2026-10-07）。エンジンと CPU を取り合うと 2 秒強が 8 秒に延びた。
	log.Follow.Info("81 マスを読みました（追跡）", "ms", time.Since(started).Milliseconds())
	if err != nil {
		result.RecognizeError = err.Error()
		return result
	}
	result.SFEN = board.SFEN
	result.Confidence = board.Confidence
	result.Debug = board.Debug
	result.CellConfidence = recognize.CellConfidence(board.Debug)
	result.CellHidden = recognize.CellHidden(board.Debug)
	// 速い経路のマス割りを控える（`celldiff.go`）。
	s.noteCellRects(img, board.Debug)

	// ⚠️ **追っている盤かどうかを見る**（2026-09-15）。中継には**大盤**（解説用）が
	// 映り、あちらは**将棋の局面としては矛盾しない**ので盤面だけでは弾けない ——
	// 解説が本譜から 1 手の変化を並べていたら**そのまま棋譜に足してしまう**。
	s.mu.Lock()
	anchor, has := s.fol.boardAnchor, s.fol.boardAnchor.Board.Dx() > 0
	moved := has && s.fol.anchorRegion != region
	s.mu.Unlock()
	if has {
		if got, ok := recognize.SignatureOf(board.Debug); ok {
			// ⚠️ **枠を動かしたらマスタを取り直す**（2026-09-15 に実機で踏んだ）。
			// 記述子は**画像の中の座標**なので、枠が動けば盤は同じでも必ず食い違い、
			// **そこから全部見送られる**。**枠を動かすのは狙いを直す操作。**
			if moved {
				s.mu.Lock()
				s.fol.boardAnchor, s.fol.anchorRegion = got, region
				// ⚠️ **比べる相手も捨てること**（画像の中の座標が変わったので、もう比べられない）。
				s.setCellBaseLocked(nil)
				s.mu.Unlock()
				log.Follow.Info("枠が動いたので追う盤を取り直しました", "board", got.Board)
			} else if same, why := anchor.Matches(got); !same {
				result.OffBoard, result.OffBoardReason = true, why
				log.Follow.Info("別の盤と判断しました", "reason", why,
					"master", anchor.Board, "got", got.Board,
					"colorGap", anchor.ColorGap(got))
			}
		}
	}
	s.noteQuiet(result)
	return result
}
