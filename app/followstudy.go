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
	"slices"
	"strings"
	"time"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/ikkyoku/log"
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
	// Unsettled は**行き先が決まっていない候補**を並べたか（`fallbackFollow` だけが立てる）。
	//
	// ⚠️ **`FollowChoices` には 2 つの意味がある。** `Connect` の解（行き着く局面は同じで、
	// 順番だけが決まらない）と、`Rank` の 1 手の候補（**行き着く局面がそれぞれ違う**）。
	// **追従が聞かずに進んでよいのは前者だけ**（2026-10-05 に実機で踏んだ。指している手が
	// 盤を覆っている 1 枚から、差が 1 マスぶんも無い 1 位を採って空想の手を足した）。
	// 画面には出さない（手で繋ぐ側は、どちらでも人に選ばせる）。
	Unsettled bool `json:"-"`
	// Unexplained は**駒が来たマスがあるのに、今の向きでは何手先まで探しても説明が付かなかった**か
	// （`followArrived` で先を探し、繋がらずに「変わっていない」へ戻したときだけ立つ。2026-10-06）。
	// ⚠️ **「盤が上下逆」を疑うのはこのときだけ**（`looksFlipped`）。画面には出さない。
	Unexplained bool `json:"-"`
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
	// ⚠️ **見えないマスは渡さない**（nil）。訂正タブの盤は人が見て直したもので、
	// 被っていたマスも人が決めている。
	return s.followProbe(p.Board, cost, nil, fromImage, rotated)
}

// followProbe は下見の本体。**盤と費用表をもらうだけで、どこから来たかを知らない。**
//
// ⚠️ **訂正タブを読むのは呼び出し側**（`FollowProbe`）。⚠️ **追従（`FollowAuto`）
// からは訂正タブを通さないこと**（2026-09-15 に実機で踏んだ）——
// 以前は 1 周ごとに `PositionService.Load` を呼んでいたので、
// **人が訂正タブで作業していると 1 秒ごとに中継の盤で上書きされた。**
//
// fromImage は**推測してよいか**（人が並べた盤を機械が推測で直す理由は無い）。
//
// unseen は**手や頭が被って見えないマス**（nil なら全部見えている。2026-10-06）。
// ⚠️ **推測するかどうか（fromImage）とは別に効かせること** —— 見えないマスは
// 「覆す」話ではなく、そもそも根拠にも減点にもしないマス（`position.CellMask`）。
func (s *StudyService) followProbe(board *position.Board, cost *position.CellCost, unseen *position.CellMask, fromImage, rotated bool) (FollowProbe, error) {
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
	opt := position.ConnectOptions{Unseen: unseen}
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
	// arrived は「何も指していない」が 1 位なのに、駒が来たマスがあったので先を探しに行ったか。
	arrived := false
	if fromImage {
		got, rerr := position.Rank(from, board, opt)
		if rerr == nil && len(got.Candidates) > 0 {
			ranked = got
			top := got.Candidates[0]
			out.Fit = top.Fit
			log.Info("候補を並べました", "tip", tipID, "moves", top.Moves,
				"fit", top.Fit, "margin", got.Margin, "cost", top.Cost, "rotated", rotated,
				"unseen", unseen.Count())

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
			//
			// ⚠️ **ただし駒が来たマスがあるなら、何かは指されている**（2026-10-06 に実機で踏んだ。
			// `followArrived`）。指して 1 秒で取り返された 1 枚（▲2四歩 △同歩）は歩が 1 マス
			// ずれたようにしか見えず、1 手の候補より「何も指していない」のほうがよく合う。
			// そこで終えると 2 手先を探しに行かず、見送り続けて見失った。`Connect` へ進む。
			if len(top.Moves) == 0 {
				if !followArrived(top.Fixed, cost) {
					out.Kind, out.Reason = FollowSame, "盤面は変わっていません"
					return out, nil
				}
				arrived = true
				log.Info("何も指していないが 1 位ですが、駒が来たマスがあるので先を探します",
					"tip", tipID, "rotated", rotated)
			} else if got.Decided(position.DefaultMinFit, position.DefaultMinMargin) {
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
	log.Info("本譜の先に繋げるか下見しました",
		"tip", tipID, "stop", r.Stop.String(), "depth", r.Depth,
		"candidates", len(r.Solutions), "nodes", r.Nodes,
		"cost", r.Cost, "fixed", len(r.Fixed), "rotated", rotated)

	out.Depth = r.Depth
	// ⚠️ **駒が来たので探しに来ただけなら、足せる答えが出なければ元の「変わっていない」に戻すこと**
	// （`followArrived`）。読みの揺れで来たように見えただけの 1 枚を「繋がらない」にすると、
	// 「見失っています」に数えられる。⚠️ **「何手か進んだが順番が多すぎる」（`More`）も同じ** ——
	// 盤が上下逆に映っていると、片側が行って戻る手を挟んで説明が付いてしまい、これになる。
	// そのときは `Unexplained` を立てる（「盤が上下逆」を疑う合図。`looksFlipped`）。
	if arrived && (r.Stop != position.StopFound || r.More) {
		out.Kind, out.Reason, out.Unexplained = FollowSame, "盤面は変わっていません", true
		out.Depth = 0
		return out, nil
	}
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

// followArrivedMinCost は「駒が来た」と言い切る、そのマスの読みの確かさ（費用表の値）の下限。
//
// suteme は空きを確信度 0（費用は下限の 0.15）で返し、駒のマスは駒の確信度で返す。
// ⚠️ **当て推量**（2026-10-06）。下げすぎると、中継の読みの揺れのたびに深い探索
// （`Connect`。打ち切りは `followTimeout`）を回すことになる。
const followArrivedMinCost = 0.5

// followArrived は「何も指していない」との食い違いに、**指された跡**があるかを返す（2026-10-06）。
//
// 跡と見なすのは 2 通り。どちらも**読みの確かなマスだけ**（`followArrivedMinCost`）:
//
//   - **駒が来た**: 読んだ盤では駒があるのに、今の局面では空きか相手の駒
//     （`position.backed` と同じく先後まで）。▲2四歩 △同歩 を 1 枚で撮ると、2四 に後手の歩が来る
//   - **同じ側の別の駒に入れ替わり、しかも空いたマスがある**: ▲2四歩 のあと △同歩 ▲同飛 を
//     1 枚で撮ると、2四 は先手の歩が先手の飛車になっただけで、残りは 2三・2八 が空いたことだけ。
//     ⚠️ **種類の変化だけでは数えない**（中継は種類を外しやすい。銀を香と読む）。
//     指された手があるなら、どこかの駒は元のマスを空けている
//
// ⚠️ **空いただけは数えない**（空きは手や影で簡単に作られる）。
// 費用表が無いときは厳密一致の側なので、ここには来ない。
func followArrived(fixed []position.Fix, cost *position.CellCost) bool {
	if cost == nil {
		return false
	}
	sure := func(f position.Fix) bool { return cost[f.Rank][f.File] >= followArrivedMinCost }
	vacated, swapped := false, false
	for _, f := range fixed {
		switch {
		case f.Was.IsEmpty():
			if !f.Now.IsEmpty() {
				vacated = true
			}
		case f.Now.IsEmpty() || f.Now.Black() != f.Was.Black():
			if sure(f) {
				return true
			}
		case f.Now != f.Was:
			if sure(f) {
				swapped = true
			}
		}
	}
	return swapped && vacated
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
	log.Info("撮った盤面を本譜の先に繋ぎました",
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
	out.Guess, out.Unsettled = true, true
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
//
// ⚠️ **1 枚から取れるだけ取ること**（2026-09-15 に実機で踏んだ）。**認識 1 枚に
// 2.1 秒かかる**ので、**1 周 1 手にすると 3 秒に 1 手しか進めない** ——
// ゲーム画面はそれより速く進むので、**遅れが一方的に開いて 4 手を超えた時点で
// 永久に復帰できなくなった**（実機で 4 手目以降 90 手まで無反応）。
//
// **撮った 1 枚には「何手ぶんも先」が写っている。** それを 1 手だけ取り出して
// 残りを捨て、また 2 秒かけて撮り直すのは無駄でしかない。
// ⚠️ **1 手ごとの判断の厳しさは変えていない** —— 同じ `followProbe` を
// 同じ盤に対して繰り返すだけで、**決められなくなった時点で止まる**。
//
// ⚠️ **1 枚だけでは足さない**（2026-10-05。`followPending`）。足せる手が見つかったら
// 控えておき、**次の 1 枚が同じ答えを出したとき**（`FollowAuto`）か、**次の 1 枚が
// 変わっていなかったとき**（`FollowConfirm`）に初めて足す。次の 1 枚が**先へ進んでいたら、
// 2 枚で揃った頭の手だけ足して残りを控え直す**（2026-10-06。`followConfirmed`）。
//
// cellHidden は**手や頭が被って見えないマス**（suteme の `CellDebug.Hidden`。81 個・行優先。2026-10-06）。
// 見えないマスは根拠にも減点にもせず、行き先が見えない手は足さずに待つ（`position.CellMask`）。
// ⚠️ **無くても動くこと**（nil・数が合わない → 全部見えていることにする。今までどおり）。
func (s *StudyService) FollowAuto(boardSFEN string, cellConfidence []float64, cellHidden []bool) (FollowAuto, error) {
	// ⚠️ **訂正タブを通さないこと**（2026-09-15 に実機で踏んだ）。
	// 以前は呼び出し側が毎周 `PositionService.Load` を呼んでいたので、
	// **人が訂正タブで作業していると 1 秒ごとに中継の盤で上書きされた**
	// （学習データを登録しようとして消えた）。**訂正タブは人の作業場。**
	board, cost, unseen, rotated, e := s.src.followFrame(boardSFEN, cellConfidence, cellHidden)
	if e != nil {
		return FollowAuto{}, e
	}
	// ⚠️ **修復してよいのは費用表があるときだけ**（`followCost` と同じ約束）。
	// 確信度が無ければ**どのマスを覆してよいかの根拠が無い**ので、厳密一致に倒す。
	p, e := s.followProbe(board, cost, unseen, cost != nil, rotated)
	if e != nil {
		return FollowAuto{}, e
	}
	moves, ok := followAddable(p)
	if !ok {
		// ⚠️ **控えと矛盾しない 1 枚なら、控えは捨てない**（2026-10-06 に実機で踏んだ。`pendingStillFits`）。
		if s.pendingStillFits(board, cost, unseen) {
			return FollowAuto{Kind: p.Kind, Pending: true, Reason: "確かめています",
				Fit: p.Fit, Unseen: unseen.Count(), State: s.State()}, nil
		}
		// **足せる手が無い 1 枚が来たら、控えは捨てる**（手が映った 1 枚の答えは
		// 次の 1 枚で消えるのが普通で、それがこの形）。
		s.setPending(nil)
		return s.followFrom(board, cost, unseen, rotated)
	}
	k := s.takePendingPrefix(moves)
	if k == 0 {
		// **初めて出た答え。** 控えて、次の 1 枚を待つ。
		s.setPending(&followPending{tip: s.mainTip(), moves: moves, board: board, cost: cost,
			unseen: unseen, rotated: rotated})
		return FollowAuto{Kind: p.Kind, Pending: true, Reason: "確かめています",
			Guess: p.Guess, Fit: p.Fit, Unseen: unseen.Count(), State: s.State()}, nil
	}
	if k == len(moves) {
		// **2 枚続けて同じ答え**（控えのほうが長ければ、その先は 2 枚目で消えた）。
		// ここから先は今までどおり（1 枚から取れるだけ取る）。
		return s.followFrom(board, cost, unseen, rotated)
	}
	// **2 枚目のほうが先へ進んでいる**（2026-10-06）。2 枚で揃った頭の k 手だけ足し、
	// 残りは控え直して次の 1 枚で確かめる。
	return s.followConfirmed(p, k, board, cost, unseen, rotated)
}

// followConfirmed は**2 枚で揃った頭の k 手だけ**足し、残りを控え直す（2026-10-06）。
//
// **実機の症状**: 5 秒おきの指し手が 2〜3 手続くと見失った。同じ答えを 2 枚に求めると、
// **読み始めから次の 1 枚まで（約 2.75 秒）盤が止まっている**必要があり、そのあいだに
// 次の手（指す手が盤に入るのも含む）が来ると、2 枚目は「1 手先まで進んだ答え」になって
// 控え直しになる。速い手が続くと 1 手も足せないまま遅れが `Connect` の 4 手を超えた。
//
// ⚠️ **足すのは 2 枚が揃って言っている手だけ**（1 枚だけの読み違いを足さない、は崩さない）。
// 2 枚目にしか無い手は控え直すので、それも次の 1 枚で確かめてから入る。
func (s *StudyService) followConfirmed(p FollowProbe, k int, board *position.Board, cost *position.CellCost, unseen *position.CellMask, rotated bool) (FollowAuto, error) {
	s.mu.Lock()
	atTip := s.study != nil && s.study.CurrentID() == mainTipID(s.study)
	s.mu.Unlock()

	top := p.Candidates[0]
	applied, err := s.FollowApply(top.Moves[:k], p.Rev, atTip)
	if err != nil {
		return FollowAuto{}, err
	}
	s.setPending(&followPending{tip: s.mainTip(), moves: slices.Clone(top.Moves[k:]), board: board,
		cost: cost, unseen: unseen, rotated: rotated})
	a := FollowAuto{Kind: p.Kind, Applied: true, Pending: true, Reason: "確かめています",
		Moves: slices.Clone(top.Moves[:k]), Added: applied.Added,
		// ⚠️ **順番が決まらずに選んだ手は推測**（`followFrom` と同じ判断）。
		Guess: p.Guess || p.Kind == FollowChoices,
		Fit:   p.Fit, Fixed: p.Fixed, Mismatch: p.Mismatch, Unseen: unseen.Count(),
		Note: applied.Note, State: applied.State}
	if len(top.Text) >= k {
		a.Text = slices.Clone(top.Text[:k])
	}
	log.Info("2 枚で揃った手だけ足し、残りを確かめます", "added", a.Moves, "pending", top.Moves[k:])
	return a, nil
}

// FollowConfirm は**控えた答えを、撮り直した 1 枚が変わっていなかったことで確かめて**足す（2026-10-05）。
//
// 呼ぶのはフロントで、`CaptureQuiet` が「変わっていない」（`unchanged`）を返したとき。
// ⚠️ **ふるいの「変わっていない」は、控えた 1 枚から何も変わっていないこと**（間に
// 変化があれば、静止したところで読み直しになる）なので、**同じ画像を読み直したのと同じ**
// （認識は同じ画像に同じ答えを返す）。読み直さないぶん、確かめは 1 周（約 0.25 秒）で済む。
//
// 控えが無ければ何もしない（`FollowSame`）。⚠️ **本譜の先端が控えたときから動いていたら捨てる**
// （手で繋いだ・戻したなど。違う局面の先に繋がないため）。
func (s *StudyService) FollowConfirm() (FollowAuto, error) {
	s.mu.Lock()
	pend := s.pending
	s.pending = nil
	s.mu.Unlock()
	if pend == nil || pend.tip != s.mainTip() {
		return FollowAuto{Kind: FollowSame, Reason: "盤面は変わっていません", State: s.State()}, nil
	}
	if pend.board == nil {
		// **速い経路で控えた手**（`FollowCells`）。読んだ盤は無いので、手順をそのまま足す。
		return s.fastApply(pend.moves, true)
	}
	return s.followFrom(pend.board, pend.cost, pend.unseen, pend.rotated)
}

// followCellsDepth は速い経路で探す手数の上限（1 枚に写るのは、早指しでもふつう 2 手まで）。
const followCellsDepth = 2

// FollowCells は**変わったマスだけで手を割り出して**追従する（速い経路。2026-10-07）。
//
// changed は**撮った画像の向き**での 81 マスの番号（行優先。`CaptureResult.Cells`）。比べた相手は
// 本譜の先端とぴったり合っていた 1 枚（`FollowAuto.AtFrame`）。81 マスの推論（2.1 秒）を回さないので、
// **指されてから手が入るまでが「止まったのを確かめる 0.25〜0.5 秒」＋数ミリ秒**になる。
//
// ⚠️ **合う手順が 1 つに決まるときだけ進む**（`position.ChangedMoves`）。成・不成、打った駒の種類、
// 手や頭が被った 1 枚など、決まらなければ何もせずに返す（`Applied` も `Pending` も偽）。
// **フロントはそれを見て、同じ 1 枚を 81 マスで読む**（`CaptureService.RecognizeQuiet` → `FollowAuto`）。
// ⚠️ **1 枚だけでは足さない**のは同じ（`followPending`）。次の 1 枚が変わっていなければ
// `FollowConfirm` で足す。⚠️ **直前の手のマスは、見た目だけ変わってよい**（ゲーム画面の色付け）。
func (s *StudyService) FollowCells(changed []int) (FollowAuto, error) {
	mask := &position.CellMask{}
	for _, i := range changed {
		if i >= 0 && i < 81 {
			mask[i/9][i%9] = true
		}
	}
	// ⚠️ **撮った向きと解析の向きを揃えること**（後手目線で採った局面は 180 度回してある）。
	if s.src.followRotated() {
		mask = mask.Rotate180()
	}

	s.mu.Lock()
	if s.study == nil {
		s.mu.Unlock()
		return FollowAuto{}, fmt.Errorf("解析タブにまだ局面がありません")
	}
	_, from, err := s.study.MainTip()
	main := s.study.MainLine()
	s.mu.Unlock()
	if err != nil {
		return FollowAuto{}, err
	}

	var extra *position.CellMask
	if len(main) > 0 {
		extra = usiCells(main[len(main)-1])
	}
	sols, err := position.ChangedMoves(from, mask, extra, followCellsDepth)
	if err != nil || len(sols) != 1 {
		log.Info("変わったマスでは手が決まりません（81 マスを読みます）",
			"変わったマス", mask.Count(), "合う手順", len(sols), "error", err)
		return FollowAuto{Kind: FollowUnreachable, Reason: "変わったマスでは手が決まりません", State: s.State()}, nil
	}
	moves := sols[0]
	log.Info("変わったマスで手を割り出しました", "moves", moves)

	k := s.takePendingPrefix(moves)
	switch {
	case k == 0:
		// **初めて出た答え。** 控えて、次の 1 枚を待つ（読んだ盤は無い）。
		s.setPending(&followPending{tip: s.mainTip(), moves: moves})
		return FollowAuto{Kind: FollowUnique, Pending: true, Reason: "確かめています", State: s.State()}, nil
	case k == len(moves):
		return s.fastApply(moves, true)
	default:
		// **2 枚目のほうが先へ進んでいる**（`followConfirmed` と同じ）。揃った頭だけ足し、残りを控え直す。
		a, err := s.fastApply(moves[:k], false)
		if err != nil {
			return a, err
		}
		s.setPending(&followPending{tip: s.mainTip(), moves: slices.Clone(moves[k:])})
		a.Pending, a.Reason = true, "確かめています"
		return a, nil
	}
}

// fastApply は速い経路で決まった手順を本譜の先に足す。atFrame は「これでこの 1 枚とぴったり合う」か。
func (s *StudyService) fastApply(moves []string, atFrame bool) (FollowAuto, error) {
	s.mu.Lock()
	if s.study == nil {
		s.mu.Unlock()
		return FollowAuto{}, fmt.Errorf("解析タブにまだ局面がありません")
	}
	atTip := s.study.CurrentID() == mainTipID(s.study)
	rev, main, rootSfen := s.rev, s.study.MainLine(), rootSFEN(s.study)
	s.mu.Unlock()

	text := followText(rootSfen, main, moves)
	applied, err := s.FollowApply(moves, rev, atTip)
	if err != nil {
		return FollowAuto{}, err
	}
	return FollowAuto{Kind: FollowUnique, Applied: true, Moves: slices.Clone(moves), Text: text,
		Added: applied.Added, Guess: true, Fit: 1, Note: applied.Note, State: applied.State,
		AtFrame: atFrame}, nil
}

// usiCells は USI の手が動かすマス（元と行き先。打つ手は行き先だけ）。読めなければ nil。
func usiCells(mv string) *position.CellMask {
	sq := func(s string) (int, int, bool) {
		if len(s) != 2 || s[0] < '1' || s[0] > '9' || s[1] < 'a' || s[1] > 'i' {
			return 0, 0, false
		}
		return int(s[1] - 'a'), 9 - int(s[0]-'0'), true
	}
	if len(mv) < 4 {
		return nil
	}
	m := &position.CellMask{}
	if mv[1] != '*' {
		r, f, ok := sq(mv[0:2])
		if !ok {
			return nil
		}
		m[r][f] = true
	}
	r, f, ok := sq(mv[2:4])
	if !ok {
		return nil
	}
	m[r][f] = true
	return m
}

// pendingStillFits は**足せる手が出なかった 1 枚が、控えた手順と矛盾しないか**を返す（2026-10-06）。
//
// 矛盾しないとは、**控えた手順を指した盤でこの 1 枚を説明しても、何も指していない盤より
// 悪くならない**こと（`position.MatchCost` で比べる）。
//
// **実機の症状**: ゲーム画面で △1五歩 の歩が、明るさが細かく揺れるたびに「読める・読めない」を
// 繰り返し（画素はほぼ同じ）、読めた 1 枚で控えても、次の読めない 1 枚で捨てていた。2 回続けて
// 読めるまで 90 秒入らなかった。読めない 1 枚は 1四 が空いて 1五 も空き —— **「何も指していない」
// とも「△1五歩」とも同じだけ食い違う**ので、控えを否定する材料になっていない。
//
// ⚠️ **手が映った 1 枚の空想の手は、今までどおり捨てられること**（`TestFollowAutoDropsTransientFrame`）。
// 手がどいた 1 枚は「何も指していない」とぴったり合い、空想の手のほうが食い違うので、ここは偽になる。
// ⚠️ **費用表が無いときは偽**（比べる根拠が無いので今までどおり捨てる）。
func (s *StudyService) pendingStillFits(board *position.Board, cost *position.CellCost, unseen *position.CellMask) bool {
	if board == nil || cost == nil {
		return false
	}
	s.mu.Lock()
	pend := s.pending
	if pend == nil || s.study == nil || pend.tip != mainTipID(s.study) {
		s.mu.Unlock()
		return false
	}
	_, from, err := s.study.MainTip()
	s.mu.Unlock()
	if err != nil {
		return false
	}
	after := from.Clone()
	for _, m := range pend.moves {
		if after.ApplyMove(m) != nil {
			return false
		}
	}
	opt := position.ConnectOptions{Cost: cost, Unseen: unseen}
	return position.MatchCost(after.Board, board, opt) <= position.MatchCost(from.Board, board, opt)
}

// followPending は**1 枚目で見つけた、まだ足していない答え**（2026-10-05）。
//
// **実機の症状**: 指している手や頭が盤に映った 1 枚から、指していない手を足した
// （▲4八飛・△6一玉・△4一歩打）。どれも**その 1 枚だけ**の読み違いで、次の 1 枚では
// 消えていた。△4一歩打は撮った盤と完全に一致していたので、行き先の裏付け（`position.backed`）も
// 行き先の決まらない候補（`Unsettled`）もすり抜けた。
//
// ⚠️ **ヒントであって動作条件ではない**（設計原則1）。控えが無くても、次の周で決まるだけ。
// ⚠️ **比べるのは「本譜の先端」と「最初に足す手」**（`StudyState.Rev` ではない —— 手順を
// クリックしただけで進むので、見ているあいだ永久に確かめられなくなる）。
type followPending struct {
	tip     int
	moves   []string
	board   *position.Board
	cost    *position.CellCost
	unseen  *position.CellMask
	rotated bool
}

func (s *StudyService) setPending(p *followPending) {
	s.mu.Lock()
	s.pending = p
	s.mu.Unlock()
}

// takePendingPrefix は控え（今の先端からのもの）と今の答えが**頭から何手揃っているか**を返す。
// 1 手でも揃っていれば控えを取り出す（0 なら何もしない）。
//
// ⚠️ **完全一致を求めないこと**（2026-10-06）。2 枚目が 1 手先まで進んでいると
// 控え直しが続き、速い手が続くあいだ 1 手も足せなかった（`followConfirmed`）。
func (s *StudyService) takePendingPrefix(moves []string) int {
	tip := s.mainTip()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil || s.pending.tip != tip {
		return 0
	}
	k := 0
	for k < len(moves) && k < len(s.pending.moves) && moves[k] == s.pending.moves[k] {
		k++
	}
	if k > 0 {
		s.pending = nil
	}
	return k
}

// mainTip は本譜の先端の節点 id（局面が無ければ -1）。
func (s *StudyService) mainTip() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return -1
	}
	return mainTipID(s.study)
}

// followAddable は下見の結果が**追従が足してよい形か**と、最初に足す手を返す。
// ⚠️ **`followFrom` の繰り返しの判断と揃えること**（ずれると、控えたのに足さない／
// 控えずに足す、が起きる）。
func followAddable(p FollowProbe) ([]string, bool) {
	if len(p.Candidates) == 0 {
		return nil, false
	}
	switch {
	case p.Kind == FollowUnique:
	case p.Kind == FollowChoices && !p.More && !p.Unsettled:
	default:
		return nil, false
	}
	return p.Candidates[0].Moves, true
}

// followFrom は撮った 1 枚から**取れるだけ取って足す**（確かめが済んだあとの本体）。
func (s *StudyService) followFrom(board *position.Board, cost *position.CellCost, unseen *position.CellMask, rotated bool) (a FollowAuto, err error) {
	// ⚠️ **先端を見ていたなら付いていく**（`tail -f` と同じ）。
	// 戻って読んでいるなら**動かさない**（`mergeReloadLocked` と同じ約束）。
	// ⚠️ **判定は繰り返しに入る前の 1 回だけ** —— 自分で足した手で先端が動くので、
	// 中で見ると**2 手目以降は必ず「先端ではない」**になる。
	s.mu.Lock()
	atTip := s.study != nil && s.study.CurrentID() == mainTipID(s.study)
	s.mu.Unlock()

	// unexplained は 1 周目の下見が「駒が来たのに説明が付かない」だったか（`looksFlipped` を呼ぶ条件）。
	unexplained := false
	for i := 0; i < followCatchUp; i++ {
		p, e := s.followProbe(board, cost, unseen, cost != nil, rotated)
		if e != nil {
			if a.Applied {
				// 途中まで足せているなら、それは成果。**捨てない。**
				break
			}
			return FollowAuto{}, e
		}
		if i == 0 {
			unexplained = p.Unexplained
		}
		if !a.Applied {
			// **1 周目の下見がこの周の「結果」**（見送った理由もここから出る）。
			a = FollowAuto{Kind: p.Kind, Reason: p.Reason, Guess: p.Guess,
				Fit: p.Fit, Fixed: p.Fixed, Mismatch: p.Mismatch, Unseen: unseen.Count()}
		}
		if len(p.Candidates) == 0 || (p.Kind != FollowUnique && p.Kind != FollowChoices) {
			// **足せる手が無い**（変わっていない／読めない／繋がらない）。
			// ⚠️ **2 手目以降なら、これが「追いつき切った」の正常な終わり方。**
			// **ぴったり「変わっていない」で終わったなら、この 1 枚は先端そのもの**（速い経路の比べる相手）。
			a.AtFrame = p.Kind == FollowSame && (cost == nil || p.Fit >= 1-1e-9)
			break
		}
		// ⚠️ **順番が決まらなくても、行き先が決まっているなら進む**
		// （2026-09-15 に実機で踏んだ）。**追従には聞く相手が居ない。**
		//
		// **実機の症状**: 1 周で繋げられないと、そのぶん遅れが開き、次の周はもっと
		// 繋がらなくなる —— **1 回の見送りが雪崩になって、そこから永久に戻らない**
		// （4 手目で止まって 90 手まで無反応）。
		//
		// ⚠️ **順番が入れ替わっても行き着く局面は同じ**（それが transposition の
		// 定義）。**解析は 1 文字も変わらない**ので、失うのは棋譜の手順の並びだけ。
		// ⚠️ **だから必ず推測の印を付けること**（`Node.Guess` → 橙の丸）——
		// **`TODO.md`「本譜のロック」が守りたいのは「嘘を実際に現れた指し手として
		// 残さない」こと**で、印が付いていればそれは守られている。
		// ⚠️ **並べきれないとき（`More`）は進まない** —— 候補として出せないなら
		// 行き先も確かめられていない。
		// ⚠️ **`Rank` の候補を並べただけのとき（`Unsettled`）も進まない**
		// （2026-10-05 に実機で踏んだ）。**候補ごとに行き先が違う**ので、上の
		// 「行き着く局面は同じ」が成り立たない。**実機の症状**: 指している手が盤を
		// 覆っている 1 枚（一致度 0.83・差 0.15 ＝ 1 マスぶんも無い）から ▲4八飛を
		// 足し、次の 1 枚でそれを打ち消す ▲2八飛を足して、本当に指された
		// ▲4五同銀を落とした。そこから先は 1 手も繋がらなくなった。
		// **次の 1 枚（手がどいたあと）で決まる**ので、見送っても雪崩にはならない。
		// ⚠️ **手で繋ぐ側（`FollowProbe`）は今までどおり人に聞く** ——
		// **人が居るのに黙って選ぶ理由は無い。**
		if p.Kind == FollowChoices {
			if p.More || p.Unsettled {
				break
			}
			a.Guess = true
		}
		moves := p.Candidates[0].Moves
		applied, aerr := s.FollowApply(moves, p.Rev, atTip)
		if aerr != nil {
			if a.Applied {
				break
			}
			return a, aerr
		}
		a.Applied = true
		a.Moves = append(a.Moves, moves...)
		a.Text = append(a.Text, p.Candidates[0].Text...)
		a.Added += applied.Added
		// ⚠️ **推測が 1 つでも混じったら印を付ける**（`Node.Guess` は手ごとに付く）。
		a.Guess = a.Guess || p.Guess
		a.Fit, a.Fixed, a.Mismatch = p.Fit, p.Fixed, p.Mismatch
		a.Note, a.State = applied.Note, applied.State
	}
	if !a.Applied {
		a.State = s.State()
		// ⚠️ **1 手も足せないときは「目線が逆かもしれない」を疑う**
		// （2026-09-15 に実機で踏んだ）。
		//
		// **実機の症状**: 後手で対局していたら 1 手目から 1 手も進まなかった。
		// ゲーム画面は**自分が手前**に出るので、後手なら**盤が上下逆に映る** ——
		// 相手（先手）が指した手は、アプリから見ると「上側＝後手が動いた」に
		// なるので、**先手のどの手でも説明できず「変わっていません」で終わる。**
		//
		// ⚠️ **平手の初期局面は上下対称なので、盤を見ても目線が逆だと分からない。**
		// **最初の 1 手が指されて初めて分かる**ので、ここが唯一の検出の機会。
		//
		// ⚠️ **黙って回さないこと**（`_docs/design-position.md` の「取り込みの向き」）——
		// 盤の向きは**局面の解釈そのもの**で、手順の並びとは重みが違う。
		// **人に言うところまで**にする。
		//
		// ⚠️ **疑うのは「駒が来たのに今の向きでは説明が付かない」1 枚だけ**（`FollowProbe.Unexplained`。
		// 2026-10-06 に見直した）。逆に映っていれば、相手の手は「来るはずのない側の駒が来た」に
		// 見えるので必ずここに入る。それ以外（変わっていない・読めない・盤が無い・手が被っている）で
		// 疑う理由は無く、以前は毎周回して 1 周に約 1 秒足していたうえ、正しく追えているのに
		// 「逆」と言って止めた。⚠️ **費用表が無いとき**は「来た」が判定できないので、今までどおり毎回見る
		// （`Rank` だけなので安い）。
		if cost == nil || unexplained {
			a.Flipped, a.FlipMove = s.looksFlipped(board, cost, unseen, a.Fit)
		}
	}
	if a.Added > 1 {
		log.Info("1 枚から追いつきました", "moves", a.Moves, "guess", a.Guess)
	}
	return a, nil
}

// followCatchUp は**1 枚の画像から足す手数の上限**。
//
// ⚠️ **上限を置くこと** —— `followProbe` が毎回「足せる」と言い続ける状況
// （費用表が壊れている等）で**無限に手が生える**のを防ぐ。
// 12 手あれば、認識 2 秒 × 早指しでも十分に追いつく。
const followCatchUp = 12

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
	// Flipped は**盤が上下逆に映っている疑い**（2026-09-15）。
	//
	// ⚠️ **これが立っても勝手に回さない。** 画面に出して人に決めてもらう
	// （訂正タブの「目線」）。**盤の向きは局面の解釈そのもの**なので、
	// 黙って変えると何が起きたのか分からなくなる。
	Flipped bool `json:"flipped"`
	// FlipMove は「逆向きなら繋がる」と判断した根拠の手（日本語表記）。
	//
	// ⚠️ **根拠を出すこと** —— 「目線が逆かも」とだけ言われても確かめようが無い。
	FlipMove string `json:"flipMove"`
	// Pending は**足せる手を見つけたが、まだ足していない**か（2026-10-05。`followPending`）。
	// 次の 1 枚で確かめる。⚠️ **見送り（繋がらない）として数えないこと**。
	Pending bool `json:"pending"`
	// Unseen は**この 1 枚で見えなかったマスの数**（手や頭が被った。2026-10-06）。
	//
	// 足せなかった周の理由を言い分けるのに使う（「変わっていません」と「何かが被っている」は
	// 次にすることが同じ —— 待つ —— でも、**出す言葉は違う**）。⚠️ **足したかどうかの
	// 判断には使わないこと**（それは `position` がマスごとにやっている）。
	Unseen int `json:"unseen"`
	// AtFrame は**本譜の先端が、この 1 枚とぴったり合っているか**（2026-10-07）。
	// 真なら、この 1 枚を**速い経路の比べる相手**にしてよい（フロントが `CaptureService.SetCellBase` を呼ぶ）。
	// ⚠️ **ぴったり（一致度 1）のときだけ立てること。** 行き先の駒が読めていないだけの 1 枚
	// （一致度 0.9965）を比べる相手にすると、その手のマスの変化が二度と見えなくなる。
	AtFrame bool `json:"atFrame"`
}

// looksFlipped は**盤を 180 度回したら 1 手で説明が付くか**を見る（2026-09-15。2026-10-06 に見直した）。
//
// ⚠️ **読むだけ。木も訂正タブも 1 つも触らない。**
// ⚠️ **呼ぶのは「駒が来たのに今の向きでは説明が付かない」ときだけ**（呼び出し側の `Unexplained`）。
//
// ⚠️ **`Rank`（1 手）だけで見ること。`Connect` は回さない。** 以前は回した盤で下見を丸ごと
// やり直しており、何手先まで探す分（約 1 秒）が毎周乗っていた。逆に映っているのは追い始めた
// ときからなので、**相手が 1 手指した最初の 1 枚で必ず 1 手の答えが出る**（その 1 枚を逃しても
// 次の手でまた出る）。
//
// ⚠️ **言い切れるときだけ「逆」と言う**（`Decided`。差の付かない 1 位では言わない）。
// fit は**今の向きでの一致度**（`FollowProbe.Fit`）で、⚠️ **回した説明はそれより良く合うときだけ採る**
// （2026-10-06 に実機で踏んだ。▲6八玉 のあとで目線を後手にして採ると、回した盤が
// 玉の 2 マスを直して △4二玉 で説明が付いてしまい、正しく追えているのに止められた）。
func (s *StudyService) looksFlipped(board *position.Board, cost *position.CellCost, unseen *position.CellMask, fit float64) (bool, string) {
	if board == nil {
		return false, ""
	}
	s.mu.Lock()
	if s.study == nil {
		s.mu.Unlock()
		return false, ""
	}
	_, from, err := s.study.MainTip()
	main, rootSfen := s.study.MainLine(), rootSFEN(s.study)
	s.mu.Unlock()
	if err != nil {
		return false, ""
	}

	// ⚠️ **費用表も見えないマスも盤と一緒に回すこと**（回し忘れると別のマスで比べる）。
	opt := position.ConnectOptions{Unseen: unseen.Rotate180()}
	if cost != nil {
		opt.Cost, opt.Tolerance = cost.Rotate180(), position.DefaultTolerance
	}
	got, err := position.Rank(from, board.Rotate180(), opt)
	if err != nil || !got.Decided(position.DefaultMinFit, position.DefaultMinMargin) {
		return false, ""
	}
	top := got.Candidates[0]
	if len(top.Moves) == 0 || top.Fit <= fit {
		return false, ""
	}
	text := strings.Join(followText(rootSfen, main, top.Moves), " ")
	log.Info("盤を回すと 1 手で説明が付きます（目線が逆かもしれません）",
		"moves", top.Moves, "fit", top.Fit, "margin", got.Margin, "今の向きの一致度", fit)
	return true, text
}
