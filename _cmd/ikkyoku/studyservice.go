package main

import (
	"fmt"
	"log/slog"
	"sync"

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
// **これから**: 手順（`Moves`）と分岐ツリーがここに乗る。合法手生成が要るので
// `ikkyoku → engine` の Go 依存が戻るが、**解析だけは USI 経由のまま**にすること
// （棋力の問題を設定の問題にした意味が無くなる）。
type StudyService struct {
	logger *slog.Logger
	// src は訂正タブの局面。**読むのは Adopt の瞬間だけ。**
	src *PositionService

	mu sync.Mutex
	// pos は確定した局面の写し。まだ採っていなければ nil。
	pos *position.Position
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
	s.pos = p
	st := s.state()
	s.mu.Unlock()

	s.logger.Info("局面を解析タブへ採りました", "sfen", st.SFEN)
	return st, nil
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
	s.pos = nil
	return s.state()
}

// positionSFEN は解析にかける局面の SFEN を返す（AnalyzeService 用）。
//
// **局面はフロントを経由させない。** 解析するのは常に「今ここが持っている局面」で、
// SFEN を渡してもらう形にすると、採り直した直後に古い局面を解析する経路ができる。
func (s *StudyService) positionSFEN() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos == nil {
		return "", fmt.Errorf("解析する局面がありません。訂正タブで「この局面を解析する」を押してください")
	}
	return s.pos.SFEN()
}

// state はロックを取った状態で呼ぶこと。
func (s *StudyService) state() StudyState {
	if s.pos == nil {
		return StudyState{Hands: []position.Stock{}, Warnings: []string{}}
	}
	full, _ := s.pos.SFEN() // Adopt が通っている以上ここは埋まる
	warnings := s.pos.Warnings()
	if warnings == nil {
		warnings = []string{}
	}
	return StudyState{
		Loaded:     true,
		BoardSFEN:  s.pos.BoardSFEN(),
		SFEN:       full,
		Turn:       int(s.pos.Turn),
		TurnLabel:  s.pos.Turn.String(),
		MoveNumber: s.pos.MoveNumber,
		Hands:      s.pos.Inventory(),
		Warnings:   warnings,
	}
}
