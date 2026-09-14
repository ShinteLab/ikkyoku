package app

// 「撮った盤面を、今の本譜の先に繋ぐ」（Phase 6 の入口。2026-09-14）。
//
// 中継を観ながら数手ごとに撮り直すと、認識で分かるのは**盤面だけ**。
// そこから手順を割り出すのは `position.Connect` の仕事で、ここはその口。
//
// ⚠️ **`Adopt`（この局面を解析する）とは別物。** あちらは**根ごと入れ替える**
// （前の手順も評価値も捨てる）。こちらは**本譜の先へ足す**ので、
// **ユーザーの検討枝も評価値も残る**。押し間違えると検討が消えるので、
// **画面でも別のボタンにしてある。**
//
// ⚠️ **2 段になっているのは、順番が決められないことがあるから。**
// `FollowProbe` は木を 1 手も触らずに候補だけ返し、`FollowApply` で初めて据える。
// **自動で 1 本選ばないこと** —— 選んだ手順は「実際に現れた指し手」として棋譜に残る
// （`TODO.md`「本譜のロック」が一番避けたい壊れ方）。

import (
	"context"
	"fmt"
	"time"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/ikkyoku/position"
)

// followTimeout は経路探索に使ってよい時間。
//
// 実測では深さ 4 で 1ms 足らずだが、**上限は節点数（`position.DefaultMaxNodes`）で
// 掛かっている**ので、これは「万一そこが効かなかったとき」の保険。
const followTimeout = 3 * time.Second

// 繋ぎ方の種類（`FollowProbe.Kind`）。**画面の出し分けはこれ 1 つで決まる。**
const (
	// FollowSame は盤面が変わっていない（何もすることが無い）。
	FollowSame = "same"
	// FollowUnique は手順が 1 本に決まった（**そのまま据えてよい**）。
	FollowUnique = "unique"
	// FollowChoices は複数の順番があり得る（**人に選ばせる**）。
	FollowChoices = "choices"
	// FollowUnreachable は今の本譜からは繋がらない（認識の誤りか、別の対局）。
	FollowUnreachable = "unreachable"
	// FollowBudget は探し切れなかった（**到達不能とは違う**。もう一度試してよい）。
	FollowBudget = "budget"
)

// FollowChoice は繋ぎ方の候補 1 本。
type FollowChoice struct {
	// Moves は手順（USI）。**`FollowApply` にそのまま渡す。**
	Moves []string `json:"moves"`
	// Text は日本語表記（"▲７六歩" など）。**画面に並べるのはこちら。**
	//
	// ⚠️ **フロントで組み立て直さないこと** —— 表記は `core/kifu` の仕事で、
	// "同" も "右左上引" も**そこまでの手順が分かっていないと決まらない**。
	Text []string `json:"text"`
}

// FollowProbe は「繋げるか」の下見。**木は 1 手も触っていない。**
type FollowProbe struct {
	// Kind は上の 5 つのどれか。
	Kind string `json:"kind"`
	// Candidates は繋ぎ方の候補（`FollowUnique` なら 1 本、`FollowChoices` なら複数）。
	Candidates []FollowChoice `json:"candidates"`
	// More は候補が多すぎて並べきれなかったか。
	//
	// ⚠️ **立ったら選ばせないこと**（選択肢として出せない ＝ 決めようが無い）。
	More bool `json:"more"`
	// Rev は下見したときの版。**据えるときの合鍵**（`FollowApply`）。
	//
	// ⚠️ **下見と据えるのは別の呼び出し**なので、その間に別の窓が手を指しうる。
	// 食い違ったら断る（**違う局面の先に繋がないため**）。
	Rev int `json:"rev"`
	// Depth は繋がる手数（`FollowSame` なら 0）。
	Depth int `json:"depth"`
	// Reason は繋がらなかった理由（繋がるなら空）。**そのまま画面に出す文。**
	Reason string `json:"reason"`
}

// FollowApplied は据えた結果。
type FollowApplied struct {
	// State は据えたあとの解析タブの状態。**そのまま描ける。**
	State StudyState `json:"state"`
	// Added は新しく生えた手数（既にあった手を辿っただけなら 0）。
	Added int `json:"added"`
	// Note は本譜を押しのけたときの断り（押しのけていなければ空）。
	Note string `json:"note"`
}

// FollowProbe は訂正タブの盤面を**今の本譜の先**に繋ぐ候補を探す。
//
// ⚠️ **木を 1 手も触らない**（`study:changed` も出さない）。据えるのは `FollowApply`。
//
// ⚠️ **繋ぎ先は本譜の先端（`MainTip`）で、「今見ている局面」ではない。**
// ユーザーが枝の途中を読んでいるのは普通で、そこへ繋ぐと**検討の枝に中継の手が生える**。
//
// ⚠️ **`Adopt` と違って局面の確定を要求しない。** 要るのは盤面だけで、
// **手番も駒台の先後も経路から決まる**（`position.Connect`）。
// **これが Phase 6 の要点** —— 追従しながら人が駒台を埋めるのは不可能なので、
// ここで確定を求めると自動化そのものが成り立たない。
func (s *StudyService) FollowProbe() (FollowProbe, error) {
	// ⚠️ **向きはここで直る**（`Adopt` と同じ写し・同じ 1 か所）。
	// 撮った画像が後手目線なら盤も先後も 180 度回る。
	p, rotated := s.src.adoptPosition()
	if p == nil {
		return FollowProbe{}, fmt.Errorf("まだ局面がありません")
	}

	s.mu.Lock()
	if s.study == nil {
		s.mu.Unlock()
		return FollowProbe{}, fmt.Errorf("解析タブにまだ局面がありません（先に「この局面を解析する」で始めてください）")
	}
	tipID, from, err := s.study.MainTip()
	if err != nil {
		s.mu.Unlock()
		return FollowProbe{}, err
	}
	rev, main, rootSfen := s.rev, s.study.MainLine(), rootSFEN(s.study)
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), followTimeout)
	defer cancel()
	r, err := position.Connect(ctx, from, p.Board, position.ConnectOptions{})
	if err != nil {
		return FollowProbe{}, err
	}
	s.logger.Info("本譜の先に繋げるか下見しました",
		"tip", tipID, "stop", r.Stop.String(), "depth", r.Depth,
		"candidates", len(r.Solutions), "nodes", r.Nodes, "rotated", rotated)

	out := FollowProbe{Rev: rev, Depth: r.Depth, Candidates: []FollowChoice{}}
	switch r.Stop {
	case position.StopSame:
		out.Kind, out.Reason = FollowSame, "盤面は変わっていません"
		return out, nil
	case position.StopBudget:
		out.Kind, out.Reason = FollowBudget, "手順を探し切れませんでした。もう一度お試しください"
		return out, nil
	case position.StopTooFar, position.StopUnreachable:
		// ⚠️ **ここで木を触らない。** 撮った 1 枚は訂正タブに残っているので、
		// **「この局面を解析する」で新しく始められる**（設計原則3）。
		out.Kind = FollowUnreachable
		out.Reason = "今の本譜からは繋がりません（認識の誤りか、別の対局かもしれません）"
		return out, nil
	}

	// ⚠️ **並べきれないなら選ばせない。** 候補として出せない以上、決めようが無い。
	if r.More {
		out.Kind, out.More = FollowUnreachable, true
		out.Reason = fmt.Sprintf("%d手ぶん進んだようですが、順番の候補が多すぎて決められません", r.Depth)
		return out, nil
	}
	for _, moves := range r.Solutions {
		out.Candidates = append(out.Candidates, FollowChoice{
			Moves: moves,
			Text:  followText(rootSfen, main, moves),
		})
	}
	if r.Unique {
		out.Kind = FollowUnique
		return out, nil
	}
	out.Kind = FollowChoices
	out.Reason = fmt.Sprintf("%d手ぶん進んだようですが、順番が決められません", r.Depth)
	return out, nil
}

// FollowApply は選んだ手順を**本譜の先**に据える。
//
// ⚠️ **据え方は `Graft` 1 か所**（URL の取り直しと同じ意味論）。**消さずに据える**ので、
// ユーザーの検討枝も評価値もそのまま残る。
//
// ⚠️ **`evals` を触らないこと。** `reset` も `dropAfter` も世代を進めるので、
// **走っている解析の途中経過が捨てられる**（中継を追うたびにそれが起きる）。
//
// goTo は**繋いだ先へ移るか**。⚠️ **手で押したときだけ真にすること** ——
// 自動で追い続ける段（Phase 6 の本体）では**見ている場所を勝手に動かさない**
// （`mergeReloadLocked` と同じ約束）。
func (s *StudyService) FollowApply(moves []string, rev int, goTo bool) (a FollowApplied, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO）。
	defer func() { s.publish(a.State, err) }()
	if len(moves) == 0 {
		return FollowApplied{}, fmt.Errorf("繋ぐ手順がありません")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return FollowApplied{}, fmt.Errorf("解析タブにまだ局面がありません")
	}
	// ⚠️ **下見のあいだに局面が変わっていたら断る。** 別の窓が手を指したかもしれず、
	// そのまま据えると**違う局面の先に繋がる**。
	if rev != s.rev {
		return FollowApplied{}, fmt.Errorf("局面が変わりました。もう一度お試しください")
	}

	g := s.study.Graft(append(s.study.MainLine(), moves...))
	if g.Note != "" && g.Added == 0 {
		return FollowApplied{}, fmt.Errorf("%s", g.Note)
	}
	if goTo {
		// **手で押した操作なので、繋いだ先を見せる。**
		if id, _, err := s.study.MainTip(); err == nil {
			_ = s.study.GoTo(id)
		}
	}
	a = FollowApplied{State: s.changed(), Added: g.Added, Note: reloadNote(g)}
	s.logger.Info("撮った盤面を本譜の先に繋ぎました",
		"moves", len(moves), "added", g.Added, "movedAt", g.MovedAt, "goTo", goTo)
	return a, nil
}

// followText は候補の手順を日本語表記にする。
//
// ⚠️ **根から本譜を辿り直してから変換すること。** "同" は直前の手の移動先で決まるし、
// "右左上引" もそこまでの盤が要る。**候補ごとに作り直す**（`Notation` は進むので
// 使い回せない）。**読めなかったら USI のまま出す**（表記が出ないだけ。設計原則3）。
func followText(rootSfen string, main, moves []string) []string {
	out := make([]string, 0, len(moves))
	nt, err := kifu.NewNotation(rootSfen)
	if err != nil {
		nt = nil
	}
	if nt != nil {
		for _, mv := range main {
			if _, err := nt.Next(mv); err != nil {
				nt = nil
				break
			}
		}
	}
	for _, mv := range moves {
		text := mv
		if nt != nil {
			if t, err := nt.Next(mv); err == nil && t.Text != "" {
				text = t.Text
			} else if err != nil {
				nt = nil
			}
		}
		out = append(out, text)
	}
	return out
}
