package main

import (
	"fmt"
	"log/slog"
	"sync"

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
	st := s.state()
	s.mu.Unlock()

	s.logger.Info("局面を解析タブへ採りました", "sfen", st.SFEN)
	return st, nil
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
	if err := s.study.Play(move); err != nil {
		return s.state(), err
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
	return s.state()
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
	return analyzeTarget{Root: root, Moves: s.study.Played(), Current: cur}, nil
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
		Moves:      s.study.Moves(),
		Ply:        s.study.Ply(),
		Played:     s.study.Played(),
		Legal:      moves,
		LegalError: legalErr,
	}
}
