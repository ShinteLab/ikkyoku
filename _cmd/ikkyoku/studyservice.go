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
// **これから**: 分岐ツリー（今は一直線。戻って別の手を指すと先は捨てる）。
// 木の形は `core/kifu` が持てるようになってから決める。
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
	// evals は手順の 1 手ごとの評価値（評価値グラフ。2026-08-12）。
	//
	// ⚠️ **置き場所がここなのは、記録が手順に紐づくから。** 手順を切る操作
	// （`Play` の分岐・`Undo`・根の入れ替え）を知っているのはここだけなので、
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

	// RootSFEN は根の局面（採ったときの局面）。**エンジンに渡すのはこれ + Played。**
	RootSFEN string `json:"rootSfen"`
	// First は根までに指された手数（＝**棋譜の手数の起点**）。
	//
	// ⚠️ **`Move.Number` は根からの手数で、棋譜の手数ではない**（あちらは `GoTo` に
	// 渡す値も兼ねているので、起点をずらせない）。撮った 41 手目の局面を根にすると
	// `Move.Number` は 1 から始まるので、**画面に手数として出すときは First を足す**。
	// 評価値グラフの横軸（`EvalGraph.First`）と**同じ値**にすること。
	First int `json:"first"`
	// Moves は根から指した手順（棋譜の順。日本語表記つき）。
	Moves []position.Move `json:"moves"`
	// Ply は今どこまで進めて見ているか（0 なら根）。
	//
	// **len(Moves) より小さいことがある**（戻って見ている状態）。
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
	s.evals.reset()
	st := s.state()
	s.mu.Unlock()

	s.logger.Info("棋譜を読み込みました",
		"moves", load.Loaded, "total", load.Total, "note", load.Note)
	return KifuLoad{State: st, Summary: kifuSummary(load), Note: load.Note}, nil
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
	// **何を読んだかを出す。** URL は打ち間違えても「棋譜が読めません」としか
	// 出ないことがあるので、**取れた側の事実**（どこから・何文字コードで）を見せる。
	load.Summary += fmt.Sprintf("（%s）", got.Encoding)
	return load, nil
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
// **戻って見ている途中で指すと、そこから先の手順は捨てる。**
func (s *StudyService) Play(move string) (StudyState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return s.state(), fmt.Errorf("まだ局面がありません")
	}
	// ⚠️ **手順を切るかどうかは指す前にしか分からない。** 戻って見ている途中で
	// 別の手を指すと、そこから先の手順は捨てられる（`position.Study.Play`）ので、
	// **その先の評価値も一緒に捨てる**（別の手順に付いた値なので）。
	// 同じ手を指し直しただけなら手順は変わらないので、評価値も残す。
	ply := s.study.Ply()
	prev := s.study.Moves()
	branched := ply >= len(prev) || prev[ply].USI != move
	if err := s.study.Play(move); err != nil {
		return s.state(), err
	}
	if branched {
		s.evals.dropAfter(ply)
	}
	return s.state(), nil
}

// Undo は 1 手戻す（**手順からも消す**。「指し間違えた」の取り消し）。
//
// 戻って見るだけなら GoTo。**混同しないこと。**
func (s *StudyService) Undo() (StudyState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return s.state(), fmt.Errorf("まだ局面がありません")
	}
	if err := s.study.Undo(); err != nil {
		return s.state(), err
	}
	// **手順から消えた手の評価値も消す**（`GoTo` との違いがここにも出る。
	// あちらは手順を消さないので、評価値もそのまま残す）。
	s.evals.dropAfter(s.study.Ply())
	return s.state(), nil
}

// GoTo は手順の n 手目まで進めた局面を見る（0 なら根）。**手順は消さない。**
func (s *StudyService) GoTo(n int) (StudyState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return s.state(), fmt.Errorf("まだ局面がありません")
	}
	if err := s.study.GoTo(n); err != nil {
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
	g := EvalGraph{Series: s.evals.series()}
	if g.Series == nil {
		g.Series = []EvalSeries{}
	}
	if s.study == nil {
		return g
	}
	base := s.moveBaseLocked()
	g.First = base
	g.Ply = s.study.Ply()
	g.Number = base + s.study.Ply()
	g.Last = base + len(s.study.Moves())
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
// epoch は `analyzeTarget` で受け取った手順の世代。**食い違っていたら捨てる** ——
// 解析は非同期なので、**手順を切った後に前の枝の途中経過が届く**。
func (s *StudyService) recordEval(epoch, ply int, engineID, label string, sc analyze.Score, depth int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return
	}
	moves := s.study.Moves()
	if ply < 0 || ply > len(moves) {
		return
	}
	text := ""
	if ply > 0 {
		text = moves[ply-1].Text
		if text == "" {
			text = moves[ply-1].USI
		}
	}
	s.evals.record(epoch, engineID, label, EvalPoint{
		Ply:    ply,
		Number: s.moveBaseLocked() + ply,
		CP:     sc.CP,
		Mate:   sc.Mate,
		Label:  sc.Label,
		Depth:  depth,
		Move:   text,
	})
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
	// Ply は解析する局面が根から何手目か。**評価値グラフの横軸の位置。**
	Ply int
	// Epoch は手順の世代。**記録を書き戻すときの合鍵**（`recordEval`）。
	//
	// ⚠️ **解析は非同期なので、手順を切った後に途中経過が届く。** これが無いと
	// **捨てたはずの枝の評価値がグラフに書き戻る。**
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
		Ply:     s.study.Ply(),
		Epoch:   s.evals.epoch,
	}, nil
}

// state はロックを取った状態で呼ぶこと。
func (s *StudyService) state() StudyState {
	if s.study == nil {
		return StudyState{
			Hands: []position.Stock{}, Warnings: []string{},
			Moves: []position.Move{}, Played: []string{}, Legal: []legal.Move{},
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
		Moves:      s.study.Moves(),
		Ply:        s.study.Ply(),
		Played:     s.study.Played(),
		Legal:      moves,
		LegalError: legalErr,
	}
}
