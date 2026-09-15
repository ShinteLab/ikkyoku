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
	"strings"
	"time"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/ikkyoku/position"
)

// followTimeout は経路探索に使ってよい時間。
//
// ⚠️ **追従は 1 秒ごとに回る**ので、ここが長いと**1 tick がまるごと止まる**
// （実機で 3 秒に張り付いた）。**深いところを探すのは「2 手以上飛んだとき」だけ**
// なので、**見切って候補を並べるほうが早い。**
//
// ⚠️ **上限は節点数でも掛かっている**（`position.DefaultMaxNodes`）。
// こちらは「1 節点が思ったより重かったとき」の保険。
const followTimeout = 1500 * time.Millisecond

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
	// FollowUnreadable は**そもそも盤として読めていない**（CM・解説の画面）。
	//
	// ⚠️ **`FollowUnreachable` と混ぜないこと。** あちらは「盤は読めているが
	// 本譜から繋がらない」で、**次にすることが違う**（直す / 撮り直す）。
	FollowUnreadable = "unreadable"
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

// FollowFix は**認識を覆した 1 マス**（修復したときだけ出る）。
//
// ⚠️ **必ず画面に出すこと。** 黙って直すと、盤に出ている局面が「撮ったもの」なのか
// 「こちらが直したもの」なのか区別が付かなくなる。
type FollowFix struct {
	// Rank / File は盤の座標（rank 0 が一段目、file 0 が 9 筋）。
	//
	// ⚠️ **盤の上で光らせるのに要る**（`Square` は読ませる用で、位置決めには使えない）。
	Rank int `json:"rank"`
	File int `json:"file"`
	// Square は「９三」のようなマスの名前。
	Square string `json:"square"`
	// Was は認識結果（覆される前）。Now は覆した後。
	Was string `json:"was"`
	Now string `json:"now"`
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
	// Fixed は**認識を覆したマス**（覆していなければ空）。
	//
	// ⚠️ **空でないなら、繋いだ先の盤は「撮ったもの」ではない。**
	// **画面に出すこと**（黙って直さない）。
	Fixed []FollowFix `json:"fixed"`
	// Guess は**ぴったり一致したのではなく、候補の中で一番よく合うものを選んだ**か。
	//
	// ⚠️ **画面で言い分けること。** 「本譜の 1 手先です」と
	// 「おそらく〜です」は**確かさが違う**ので、同じ言い方にしない。
	Guess bool `json:"guess"`
	// Fit は 1 位の候補が撮った盤面をどれくらい説明できているか（0〜1）。
	Fit float64 `json:"fit"`
	// Mismatch は**本譜から説明できなかったマス**（繋がらなかったときだけ）。
	//
	// ⚠️ **これを返さないと「繋がりません」としか言えない。** どこがどう違うのかが
	// 分からなければ**人は 81 マスを端から見直すことになる**。
	// `Was` が認識結果、`Now` が**本譜から辿るとそうなるはずの駒**なので、
	// **そのまま直し方の指示になる。**
	Mismatch []FollowFix `json:"mismatch"`
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
	// ⚠️ **修復は認識を通った盤のときだけ**（手合割・詰将棋では費用表が無い）。
	// 人が並べた盤を機械が覆す理由は無いので、そちらは厳密一致のまま。
	cost, fromImage := s.src.followCost()
	return s.followProbe(p.Board, cost, fromImage, rotated)
}

// followProbe は下見の本体。**盤と費用表をもらうだけで、どこから来たかを知らない。**
//
// ⚠️ **訂正タブを読むのは呼び出し側**（`FollowProbe`）。⚠️ **追従（`FollowAuto`）
// からは訂正タブを通さないこと**（2026-09-15 に実機で踏んだ）——
// 以前は 1 周ごとに `PositionService.Load` を呼んでいたので、
// **人が訂正タブで作業していると 1 秒ごとに中継の盤で上書きされた。**
//
// fromImage は**推測してよいか**（人が並べた盤を機械が推測で直す理由は無い）。
func (s *StudyService) followProbe(board *position.Board, cost *position.CellCost, fromImage, rotated bool) (FollowProbe, error) {
	if board == nil {
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

	// ⚠️ **`fromImage` が「推測してよいか」の唯一の判断。**
	opt := position.ConnectOptions{}
	if fromImage {
		opt.Cost, opt.Tolerance = cost, position.DefaultTolerance
	}

	out := FollowProbe{Rev: rev, Candidates: []FollowChoice{}}

	// ⚠️ **まず「どの候補が一番よく合うか」を聞く**（2026-09-15 に順番を入れ替えた）。
	//
	// **中継は 0 手か 1 手しか進まないのが普通**で、そこは `Rank`（実測 29µs）だけで
	// 片が付く。⚠️ **以前は先に `Connect` を回しており、中盤の局面では 3 秒の
	// タイムアウトに当たったうえ、打ち切りでは推測へ進まない作りだったので
	// 追跡が無反応になった**（実機のログ: `stop=探し切れませんでした nodes=10021`）。
	//
	// ⚠️ **厳密一致が弱くなるわけではない** —— ぴったり合う候補は費用 0 で必ず
	// 1 位になる。`Connect` に残るのは**2 手以上飛んだとき**の仕事だけ。
	var ranked position.RankResult
	if fromImage {
		got, rerr := position.Rank(from, board, opt)
		if rerr == nil && len(got.Candidates) > 0 {
			ranked = got
			top := got.Candidates[0]
			out.Fit = top.Fit
			s.logger.Info("候補を並べました", "tip", tipID, "moves", top.Moves,
				"fit", top.Fit, "margin", got.Margin, "cost", top.Cost, "rotated", rotated)

			// ⚠️ **盤として読めていないならここで終わり**（CM・解説の画面）。
			// 深く探しても意味が無いので `Connect` へ進まない。
			if !got.Readable(position.DefaultMinFit) {
				out.Kind = FollowUnreadable
				out.Reason = fmt.Sprintf("この画面は今の対局の盤面として読めません（一致 %d%%）。"+
					"盤が映っている場面で撮り直してください", int(top.Fit*100))
				return out, nil
			}
			// ⚠️ **「何も動いていない」が 1 位なら、差は問わずにそこで終わり**
			// （2026-09-15 に実機のログで気づいた）。長考中は**毎 tick これ**になる。
			//
			// **足せる手が無いのに深い探索へ進む理由が無い** —— 進んでも
			// 「隔たりが大きすぎます」が返るだけで、**1 tick まるごと無駄**。
			//
			// ⚠️ **差が小さいからといって「決められない」に落とさないこと。**
			// 動いていないのが一番よく合うなら、**手を足さないのが正解**で、
			// 迷う余地は無い（**足す側にだけ差を要求する**）。
			if len(top.Moves) == 0 {
				out.Kind, out.Reason = FollowSame, "盤面は変わっていません"
				return out, nil
			}
			if got.Decided(position.DefaultMinFit, position.DefaultMinMargin) {
				// ⚠️ **費用 0 は推測ではない**（認識とぴったり合っている）。
				out.Kind, out.Depth, out.Guess = FollowUnique, len(top.Moves), top.Cost > 0
				out.Fixed = followFixes(top.Fixed)
				out.Candidates = []FollowChoice{{
					Moves: top.Moves,
					Text:  followText(rootSfen, main, top.Moves),
				}}
				return out, nil
			}
		}
	}

	// **1 手では決まらなかった。** ここから先は「何手進んだのか」を探す仕事
	// （CM を挟んで飛んだとき）。⚠️ **時間がかかるのはこちらだけ。**
	ctx, cancel := context.WithTimeout(context.Background(), followTimeout)
	defer cancel()
	r, err := position.Connect(ctx, from, board, opt)
	if err != nil {
		return FollowProbe{}, err
	}
	s.logger.Info("本譜の先に繋げるか下見しました",
		"tip", tipID, "stop", r.Stop.String(), "depth", r.Depth,
		"candidates", len(r.Solutions), "nodes", r.Nodes,
		"cost", r.Cost, "fixed", len(r.Fixed), "rotated", rotated)

	out.Depth = r.Depth
	switch r.Stop {
	case position.StopSame:
		out.Kind, out.Reason = FollowSame, "盤面は変わっていません"
		return out, nil
	case position.StopBudget, position.StopTooFar, position.StopUnreachable:
		// ⚠️ **ここで行き止まりにしないこと**（2026-09-15 に実機で踏んだ）。
		// 打ち切り（`StopBudget`）でも**先に並べた候補は残っている**ので、それを出す。
		// **黙って無反応になるのが一番たちが悪い。**
		return s.fallbackFollow(ranked, r, rootSfen, main, fromImage, out), nil
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
		out.Kind, out.Fixed = FollowUnique, followFixes(r.Fixed)
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

	g := s.study.GraftGuess(append(s.study.MainLine(), moves...), len(moves))
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

// unreachableReason は「繋がらない」を**どこが説明できないか**まで含めて言う。
//
// ⚠️ **「繋がりません」だけで済ませないこと。** それでは人はどこを直せばよいか
// 分からず、**81 マスを端から見直すことになる**（実機でそうなった）。
func unreachableReason(miss []FollowFix, depth int) string {
	if len(miss) == 0 {
		return "今の本譜からは繋がりません（認識の誤りか、別の対局かもしれません）"
	}
	// **多いときは全部並べない**（読めなくなるので、頭の 3 つと件数）。
	const show = 3
	parts := make([]string, 0, show)
	for i, m := range miss {
		if i == show {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s→%s", m.Square, m.Was, m.Now))
	}
	more := ""
	if len(miss) > show {
		more = fmt.Sprintf(" ほか%dマス", len(miss)-show)
	}
	at := "本譜の先端"
	if depth > 0 {
		at = fmt.Sprintf("本譜の%d手先", depth)
	}
	return fmt.Sprintf("今の本譜からは繋がりません。%sと%dマス食い違っています（%s%s）。"+
		"「→」の右が本譜から辿ったときの駒です",
		at, len(miss), strings.Join(parts, "、"), more)
}

// followFixes は覆したマスを画面に出せる形にする。
//
// ⚠️ **マスの名前を作っているだけで、棋譜の表記ではない**（指し手の表記は
// `core/kifu` の仕事。**そちらをここに書き写さないこと**）。
func followFixes(in []position.Fix) []FollowFix {
	out := make([]FollowFix, 0, len(in))
	for _, f := range in {
		out = append(out, FollowFix{
			Rank:   f.Rank,
			File:   f.File,
			Square: squareText(f.Rank, f.File),
			Was:    f.Was.Name(),
			Now:    f.Now.Name(),
		})
	}
	return out
}

// squareText は盤の座標を「９三」にする（file 0 が 9 筋、rank 0 が一段目）。
func squareText(rank, file int) string {
	files := [...]string{"９", "８", "７", "６", "５", "４", "３", "２", "１"}
	ranks := [...]string{"一", "二", "三", "四", "五", "六", "七", "八", "九"}
	if rank < 0 || rank > 8 || file < 0 || file > 8 {
		return "?"
	}
	return files[file] + ranks[rank]
}

// fallbackFollow は `Connect` が決められなかったときの出口。
//
// ⚠️ **黙って無反応にしないこと**（2026-09-15 に実機で踏んだ）。打ち切りでも
// **先に並べた候補は残っている**ので、それを出す。「何も起きない」が一番たちが悪い。
func (s *StudyService) fallbackFollow(ranked position.RankResult, r position.ConnectResult,
	rootSfen string, main []string, fromImage bool, out FollowProbe) FollowProbe {
	// 人が並べた盤（手合割・詰将棋）は推測しない。食い違いだけ出す。
	if !fromImage || len(ranked.Candidates) == 0 {
		out.Kind = FollowUnreachable
		out.Mismatch = followFixes(r.Near)
		out.Reason = unreachableReason(out.Mismatch, r.NearDepth)
		return out
	}
	out.Guess = true
	out.Mismatch = followFixes(ranked.Candidates[0].Fixed)
	for _, c := range ranked.Candidates {
		if len(c.Moves) == 0 {
			continue // 「何もしない」は繋ぐ選択肢ではない
		}
		out.Candidates = append(out.Candidates, FollowChoice{
			Moves: c.Moves,
			Text:  followText(rootSfen, main, c.Moves),
		})
	}
	if len(out.Candidates) == 0 {
		out.Kind = FollowUnreachable
		out.Reason = "今の本譜からは繋がりません"
		return out
	}
	out.Kind = FollowChoices
	out.Reason = "どの手が指されたか決められません（認識が曖昧か、2 手以上進んでいます）"
	return out
}

// FollowAuto は**下見して、繋がるなら人に聞かずに繋ぐ**（中継の追従。2026-09-15）。
//
// ⚠️ **ここが「録画」の 1 手ぶん。** 訂正タブを挟むかどうかを**こちらが決めない**
// のが要点で、**おかしくてもとにかく進む**（判断するのは人で、そのために
// 戻れるようにしてある）。
//
// ⚠️ **進めないのは「情報が無いとき」だけ。** 盤が映っていない（CM・解説）・
// 盤面が変わっていない・どの手か決められない —— どれも**正しさの判断ではなく、
// 足せる手が無いという事実**。⚠️ **ここに「怪しいから止める」を足さないこと。**
//
// ⚠️ **見ている場所は「先端を見ていたときだけ」動かす。** 戻って検討している
// 最中に飛ばされると、**中継が進むたびに読んでいた枝から引き剥がされる**。
func (s *StudyService) FollowAuto(boardSFEN string, cellConfidence []float64) (a FollowAuto, err error) {
	// ⚠️ **訂正タブを通さないこと**（2026-09-15 に実機で踏んだ）。
	// 以前は呼び出し側が毎周 `PositionService.Load` を呼んでいたので、
	// **人が訂正タブで作業していると 1 秒ごとに中継の盤で上書きされた**
	// （学習データを登録しようとして消えた）。**訂正タブは人の作業場。**
	board, cost, rotated, e := s.src.followFrame(boardSFEN, cellConfidence)
	if e != nil {
		return FollowAuto{}, e
	}
	// ⚠️ **修復してよいのは費用表があるときだけ**（`followCost` と同じ約束）。
	// 確信度が無ければ**どのマスを覆してよいかの根拠が無い**ので、厳密一致に倒す。
	p, e := s.followProbe(board, cost, cost != nil, rotated)
	if e != nil {
		return FollowAuto{}, e
	}
	a = FollowAuto{Kind: p.Kind, Reason: p.Reason, Guess: p.Guess, Fit: p.Fit,
		Fixed: p.Fixed, Mismatch: p.Mismatch}
	if p.Kind != FollowUnique || len(p.Candidates) == 0 {
		// **足せる手が無い**（変わっていない／読めない／決められない）。
		a.State = s.State()
		return a, nil
	}

	// ⚠️ **先端を見ていたなら付いていく**（`tail -f` と同じ）。
	// 戻って読んでいるなら**動かさない**（`mergeReloadLocked` と同じ約束）。
	s.mu.Lock()
	atTip := s.study != nil && s.study.CurrentID() == mainTipID(s.study)
	s.mu.Unlock()

	moves := p.Candidates[0].Moves
	applied, err := s.FollowApply(moves, p.Rev, atTip)
	if err != nil {
		return a, err
	}
	a.Applied, a.Moves, a.Added = true, moves, applied.Added
	a.Text = p.Candidates[0].Text
	a.Note, a.State = applied.Note, applied.State
	return a, nil
}

// FollowAuto は 1 手ぶんの追従の結果。
type FollowAuto struct {
	// Applied は手を足したか（**偽でも失敗ではない** —— 足せる手が無かっただけ）。
	Applied bool `json:"applied"`
	// Moves / Text は足した手（USI と日本語表記）。
	Moves []string `json:"moves"`
	Text  []string `json:"text"`
	// Added は新しく生えた手数。
	Added int `json:"added"`
	// Kind は下見の結果（`FollowUnique` 以外なら足していない）。
	Kind string `json:"kind"`
	// Guess は推測で選んだか。⚠️ **手順にも印が付く**（`Node.Guess`）。
	Guess bool `json:"guess"`
	// Fit は盤面の一致度（0〜1）。
	Fit float64 `json:"fit"`
	// Fixed は認識を覆したマス。Mismatch は説明できなかったマス。
	Fixed    []FollowFix `json:"fixed"`
	Mismatch []FollowFix `json:"mismatch"`
	// Reason は足せなかった理由（足したなら空）。
	Reason string `json:"reason"`
	// Note は本譜を押しのけたときの断り。
	Note string `json:"note"`
	// State は結果の解析タブの状態。**そのまま描ける。**
	State StudyState `json:"state"`
}
