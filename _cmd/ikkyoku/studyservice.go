package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/ikkyoku/analyze"
	"github.com/ShinteLab/ikkyoku/kifuweb"
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
	// SourceURL は棋譜の取得元（**URL から読んだときだけ埋まる**。2026-08-13）。
	//
	// **空でなければ「再読み込み」を出す**（`ReloadKifu`）。⚠️ **フロントで
	// URL 欄の中身から判断しないこと** —— 入力タブの欄はいつでも書き換えられるので、
	// **今の手順がどこから来たか**とは別物になる。
	SourceURL string `json:"sourceUrl"`

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
}

// Adopt は訂正タブの局面を採って、解析タブの根にする。
//
// ⚠️ **確定していない局面は採らない。** 手番か駒台の先後が未決だと
// `position.Position.SFEN()` がエラーを返すので、そのまま理由として返す。
// ここで先手に倒すと、決めていない手番でエンジンが読むことになる（設計原則5）。
// **これは警告ではなくエラー** —— 決めてもらう以外に手が無い。
func (s *StudyService) Adopt() (StudyState, error) {
	p := s.src.clonePosition()
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
	// **評価値グラフも捨てる。** 別の局面から始まる別の手順なので、前の折れ線を
	// 残すと**違う対局の評価値が同じ横軸に並ぶ。**
	s.evals.reset()
	st := s.state()
	s.mu.Unlock()

	s.logger.Info("局面を解析タブへ採りました", "sfen", st.SFEN)
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
}

// LoadKifu は KIF テキストを読んで解析タブの根と手順にする。
//
// ⚠️ **訂正タブを経由しない 2 つめの入口。** 「受け渡しは Adopt の 1 か所だけ」は
// **訂正タブとの受け渡し**の話で、入力の口が増えること自体は想定どおり
// （画像は認識を通るので訂正タブへ、KIF は既に確定しているので直接ここへ）。
// **`PositionService` は触らない** —— 撮った局面を消してしまうと、
// 貼り付けたのが誤りだったときに戻る先が無くなる。
//
// **指し手が全て反映された状態**（最終手まで進めた局面）で返す。戻って見たければ
// 手順のリストから辿れる。
func (s *StudyService) LoadKifu(text string) (KifuLoad, error) {
	study, load, err := position.FromKIF(text)
	if err != nil {
		return KifuLoad{State: s.State()}, err
	}

	s.mu.Lock()
	// **根ごと入れ替える**（Adopt と同じ）。前の手順と解析結果は別の局面の話になる。
	s.study = study
	// **対局者はここでだけ埋まる**（勝率バーの左右に出す）。
	s.game = load.Game
	// ⚠️ **取得元は捨てる。** 貼り付けた棋譜には取り直す先が無い。
	// URL から読んだときは `LoadKifuURL` が**このあとに**入れ直す。
	s.sourceURL = ""
	s.evals.reset()
	st := s.state()
	s.mu.Unlock()

	s.logger.Info("棋譜を読み込みました",
		"moves", load.Loaded, "total", load.Total, "note", load.Note)
	return KifuLoad{State: st, Summary: kifuSummary(load), Note: load.Note}, nil
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
// **取ってくるのは `ikkyoku/kifuweb`**（文字コードの判別もあちら。日本将棋連盟の
// 棋譜中継は Shift_JIS）。ここは繋ぐだけで、**取得も KIF の解釈もここに書かない。**
func (s *StudyService) LoadKifuURL(rawURL string) (KifuLoad, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kifuFetchTimeout)
	defer cancel()

	got, err := kifuweb.Fetch(ctx, rawURL)
	if err != nil {
		return KifuLoad{State: s.State()}, err
	}
	s.logger.Info("棋譜を取得しました", "url", got.URL, "encoding", got.Encoding, "bytes", len(got.Text))

	load, err := s.LoadKifu(got.Text)
	if err != nil {
		return load, err
	}
	// **取得元を覚える**（手順の見出しの「再読み込み」の鍵）。⚠️ **`LoadKifu` が
	// 捨てたあとに入れ直している** —— あちらは貼り付けの口でもあるので、
	// 取得元を知らないほうが正しい。
	s.mu.Lock()
	s.sourceURL = got.URL
	st := s.state()
	s.mu.Unlock()
	load.State = st
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
	s.mu.Lock()
	url := s.sourceURL
	s.mu.Unlock()
	if url == "" {
		return KifuLoad{State: s.State()},
			fmt.Errorf("この局面は URL から読み込んだものではないので、取り直せません")
	}

	ctx, cancel := context.WithTimeout(context.Background(), kifuFetchTimeout)
	defer cancel()
	got, err := kifuweb.Fetch(ctx, url)
	if err != nil {
		return KifuLoad{State: s.State()}, err
	}
	// ⚠️ **取れなかった / 読めなかったときは今の手順を壊さないこと。**
	// 組み立てが通ってから初めて入れ替える。
	study, load, err := position.FromKIF(got.Text)
	if err != nil {
		return KifuLoad{State: s.State()}, err
	}

	s.mu.Lock()
	graft := s.mergeReloadLocked(study)
	s.game = load.Game
	st := s.state()
	s.mu.Unlock()

	summary := kifuSummary(load) + fmt.Sprintf("（%s）", got.Encoding)
	note := load.Note
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
	s.logger.Info("棋譜を取り直しました", "url", got.URL, "moves", load.Loaded,
		"kept", graft.Kept, "added", graft.Added, "movedAt", graft.MovedAt)
	return KifuLoad{State: st, Summary: summary, Note: note}, nil
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
		s.evals.reset()
		return position.GraftResult{}
	}
	// **最後の手を見ていたか**を、据え直す前に覚えておく（下記）。
	atEnd := s.study.Ply() >= len(s.study.MainLine())
	r := s.study.Graft(next.MainLine())
	// **見ている位置。** ⚠️ **最後の手を見ていたなら、伸びた先の最後まで進める**
	// —— 中継を追う使い方では「今の局面が見たい」が普通だから。途中や枝を見て
	// いたなら**そのまま**（据え直しでは何も消えていないので、行き先が生きている）。
	if atEnd {
		// ⚠️ **本譜の終わりまで進める。** 今居る節点から `Line()` を取ると、
		// 押しのけられた**古い枝の終わり**へ行ってしまう（実際に踏んだ）。
		// 根まで戻れば `Line()` は本譜そのものになる。
		if err := s.study.GoTo(0); err == nil {
			line := s.study.Line()
			_ = s.study.GoTo(line[len(line)-1])
		}
	}
	return r
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
func (s *StudyService) NewGame(handicap string) (KifuLoad, error) {
	study, err := position.NewGame(handicap)
	if err != nil {
		return KifuLoad{State: s.State()}, err
	}

	s.mu.Lock()
	// **根ごと入れ替える**（Adopt / LoadKifu と同じ）。前の手順と解析結果は
	// 別の局面の話になる。
	s.study = study
	// **新規対局に対局者は居ない**（名前を入れる口はまだ無い）。
	s.game = position.Game{}
	// **取り直す先も無い。**
	s.sourceURL = ""
	s.evals.reset()
	st := s.state()
	s.mu.Unlock()

	name := strings.TrimSpace(handicap)
	if name == "" {
		name = position.Hirate
	}
	s.logger.Info("新しい対局を作りました", "handicap", name, "sfen", st.SFEN)
	return KifuLoad{State: st, Summary: fmt.Sprintf("%sで対局を始めました", name)}, nil
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
func (s *StudyService) Play(move string) (StudyState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return s.state(), fmt.Errorf("まだ局面がありません")
	}
	if err := s.study.Play(move); err != nil {
		return s.state(), err
	}
	return s.state(), nil
}

// AddLine は解析の候補手（読み筋）を**枝として木に足す**（候補手の右クリック）。
//
// ⚠️ **押しても指さない**（＝今見ている局面は動かない）。動くと走っている解析が
// 別の局面のものになり、**候補を続けて足せない**。
//
// ⚠️ **候補の頭が本譜と同じなら枝を増やさず、食い違うところで枝にする**
// （判断は `position.Study.AddLine`。**フロントで突き合わせないこと**）。
func (s *StudyService) AddLine(moves []string) (AddLine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return AddLine{State: s.state()}, fmt.Errorf("まだ局面がありません")
	}
	if len(moves) == 0 {
		return AddLine{State: s.state()}, fmt.Errorf("読み筋がありません")
	}
	first, added, note := s.study.AddLine(moves)
	return AddLine{State: s.state(), FirstID: first, Added: added, Note: note}, nil
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
func (s *StudyService) DropFrom(id int) (StudyState, error) {
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
	return s.state(), nil
}

// GoTo はその節点の局面を見る（0 なら根）。**手順は消さない。**
//
// ⚠️ **引数は節点の id で、手数ではない。**
func (s *StudyService) GoTo(id int) (StudyState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return s.state(), fmt.Errorf("まだ局面がありません")
	}
	if err := s.study.GoTo(id); err != nil {
		return s.state(), err
	}
	return s.state(), nil
}

// State は今の状態を返す（何も変えない）。フロントの初期表示用。
func (s *StudyService) State() StudyState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state()
}

// Clear は解析タブを空に戻す。**撮り直したときに呼ぶ** —— 前の局面の盤と
// 評価値が新しい認識結果の裏で生き残っていると、どちらが今の話か分からなくなる。
func (s *StudyService) Clear() StudyState {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.study = nil
	s.game = position.Game{}
	s.sourceURL = ""
	s.evals.reset()
	return s.state()
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
		return EvalGraph{Series: []EvalSeries{}, IDs: []int{}}
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
	// ⚠️ **右端は「今辿っている 1 本」の終わり**（木全体の最大手数ではない）。
	g.Last = base + len(line) - 1
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
	s.evals.record(epoch, engineID, label, EvalPoint{
		ID:     id,
		Ply:    node.Number,
		Number: s.moveBaseLocked() + node.Number,
		CP:     sc.CP,
		Mate:   sc.Mate,
		Label:  sc.Label,
		Depth:  depth,
		Move:   text,
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

// state はロックを取った状態で呼ぶこと。
func (s *StudyService) state() StudyState {
	if s.study == nil {
		return StudyState{
			Hands: []position.Stock{}, Warnings: []string{},
			Nodes: []position.Node{}, Line: []int{},
			Played: []string{}, Legal: []legal.Move{},
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
		Line:       s.study.Line(),
		Ply:        s.study.Ply(),
		Played:     s.study.Played(),
		Legal:      moves,
		LegalError: legalErr,
		Black:      s.game.Black,
		White:      s.game.White,
		SourceURL:  s.sourceURL,
	}
}
