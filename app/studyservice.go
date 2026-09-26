package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/ikkyoku/analyze"
	"github.com/ShinteLab/ikkyoku/legal"
	"github.com/ShinteLab/ikkyoku/position"
)

// StudyService は**確定した局面**を持つ Service（解析タブ）。
//
// ⚠️ **PositionService とは別の局面を持つ。同じものを 2 つの意味で使わない。**
//
//	PositionService … 訂正タブ。認識の誤りを直す面。**合法性を問わない**自由編集で、
//	                  手番も駒台の先後も未決でよい。**画像に見えたとおりの盤**
//	StudyService    … 解析タブ。手を選んで局面を進める面。**確定した局面が根**で、
//	                  ここから先は合法手だけを辿る（手を進める UI は次段）
//
// 受け渡しは `Adopt`（訂正タブの「この局面を解析する」）の 1 か所だけ。**写しを取る**
// ので、採ったあとに訂正タブ側をいじってもこちらは動かない（動くと、解析結果が
// どの局面の値なのか分からなくなる）。
//
// **戻れる。** 訂正タブへ戻って直し、もう一度 `Adopt` すればよい。そのとき前の
// 手順と解析結果は捨てる（別の局面の話になるため）。捨てるのはフロントではなく
// ここが `Adopt` のたびに根ごと入れ替えることで担保する。
//
// **永続化しない**（PositionService と同じ。2026-08-07 決定）。検討セッションは
// メモリ上に持つだけで、保存は明示的なエクスポート。
//
// **手順はここに乗る**（2026-08-11）。`position.Study` が「根 + 指した手の並び」を
// 持ち、合法手は `ikkyoku/legal`（engine）が出す。⚠️ **`ikkyoku → engine` の Go 依存が
// ここで戻るが、解析だけは USI 経由のまま**にすること（棋力の問題を設定の問題にした
// 意味が無くなる）。**用途が違う** —— こちらは手の検証であって解析ではない。
//
// **手順は木**（2026-08-13）。戻って別の手を指すと**枝が生える**（前の手順は消えない）。
// 木の形の最終形は `core/kifu` が持てるようになってから決める（`position` に仮置き）。
type StudyService struct {
	logger *slog.Logger
	// src は訂正タブの局面。**読むのは Adopt の瞬間だけ。**
	src *PositionService

	// Emit は**局面が変わったこと**をフロントへ知らせる口（`study:changed`）。
	// 形は `AnalyzeService.Emit` と同じで、`main.go` が起動時に 1 度だけ入れる。
	//
	// ⚠️ **nil でも動くこと**（設計原則3）。イベントを捨てても、**呼んだ窓は
	// 戻り値で描ける**ので今までどおり動く。効かなくなるのは**別の窓との連動**だけ。
	Emit EventEmitter

	mu sync.Mutex
	// study は確定した局面を根にした検討（手順を含む）。まだ採っていなければ nil。
	//
	// ⚠️ **局面を単体で持たない。** 「今の局面」は根 + 手順から組み立てるもので、
	// 別に持つと手順とずれる（どちらが本当か分からなくなる）。
	study *position.Study
	// game は対局の素性（対局者・棋戦・日時）。**棋譜を読んだときだけ埋まる。**
	//
	// ⚠️ **ikkyoku で対局者フィールドを定義し直さないこと**（`position.Game` は
	// `core/kifu.Document` のエイリアス）。今使っているのは対局者名だけだが、
	// 棋戦名や日時が要るようになってもここから足せる。
	//
	// ⚠️ **根を入れ替えたら捨てること。** 撮った局面にも新規対局にも対局者は
	// 居ないので、前の棋譜の名前が残っていると**別の対局の名前を今の盤に出す**。
	game position.Game
	// sourceURL は棋譜の取得元（**URL から読んだときだけ埋まる**。2026-08-13）。
	//
	// **再取得できることが分かるのはここだけ**なので、`StudyState` に載せて
	// 手順の見出しに「再読み込み」を出す鍵にする。⚠️ **根を入れ替えたら捨てること**
	// —— 撮った局面にも貼り付けた棋譜にも新規対局にも取得元は無いので、
	// 残っていると**別の対局の URL で今の手順を上書きできてしまう**。
	sourceURL string
	// gameID は棚（`kicho`）の棋譜 id（**棚から開いたときだけ埋まる**。2026-09-16）。
	//
	// **控えをこの棋譜に紐づける鍵**（`StudyRecord.GameID`）。次に同じ棋譜を
	// 棚から開いたとき、**前の検討がそのまま戻る**のはこれがあるから。
	//
	// ⚠️ **`sourceURL` とは別物。** あちらは**取り直す先**（URL）で、こちらは
	// **棚のどの行か**。URL から取っただけで棚に入れていない棋譜は
	// `sourceURL` だけが埋まる。
	//
	// ⚠️ **根を入れ替えたら捨てること**（`sourceURL` と同じ）。撮った局面にも
	// 新規対局にも棚の行は無いので、残っていると**別の対局の控えを上書きする**。
	gameID string
	// kifuFollow は**取得元の URL を自動で取り直しているか**（2026-09-26）。
	//
	// ⚠️ **持っているのは入切だけ。** 取り直す間隔を刻むのはフロントの持ち主の窓
	// （連続解析が走っているかを知っているのがそちらだけなので）。ここに置くのは
	// **どの窓でも同じ入切が見える**ようにするため（`StudyState.KifuFollow`）。
	//
	// ⚠️ **`sourceURL` を捨てるところでは必ず一緒に切ること** —— 残ると、
	// 取り直す先が無いのに「自動更新中」と出る。⚠️ **設定ファイルにも控えにも
	// 持たない**（その場かぎり。再起動や前の検討を開き直したら切れている）。
	kifuFollow bool
	// sourceKey は**この検討の出どころの鍵**（2026-09-26。`studykey.go`）。
	//
	// 中継カードなら取得元、貼り付けなら本文のハッシュ。**棚に入っていない検討を、
	// あとから同じ棋譜に結び直す**ための手掛かりで、控えに一緒に書く。
	// ⚠️ **`sourceURL` を捨てるところでは一緒に捨てること**（撮った局面にも
	// 新規対局にも出どころは無い。残ると**別の棋譜の控えと結ばれる**）。
	sourceKey string
	// rev は状態の版（**変えるたびに 1 つ進む**。2026-09-08）。
	//
	// **どの窓がどこまで描いたかを揃えるための番号。** `study:changed` は
	// メソッドの戻り値とは**別の経路**で届くので、**順番が入れ替わりうる**
	// （続けて手を辿ると、古い局面のイベントが後から届く）。受け取る側は
	// 「**既に描いた版より新しいときだけ描く**」で弾ける。
	//
	// ⚠️ **画面に出す数ではない。** 手数とは何の関係も無い。
	rev int
	// session は今の検討の id（**控え 1 ファイルの鍵**。2026-09-16）。
	//
	// ⚠️ **根を入れ替えたら振り直すこと**（`newSessionLocked`）。別の対局の話に
	// なったのに同じ id で書くと、**前の検討の控えが上書きされる。**
	//
	// ⚠️ **棚（`kicho`）の id とは無関係。** 中継を撮っている最中は棚に行が
	// 作れないので、**同一性を棋譜から借りない**（`studyrecord.go`）。
	session string
	// dirty は**控えを書き直す必要がある**ことを知らせる口（`StudyStore` が入れる）。
	//
	// ⚠️ **nil でも動くこと**（設計原則3）。控えはおまけで、**無いと解析が
	// できないものにしない。**
	// ⚠️ **ロックを持ったまま呼ぶので、ここで待たないこと**（詰まる）。
	dirty func()
	// save は**今すぐ控えを書かせる**口（`StudyStore` が入れる。2026-09-16）。
	//
	// ⚠️ **セッションを入れ替える前に呼ぶこと**（`saveNow`）—— 間引きの幅
	// （3 秒）のあいだに入れ替えると、**直前までの手と評価値が前のセッションから
	// 落ちる**。⚠️ **nil でも動くこと**（設計原則3）。
	save func()
	// resume は**出どころの鍵が同じ控え**を今の検討と入れ替える口（`StudyStore` が入れる。
	// 2026-09-26。`studykey.go`）。戻せなければ false。
	//
	// ⚠️ **nil でも動くこと**（設計原則3）。無ければ棋譜を普通に読み込むだけ。
	// ⚠️ **ロックを持ったまま呼ばないこと**（向こうが `adoptSession` でロックを取る）。
	resume func(key string) (StudyState, bool)
	// evals は手順の 1 手ごとの評価値（評価値グラフ。2026-08-12）。
	//
	// ⚠️ **置き場所がここなのは、記録が手順に紐づくから。** 節点を消す操作
	// （`DropFrom`・根の入れ替え）を知っているのはここだけなので、
	// **どこまでを捨てるかの判断もここに置く**（`AnalyzeService` は記録を頼むだけ）。
	evals evalStore
}

func NewStudyService(logger *slog.Logger, src *PositionService) *StudyService {
	return &StudyService{logger: logger, src: src}
}

// StudyState は解析タブが描くのに要るもの一式。
//
// **EditState とは別の型にしてある。** 見た目が似ていても中身の意味が違う
// （あちらは「直している最中の局面」、こちらは「確定した局面」）ので、
// 片方の型をもう片方に流用しない。訂正の道具（在庫の負の値・足りない駒）は
// ここには要らない。
type StudyState struct {
	// Loaded はまだ何も採っていなければ false。
	Loaded bool `json:"loaded"`
	// BoardSFEN は盤面部分の SFEN。盤を描くのに使う。
	BoardSFEN string `json:"boardSfen"`
	// SFEN は局面全体の SFEN。**確定しているので必ず埋まる**
	// （Adopt が確定していない局面を断るため）。
	SFEN string `json:"sfen"`
	// Turn は 1=先手番 / 2=後手番（0 は Loaded == false のときだけ）。
	Turn      int    `json:"turn"`
	TurnLabel string `json:"turnLabel"`
	// MoveNumber は SFEN の数え方の手数（0 は不明）。
	MoveNumber int `json:"moveNumber"`
	// Hands は駒台。**訂正タブと違い未決は残っていない**（確定した局面なので）。
	Hands []position.Stock `json:"hands"`
	// Warnings は局面として成立していない点。**確定を止めはしない**
	// （詰将棋のような「論理的におかしくても正しい」局面があるため。設計原則3）。
	// 解析タブでも出しておくのは、変な評価値が出たときの手掛かりになるから。
	Warnings []string `json:"warnings"`
	// Black / White は対局者名（**棋譜を読んだときだけ埋まる。無ければ空**）。
	//
	// 勝率バーの左右に出す。⚠️ **空のときに「先手」「後手」で埋めないこと** ——
	// 名前が分かっているのか、既定を出しているだけなのかが区別できなくなる。
	// **既定の文言は表示側が持つ。**
	Black string `json:"black"`
	White string `json:"white"`
	// Handicap は手合割（**平手なら空**。2026-09-12）。
	//
	// **駒落ちかどうかを画面が知るための 1 つの口。** 対局者名の既定を
	// 「先手／後手」から「下手／上手」に替えるのに使う。
	// ⚠️ **これで局面の解釈を変えないこと** —— 落ちている駒も手番も
	// 根の SFEN が既に持っている（駒落ちの初手は上手＝後手）。
	// ⚠️ **平手を "平手" と書いて埋めない** —— 「駒落ちか」の判定が
	// 文字列比較 1 つで済まなくなる。
	Handicap string `json:"handicap"`
	// SourceURL は棋譜の取得元（**URL から読んだときだけ埋まる**。2026-08-13）。
	//
	// **空でなければ「再読み込み」を出す**（`ReloadKifu`）。⚠️ **フロントで
	// URL 欄の中身から判断しないこと** —— 入力タブの欄はいつでも書き換えられるので、
	// **今の手順がどこから来たか**とは別物になる。
	SourceURL string `json:"sourceUrl"`
	// GameID は棚（`kicho`）の棋譜 id（**棚と結んでいるときだけ**。2026-09-16）。
	//
	// **棋譜タブのどの行とこの検討が結ばれているか。** 棋譜タブから開いたとき・
	// 同じ出どころの棋譜を棋譜タブに入れたとき（`linkStudy`）に付く。
	// ⚠️ **空が普通**（撮った 1 局面・貼り付け・URL から取っただけ）。
	// （2026-09-26 まで「棚に登録する」のアイコンの出し分けに使っていた。今は画面の
	// 出し分けには使っていない）
	//
	// ⚠️ **id を画面に出すためのものではない**（人が読む値ではない）。
	GameID string `json:"gameId"`
	// KifuFollow は**取得元の URL を自動で取り直しているか**（2026-09-26）。
	//
	// ⚠️ **どの窓でも同じ入切を見せるための口。** 取り直す間隔を刻むのは
	// フロントの持ち主の窓（`FollowKifu` を呼ぶ）。`SourceURL` が空なら必ず偽。
	KifuFollow bool `json:"kifuFollow"`

	// RootSFEN は根の局面（採ったときの局面）。**エンジンに渡すのはこれ + Played。**
	RootSFEN string `json:"rootSfen"`
	// First は根までに指された手数（＝**棋譜の手数の起点**）。
	//
	// ⚠️ **`Move.Number` は根からの手数で、棋譜の手数ではない**（あちらは `GoTo` に
	// 渡す値も兼ねているので、起点をずらせない）。撮った 41 手目の局面を根にすると
	// `Move.Number` は 1 から始まるので、**画面に手数として出すときは First を足す**。
	// 評価値グラフの横軸（`EvalGraph.First`）と**同じ値**にすること。
	First int `json:"first"`
	// Nodes は手順ツリーの全部の手（**表示順**。日本語表記つき）。
	//
	// ⚠️ **一直線ではない**（2026-08-13）。`Depth` が字下げ、`Main` が本譜側。
	// 並びは「その手 → 枝 → 本譜の続き」で、**ある手の子孫は必ずその直後に固まる**。
	Nodes []position.Node `json:"nodes"`
	// CurrentID は今見ている節点（0 なら根）。**手順リストの現在位置。**
	CurrentID int `json:"currentId"`
	// MainTip は**本譜の先端**の節点（手が 1 つも無ければ 0 ＝ 根）。
	//
	// **中継を追うときの繋ぎ先**（`FollowProbe`）で、⚠️ **`CurrentID` とは別物** ——
	// ユーザーが枝の途中を読んでいるのは普通にある。
	// ⚠️ **`Nodes` を走査して `Main` の最後を探さないこと**（フロントで同じ判定を
	// 書くと、「分岐にする」で下げた手の扱いが割れる）。
	MainTip int `json:"mainTip"`
	// Line は今の経路の節点 id（**ply 番目がその手数の節点**。先頭は根の 0）。
	//
	// ⚠️ **連続解析が次に進む先はここから取る。** 枝に居るならその枝を辿る。
	Line []int `json:"line"`
	// Ply は今どこまで進めて見ているか（0 なら根）。
	//
	// **len(Line)-1 より小さいことがある**（戻って見ている状態）。
	Ply int `json:"ply"`
	// Played は今の局面までの手（USI）。**解析に渡す moves そのもの。**
	Played []string `json:"played"`
	// Legal は今の局面で指せる手。**駒をクリックしたときに光らせる先。**
	//
	// ⚠️ **同じ移動先に成りと不成の 2 つが並ぶことがある。** どちらを指すかは
	// 人が決めることなので、**Go 側で片方に丸めないこと**（UI が聞く）。
	Legal []legal.Move `json:"legal"`
	// LegalError は合法手を出せなかった理由（出せたなら空）。
	//
	// ⚠️ **これはエラーにしない**（設計原則3）。玉の欠けた局面などでは手を進められ
	// ないが、盤は描けるし解析タブに居ることもできる。**手が指せなくなるだけ。**
	LegalError string `json:"legalError"`

	// Rev は状態の版（**変わるたびに 1 つ進む**。2026-09-08）。
	//
	// ⚠️ **画面に出すものではない。** `study:changed` は戻り値と別の経路で届くので、
	// **自分が既に描いたもの**と**遅れて届いた古いもの**をこれで弾く
	// （フロントは「既に描いた版より新しいときだけ描く」）。
	Rev int `json:"rev"`
}

// Adopt は訂正タブの局面を採って、解析タブの根にする。
//
// ⚠️ **確定していない局面は採らない。** 手番か駒台の先後が未決だと
// `position.Position.SFEN()` がエラーを返すので、そのまま理由として返す。
// ここで先手に倒すと、決めていない手番でエンジンが読むことになる（設計原則5）。
// **これは警告ではなくエラー** —— 決めてもらう以外に手が無い。
func (s *StudyService) Adopt() (st StudyState, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO なので、
	// ここで登録しておけば `s.mu.Unlock()` の**後**に走る）。
	defer func() { s.publish(st, err) }()
	// ⚠️ **入れ替える前に控えを書かせること**（2026-09-16）。**根を入れ替えると
	// 別のセッションになる**ので、間引きの幅（3 秒）のあいだに入れ替えると
	// **直前までの手と評価値が前のセッションから落ちる。**
	// ⚠️ **ロックを取る前に呼ぶこと**（向こうがロックを取る）。
	s.saveNow()
	// ⚠️ **ここで向きが直る。** 撮った画像が後手目線なら、盤・先後・駒台・手番を
	// まとめて 180 度回した写しが返る（`PositionService.adoptPosition`）。
	// 訂正タブ側は撮った向きのままで、**回るのはこの 1 回だけ**。
	p, rotated := s.src.adoptPosition()
	if p == nil {
		return s.State(), fmt.Errorf("まだ局面がありません")
	}
	// 確定しているかの判定は SFEN が組み上がるかどうかそのもの。
	if _, err := p.SFEN(); err != nil {
		return s.State(), err
	}

	s.mu.Lock()
	// **根ごと入れ替える。** 前の手順と（呼び出し側が消す）解析結果は捨てる ——
	// 別の局面の話になるので、残すと「どちらの局面の手順か」が分からなくなる。
	s.study = position.NewStudy(p)
	// **対局者も捨てる**（撮った局面に対局者は付いていない）。
	s.game = position.Game{}
	// **取得元も捨てる**（撮った局面に取得元は無い）。
	s.sourceURL = ""
	s.kifuFollow = false
	s.sourceKey = ""
	// **棚の行も捨てる**（撮った局面は棚のどの棋譜でもない）。
	s.gameID = ""
	// **評価値グラフも捨てる。** 別の局面から始まる別の手順なので、前の折れ線を
	// 残すと**違う対局の評価値が同じ横軸に並ぶ。**
	s.newSessionLocked()
	st = s.changed()
	s.mu.Unlock()

	s.logger.Info("局面を解析タブへ採りました", "sfen", st.SFEN, "rotated", rotated)
	return st, nil
}

// KifuLoad は棋譜を読み込んだ結果（入力タブの「棋譜を貼り付ける」）。
type KifuLoad struct {
	// State は読み込んだあとの解析タブの状態。**そのまま showStudy に渡す。**
	State StudyState `json:"state"`
	// Summary は「何手読み込んだか」の 1 行（対局者・棋戦が分かれば添える）。
	Summary string `json:"summary"`
	// Note は全部は載らなかった理由（載ったなら空）。
	//
	// ⚠️ **これはエラーではない。** 途中で止まっても、そこまでの手順は正しいので
	// 解析できる（設計原則3）。**フロントで空でないことをエラー扱いしないこと。**
	Note string `json:"note"`
	// Added は取り直しで**本譜に増えた手の数**（`ReloadKifu` / `FollowKifu` だけが埋める）。
	//
	// 自動更新で**手が来たかどうか**を知らせるのに使う（来ていないときは黙る）。
	Added int `json:"added"`
	// Followed は自動更新で**先端へ付いていったか**（`FollowKifu` だけが立てる）。
	Followed bool `json:"followed"`
}

// LoadKifu は KIF テキストを読んで解析タブの根と手順にする。
//
// ⚠️ **訂正タブを経由しない 2 つめの入口。** 「受け渡しは Adopt の 1 か所だけ」は
// **訂正タブとの受け渡し**の話で、入力の口が増えること自体は想定どおり
// （画像は認識を通るので訂正タブへ、KIF は既に確定しているので直接ここへ）。
// **`PositionService` は触らない** —— 撮った局面を消してしまうと、
// 貼り付けたのが誤りだったときに戻る先が無くなる。
//
// **指し手は全て載せるが、見ているのは開始局面**（2026-08-18）。手順はそのまま
// 手順のリストから辿れる。⚠️ **最終手に置かないこと** —— 棋譜を読むのは
// 「この対局を初手から解析する」ためで、連続解析の始点も**今見ている手**なので、
// 最終手に置くと**押す前に必ず開始局面まで戻る操作が要る**（先まで見たいなら
// 手順リストか評価値グラフで飛べばよい）。
//
// ⚠️ **そのぶん `ReloadKifu` は伸びた先へ進まない**（あちらの「最後の手を見ていたら
// 進める」の条件から外れる）。中継を追うなら**一度最終手を見ておくこと**で、
// これは「見ている位置を保つ」という取り直しの規約どおり。
func (s *StudyService) LoadKifu(text string) (KifuLoad, error) {
	// ⚠️ **同じ本文を前にも解析していたら、その続きから開く**（2026-09-26）。
	// 鍵は本文のハッシュで、**棋譜タブに登録したときに結び直す手掛かり**にもなる。
	key := pasteKeyOf(text)
	if load, ok := s.resumeByKey(key, text); ok {
		return load, nil
	}
	// ⚠️ **貼り付けには取り直す先も棚の行も無い**ので、どちらも空。
	return s.loadKifuFrom(text, "", "", key)
}

// resumeByKey は**出どころの鍵が同じ控え**があれば、それを開いてから棋譜を据え直す
// （2026-09-26。中継カードの「解析する」と貼り付けの「解析する」）。
//
// **中継は数時間かけて完成し、そのあいだに別の棋譜を解析するのが普通。**
// 控えは起動時に一番新しいものしか戻らないので、これが無いと開き直すたびに
// **解析がゼロから**になる（追いつくまで数分かかる）。
//
// ⚠️ **据え直しは `graftKifu`**（棋譜タブから開くときと同じ規約：枝と評価値を
// 捨てない・見ている位置を動かさない・本譜が入れ替わったら断る）。
// ⚠️ **据え直せなくても開けたことは成功**（設計原則3）。
func (s *StudyService) resumeByKey(key, text string) (KifuLoad, bool) {
	s.mu.Lock()
	resume := s.resume
	s.mu.Unlock()
	if resume == nil || key == "" {
		return KifuLoad{}, false
	}
	st, ok := resume(key)
	if !ok {
		return KifuLoad{}, false
	}
	load, err := s.graftKifu(text)
	if err != nil {
		s.logger.Warn("前の検討に棋譜を据え直せませんでした", "key", key, "error", err)
		return KifuLoad{State: st, Summary: "前の検討の続きを開きました",
			Note: "棋譜は読めませんでした"}, true
	}
	load.Summary = "前の検討の続きを開きました：" + load.Summary
	return load, true
}

// linkGame は**出どころの鍵が同じなら**、今の検討に棚の棋譜 id を結ぶ（2026-09-26）。
// 結んだなら true。
//
// ⚠️ **既に結んでいるなら触らない**（別の行へ移すと、前の行から開けなくなる）。
// ⚠️ **ロックの外で publish すること**（切り離した窓にも伝わる）。
func (s *StudyService) linkGame(key, gameID string) bool {
	if key == "" || gameID == "" {
		return false
	}
	s.mu.Lock()
	if s.study == nil || s.sourceKey != key || s.gameID != "" {
		s.mu.Unlock()
		return false
	}
	s.gameID = gameID
	// ⚠️ **`changed()` を通すこと** —— 控えを書き直させる（`markDirty`）。
	st := s.changed()
	s.mu.Unlock()
	s.publish(st, nil)
	s.logger.Info("今の検討を棋譜タブの棋譜に結びました", "gameId", gameID)
	return true
}

// loadKifuFrom は KIF テキストを読み込んで、**取得元の URL も一緒に覚える**。
//
// ⚠️ **取得元まで含めて 1 回のロックで入れ替えること**（2026-09-08）。以前は
// `LoadKifu` が読み込んでから取得元を入れ直していたが、**`study:changed` を
// 出すようになると「取得元が空の状態」が 1 回ぶん外へ漏れる**
// （別の窓で再読み込みのアイコンが出たり消えたりする）。
//
// **`sourceURL` は手順の見出しの「再読み込み」の鍵**（これが空だとアイコンが出ない）。
// ⚠️ **公開しない** —— フロントから任意の URL を紐付けられると、
// 「今の手順がどこから来たか」が実際の取得元と食い違いうる。
// gameID は棚（`kicho`）の棋譜 id。**棚から開いた入口だけが埋める**
// （控えをこの棋譜に紐づける鍵。2026-09-16）。
func (s *StudyService) loadKifuFrom(text, sourceURL, gameID, key string) (load KifuLoad, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO なので、
	// ここで登録しておけば `s.mu.Unlock()` の**後**に走る）。
	defer func() { s.publish(load.State, err) }()

	// ⚠️ **入れ替える前に控えを書かせること**（2026-09-16）。**根を入れ替えると
	// 別のセッションになる**ので、間引きの幅（3 秒）のあいだに入れ替えると
	// **直前までの手と評価値が前のセッションから落ちる。**
	// ⚠️ **ロックを取る前に呼ぶこと**（向こうがロックを取る）。
	s.saveNow()

	study, k, err := position.FromKIF(text)
	if err != nil {
		return KifuLoad{State: s.State()}, err
	}

	s.mu.Lock()
	// **根ごと入れ替える**（Adopt と同じ）。前の手順と解析結果は別の局面の話になる。
	s.study = study
	// **見るのは開始局面**（`FromKIF` は最終手まで進めた状態で返す）。
	// ⚠️ **手順は 1 手も消さない** —— `GoTo` は見る位置を動かすだけ。
	_ = s.study.GoTo(0)
	// **対局者はここでだけ埋まる**（勝率バーの左右に出す）。
	s.game = k.Game
	// **取得元が分かっている入口だけが埋める**（貼り付けは空）。
	s.sourceURL = strings.TrimSpace(sourceURL)
	// ⚠️ **読み込んだだけでは追わない**（自動更新は人が入れるもの）。
	s.kifuFollow = false
	// **棚から開いた入口だけが埋める**（貼り付けも URL の取得だけも空）。
	s.gameID = strings.TrimSpace(gameID)
	// **出どころの鍵**（控えを同じ棋譜に結び直す手掛かり。`studykey.go`）。
	s.sourceKey = key
	s.newSessionLocked()
	st := s.changed()
	s.mu.Unlock()

	s.logger.Info("棋譜を読み込みました",
		"moves", k.Loaded, "total", k.Total, "note", k.Note, "url", s.sourceURL)
	return KifuLoad{State: st, Summary: kifuSummary(k), Note: k.Note}, nil
}

// AddLine は候補手を枝として足した結果（解析タブの候補手の右クリック）。
type AddLine struct {
	// State は足したあとの解析タブの状態。**今見ている局面は動いていない。**
	State StudyState `json:"state"`
	// FirstID は最初に生えた節点（**0 なら 1 手も増えていない**＝全部が既にあった）。
	//
	// ⚠️ **0 を失敗として扱わないこと。** 候補が本譜と同じ手順なら 1 手も
	// 増えないのが正しい（そのときは既にある手順を辿るだけ）。
	FirstID int `json:"firstId"`
	// Added は新しく生えた手数。
	Added int `json:"added"`
	// Note は全部は足せなかった理由（足せたなら空）。**エラーではない**（設計原則3）。
	Note string `json:"note"`
}

// kifuFetchTimeout は棋譜を取りに行くときの上限。
//
// 棋譜 1 局は数十 KB なので、これで足りないのは相手が居ないときだけ。
// **長くしないこと**（返らない URL を打ったときに画面が固まる）。
const kifuFetchTimeout = 20 * time.Second

// LoadKifuURL は URL から棋譜を取ってきて読み込む（`LoadKifu` の口違い）。
//
// ⚠️ **画面からは呼んでいない**（2026-09-12）。入力タブの URL の欄は**「取得」
// だけ**になり、**解析はカードの「解析する」**（`KifuService.SendToStudyGame`）が
// 担う。**残してあるのは `ReloadKifu` と同じ取得の口だから**で、ここを消すと
// 「URL から 1 手も経由せず読む」という口が無くなる。
//
// ⚠️ **画面にボタンを戻すなら、カードとどちらが正かを先に決めること** ——
// 同じ URL に対して「解析する」が 2 か所にある状態にはしない。
//
// **取ってくるのは `fetchKIF`**（中身は `kicho/scrape`。文字コードの判別も
// HTML から .kif を辿るのもあちら。日本将棋連盟の棋譜中継は Shift_JIS）。
// ここは繋ぐだけで、**取得も KIF の解釈もここに書かない。**
func (s *StudyService) LoadKifuURL(rawURL string) (KifuLoad, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kifuFetchTimeout)
	defer cancel()

	got, err := fetchKIF(ctx, rawURL)
	if err != nil {
		return KifuLoad{State: s.State()}, err
	}
	s.logger.Info("棋譜を取得しました", "url", got.URL, "encoding", got.Encoding, "bytes", len(got.Text))

	// ⚠️ **棚の行は無い**（URL から取っただけで棚には入っていない）。
	load, err := s.loadKifuFrom(got.Text, got.URL, "", "")
	if err != nil {
		return load, err
	}
	// **何を読んだかを出す。** URL は打ち間違えても「棋譜が読めません」としか
	// 出ないことがあるので、**取れた側の事実**（どこから・何文字コードで）を見せる。
	load.Summary += fmt.Sprintf("（%s）", got.Encoding)
	return load, nil
}

// ReloadKifu は取得元の URL から棋譜を取り直して、**URL の側を正**にする
// （解析タブの手順の見出しの「再読み込み」。2026-08-13）。
//
// 中継の .kif は 1 手進むたびに書き換わるので、**同じ URL をもう一度読んで
// 手順を最新にする**のがこの口。⚠️ **入力タブから読み直させないこと** ——
// あちらを通ると `LoadKifu` が根ごと入れ替えるので、**評価値が全部消える。**
//
// # 何を残して何を捨てるか
//
// **食い違ったところから先だけを差し替える。**
//
//	根が違う                … 別の対局なので全部捨てる（`LoadKifu` と同じ）
//	手順の頭が一致している    … 一致している範囲の**解析結果はそのまま残す**
//	途中から食い違う          … **その先は捨てて URL のものにする**（URL が正）
//
// ⚠️ **「捨てる」で済んでいるのは分岐ツリーがまだ無いから。** 木が入ったら、
// ここは**捨てるのではなく別の枝として残す**ことになる（`TODO.md` の「本譜のロック」。
// 本譜は URL が更新し続け、分岐は手入力、という使い方が一番よくある）。
// **そのときに直す場所はここ 1 か所**にしてある。
//
// ⚠️ **食い違いが無ければ `dropAfter` を呼ばないこと。** あれは世代（epoch）を
// 進めるので、**走っている解析の途中経過が捨てられる**（1 手進むたびに
// 再読み込みする使い方では、毎回それが起きる）。
func (s *StudyService) ReloadKifu() (KifuLoad, error) {
	return s.reloadKifu(false)
}

// FollowKifu は**自動更新の 1 回ぶん**（2026-09-26）。取り直しは `ReloadKifu` と
// 同じで、違うのは**本譜の先端を見ていたなら、伸びた先の先端へ付いていく**ことだけ
// （`tail -f` と同じ。中継を画像で追う `FollowAuto` と同じ約束）。
//
// ⚠️ **先端を見ていないなら動かさない。** 戻って検討している最中に飛ばされると、
// **中継が進むたびに読んでいた枝から引き剥がされる**（`TODO.md`「本譜のロック」の
// 「今見ている場所を勝手に動かさない」）。
//
// ⚠️ **`advance` を偽にして呼ぶのは連続解析が走っているとき**（フロントが決める）。
// あちらが局面を 1 手ずつ動かしているので、横から動かすと**解析を打ち切り合う**。
// 取り直すだけなら見ている位置は動かないので、手順は伸ばしておく。
//
// ⚠️ **終局まで載ったら自動更新を切る**（投了のあとは伸びない。サーバを叩き続けない）。
func (s *StudyService) FollowKifu(advance bool) (KifuLoad, error) {
	return s.reloadKifu(advance)
}

// SetKifuFollow は自動更新の入切（2026-09-26。手順の見出しの自動更新のアイコン）。
//
// ⚠️ **ここは入切を覚えるだけで、取りに行かない。** 刻むのはフロント
// （`FollowKifu` を呼ぶ）。⚠️ **URL から読んでいない局面では入れられない**
// （取り直す先が無い）。
func (s *StudyService) SetKifuFollow(on bool) (st StudyState, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO）。
	// **切り離した窓のアイコンもこれで揃う。**
	defer func() { s.publish(st, err) }()

	s.mu.Lock()
	defer s.mu.Unlock()
	if on && (s.study == nil || s.sourceURL == "") {
		return s.state(), fmt.Errorf("この局面は URL から読み込んだものではないので、自動更新できません")
	}
	if s.kifuFollow == on {
		return s.state(), nil
	}
	s.kifuFollow = on
	s.logger.Info("棋譜の自動更新", "on", on, "url", s.sourceURL)
	// ⚠️ **`changed` ではなく版だけ進める** —— 控え（`markDirty`）に書く中身は
	// 何も変わっていない。版を進めないと、`study:changed` を受けた窓が
	// 「既に描いた版」として捨てる。
	s.rev++
	return s.state(), nil
}

// reloadKifu は `ReloadKifu` と `FollowKifu` の本体。
func (s *StudyService) reloadKifu(advance bool) (load KifuLoad, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO なので、
	// ここで登録しておけば `s.mu.Unlock()` の**後**に走る）。
	defer func() { s.publish(load.State, err) }()

	s.mu.Lock()
	url := s.sourceURL
	s.mu.Unlock()
	if url == "" {
		return KifuLoad{State: s.State()},
			fmt.Errorf("この局面は URL から読み込んだものではないので、取り直せません")
	}

	// ⚠️ **据え直しでも根が違えば別のセッションになる**（`mergeReloadLocked`）ので、
	// ここでも先に書かせること。
	s.saveNow()

	ctx, cancel := context.WithTimeout(context.Background(), kifuFetchTimeout)
	defer cancel()
	got, err := fetchKIF(ctx, url)
	if err != nil {
		return KifuLoad{State: s.State()}, err
	}
	// ⚠️ **取れなかった / 読めなかったときは今の手順を壊さないこと。**
	// 組み立てが通ってから初めて入れ替える。
	study, k, err := position.FromKIF(got.Text)
	if err != nil {
		return KifuLoad{State: s.State()}, err
	}

	s.mu.Lock()
	// ⚠️ **取りに行っているあいだに別の局面へ入れ替わっていたら据えないこと**
	// （2026-09-26）。取得は数秒かかり、**自動更新は人が見ていないところで
	// 走る** —— 撮った局面を採った直後に古い取得が返ってくると、根が違うので
	// `mergeReloadLocked` が**木ごと入れ替えて、採った局面を消す。**
	if s.sourceURL != url {
		st := s.state()
		s.mu.Unlock()
		return KifuLoad{State: st},
			fmt.Errorf("取り直しているあいだに別の局面に入れ替わったので、取り直した棋譜は使いませんでした")
	}
	// **先端を見ていたか**は据え直す前に見る（据え直すと先端が動く）。
	// ⚠️ **取りに行く前ではなく、ここで見ること** —— 取っているあいだに
	// 人が手順を押していたら、そちらが今の意思。
	atTip := s.study != nil && s.study.CurrentID() == mainTipID(s.study)
	graft := s.mergeReloadLocked(study)
	s.game = k.Game
	followed := false
	if advance && s.kifuFollow && atTip && graft.Added > 0 {
		if id, _, e := s.study.MainTip(); e == nil && s.study.GoTo(id) == nil {
			followed = true
		}
	}
	stopped := false
	if s.kifuFollow && s.study.RecordEnd() != 0 {
		// **終局まで載った。** これ以上伸びないので追うのをやめる。
		s.kifuFollow = false
		stopped = true
	}
	st := s.changed()
	s.mu.Unlock()

	summary := kifuSummary(k) + fmt.Sprintf("（%s）", got.Encoding)
	note := k.Note
	var stopNote string
	if stopped {
		stopNote = "終局まで載ったので自動更新を止めました"
	}
	for _, msg := range []string{graft.Note, reloadNote(graft), stopNote} {
		if msg == "" {
			continue
		}
		if note == "" {
			note = msg
		} else {
			note += "／" + msg
		}
	}
	s.logger.Info("棋譜を取り直しました", "url", got.URL, "moves", k.Loaded,
		"kept", graft.Kept, "added", graft.Added, "movedAt", graft.MovedAt,
		"auto", advance, "followed", followed)
	return KifuLoad{State: st, Summary: summary, Note: note,
		Added: graft.Added, Followed: followed}, nil
}

// graftKifu は手元の KIF を**今の木に据え直す**（棚から前の検討を開いたとき）。
//
// ⚠️ **`ReloadKifu` との違いは取りに行かないことだけ。** 棚の行には KIF 本文が
// あるので、URL を叩く理由が無い。据え直しの規約（枝を捨てない・見ている位置を
// 動かさない・本譜が入れ替わったら断る）は**あちらと同じものを通す**。
//
// ⚠️ **公開しない** —— フロントから任意の KIF を今の木に混ぜられると、
// 「今の手順がどこから来たか」が追えなくなる。
func (s *StudyService) graftKifu(text string) (load KifuLoad, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO）。
	defer func() { s.publish(load.State, err) }()

	// ⚠️ **据え直しでも根が違えば別のセッションになる**（`mergeReloadLocked`）。
	s.saveNow()

	// ⚠️ **読めなかったときは今の手順を壊さないこと**（`ReloadKifu` と同じ）。
	study, k, err := position.FromKIF(text)
	if err != nil {
		return KifuLoad{State: s.State()}, err
	}

	s.mu.Lock()
	graft := s.mergeReloadLocked(study)
	s.game = k.Game
	st := s.changed()
	s.mu.Unlock()

	note := k.Note
	for _, msg := range []string{graft.Note, reloadNote(graft)} {
		if msg == "" {
			continue
		}
		if note == "" {
			note = msg
		} else {
			note += "／" + msg
		}
	}
	s.logger.Info("前の検討に棋譜を据え直しました",
		"moves", k.Loaded, "kept", graft.Kept, "added", graft.Added, "movedAt", graft.MovedAt)
	return KifuLoad{State: st, Summary: kifuSummary(k), Note: note}, nil
}

// reloadNote は取り直しで**本譜が入れ替わった**ことの断り（入れ替わっていなければ空）。
//
// **黙って据え替えない。** 自分で指していた手が本譜から外れるので、どこからかを出す。
//
// ⚠️ **手が増えただけのときは何も言わない。** 中継が進めば毎回増えるので、
// 毎回断ると読み飛ばされる（**本当に読んでほしいのは押しのけたとき**）。
func reloadNote(g position.GraftResult) string {
	if g.MovedAt == 0 {
		return ""
	}
	return fmt.Sprintf("%d手目から棋譜のものにしました（それまでの手順は枝として残ります）",
		g.MovedAt)
}

// mergeReloadLocked は取り直した棋譜を**今の木に据え直す**（`position.Study.Graft`）。
//
// ⚠️ **木ごと入れ替えないこと**（2026-08-13。枝が入るまではそうしていた）。
// 入れ替えると**ユーザーが足した検討が中継の 1 手ごとに消える** ——
// `TODO.md`「本譜のロック」の「本譜は URL が更新し続け、分岐は手入力」が
// 一番よくある使い方なので、ここが消すと使い物にならない。
//
// **ロックを取った状態で呼ぶこと。**
func (s *StudyService) mergeReloadLocked(next *position.Study) position.GraftResult {
	// ⚠️ **組み上がらなかった側は「違う」に倒す**（両方空を一致と読まないこと）。
	if s.study == nil || rootSFEN(s.study) == "" || rootSFEN(s.study) != rootSFEN(next) {
		// **別の対局**（あるいは初回）。木ごと入れ替えて折れ線も捨てる。
		s.study = next
		s.newSessionLocked()
		return position.GraftResult{}
	}
	// **見ている位置は動かさない**（2026-08-19。以前は「最後の手を見ていたなら
	// 伸びた先の最後まで進める」だった）。⚠️ **戻さないこと** —— あの判定は
	// **深さ**（`Ply() >= len(MainLine())`）でしか「最後の手か」を見ておらず、
	// **枝は本譜より深くなり得る**ので、**枝を見ている最中に取り直すと本譜の
	// 終わりへ飛ばされていた**（実測で踏んだ）。据え直しでは節点が 1 つも
	// 消えないので、**取り直す前に選んでいた手はそのまま生きている。**
	//
	// ⚠️ **そのぶん中継を追うときは、取り直したあと自分で最終手へ移ること**
	// （十字キーの下か手順リスト）。**「今の局面が見たい」を勝手に決めない**
	// ——どこを見ているかはユーザーが選んだ状態で、取り直しはあくまで
	// **URL の側を正にする**操作。
	got := s.study.Graft(next.MainLine())
	// **終局まで載ったなら、そこが対局の終わり**（2026-09-16）。
	//
	// ⚠️ **据え直した側の id は使えない**（別の木の番号）。本譜の先端が
	// そのまま対局の終わりなので、こちらの木で引き直す。
	// ⚠️ **終わっていなければ触らないこと** —— 中継は 1 手進むたびにここを
	// 通るので、**毎回 0 に戻すと投了図以下の印が出たり消えたりする。**
	if next.RecordEnd() != 0 {
		if id, _, err := s.study.MainTip(); err == nil {
			s.study.SetRecordEnd(id)
		}
	}
	return got
}

// currentGameID は今の検討が結んでいる棚の棋譜 id（結んでいなければ空）。
//
// ⚠️ **公開しない**（Service の公開メソッドはフロントの API になる）。
// 画面へ出したいなら `StudyState` に載せること。
func (s *StudyService) currentGameID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gameID
}

// currentSession は今の検討の控えの id（**控えの書き直しが今の検討とぶつからないか**
// を見るため。公開しない）。
func (s *StudyService) currentSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

// mainTipID は本譜の先端の節点（組み立てられなければ根）。**画面へ出す用。**
func mainTipID(st *position.Study) int {
	id, _, err := st.MainTip()
	if err != nil {
		return 0
	}
	return id
}

// rootSFEN は根の局面の SFEN（組み上がらなければ空）。**同じ対局かの判定用。**
func rootSFEN(s *position.Study) string {
	v, err := s.Root().SFEN()
	if err != nil {
		return ""
	}
	return v
}

// NewGame は何もないところから対局を始める（入力タブの「新しく対局を始める」）。
//
// ⚠️ **訂正タブを経由しない 3 つめの入口**（画像 → 訂正タブ、棋譜 → ここ、新規 → ここ）。
// 棋譜と同じ扱いなのは、**初期局面が手合割で一意に決まる**から（直すものが無い）。
// **`PositionService` は触らない** —— 撮った局面を消してしまうと、
// 新しく始めたのが誤操作だったときに戻る先が無くなる。
//
// ⚠️ **戻り値の型を棋譜の読み込みと共有しているのは、フロントの描き方が同じだから**
// （根を入れ替えて解析タブを開き、1 行の説明を出す）。**別の経路を作らないこと。**
//
// ⚠️ **「自分がどちら側か」はここでは受けない。** 今それが決めているのは
// **画面の向き（視点）だけ**で、局面には一切効かない（平手の初期局面は
// どちらを持っても同じ）。対局モード（人が片側を持ち、エンジンがもう片側を指す）を
// 入れる段になったら、その時点でここに載せる。**先回りして未使用の欄を作らない。**
func (s *StudyService) NewGame(handicap string) (load KifuLoad, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO なので、
	// ここで登録しておけば `s.mu.Unlock()` の**後**に走る）。
	defer func() { s.publish(load.State, err) }()

	// ⚠️ **入れ替える前に控えを書かせること**（2026-09-16）。**根を入れ替えると
	// 別のセッションになる**ので、間引きの幅（3 秒）のあいだに入れ替えると
	// **直前までの手と評価値が前のセッションから落ちる。**
	// ⚠️ **ロックを取る前に呼ぶこと**（向こうがロックを取る）。
	s.saveNow()

	study, err := position.NewGame(handicap)
	if err != nil {
		return KifuLoad{State: s.State()}, err
	}

	name := strings.TrimSpace(handicap)
	if name == "" {
		name = position.Hirate
	}

	s.mu.Lock()
	// **根ごと入れ替える**（Adopt / LoadKifu と同じ）。前の手順と解析結果は
	// 別の局面の話になる。
	s.study = study
	// **新規対局に対局者は居ないが、手合割は残す**（2026-09-12）。
	// ⚠️ **駒落ちを捨てないこと** —— 手合割が根の SFEN にしか無いと、
	// 棚へ保存する段でも KIF に書き出す段でも**平手の棋譜として扱われる**
	// （落ちている駒が「初手までに消えた」ことになり、そこから先が全部ずれる）。
	// 名前は `core/kifu` が読める表記そのまま（別名は向こうが吸収する）。
	s.game = position.Game{Handicap: name}
	// **取り直す先も無い。**
	s.sourceURL = ""
	s.kifuFollow = false
	s.sourceKey = ""
	s.gameID = ""
	s.newSessionLocked()
	st := s.changed()
	s.mu.Unlock()

	s.logger.Info("新しい対局を作りました", "handicap", name, "sfen", st.SFEN)
	summary := fmt.Sprintf("%sで対局を始めました", name)
	if name != position.Hirate {
		// ⚠️ **駒落ちは上手（後手）から指す。** 盤を見ただけでは手番が分からず、
		// 「始めた直後なのに後手番になっている」はバグに見える。**先に言っておく。**
		summary += "（上手＝後手から指します）"
	}
	return KifuLoad{State: st, Summary: summary}, nil
}

// handicapLabel は画面に出す手合割名（**平手と未設定は空**）。
//
// ⚠️ **平手を空にするのは意図的。** 画面側は「空でなければ駒落ち」で分岐する
// （`StudyState.Handicap`）。ここで "平手" を返すと、表示のたびに
// 「平手かどうか」の比較をフロントにも書くことになる。
func handicapLabel(name string) string {
	v := strings.TrimSpace(name)
	if v == kifu.HirateHandicap {
		return ""
	}
	return v
}

// kifuSummary は「何手読み込んだか」の 1 行を組み立てる。
func kifuSummary(load position.KIFLoad) string {
	head := fmt.Sprintf("%d手を読み込みました", load.Loaded)
	if load.Loaded < load.Total {
		head = fmt.Sprintf("%d手を読み込みました（棋譜には%d手）", load.Loaded, load.Total)
	}
	var who []string
	if load.Game.Event != "" {
		who = append(who, load.Game.Event)
	}
	if load.Game.Black != "" || load.Game.White != "" {
		who = append(who, fmt.Sprintf("先手 %s / 後手 %s", load.Game.Black, load.Game.White))
	}
	if load.Game.Handicap != "" && load.Game.Handicap != kifu.HirateHandicap {
		who = append(who, load.Game.Handicap)
	}
	if len(who) == 0 {
		return head
	}
	return head + "（" + strings.Join(who, "・") + "）"
}

// Play は 1 手指して局面を進める（解析タブの盤のクリック）。
//
// ⚠️ **合法手だけ。** 訂正タブ（どこへでも動かせる）とは別の面で、ここは実際の
// 対局と同じように進める。指せない手は Go 側で断る（**フロントで同じ判定を
// 書かないこと** —— 合法手の一覧は `StudyState.Legal` に出しているので、
// 画面はそれを光らせるだけでよい）。
//
// ⚠️ **戻って見ている途中で別の手を指すと、枝が生える**（2026-08-13。前の手順は
// **消えない**）。**評価値も捨てない** —— 記録は節点に紐づいているので、
// 枝を選び直せばそちらの折れ線がそのまま出る。
func (s *StudyService) Play(move string) (st StudyState, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO なので、
	// ここで登録しておけば `s.mu.Unlock()` の**後**に走る）。
	defer func() { s.publish(st, err) }()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return s.state(), fmt.Errorf("まだ局面がありません")
	}
	if err := s.study.Play(move); err != nil {
		return s.state(), err
	}
	return s.changed(), nil
}

// PlayLine は**エンジンが挙げた手を 1 手指す**（候補手の右クリック →「手順を指す」。
// 2026-09-14）。**自動で指し継ぐ**ときに 1 手ごとに呼ばれる。
//
// ⚠️ **`AddLine` と `Play` のどちらでも代わりにならない。** あちらは
// 「足すが進まない」「進むが誰の手か残らない」で、ここが要るのは
// **足して、進んで、誰が挙げた手かも残す**から（`position.Study.PlayLine`）。
//
// engineID は**その手を挙げたエンジン**の登録 ID（手順リストの色の丸）。
// ⚠️ **空でも指せること** —— 出所が分からなくても手そのものは正しい。
//
// head は**この手を変化の頭にするか**。⚠️ **指し継ぐ列の 1 手目だけ真にすること**
// —— 毎手立てると手順リストが 1 手ごとに 1 段ずつ下がる。
// **1 手目を頭にしてあるのは、棋譜の本譜を 1 手も動かさないため**
// （エンジンが指した手は**実際に現れた指し手ではない**。`AddLine` と同じ扱い）。
func (s *StudyService) PlayLine(engineID, move string, head bool) (st StudyState, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO なので、
	// ここで登録しておけば `s.mu.Unlock()` の**後**に走る）。
	defer func() { s.publish(st, err) }()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return s.state(), fmt.Errorf("まだ局面がありません")
	}
	if err := s.study.PlayLine(move, engineID, head); err != nil {
		return s.state(), err
	}
	return s.changed(), nil
}

// AddLine は解析の候補手（読み筋）を**枝として木に足す**（候補手の右クリック）。
//
// ⚠️ **押しても指さない**（＝今見ている局面は動かない）。動くと走っている解析が
// 別の局面のものになり、**候補を続けて足せない**。
//
// ⚠️ **候補の頭が本譜と同じなら枝を増やさず、食い違うところで枝にする**
// （判断は `position.Study.AddLine`。**フロントで突き合わせないこと**）。
//
// engineID は**その読み筋を出したエンジン**の登録 ID。手順リストで
// **誰が言った手なのか**を色で出すのに使う（`Node.Sources`）。
// ⚠️ **空でも足せること** —— 出所が分からない読み筋でも、手順に足す価値は変わらない。
func (s *StudyService) AddLine(engineID string, moves []string) (line AddLine, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO なので、
	// ここで登録しておけば `s.mu.Unlock()` の**後**に走る）。
	defer func() { s.publish(line.State, err) }()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return AddLine{State: s.state()}, fmt.Errorf("まだ局面がありません")
	}
	if len(moves) == 0 {
		return AddLine{State: s.state()}, fmt.Errorf("読み筋がありません")
	}
	first, added, note := s.study.AddLine(moves, engineID)
	// ⚠️ **詰将棋では読み筋が本線**（2026-09-12）。詰将棋に「本譜」は無く、
	// **足した手順そのものが答え**なので、1 段下げて畳んだ形で置くと読みづらい。
	//
	// ⚠️ **エラーは握る。** `Promote` は**もう本線があるなら断る**ので、
	// 2 本目以降（＝余詰）は枝のまま残る —— **それが正しい姿**
	// （先に足したほうを黙って押しのけない）。
	if first != 0 && s.study.Root().MateProblem {
		_ = s.study.Promote(first)
	}
	return AddLine{State: s.changed(), FirstID: first, Added: added, Note: note}, nil
}

// Branch はその手から先を**本譜ではなく変化にする**（手順リストの右クリック →
// 「分岐にする」。2026-08-14）。
//
// **本譜の先端から試しに指した手を、エンジンの読み筋と同じ扱いに落とす操作。**
// 手順リストでは 1 段下がって**前の手にぶら下がり**、畳めるようになる。
//
// ⚠️ **手順は 1 手も消えない**（`DropFrom` と混同しないこと）。**見ている局面も
// 動かない** —— 見え方が変わるだけなので、盤まで動くと何が起きたのか分からない。
//
// ⚠️ **評価値も捨てない。** 節点はそのまま（id も変わらない）で、
// **本譜かどうかが変わるだけ**。
func (s *StudyService) Branch(id int) (st StudyState, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO なので、
	// ここで登録しておけば `s.mu.Unlock()` の**後**に走る）。
	defer func() { s.publish(st, err) }()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return s.state(), fmt.Errorf("まだ局面がありません")
	}
	if err := s.study.Branch(id); err != nil {
		return s.state(), err
	}
	return s.changed(), nil
}

// Promote はその手を**分かれ道の続き（本線）に選ぶ**（手順リストの右クリック →
// 「本線にする」。2026-08-18）。**`Branch` の裏返し。**
//
// エンジンの読み筋を 2 本足すと**どちらも同格の候補**として並ぶ（続きが決まって
// いない状態）。そこから「この続きを辿る」と決めるのがこれで、選んだ手は
// **同じ深さで続く 1 本**になり、残りは枝として 1 段下がる。
//
// ⚠️ **手順は 1 手も消えない。見ている局面も動かない**（`Branch` と同じ）。
// **評価値も捨てない** —— 節点はそのままで、どれを続きとするかが変わるだけ。
//
// ⚠️ **続きが既に決まっているなら断る**（`position.Study.Promote`）。
// 選び直すときは**先に「分岐にする」で外す。**
func (s *StudyService) Promote(id int) (st StudyState, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO なので、
	// ここで登録しておけば `s.mu.Unlock()` の**後**に走る）。
	defer func() { s.publish(st, err) }()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return s.state(), fmt.Errorf("まだ局面がありません")
	}
	if err := s.study.Promote(id); err != nil {
		return s.state(), err
	}
	return s.changed(), nil
}

// DropFrom はその手**とその先（子孫の枝も全部）**を消す（**手順リストの右クリック**）。
//
// ⚠️ **「1手戻す」は無くなった**（2026-08-13）。**消す量を手そのもので指す**形に
// してある —— `Undo` は「今どこを見ているか」に依存していたので、戻って見ている
// 最中に押すと何が消えるのか分かりにくかった。今は**押した手から下**で、
// 画面の見た目とそのまま一致する。
//
// **枝も本譜も同じように消せる**（本譜は URL から取り直せる）。
// **見るだけなら GoTo。混同しないこと**（あちらは何も消さない）。
//
// ⚠️ **引数は節点の id で、手数ではない**（枝があると同じ手数が何個もある）。
func (s *StudyService) DropFrom(id int) (st StudyState, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO なので、
	// ここで登録しておけば `s.mu.Unlock()` の**後**に走る）。
	defer func() { s.publish(st, err) }()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return s.state(), fmt.Errorf("まだ局面がありません")
	}
	gone, err := s.study.DropFrom(id)
	if err != nil {
		return s.state(), err
	}
	// **消えた節点の評価値も消す**（`GoTo` との違いがここにも出る。
	// あちらは何も消さないので、評価値もそのまま残る）。
	s.evals.drop(gone)
	return s.changed(), nil
}

// GoTo はその節点の局面を見る（0 なら根）。**手順は消さない。**
//
// ⚠️ **引数は節点の id で、手数ではない。**
func (s *StudyService) GoTo(id int) (st StudyState, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO なので、
	// ここで登録しておけば `s.mu.Unlock()` の**後**に走る）。
	defer func() { s.publish(st, err) }()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return s.state(), fmt.Errorf("まだ局面がありません")
	}
	if err := s.study.GoTo(id); err != nil {
		return s.state(), err
	}
	return s.changed(), nil
}

// State は今の状態を返す（何も変えない）。フロントの初期表示用。
func (s *StudyService) State() StudyState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state()
}

// Clear は解析タブを空に戻す（根・対局者・取得元・評価値を全部捨てる）。
//
// ⚠️ **撮り直しでは呼ばない**（2026-08-22 にフロントの呼び出しを外した）。
// **撮ることと、解析している局面を捨てることは別の操作** —— 学習データを
// 集めるために撮る使い方では、**解析している局面はそのまま**でないと
// 撮るたびに検討が消える。捨てるのは「この局面を解析する」を押したときで、
// **そこは `Adopt` が根ごと入れ替えるので、先回りして消す必要が無い。**
//
// ⚠️ **今は呼び出し側が無い。** 消さずに残してあるのは「解析タブを空にする」
// という操作そのものは正当だから（入口が要るようになったらここを使う）。
// **撮った時点で呼ぶ形に戻さないこと。**
func (s *StudyService) Clear() (st StudyState) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO なので、
	// ここで登録しておけば `s.mu.Unlock()` の**後**に走る）。
	defer func() { s.publish(st, nil) }()

	// ⚠️ **入れ替える前に控えを書かせること**（2026-09-16）。**根を入れ替えると
	// 別のセッションになる**ので、間引きの幅（3 秒）のあいだに入れ替えると
	// **直前までの手と評価値が前のセッションから落ちる。**
	// ⚠️ **ロックを取る前に呼ぶこと**（向こうがロックを取る）。
	s.saveNow()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.study = nil
	s.game = position.Game{}
	s.sourceURL = ""
	s.kifuFollow = false
	s.sourceKey = ""
	s.gameID = ""
	s.newSessionLocked()
	return s.changed()
}

// Evals は評価値グラフの中身を返す（解析タブ）。
//
// **フロントはこれを描くだけ。** 点の並びも横軸の範囲を決める材料もここが返すので、
// **フロント側で `StudyState` と突き合わせて計算しないこと**
// （2 つの値が別のタイミングで届くぶんだけずれる）。
//
// ⚠️ **エンジンごとに別の折れ線。** 合成しない（平均も多数決も取らない）。
func (s *StudyService) Evals() EvalGraph {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return EvalGraph{Series: []EvalSeries{}, IDs: []int{}, Ref: []EvalSeries{}, Branches: []int{}}
	}
	// ⚠️ **点は「今の経路」だけ**（枝と本譜を 1 本の折れ線に混ぜない）。
	line := s.study.Line()
	g := EvalGraph{Series: s.evals.series(line), IDs: line}
	if g.Series == nil {
		g.Series = []EvalSeries{}
	}
	base := s.moveBaseLocked()
	g.First = base
	g.Ply = s.study.Ply()
	g.Number = base + s.study.Ply()
	if ply := s.study.Ply(); ply > 0 && ply < len(line) {
		if n, found := s.nodeLocked(line[ply]); found {
			g.Move = n.Text
		}
	}
	// ⚠️ **右端は「今辿っている 1 本」の終わり**（木全体の最大手数ではない）。
	g.Last = base + len(line) - 1
	// **枝に居るなら、分かれなかったほうの線も薄く出す**（2026-08-13）。
	// 枝を選んだ結果がどう転んだかは、**元の線と並べて初めて読める。**
	if forkID, ref, ok := s.study.Fork(); ok {
		fork := 0
		if n, found := s.nodeLocked(forkID); found {
			fork = n.Number
		}
		g.Fork = base + fork
		// ⚠️ **共有している手前は落とす**（同じ点を 2 本描くことになる）。
		for _, se := range s.evals.series(ref) {
			pts := make([]EvalPoint, 0, len(se.Points))
			for _, p := range se.Points {
				if p.Ply > fork {
					pts = append(pts, p)
				}
			}
			if len(pts) > 0 {
				se.Points = pts
				g.Ref = append(g.Ref, se)
			}
		}
		// ⚠️ **元の線が先まで伸びているなら、横軸もそこまで広げること** ——
		// 切ると**枝が今どのあたりに居るのか**が読めない（それがこの線の目的）。
		if end := base + len(ref) - 1; end > g.Last {
			g.Last = end
		}
	}
	if g.Ref == nil {
		g.Ref = []EvalSeries{}
	}
	// **枝が分かれている手に、欄外の印を打つ**（2026-09-16）。
	//
	// ⚠️ **id ではなく棋譜手数で渡すこと** —— 印を置くのは横軸の上で、
	// **押せるようにはしない**（複数の枝があるとどれへ行くのか決まらない。
	// 決まらないものを選ばせないのがこのアプリの方針）。
	// ⚠️ **手数は `line` の添字から出す**（`Branching` が返すのは同じ 1 本の上の
	// 節点なので、節点を引き直す必要が無い）。
	branching := map[int]bool{}
	for _, id := range s.study.Branching() {
		branching[id] = true
	}
	g.Branches = []int{}
	for i, id := range line {
		if branching[id] {
			g.Branches = append(g.Branches, base+i)
		}
	}
	return g
}

// moveBaseLocked は根の局面までに指された手数（＝横軸の左端）。
//
// ⚠️ **SFEN の手数は「次に指す手の番号」**なので 1 を引く（棋譜の数え方に直す）。
// 根が初期局面なら 0、撮った 40 手目の局面が根なら 40 になる。手数が不明（0）の
// ときは 0 として扱う —— **勝手に推測しない**（分からないものは分からない）。
func (s *StudyService) moveBaseLocked() int {
	n := s.study.Root().MoveNumber
	if n <= 0 {
		return 0
	}
	return n - 1
}

// recordEval は解析の途中経過を評価値グラフに書く（`AnalyzeService` から）。
//
// ⚠️ **公開しない**（Service の公開メソッドはフロントの API になる）。記録するのは
// エンジンの答えであって、フロントが決めることではない。
//
// epoch は `analyzeTarget` で受け取った根の世代。**食い違っていたら捨てる** ——
// 解析は非同期なので、**根を入れ替えた後に前の対局の途中経過が届く**（節点の id は
// 木ごとに 1 から振り直すので、そのままだと別の局面の値として書き戻る）。
//
// ⚠️ **記録の鍵は節点の id**（手数ではない）。**消えた節点には書かない。**
func (s *StudyService) recordEval(epoch, id int, engineID, label string, sc analyze.Score, depth int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return
	}
	// ⚠️ **消えた節点なら捨てる**（右クリックで消した枝の解析が後から届く）。
	node, ok := s.nodeLocked(id)
	if !ok {
		return
	}
	text := node.Text
	if text == "" {
		text = node.USI
	}
	// ⚠️ **ここでも控えを促すこと。** 評価値は `changed()` を通らないので、
	// **これが無いと「手は進めたが解析はまだ」の状態しか残らない**（評価値が
	// 1 つも入っていない控えになる ＝ Step 1 の目的が果たせない）。
	defer s.markDirty()
	s.evals.record(epoch, engineID, label, EvalPoint{
		ID:      id,
		Ply:     node.Number,
		Number:  s.moveBaseLocked() + node.Number,
		CP:      sc.CP,
		Mate:    sc.Mate,
		WinRate: sc.WinRate,
		Label:   sc.Label,
		Depth:   depth,
		Move:    text,
	})
}

// nodeLocked は節点の写しを引く（0 は根。**消えていれば false**）。
func (s *StudyService) nodeLocked(id int) (position.Node, bool) {
	if id == 0 {
		return position.Node{}, true
	}
	for _, n := range s.study.Nodes() {
		if n.ID == id {
			return n, true
		}
	}
	return position.Node{}, false
}

// analyzeTarget は解析にかける対象（AnalyzeService 用）。
type analyzeTarget struct {
	// Root は根の局面（採ったときの局面）。
	Root string
	// Moves は根から解析する局面までの手（USI）。
	Moves []string
	// Current は解析する局面そのものの SFEN。
	//
	// **エンジンには渡さない**（渡すのは Root + Moves）。これは画面に「何を評価した
	// 値なのか」を出すためと、**局面が変わったら結果を消す**判定のため。
	// ⚠️ 手を進めても Root は変わらないので、**Root で判定すると結果が残り続ける。**
	Current string
	// NodeID は解析する局面の節点（**評価値の記録先**。0 は根）。
	//
	// ⚠️ **手数ではない**（枝があると同じ手数が何個もある）。手数に丸めると、
	// **枝で出した評価値が本譜の同じ手数の点として書き戻る。**
	NodeID int
	// Epoch は根の世代。**記録を書き戻すときの合鍵**（`recordEval`）。
	//
	// ⚠️ **解析は非同期なので、根を入れ替えた後に途中経過が届く。** 節点の id は
	// 木ごとに 1 から振り直すので、これが無いと**前の対局の評価値が同じ id の
	// 別の局面に書き戻る。**
	Epoch int
}

// analyzeTarget は解析にかける「根 + そこまでの手順」を返す。
//
// **局面はフロントを経由させない。** 解析するのは常に「今ここが持っている局面」で、
// SFEN を渡してもらう形にすると、採り直した直後に古い局面を解析する経路ができる。
//
// ⚠️ **組み立て直した 1 つの SFEN ではなく、根と手順を分けて返す。**
// `position sfen <根> moves ...` でないと、千日手と連続王手をエンジンが判定できない。
func (s *StudyService) analyzeTarget() (analyzeTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return analyzeTarget{},
			fmt.Errorf("解析する局面がありません。訂正タブで「この局面を解析する」を押してください")
	}
	root, err := s.study.Root().SFEN()
	if err != nil {
		return analyzeTarget{}, err
	}
	cur, err := s.study.Current().SFEN()
	if err != nil {
		return analyzeTarget{}, err
	}
	return analyzeTarget{
		Root:    root,
		Moves:   s.study.Played(),
		Current: cur,
		NodeID:  s.study.CurrentID(),
		Epoch:   s.evals.epoch,
	}, nil
}

// changed は**変えたあとの状態**を作る（`rev` を 1 つ進める）。
//
// ⚠️ **読むだけのときは `state()` を使うこと。** `rev` は「別の窓が描き直すべきか」
// の判断そのものなので、何も変えていないのに進めると**全部の窓が無駄に描き直す**。
// ⚠️ **ロックを取った状態で呼ぶこと。**
func (s *StudyService) changed() StudyState {
	s.rev++
	s.markDirty()
	return s.state()
}

// markDirty は**控えを書き直す必要がある**ことを知らせる（2026-09-16）。
//
// ⚠️ **ロックを取った状態で呼ぶこと**（呼ぶ側が既に持っている）。
// ⚠️ **ここで書かないこと** —— ディスクへの書き込みをロックの中でやると、
// **解析の途中経過が届くたびに待たされる**（深さが進むたび・エンジンの数だけ来る）。
// 知らせるだけで、間引くのも書くのも `StudyStore` の仕事。
func (s *StudyService) markDirty() {
	if s.dirty != nil {
		s.dirty()
	}
}

// publish は「解析タブの局面が変わった」を**全部の窓へ**知らせる（`study:changed`）。
//
// **これが別ウィンドウとの連動の土台**（2026-09-08）。以前は変更の結果を
// **戻り値だけ**で返していたので、**呼んだ窓しか気づけなかった**。
//
// ⚠️ **ロックを外してから呼ぶこと。** 先はフロントなので、ロックを持ったまま
// 渡すと、そこから戻ってきた呼び出しと噛み合う余地がある。**`defer` は LIFO** なので、
// `s.mu.Unlock()` より**先に登録**すれば後に走る（各メソッドの先頭に置いてあるのはこのため）。
//
// ⚠️ **失敗したときは出さない**（状態は変わっていない）。出すと全部の窓が
// 同じ絵を描き直すだけになる。
func (s *StudyService) publish(st StudyState, err error) {
	if err != nil || s.Emit == nil {
		return
	}
	s.Emit("study:changed", st)
}

// state はロックを取った状態で呼ぶこと。
func (s *StudyService) state() StudyState {
	if s.study == nil {
		return StudyState{
			Hands: []position.Stock{}, Warnings: []string{},
			Nodes: []position.Node{}, Line: []int{},
			Played: []string{}, Legal: []legal.Move{},
			Rev: s.rev,
		}
	}
	// **描くのは常に「今見ている局面」**（根 + 手順の ply 手目まで）。
	// 根と混ぜないこと —— 手を進めたのに根の盤が出ると、何を見ているのか分からない。
	cur := s.study.Current()
	full, _ := cur.SFEN() // Adopt が通っている以上ここは埋まる
	root, _ := s.study.Root().SFEN()
	warnings := cur.Warnings()
	if warnings == nil {
		warnings = []string{}
	}
	// ⚠️ **合法手が出せなくても局面は返す**（設計原則3）。手が指せなくなるだけで、
	// 盤も評価値もそのまま使える。
	moves, err := s.study.Legal()
	legalErr := ""
	if err != nil {
		moves, legalErr = []legal.Move{}, err.Error()
	}
	return StudyState{
		Loaded:     true,
		BoardSFEN:  cur.BoardSFEN(),
		SFEN:       full,
		Turn:       int(cur.Turn),
		TurnLabel:  cur.Turn.String(),
		MoveNumber: cur.MoveNumber,
		Hands:      cur.Inventory(),
		Warnings:   warnings,
		RootSFEN:   root,
		First:      s.moveBaseLocked(),
		Nodes:      s.study.Nodes(),
		CurrentID:  s.study.CurrentID(),
		MainTip:    mainTipID(s.study),
		Line:       s.study.Line(),
		Ply:        s.study.Ply(),
		Played:     s.study.Played(),
		Legal:      moves,
		LegalError: legalErr,
		Black:      s.game.Black,
		White:      s.game.White,
		Handicap:   handicapLabel(s.game.Handicap),
		SourceURL:  s.sourceURL,
		GameID:     s.gameID,
		KifuFollow: s.kifuFollow,
		Rev:        s.rev,
	}
}
