package app

// 「撮った盤面を、今の本譜の先に繋ぐ」（Phase 6。2026-09-14）。
//
// 中継を追っていると、認識で分かるのは**盤面だけ**。
// そこから手順を割り出すのは `position.Connect` の仕事で、ここはその口。
//
// ⚠️ **`Adopt`（この局面を解析する）とは別物。** あちらは**根ごと入れ替える**
// （前の手順も評価値も捨てる）。こちらは**本譜の先へ足す**ので、
// **ユーザーの検討枝も評価値も残る**。
//
// ⚠️ **2 段になっているのは、順番が決められないことがあるから。**
// `followProbe` は木を 1 手も触らずに候補だけ返し、`followApply` で初めて据える。
// **自動で 1 本選ばないこと** —— 選んだ手順は「実際に現れた指し手」として棋譜に残る
// （`TODO.md`「本譜のロック」が一番避けたい壊れ方）。
//
// ⚠️ **入口は追従（`FollowAuto`）だけ。** 訂正タブから繋ぐ口は置かない（`_docs/design-follow.md`）。

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/ikkyoku/followcam"
	"github.com/ShinteLab/ikkyoku/legal"
	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/position"
	"github.com/ShinteLab/ikkyoku/recognize"
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
	// FollowChoices は複数の順番があり得る（**追従は 1 本選ばない**）。
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
	// Moves は手順（USI）。**`followApply` にそのまま渡す。**
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
	// Rev は下見したときの版。**据えるときの合鍵**（`followApply`）。
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
	// Unsettled は**行き先が決まっていない候補**を並べたか（`fallbackFollow` と、行き着く局面が
	// 揃わない `Connect` の解。`followSameEnd`）。
	//
	// ⚠️ **`FollowChoices` には 2 つの意味がある。** `Connect` の解（行き着く局面は同じで、
	// 順番だけが決まらない）と、`Rank` の 1 手の候補（**行き着く局面がそれぞれ違う**）。
	// **追従が聞かずに進んでよいのは前者だけ**（2026-10-05 に実機で踏んだ。指している手が
	// 盤を覆っている 1 枚から、差が 1 マスぶんも無い 1 位を採って空想の手を足した）。
	// 画面には出さない。
	Unsettled bool `json:"-"`
	// Unexplained は**駒が来たマスがあるのに、今の向きでは何手先まで探しても説明が付かなかった**か
	// （`followArrived` で先を探し、繋がらずに「変わっていない」へ戻したときだけ立つ。2026-10-06）。
	// ⚠️ **「盤が上下逆」を疑うのはこのときだけ**（`looksFlipped`）。画面には出さない。
	Unexplained bool `json:"-"`
	// KindOnly は「変わっていない」のとき、**食い違いが駒の種類だけ**か（駒の有無と先後は全部合っている。2026-10-07）。
	// ⚠️ **速い経路の比べる相手にしてよいかの判断にだけ使う**（`FollowAuto.AtFrame`）。画面には出さない。
	KindOnly bool `json:"-"`
	// base は**比べる相手の局面から説明した答え**（深く探す段まで来て、足し済みの手があったときだけ。2026-10-11。
	// `followExplainFromBase`）。差し替えの候補（`rewind`）を `followRewindProbe` が関門にかける。nil なら、
	// `followRewindProbe` が要るときに自分で探す。
	base *followBaseAnswer
	// ranked は**先端から 1 手の候補**（`Rank` が並べた良い順。「何も指していない」は除く。2026-10-11）。
	// ⚠️ **差し替えの比べで先端側に数えるためのもの**（`followRewindProbe`）。`Candidates` は深い探索の答えで、
	// 「変わっていません」で終わった 1 枚では空になる —— そのとき先端に 1 手足せば合う 1 枚でも、差し替えが勝っていた。
	ranked [][]string
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

// followProbe は下見の本体。**盤と費用表をもらうだけで、どこから来たかを知らない。**
//
// ⚠️ **木を 1 手も触らない**（`study:changed` も出さない）。据えるのは `followApply`。
//
// ⚠️ **繋ぎ先は本譜の先端（`MainTip`）で、「今見ている局面」ではない。**
// ユーザーが枝の途中を読んでいるのは普通で、そこへ繋ぐと**検討の枝に中継の手が生える**。
//
// ⚠️ **局面の確定を要求しない**（`Adopt` との違い）。要るのは盤面だけで、
// **手番も駒台の先後も経路から決まる**（`position.Connect`）。
//
// ⚠️ **訂正タブを通さないこと**（2026-09-15 に実機で踏んだ）——
// 以前は 1 周ごとに `PositionService.Load` を呼んでいたので、
// **人が訂正タブで作業していると 1 秒ごとに中継の盤で上書きされた。**
//
// fromImage は**推測してよいか**（人が並べた盤を機械が推測で直す理由は無い）。
//
// unseen は**手や頭が被って見えないマス**（nil なら全部見えている。2026-10-06）。
// ⚠️ **推測するかどうか（fromImage）とは別に効かせること** —— 見えないマスは
// 「覆す」話ではなく、そもそも根拠にも減点にもしないマス（`position.CellMask`）。
func (f *follower) followProbe(board *position.Board, cost *position.CellCost, unseen *position.CellMask, fromImage, rotated bool) (FollowProbe, error) {
	if board == nil {
		return FollowProbe{}, fmt.Errorf("まだ局面がありません")
	}

	f.smu.Lock()
	if f.tree() == nil {
		f.smu.Unlock()
		return FollowProbe{}, fmt.Errorf("解析タブにまだ局面がありません（先に「この局面を解析する」で始めてください）")
	}
	tipID, from, err := f.tree().MainTip()
	if err != nil {
		f.smu.Unlock()
		return FollowProbe{}, err
	}
	rev, main, rootSfen := f.host.hostRev(), f.tree().MainLine(), rootSFEN(f.tree())
	f.smu.Unlock()

	// ⚠️ **`fromImage` が「推測してよいか」の唯一の判断。**
	opt := position.ConnectOptions{Unseen: unseen, DeepDepth: followDeepDepth}
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
	// decided は `Rank` が 1 手で決めたが、その手のあとにも指された跡が残ったので先を探しに行ったときの答え
	// （`Connect` が繋げなければこれを返す）。
	var decided *FollowProbe
	if fromImage {
		got, rerr := position.Rank(from, board, opt)
		if rerr == nil && len(got.Candidates) > 0 {
			ranked = got
			top := got.Candidates[0]
			out.Fit = top.Fit
			for _, c := range got.Candidates {
				if len(c.Moves) > 0 {
					out.ranked = append(out.ranked, c.Moves)
				}
			}
			log.Follow.Info("候補を並べました", "tip", tipID, "moves", top.Moves,
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
					out.KindOnly = followKindOnly(top.Fixed)
					return out, nil
				}
				arrived = true
				log.Follow.Info("何も指していないが 1 位ですが、駒が来たマスがあるので先を探します",
					"tip", tipID, "rotated", rotated)
			} else if got.Decided(position.DefaultMinFit, position.DefaultMinMargin) {
				// ⚠️ **費用 0 は推測ではない**（認識とぴったり合っている）。
				out.Kind, out.Depth, out.Guess = FollowUnique, len(top.Moves), top.Cost > 0
				out.Fixed = followFixes(top.Fixed)
				out.Candidates = []FollowChoice{{
					Moves: top.Moves,
					Text:  followText(rootSfen, main, top.Moves),
				}}
				// ⚠️ **その手のあとにも指された跡が残るなら、先を探してから決める**（2026-10-07 に実機で踏んだ）。
				// △6五桂 ▲同桂 △同歩 ▲2四歩 を 1 枚で撮ると、▲2四歩 だけで 1 位になる（7七・6四 が空いたのは
				// 空きの読みで安い）。それを採ると ▲同桂 △同歩 が落ちて局面がずれ、その先が全部繋がらなくなった。
				// ⚠️ **繋がらなければ今までどおりこの手を返す**（深く探すのは確かめのためだけ）。
				if !followArrived(top.Fixed, cost) {
					return out, nil
				}
				d := out
				decided = &d
				out = FollowProbe{Rev: rev, Candidates: []FollowChoice{}, Fit: top.Fit, ranked: d.ranked}
				log.Follow.Info("1 位の手のあとにも指された跡があるので先を探します",
					"tip", tipID, "moves", top.Moves, "rotated", rotated)
			}
		}
	}

	// **1 手では決まらなかった。** ここから先は「何手進んだのか」を探す仕事
	// （CM を挟んで飛んだとき）。⚠️ **時間がかかるのはこちらだけ。**
	ctx, cancel := context.WithTimeout(context.Background(), followTimeout)
	defer cancel()
	// ⚠️ **手や頭が被った 1 枚では、深く繋ぐのは厳密一致だけにする**（2026-10-07）。見えないマスの周りは
	// 読みが崩れていて、安いマスを見逃せる修復では偽の手順が予算に収まる（手が覆った 1 枚から
	// ▲1六香 △2六桂 ▲同飛 を繋いだ）。1 手なら `Rank` が拾い、手がどいた次の 1 枚で入る。
	copt := opt
	if unseen.Count() > 0 {
		copt.Tolerance = 0
	}
	// ⚠️ **足し済みの手があれば、比べる相手の局面から説明する**（2026-10-11。`followbase.go`）。足し済みの手で始まる
	// 答えなら、その残りが先端から足す手。途中で分かれる答えは差し替えの候補として `followRewindProbe` に回し、
	// ここでは今までどおり先端から探した答えを返す（差し替えを関門にかけるときの比べる相手になる）。
	// ⚠️ **手や頭が被った 1 枚・費用表の無い 1 枚では使わない**（差し替えを試さない 1 枚。今までどおり先端から）。
	var r position.ConnectResult
	fromBase := false
	if bpos, since, line, bok := f.followBaseFrom(); bok && fromImage && unseen.Count() == 0 {
		ans, err := followExplainFromBase(ctx, bpos, since, line, board, copt)
		if err != nil {
			return FollowProbe{}, err
		}
		out.base = &ans
		if decided != nil {
			decided.base = &ans
		}
		r, fromBase = ans.tipResult()
		log.Follow.Info("比べる相手の局面から説明しました",
			"tip", tipID, "足し済み", len(since), "stop", ans.r.Stop.String(), "depth", ans.r.Depth,
			"candidates", len(ans.r.Solutions), "続き", len(ans.rests), "差し替え", ans.rewind != nil,
			"nodes", ans.r.Nodes, "cost", ans.r.Cost)
	}
	if !fromBase {
		var err error
		if r, err = position.Connect(ctx, from, board, copt); err != nil {
			return FollowProbe{}, err
		}
	}
	// scattered は**答えが複数あり、行き着く局面が揃わない**か（`followSameEnd`）。
	scattered := r.Stop == position.StopFound && !r.Unique && !r.More && !followSameEnd(from, r.Solutions)
	log.Follow.Info("本譜の先に繋げるか下見しました",
		"tip", tipID, "stop", r.Stop.String(), "depth", r.Depth,
		"candidates", len(r.Solutions), "nodes", r.Nodes,
		"cost", r.Cost, "fixed", len(r.Fixed), "rotated", rotated, "fromBase", fromBase)

	// ⚠️ **1 手で決まっていたなら、それより深く繋がったときだけ乗り換える**（`decided`。並べきれないときは乗り換えない）。
	if decided != nil && (r.Stop != position.StopFound || r.More || scattered || r.Depth <= decided.Depth) {
		return *decided, nil
	}
	out.Depth = r.Depth
	// ⚠️ **駒が来たので探しに来ただけなら、足せる答えが出なければ元の「変わっていない」に戻すこと**
	// （`followArrived`）。読みの揺れで来たように見えただけの 1 枚を「繋がらない」にすると、
	// 「見失っています」に数えられる。⚠️ **「何手か進んだが順番が多すぎる」（`More`）も同じ** ——
	// 盤が上下逆に映っていると、片側が行って戻る手を挟んで説明が付いてしまい、これになる。
	// そのときは `Unexplained` を立てる（「盤が上下逆」を疑う合図。`looksFlipped`）。
	if arrived && (r.Stop != position.StopFound || r.More || scattered) {
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
		return f.fallbackFollow(ranked, r, rootSfen, main, fromImage, out), nil
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
	if scattered {
		out.Guess, out.Unsettled = true, true
		out.Reason = "どの手が指されたか決められません（認識が曖昧か、2 手以上進んでいます）"
	}
	return out, nil
}

// followSameEnd は `Connect` の答えが**どれも同じ局面に行き着くか**を返す（2026-10-07）。
//
// 厳密一致の答えは全部が撮った盤に行き着くので「順番だけが違う」で済むが、⚠️ **修復の答えは
// 見逃したマスが答えごとに違い、行き着く局面も別々になる**（手が覆った 1 枚で、▲6六歩 など
// 1 手の答えが 3 つ並んだ）。それを「行き先が決まっている」として追従が足すと、空想の手が入る。
// ⚠️ **組めない手順があれば揃っていない側に倒す**。
func followSameEnd(from *position.Position, sols [][]string) bool {
	end := ""
	for i, moves := range sols {
		p := from.Clone()
		for _, mv := range moves {
			if err := p.ApplyMove(mv); err != nil {
				return false
			}
		}
		// 手数は手順の長さで揃うので、比べるのは盤・手番・駒台。
		got := followEndKey(p)
		if got == "" {
			return false
		}
		if i == 0 {
			end = got
		} else if got != end {
			return false
		}
	}
	return true
}

// followKindOnly は「何も指していない」との食い違いが**駒の種類だけ**か（駒の有無と先後は全部合っている）。
//
// 速い経路の比べる相手にしてよいかの判断（`FollowAuto.AtFrame`）。⚠️ **駒の有無の食い違いがあれば偽** ——
// 「元のマスは空いたのに行き先の駒が読めていない」1 枚（まだ足していない手の名残）を相手にすると、
// その手のマスの変化が二度と見えなくなる。
func followKindOnly(fixed []position.Fix) bool {
	for _, f := range fixed {
		if f.Was.IsEmpty() || f.Now.IsEmpty() || f.Was.Black() != f.Now.Black() {
			return false
		}
	}
	return true
}

// followArrivedMinCost は「駒が来た」と言い切る、そのマスの読みの確かさ（費用表の値）の下限。
//
// 駒のマスは推論の確信度、空きのマスは被覆率から作った確かさ（`recognize.CellConfidence`。2026-10-11 から。それまでは
// 空きは一律で下限の 0.15）。
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
	vacated, swapped := false, false
	for _, f := range fixed {
		// 判定は `followJudge`（読んだ駒 f.Was と、今の局面のマス f.Now。弱い駒の読みは何も言わない）。
		v := followJudge(f.Now, followObsCell(f.Was, cost[f.Rank][f.File]))
		switch {
		case f.Was.IsEmpty():
			if v.presence == followDisagree {
				vacated = true
			}
		case v.presence == followDisagree || v.side == followDisagree:
			return true
		case v.kind == followDisagree:
			swapped = true
		}
	}
	return swapped && vacated
}

// followApply は選んだ手順を**本譜の先**に据える。
//
// ⚠️ **据え方は `Graft` 1 か所**（URL の取り直しと同じ意味論）。**消さずに据える**ので、
// ユーザーの検討枝も評価値もそのまま残る。
//
// ⚠️ **`evals` を触らないこと。** `reset` も `dropAfter` も世代を進めるので、
// **走っている解析の途中経過が捨てられる**（中継を追うたびにそれが起きる）。
//
// goTo は**繋いだ先へ移るか**。⚠️ **先端を見ていたときだけ真にすること** ——
// 戻って読んでいる最中は**見ている場所を勝手に動かさない**
// （`mergeReloadLocked` と同じ約束）。
//
// ⚠️ **公開しないこと**（2026-10-07 に外した）。公開するとフロントの API になり、
// 下見を通さない手順を本譜へ足せる口が残る。
func (f *follower) followApply(moves []string, rev int, goTo bool) (a FollowApplied, err error) {
	return f.followGraft(moves, rev, goTo, true)
}

// followGraft は `followApply` の本体。guess が偽なら推測の印を付けない（人が選んだ手。`FollowChoose`）。
func (f *follower) followGraft(moves []string, rev int, goTo, guess bool) (a FollowApplied, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO）。
	defer func() { f.host.publish(a.State, err) }()
	if len(moves) == 0 {
		return FollowApplied{}, fmt.Errorf("繋ぐ手順がありません")
	}

	f.smu.Lock()
	defer f.smu.Unlock()
	if f.tree() == nil {
		return FollowApplied{}, fmt.Errorf("解析タブにまだ局面がありません")
	}
	// ⚠️ **下見のあいだに局面が変わっていたら断る。** 別の窓が手を指したかもしれず、
	// そのまま据えると**違う局面の先に繋がる**。
	if rev != f.host.hostRev() {
		return FollowApplied{}, fmt.Errorf("局面が変わりました。もう一度お試しください")
	}

	line := append(f.tree().MainLine(), moves...)
	var g position.GraftResult
	if guess {
		g = f.host.hostGraft(line, len(moves))
	} else {
		g = f.host.hostGraft(line, 0)
	}
	if g.Note != "" && g.Added == 0 {
		return FollowApplied{}, fmt.Errorf("%s", g.Note)
	}
	if goTo {
		// **先端を見ていたので付いていく。**
		f.host.hostGoTip()
	}
	a = FollowApplied{State: f.host.changed(), Added: g.Added, Note: reloadNote(g)}
	log.Follow.Info("撮った盤面を本譜の先に繋ぎました",
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
func (f *follower) fallbackFollow(ranked position.RankResult, r position.ConnectResult,
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
// ⚠️ **1 枚から取れるだけ取ること**（2026-09-15 に実機で踏んだ）。その日は**認識 1 枚に
// 2.1 秒かかり**、**1 周 1 手では 3 秒に 1 手しか進めなかった** ——
// ゲーム画面はそれより速く進むので、**遅れが一方的に開いて 4 手を超えた時点で
// 永久に復帰できなくなった**（実機で 4 手目以降 90 手まで無反応）。
//
// **撮った 1 枚には「何手ぶんも先」が写っている。** それを 1 手だけ取り出して
// 残りを捨て、また撮り直して読むのは無駄でしかない。
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
//
// promo は**成・不成の見た目の証拠**（2026-10-07。`PromoEvidence`）。nil で呼ばれ、足そうとしている手に
// 成りも不成も指せる手があれば、何も動かさずに `Compare` を返す（中継セッションが `followcam.Camera.ComparePieces` で
// 測って、同じ盤と証拠でもう一度呼ぶ）。⚠️ **nil でない（空でも）ときは、もう頼まない**（今までどおり決める）。
func (f *follower) FollowAuto(boardSFEN string, cellConfidence []float64, cellHidden []bool, promo []PromoEvidence) (FollowAuto, error) {
	// ⚠️ **訂正タブを通さないこと**（2026-09-15 に実機で踏んだ）。
	// 以前は呼び出し側が毎周 `PositionService.Load` を呼んでいたので、
	// **人が訂正タブで作業していると 1 秒ごとに中継の盤で上書きされた**
	// （学習データを登録しようとして消えた）。**訂正タブは人の作業場。**
	board, cost, unseen, rotated, e := f.src.followFrame(boardSFEN, cellConfidence, cellHidden)
	if e != nil {
		return FollowAuto{}, e
	}
	// ⚠️ **成・不成の見た目の証拠は、下見の前に盤へ書き込むこと**（2026-10-07。`followApplyPromo`）。
	// 下見・控え・確かめが同じ盤を見るので、2 枚目でも同じ答えになる。
	f.followApplyPromo(board, cost, promo, rotated)
	f.smu.Lock()
	f.lastFrame = board
	f.smu.Unlock()
	// ⚠️ **修復してよいのは費用表があるときだけ**。
	// 確信度が無ければ**どのマスを覆してよいかの根拠が無い**ので、厳密一致に倒す。
	p, e := f.followProbe(board, cost, unseen, cost != nil, rotated)
	if e != nil {
		return FollowAuto{}, e
	}
	moves, ok := followAddable(p)
	if ok && promo == nil {
		// ⚠️ **成りも不成も指せる手があれば、決める前に見た目を比べてもらう**（2026-10-07）。
		// 成駒は学習データに入らないのが普通で、龍を「飛」と読み違えるので、読んだ盤だけで決めると
		// 不成になる（ゲーム画面で ▲2三飛成 を 2f2c、△8七飛成 を 8f8g と記録した）。
		// ⚠️ **ここでは何も動かさない**（控えも足しもしない）。証拠を添えてもう一度呼ばれてから決める。
		// 証拠が空（測れなかった）でも nil でなければ、もう頼まずに今までどおり決める。
		if base, since, _, bok := f.followFromBase(); bok {
			if pairs := followPromoPairs(base, since, moves, rotated); len(pairs) > 0 {
				log.Follow.Info("成りも不成も指せる手があるので、見た目を比べます", "moves", moves, "組", len(pairs))
				return FollowAuto{Kind: FollowChoices, Reason: "成・不成を見た目で確かめます", Compare: pairs,
					Fit: p.Fit, Unseen: unseen.Count(), State: f.State()}, nil
			}
		}
	}
	if !ok && promo == nil {
		// ⚠️ **成る手と成らない手だけで分かれて決まらないときも、見た目を比べてもらう**（2026-10-08。`followPromoOnly`）。
		// ゲーム画面の ▲6四銀成 で、行き先を成銀とも銀とも読めず 2 通りのまま、4 秒ごとに 81 マスを読み直して 110 秒かかった。
		if pm := followPromoOnly(p); pm != nil {
			if base, since, _, bok := f.followFromBase(); bok {
				if pairs := followPromoPairs(base, since, pm, rotated); len(pairs) > 0 {
					log.Follow.Info("成りと不成で分かれたので、見た目を比べます", "moves", pm, "組", len(pairs))
					return FollowAuto{Kind: FollowChoices, Reason: "成・不成を見た目で確かめます", Compare: pairs,
						Fit: p.Fit, Unseen: unseen.Count(), State: f.State()}, nil
				}
			}
		}
	}
	// ⚠️ **打つ駒の種類だけで分かれたら、盤上の駒と見た目を比べ、それでも決まらなければ人に聞く**（2026-10-09。
	// `followDropLooks`）。決まらないまま 1 本選ばない —— 打った駒の誤りはその駒が動くまで盤に表れない。
	if !ok {
		a, np, done, err := f.followDropLooks(p, board, cost, unseen, promo, rotated)
		if done || err != nil {
			return a, err
		}
		if np != nil {
			p = *np
			moves, ok = followAddable(p)
		}
	}
	// ⚠️ **推測で足した末尾の手を差し替えるかを先に見る**（2026-10-08。`followRewindCheck`）。
	// 先端がずれたままでは、ここから先の 1 枚はどれも繋がらない。
	if a, done, err := f.followRewindCheck(board, cost, unseen, p); done {
		return a, err
	}
	if !ok {
		// ⚠️ **控えと矛盾しない 1 枚なら、控えは捨てない**（2026-10-06 に実機で踏んだ。`pendingStillFits`）。
		if f.pendingStillFits(board, cost, unseen) {
			return FollowAuto{Kind: p.Kind, Pending: true, Reason: "確かめています",
				Fit: p.Fit, Unseen: unseen.Count(), State: f.State()}, nil
		}
		// **足せる手が無い 1 枚が来たら、控えは捨てる**（手が映った 1 枚の答えは
		// 次の 1 枚で消えるのが普通で、それがこの形）。
		f.setPending(nil)
		return f.baseIf(f.followFrom(board, cost, unseen, rotated))
	}
	k := f.takePendingPrefix(moves)
	if k == 0 {
		// **初めて出た答え。** 控えて、次の 1 枚を待つ。
		f.setPending(&followPending{tip: f.mainTip(), moves: moves, board: board, cost: cost,
			unseen: unseen, rotated: rotated})
		return FollowAuto{Kind: p.Kind, Pending: true, Reason: "確かめています",
			Guess: p.Guess, Fit: p.Fit, Unseen: unseen.Count(), State: f.State()}, nil
	}
	if k == len(moves) {
		// **2 枚続けて同じ答え**（控えのほうが長ければ、その先は 2 枚目で消えた）。
		// ここから先は今までどおり（1 枚から取れるだけ取る）。
		return f.baseIf(f.followFrom(board, cost, unseen, rotated))
	}
	// **2 枚目のほうが先へ進んでいる**（2026-10-06）。2 枚で揃った頭の k 手だけ足し、
	// 残りは控え直して次の 1 枚で確かめる。
	return f.followConfirmed(p, k, board, cost, unseen, rotated)
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
func (f *follower) followConfirmed(p FollowProbe, k int, board *position.Board, cost *position.CellCost, unseen *position.CellMask, rotated bool) (FollowAuto, error) {
	f.smu.Lock()
	atTip := f.tree() != nil && f.tree().CurrentID() == mainTipID(f.tree())
	f.smu.Unlock()

	top := p.Candidates[0]
	applied, err := f.followApply(top.Moves[:k], p.Rev, atTip)
	if err != nil {
		return FollowAuto{}, err
	}
	f.setPending(&followPending{tip: f.mainTip(), moves: slices.Clone(top.Moves[k:]), board: board,
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
	log.Follow.Info("2 枚で揃った手だけ足し、残りを確かめます", "added", a.Moves, "pending", top.Moves[k:])
	return a, nil
}

// FollowConfirm は**控えた答えを、撮り直した 1 枚が変わっていなかったことで確かめて**足す（2026-10-05）。
//
// 呼ぶのは中継セッション（`app` の `followsession.go`）で、`CaptureQuiet` が「変わっていない」（`unchanged`）を返したとき。
// ⚠️ **ふるいの「変わっていない」は、控えた 1 枚から何も変わっていないこと**（間に
// 変化があれば、静止したところで読み直しになる）なので、**同じ画像を読み直したのと同じ**
// （認識は同じ画像に同じ答えを返す）。読み直さないぶん、確かめは 1 周（約 0.25 秒）で済む。
//
// 控えが無ければ何もしない（`FollowSame`）。⚠️ **本譜の先端が控えたときから動いていたら捨てる**
// （手で指した・戻したなど。違う局面の先に繋がないため）。
func (f *follower) FollowConfirm() (FollowAuto, error) {
	// **控えた差し替え**（`followRewindCheck`）なら、そちらを据える（2 枚目で確かめた）。
	if rw := f.takeRewind(); rw != nil {
		return f.followRewindApply(rw)
	}
	f.smu.Lock()
	pend := f.pending
	f.pending = nil
	f.smu.Unlock()
	if pend == nil || pend.tip != f.mainTip() {
		return FollowAuto{Kind: FollowSame, Reason: "盤面は変わっていません", State: f.State()}, nil
	}
	if pend.board == nil {
		// **速い経路で控えた手**（`FollowCells`）。読んだ盤は無いので、手順をそのまま足す。
		return f.fastApply(pend.moves, true, "速い経路（次の 1 枚が変わらず確かめた）", pend.why)
	}
	return f.baseIf(f.followFrom(pend.board, pend.cost, pend.unseen, pend.rotated))
}

// followCellsDepth は速い経路で**新しく**探す手数の上限。
// 2 では足りなかった（2026-10-07。▲3三角成 △同金 ▲7七銀 が 1 枚に写った）。動かしてよいマスで枝を刈るので 3 でも軽い。
const followCellsDepth = 3

// followCellsBehind は、比べる相手の局面より本譜の先端が何手先まで進んでいても速い経路を使うか。
// これより先へ進んでいたら 81 マスを読む（探す手数が増えて遅くなる・合わない見込みが高い）。
const followCellsBehind = 2

// followBase は**速い経路の比べる相手の 1 枚が、どの局面だったか**（2026-10-07）。
//
// 比べる相手（`followcam.Camera` の `cellBase`）を入れたときの、根と本譜の手順を覚える。
// ⚠️ **本譜の先端は比べる相手より先へ進むことがある**（81 マスの読みで手を足したとき）。そのとき
// 変わったマスは「比べる相手 → 今の 1 枚」の分なので、**比べる相手の局面から突き合わせる**。
type followBase struct {
	root string
	line []string
}

// markBase は**今の本譜の先端を、比べる相手の局面として覚える**（`AtFrame` を返すときに呼ぶ）。
func (f *follower) markBase() {
	f.smu.Lock()
	defer f.smu.Unlock()
	if f.tree() == nil {
		f.base = nil
		return
	}
	f.base = &followBase{root: rootSFEN(f.tree()), line: slices.Clone(f.tree().MainLine())}
}

// baseIf は a が `AtFrame` なら比べる相手の局面を覚えて、a をそのまま返す。
func (f *follower) baseIf(a FollowAuto, err error) (FollowAuto, error) {
	if err == nil && a.AtFrame {
		f.markBase()
	}
	return a, err
}

// followFromBase は**比べる相手の局面**と、そこから本譜の先端までに足した手順、比べる相手の 1 枚の
// 色付けのマス（そこへ至った手）を返す。覚えていない・根が変わった・本譜が食い違ったなら ok は偽。
func (f *follower) followFromBase() (base *position.Position, since []string, extra *position.CellMask, ok bool) {
	f.smu.Lock()
	defer f.smu.Unlock()
	cb := f.base
	if cb == nil || f.tree() == nil || rootSFEN(f.tree()) != cb.root {
		return nil, nil, nil, false
	}
	cur := f.tree().MainLine()
	if len(cur) < len(cb.line) || !slices.Equal(cur[:len(cb.line)], cb.line) {
		return nil, nil, nil, false
	}
	base = f.tree().Root().Clone()
	for _, m := range cb.line {
		if base.ApplyMove(m) != nil {
			return nil, nil, nil, false
		}
	}
	if len(cb.line) > 0 {
		extra = usiCells(cb.line[len(cb.line)-1])
	}
	return base, slices.Clone(cur[len(cb.line):]), extra, true
}

// cellsMask は撮った画像の向きのマスの番号を、解析の向きの CellMask にする（後手目線なら回す）。
func (f *follower) cellsMask(cells []int) *position.CellMask {
	m := &position.CellMask{}
	for _, i := range cells {
		if i >= 0 && i < 81 {
			m[i/9][i%9] = true
		}
	}
	// ⚠️ **撮った向きと解析の向きを揃えること**（後手目線で採った局面は 180 度回してある）。
	if f.src.followRotated() {
		m = m.Rotate180()
	}
	return m
}

// FollowCells は**変わったマスだけで手を割り出して**追従する（速い経路。2026-10-07）。
//
// changed は**撮った画像の向き**での 81 マスの番号（行優先。`followcam.Shot.Cells`）で、比べた相手は
// 本譜の先端と合っていた 1 枚（`AtFrame` / `FrameFits`）。認識（約 0.1 秒）を回さないので、
// **指されてから手が入るまでが「止まったのを確かめる 0.25〜0.5 秒」＋数ミリ秒**になる。
//
// ⚠️ **比べる相手の局面から突き合わせること**（`followFromBase`）。81 マスの読みで手を足したあとは
// 先端のほうが先へ進んでいて、変わったマスには足し済みの手のマスも入っている（2026-10-07 に実機で
// 踏んだ。先端から突き合わせて「合う手順 0」が続いた）。足し済みの手順で始まる答えだけを採り、残りを足す。
// ⚠️ **合う手順が 1 つに決まるときだけ進む**（`position.ChangedMoves`）。成・不成、打った駒の種類、
// 手や頭が被った 1 枚など、決まらなければ何もせずに返す（`Applied` も `Pending` も `AtFrame` も偽）。
// **中継セッションはそれを見て、同じ 1 枚を 81 マスで読む**（`followcam.Camera.RecognizeQuiet` → `FollowAuto`）。
// ⚠️ **合う手順が 1 つも無いときだけは `NoMoves` を立てる**（2026-10-08）—— 中継セッションはすぐには読まず、組が変わるのを待つ。
// 残りが無ければ（今の 1 枚が先端そのもの）`AtFrame` だけを立てて返す。
// ⚠️ **1 通りに決まっても、行き先に指した側の駒が読めるまで控えも足しもしない**（2026-10-07。
// `followBackedByRead`）。read が行き先を読んでいなければ `ReadCells` に行き先を入れて返し
// （中継セッションが `followcam.Camera.ReadCells` で読んでもう一度呼ぶ）、読めて先後が違えば何もせずに返す（81 マスを読む）。
// 手が分かれたときに頼むマスは、**違いの出るマスとどの候補の行き先も合わせたもの**（中継セッションが読むのは 1 回だけ）。
// ⚠️ **成・不成で分かれたら、同じ 1 回で見た目の比べも頼む**（`Compare`。2026-10-07）。promo はその答えで、
// **読んだマスより先に使う**（`followPickByPromo`。成駒は成っていない姿に読み違えるが、見た目が変わったかは分かる）。
// 間の値や測れなかったときは、今までどおり読んだマスで選ぶ。
// ⚠️ **1 枚だけでは足さない**のは同じ（`followPending`）。次の 1 枚が変わっていなければ
// `FollowConfirm` で足す。⚠️ **比べる相手の 1 枚に至った手のマスは、見た目だけ変わってよい**（色付け）。
func (f *follower) FollowCells(changed []int, read []followcam.CellPiece, promo []PromoEvidence) (FollowAuto, error) {
	// 控えた差し替えは、この 1 枚が同じ差し替えを言うまで「残しているだけ」にする（`followCellsRewind`）。
	f.holdRewind()
	mask := f.cellsMask(changed)
	rotated := f.src.followRotated()

	base, since, extra, ok := f.followFromBase()
	// fromBase は比べる相手の局面が分かっているか（見た目を比べられるのはそのときだけ。`followPromoSplit`）。
	fromBase := ok
	if !ok {
		// 比べる相手の局面を覚えていない。先端から突き合わせる。
		f.smu.Lock()
		if f.tree() == nil {
			f.smu.Unlock()
			return FollowAuto{}, fmt.Errorf("解析タブにまだ局面がありません")
		}
		_, tip, err := f.tree().MainTip()
		main := f.tree().MainLine()
		f.smu.Unlock()
		if err != nil {
			return FollowAuto{}, err
		}
		base, since = tip, nil
		if len(main) > 0 {
			extra = usiCells(main[len(main)-1])
		}
	}
	unclear := func(n int, err error) (FollowAuto, error) {
		log.Follow.Info("変わったマスでは手が決まりません（81 マスを読みます）",
			"変わったマス", mask.Count(), "足し済み", len(since), "合う手順", n, "error", err)
		return FollowAuto{Kind: FollowUnreachable, Reason: "変わったマスでは手が決まりません", State: f.State()}, nil
	}
	if len(since) > followCellsBehind {
		return unclear(0, nil)
	}
	// ⚠️ **空きのマスだけが変わり、打つ手でも説明が付かないなら、駒は動いていない**（2026-10-08。`followGlowOnly`）。
	// ゲーム画面で駒台の駒をつまむと、打てるマスが光る（15 マスなど）。81 マスを読むと光ったマスを歩と読み、
	// 3〜5 秒かけて繋がらないだけだった。駒を動かす手は必ず駒のあったマスを変え、打つ手は 1 手で 1 マス。
	glow, glowN := followGlowOnly(base, since, mask, extra)
	if glow && glowN > followCellsDepth {
		return f.followLooksOnly(glowN), nil
	}
	if mask.Count() > followCellsMax {
		// 多すぎて手順を割り出せない（手や頭が被った 1 枚など）。今までどおり 81 マスを読む。
		return unclear(0, nil)
	}
	sols, err := position.ChangedMoves(base, mask, extra, len(since)+followCellsDepth)
	if err != nil {
		return unclear(0, err)
	}
	// **足し済みの手順で始まる答え**だけを採り、残り（これから足す手）で数える。
	var rests [][]string
	for _, sol := range sols {
		if len(sol) < len(since) || !slices.Equal(sol[:len(since)], since) {
			continue
		}
		rest := sol[len(since):]
		if !slices.ContainsFunc(rests, func(r []string) bool { return slices.Equal(r, rest) }) {
			rests = append(rests, rest)
		}
	}
	if len(rests) == 0 && glow {
		// 空きのマスだけが変わり、打つ手でも説明が付かない（光ったマスが少ないとき）。
		return f.followLooksOnly(glowN), nil
	}
	if len(rests) == 0 {
		// ⚠️ **推測で足した末尾の手が違っていたなら、ここで差し替えを見る**（2026-10-09。`followCellsRewind`）。
		// 先端から先しか探さないと、待つあいだに 81 マスを読む周まで差し替えが出ない。
		if a, done, err := f.followCellsRewind(base, since, mask, extra, read, rotated); done {
			return a, err
		}
		// ⚠️ **合う手順が 1 つも無いことを添えること**（`NoMoves`。2026-10-08）。中継セッションはすぐ 81 マスを読まず、
		// 組が変わるのを待つ（駒を掴んで行き先が光っているだけの 1 枚が多い）。
		// ⚠️ **ただし駒のあったマスが 2 つ以上変わった組は待たない**（`followOccupied`）—— 駒をつまんだときに駒のあったマスで
		// 変わるのは浮いた駒の 1 マスだけ。それより多いのは、速い経路の手数（`followCellsDepth`）を超えて指された 1 枚で、
		// 待つと次の手で跡が消える（▲4三歩打 △同歩 が △4三歩 になり、手番と持ち駒の歩がずれた）。すぐその 1 枚を読む。
		a, err := unclear(0, nil)
		a.NoMoves = followOccupied(base, since, mask, extra) <= 1
		return a, err
	}
	// why は**この手順に決めた根拠**（手を足したときのまとめの 1 行に出す。2026-10-08。`logAdded`）。
	var why []string
	if len(rests) > 1 {
		// ⚠️ **控えた手と合う候補があれば、それを採ること**（2026-10-07 に実機で踏んだ）。▲3三角成 を
		// 81 マスの読みで控えたあと △同金 が指されると、3三 は金になって成ったかどうかが盤から消え、
		// 成・不成の 2 通りが残る。控えは前の 1 枚で決めた答えなので、それと合う候補だけに絞る。
		if pend := f.pendingMoves(); len(pend) > 0 && len(since) == 0 {
			var keep [][]string
			for _, r := range rests {
				n := min(len(r), len(pend))
				if n > 0 && slices.Equal(r[:n], pend[:n]) {
					keep = append(keep, r)
				}
			}
			if len(keep) > 0 {
				rests = keep
			}
		}
	}
	if len(rests) > 1 {
		// **成・不成や打った駒の種類で分かれた**（変わったマスの組は同じ）。違いの出るマスだけを読めば決まる。
		diff := followDiffCells(base, since, rests)
		if len(read) == 0 && promo == nil {
			diffOK := len(diff) > 0 && len(diff) <= followReadMax
			// ⚠️ **成・不成で分かれたら、見た目も比べてもらう**（2026-10-07。`followPromoSplit`）。
			// 読むだけだと、成駒を成っていない姿に読み違えたとき不成に決まる（龍を「飛」と読む）。
			var pairs []followcam.CellPair
			if fromBase {
				pairs = followPromoSplit(since, rests, rotated)
			}
			if !diffOK && len(pairs) == 0 {
				return unclear(len(rests), nil)
			}
			// ⚠️ **どの候補の行き先も一緒に頼むこと**（中継セッションが読むのは 1 回だけ。下の行き先の確かめに使う）。
			// 上限（`followReadMax`）は違いの出るマスだけに掛ける —— 行き先は手数ぶんしか増えない。
			var want []int
			if diffOK {
				want = slices.Clone(diff)
			}
			for _, r := range rests {
				for _, i := range followArrivals(base, since, r) {
					if !slices.Contains(want, i) {
						want = append(want, i)
					}
				}
			}
			slices.Sort(want)
			idx := followImageCells(want, rotated)
			log.Follow.Info("変わったマスでは手が分かれました（違いの出るマスだけ読みます）", "候補", len(rests), "読むマス", len(idx),
				"見た目を比べる組", len(pairs))
			return FollowAuto{Kind: FollowChoices, Reason: "違いの出るマスを読みます", ReadCells: idx, Compare: pairs,
				State: f.State()}, nil
		}
		// ⚠️ **見た目の証拠を、読んだマスより先に使うこと**（2026-10-07）。読んだ駒は成駒を成っていない姿に
		// 読み違えるが、見た目が変わったかは成駒を学習していなくても分かる。決まらなければ読んだマスで選ぶ。
		if len(promo) > 0 {
			rests = followPickByPromo(rests, promo, rotated)
			if len(rests) == 1 {
				why = append(why, "成・不成を見た目で決めた")
			}
		}
		if len(rests) > 1 {
			why = append(why, "違いの出るマスを読んで決めた")
			// ⚠️ **選ぶのに使うのは違いの出るマスの読みだけ**（行き先の読みまで渡すと、駒の種類まで比べる
			// `followCellFits` が、種類を読み違えた行き先で正しい手まで落とす）。
			rests = followPickByRead(base, since, rests, followReadOnly(read, diff, rotated), rotated)
		}
		if len(rests) != 1 {
			return unclear(len(rests), nil)
		}
		log.Follow.Info("マスを読んで手を決めました", "moves", rests[0])
	}
	if len(rests) != 1 {
		return unclear(len(rests), nil)
	}
	moves := rests[0]
	if len(moves) == 0 {
		// **今の 1 枚は先端そのもの**（足し済みの手で説明が付いた）。比べる相手にしてよい。
		return f.baseIf(FollowAuto{Kind: FollowSame, Reason: "盤面は変わっていません", Fit: 1,
			AtFrame: true, State: f.State()}, nil)
	}
	// ⚠️ **行き先に指した側の駒が読めてから控える・足すこと**（2026-10-07。`followBackedByRead`）。
	// 変わったマスの組だけで決めると「駒が来た」ことを確かめないまま手を足す。確かめは控える周にも
	// 確かめの周（もう一度ここへ来たとき）にも掛かる。読むのは数マスなので数ミリ秒。
	arrivals := followArrivals(base, since, moves)
	// looks は**成らない手の、動く前と動いた先のマス**（見た目が同じ駒かを確かめる。2026-10-08。`followLookPairs`）。
	var looks []followcam.CellPair
	if fromBase {
		looks = followLookPairs(since, moves, rotated)
	}
	if !followReadCovers(read, arrivals, rotated) {
		idx := followImageCells(arrivals, rotated)
		log.Follow.Info("変わったマスで手を割り出しました（行き先を読んで確かめます）", "moves", moves, "読むマス", len(idx),
			"見た目を比べる組", len(looks))
		// ⚠️ **見た目の比べも同じ 1 回で頼むこと**（中継セッションが読むのは 1 回だけ）。
		return FollowAuto{Kind: FollowChoices, Reason: "行き先のマスを読みます", ReadCells: idx, Compare: looks,
			State: f.State()}, nil
	}
	if ok, bad, got := followBackedByRead(base, since, moves, read, rotated); !ok {
		// ⚠️ **読めなくても、動く前の駒と見た目が同じなら「駒が来た」証拠にする**（2026-10-08。`followBackedByLooks`）。
		// ゲーム画面は直前の手のマスを赤く塗り、その上の駒を認識器が空きと読む（suteme の TODO）。▲7九玉 の玉が空きに
		// 読まれ、相手が次を指して色付けが移るまで止まった。見た目の近さは 0.982（光ったマスとつまんだ駒は 0.499）。
		if !followBackedByLooks(base, since, moves, read, looks, promo, rotated) {
			log.Follow.Info("行き先に指した側の駒が読めません（81 マスを読みます）", "moves", moves, "マス", bad, "読んだ駒", got)
			return FollowAuto{Kind: FollowUnreachable, Reason: "行き先に指した側の駒が読めません", State: f.State()}, nil
		}
		log.Follow.Info("行き先は読めませんが、動く前の駒と見た目が同じなので駒が来たと見なします", "moves", moves, "マス", bad)
		why = append(why, "行き先は見た目で確かめた（"+bad+"）")
	} else {
		why = append(why, "行き先を読んだ")
	}
	// ⚠️ **成らない手は、行き先の見た目が動く前の駒と同じであること**（2026-10-08。`followLooksDiffer`）。
	// ゲーム画面で銀をつまむと、浮いた銀のマスと光った行き先のマスが変わり、「銀が 1 マス動いた」と読めてしまう。
	// 行き先の読みは光ったマスを手前側の歩と読み、先後が合うので通って、つまんだだけの △5三銀 を足した。
	// 本当に動いたなら行き先には同じ見た目の駒がある（近さ 0.98 前後）。光ったマスは似ていない。
	if bad, sim, ok := followLooksDiffer(looks, promo); ok {
		log.Follow.Info("行き先の見た目が動いた駒と違います（81 マスを読みます）", "moves", moves, "組", bad, "近さ", fmt.Sprintf("%.3f", sim))
		return FollowAuto{Kind: FollowUnreachable, Reason: "行き先の見た目が動いた駒と違います", State: f.State()}, nil
	}
	log.Follow.Info("変わったマスで手を割り出しました", "moves", moves, "足し済み", len(since))
	if sims := followLookSims(looks, promo); sims != "" {
		why = append(why, "見た目の近さ "+sims)
	}
	reason := strings.Join(why, "・")

	k := f.takePendingPrefix(moves)
	switch {
	case k == 0:
		// **初めて出た答え。** 控えて、次の 1 枚を待つ（読んだ盤は無い）。
		f.setPending(&followPending{tip: f.mainTip(), moves: moves, why: reason})
		return FollowAuto{Kind: FollowUnique, Pending: true, Reason: "確かめています", State: f.State()}, nil
	case k == len(moves):
		return f.fastApply(moves, true, "速い経路（2 枚で確かめた）", reason)
	default:
		// **2 枚目のほうが先へ進んでいる**（`followConfirmed` と同じ）。揃った頭だけ足し、残りを控え直す。
		a, err := f.fastApply(moves[:k], false, "速い経路（2 枚で確かめた・頭だけ）", reason)
		if err != nil {
			return a, err
		}
		f.setPending(&followPending{tip: f.mainTip(), moves: slices.Clone(moves[k:]), why: reason})
		a.Pending, a.Reason = true, "確かめています"
		return a, nil
	}
}

// followReadMax は「違いの出るマスだけ読む」で読むマスの上限（多ければ 81 マスを読む）。
const followReadMax = 4

// followCellsMax は速い経路で手順を割り出す、変わったマスの数の上限（撮る側の `cellFastMax` と同じ値にすること）。
// 多い組でも、空きのマスだけが変わったなら「動いていない」と言える（`followGlowOnly`）ので、撮る側は組を全部渡してくる。
const followCellsMax = 8

// followGlowOnly は、変わったマス mask のうち色付けのマス（extra）を除いたものが、**比べる相手の局面でも先端でも
// 全部空き**かを返す（2026-10-08）。n はそのマスの数（0 なら偽）。
//
// ⚠️ **局面だけで決めること**（画像の色を見ない —— 光り方はゲームごとに違う）。駒を動かす手は必ず駒のあったマスを
// 変え、取る手は駒のあるマスへ行く。空きのマスだけが変わるのは打つ手だけで、1 手で 1 マスしか変えない。
// ⚠️ **足し済みの手（since）で駒が来た・去ったマスは空きにならない**ので、ここで偽になる（今までどおり割り出す）。
func followGlowOnly(base *position.Position, since []string, mask, extra *position.CellMask) (bool, int) {
	tip := followBoard(base, since, nil)
	if tip == nil {
		return false, 0
	}
	n := 0
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			if !mask[r][f] || (extra != nil && extra[r][f]) {
				continue
			}
			was, err1 := base.Board.At(r, f)
			now, err2 := tip.At(r, f)
			if err1 != nil || err2 != nil || !was.IsEmpty() || !now.IsEmpty() {
				return false, 0
			}
			n++
		}
	}
	return n > 0, n
}

// CellsLookUnmoved は、**直前に 81 マスで読んだ 1 枚（`FollowAuto`）で、変わったマス cells に「本譜の先端では駒が
// あるのに空きと読めたマス」が無いか**を返す（2026-10-08）。cells は撮った画像の向きの番号（`followcam.Shot.Cells`）。
//
// 中継セッションは 81 マスの読みが「何も指していない」だったとき、これが真なら `followcam.Camera.CellsUnmoved` でその組を
// 覚えさせる（続くあいだ読み直さない）。⚠️ **駒が空いて見えるマスがあれば偽** —— 駒が動いた跡かもしれない。
// ゲーム画面で ▲7八金 の金が直前の手の赤い色付けで空きと読まれ、6九 が空いたのも安く数えられて「何も指していない」に
// なった。その組を覚えたせいで 10 秒ごとの保険でしか見なくなり、▲7八金 が入るまで 1 分かかった。
// 駒をつまんで行き先が光っている 1 枚では、浮いた駒はそのマスに読め、光ったマスは空きのマスなので真になる。
func (f *follower) CellsLookUnmoved(cells []int) bool {
	mask := f.cellsMask(cells)
	f.smu.Lock()
	frame := f.lastFrame
	var tip *position.Position
	if f.tree() != nil {
		_, tip, _ = f.tree().MainTip()
	}
	f.smu.Unlock()
	if frame == nil || tip == nil || mask.Count() == 0 {
		return false
	}
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			if !mask[r][f] {
				continue
			}
			want, err1 := tip.Board.At(r, f)
			got, err2 := frame.At(r, f)
			if err1 != nil || err2 != nil || (!want.IsEmpty() && got.IsEmpty()) {
				return false
			}
		}
	}
	return true
}

// followOccupied は、変わったマス mask のうち色付けのマス（extra）を除いて、**比べる相手の局面か先端で駒があるマス**の数を返す
// （2026-10-08。`NoMoves` を立てるか）。駒をつまんだ 1 枚では浮いた駒の 1 マスだけになる。
func followOccupied(base *position.Position, since []string, mask, extra *position.CellMask) int {
	tip := followBoard(base, since, nil)
	n := 0
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			if !mask[r][f] || (extra != nil && extra[r][f]) {
				continue
			}
			was, _ := base.Board.At(r, f)
			now := was
			if tip != nil {
				now, _ = tip.At(r, f)
			}
			if !was.IsEmpty() || !now.IsEmpty() {
				n++
			}
		}
	}
	return n
}

// followLooksOnly は「変わったのは空きのマスだけで、駒は動いていない」と返す（`LooksOnly`）。
// ⚠️ **手は足さず、控えず、`AtFrame` も立てない**（光ったマスを比べる相手にしない）。
func (f *follower) followLooksOnly(n int) FollowAuto {
	log.Follow.Info("変わったのは空きのマスだけです（駒は動いていません。81 マスは読みません）", "マス", n)
	return FollowAuto{Kind: FollowSame, Reason: "駒は動いていません", LooksOnly: true, State: f.State()}
}

// followImageCells は解析の向きのマスの番号を、撮った画像の向きの番号にする（後手目線なら回す）。
func followImageCells(cells []int, rotated bool) []int {
	out := make([]int, 0, len(cells))
	for _, i := range cells {
		if rotated {
			i = 80 - i // 解析の向き → 撮った画像の向き
		}
		out = append(out, i)
	}
	return out
}

// followReadAt は read のうち、解析の向きのマス i を読んだものを返す。
func followReadAt(read []followcam.CellPiece, i int, rotated bool) (followcam.CellPiece, bool) {
	if rotated {
		i = 80 - i // 解析の向き → 撮った画像の向き
	}
	for _, cp := range read {
		if cp.Cell == i {
			return cp, true
		}
	}
	return followcam.CellPiece{}, false
}

// followReadOnly は read のうち、解析の向きのマス cells を読んだものだけを返す。
func followReadOnly(read []followcam.CellPiece, cells []int, rotated bool) []followcam.CellPiece {
	var out []followcam.CellPiece
	for _, i := range cells {
		if cp, ok := followReadAt(read, i, rotated); ok {
			out = append(out, cp)
		}
	}
	return out
}

// followReadCovers は read が解析の向きのマス cells をすべて読んでいるか。
func followReadCovers(read []followcam.CellPiece, cells []int, rotated bool) bool {
	for _, i := range cells {
		if _, ok := followReadAt(read, i, rotated); !ok {
			return false
		}
	}
	return true
}

// followArrivals は**手順 moves で駒が来たマス**（解析の向きの 81 マスの番号。昇順）を返す（2026-10-07）。
//
// どれかの手の行き先で、base から since と moves を指し終えた盤に駒が載っているマス。
// `position.backed` の見るマス（指したあとの盤で駒があり、指す前の盤と中身が違うマス）は必ずここに
// 含まれる —— 駒が新しく現れるのは行き先だけなので。⚠️ **中身の差だけで拾わないこと**: 取って
// 同じ種類の駒で取り返す手順（▲同金 の金が元から居た金と同じ）は行き先の中身が指す前と同じになり、
// 確かめるマスが 1 つも残らない（2026-10-07 の幻の手 [8f8g 7h8g] も、8g に元から 7h と同じ種類の
// 先手の駒が居ればこの形になる）。指し終えた盤で空いている行き先（来てまた動いた）は見ない（来た跡が残らない）。
func followArrivals(base *position.Position, since, moves []string) []int {
	final := followBoard(base, since, moves)
	if final == nil {
		return nil
	}
	var out []int
	for _, mv := range moves {
		if len(mv) < 4 || mv[2] < '1' || mv[2] > '9' || mv[3] < 'a' || mv[3] > 'i' {
			continue
		}
		r, f := int(mv[3]-'a'), 9-int(mv[2]-'0')
		if c, err := final.At(r, f); err != nil || c.IsEmpty() {
			continue
		}
		if i := r*9 + f; !slices.Contains(out, i) {
			out = append(out, i)
		}
	}
	slices.Sort(out)
	return out
}

// followBackedByRead は**手順 moves の行き先（`followArrivals`）に、指した側の駒が読めているか**を返す
// （2026-10-07）。偽なら、確かめられなかったマス（「８七」）と読んだ駒（"駒/向き"）を添える。
//
// **実機の症状**（2026-10-07。ゲーム画面の 47 手目）: 変わったマスは 8f・8g・7h（解析の向き）で、
// 実際は後手の飛車が 8f→8g と成った（8g に後手の龍）だけ。7h は先手の駒が別の先手の駒に見えただけだった。
// 速い経路は変わったマスの組だけで [8f8g 7h8g]（8g で先手が取り返す）を 1 通りと割り出して足した
// —— 画像の 8g には後手の龍がはっきり居た。直前の 81 マスの読みは [8f8g] だけだった。
// 81 マスの読みは「行き先に指した側の駒が読めていない手は採らない」（`position.backed`）を守って
// いたが、速い経路は行き先を 1 マスも読んでいなかった。
//
// ⚠️ **見るのは先後まで**（`position.backed` と同じ。駒の種類は認識が一番外すところで、求めると
// 正しい手まで落ちる）。⚠️ **空きと、向きも読めない "?"（切り出せない）は偽**（手が被っているなど。
// 81 マスを読む）。⚠️ **種類が読めない "?" でも、向きが読めていれば見る**（成駒は種類が読めないのが
// 普通。向きまで捨てると、成る手が毎回 81 マスの読みに落ちる）。
// read は撮った画像の向き（手前側が大文字・`Side` が up）なので、後手目線なら先後を入れ替えて比べる。
func followBackedByRead(base *position.Position, since, moves []string, read []followcam.CellPiece, rotated bool) (ok bool, bad, got string) {
	final := followBoard(base, since, moves)
	if final == nil {
		return false, "", ""
	}
	for _, i := range followArrivals(base, since, moves) {
		c, err := final.At(i/9, i%9)
		if err != nil {
			return false, squareText(i/9, i%9), ""
		}
		// 判定は `followJudge`（先後が読めて合うこと。撮った画像の手前側は、後手目線なら後手）。
		if followJudge(c, followObsRead(read, i, rotated)).side != followAgree {
			cp, _ := followReadAt(read, i, rotated)
			return false, squareText(i/9, i%9), cp.Piece + "/" + cp.Side
		}
	}
	return true, "", ""
}

// cellPieceUp は読んだ駒が**撮った画像の手前向きか**を返す。readable が偽なら向きが読めていない
// （空き・切り出せなかった "?"）。向き（`Side`）があればそれを、無ければ駒の字の大小で決める。
func cellPieceUp(cp followcam.CellPiece) (up, readable bool) {
	switch {
	case cp.Piece == "":
		return false, false
	case cp.Side == recognize.SideUp:
		return true, true
	case cp.Side == recognize.SideDown:
		return false, true
	case cp.Piece == "?":
		return false, false
	}
	l := strings.TrimPrefix(cp.Piece, "+")
	return l != "" && l == strings.ToUpper(l), true
}

// followBoard は base から since と rest を指した盤を返す（指せなければ nil）。
func followBoard(base *position.Position, since, rest []string) *position.Board {
	p := base.Clone()
	for _, m := range append(slices.Clone(since), rest...) {
		if p.ApplyMove(m) != nil {
			return nil
		}
	}
	return p.Board
}

// followDiffCells は候補ごとに**行き着く盤が違うマス**（解析の向きの 81 マスの番号）を返す。
func followDiffCells(base *position.Position, since []string, rests [][]string) []int {
	boards := make([]*position.Board, 0, len(rests))
	for _, r := range rests {
		b := followBoard(base, since, r)
		if b == nil {
			return nil
		}
		boards = append(boards, b)
	}
	var out []int
	for i := 0; i < 81; i++ {
		first, _ := boards[0].At(i/9, i%9)
		for _, b := range boards[1:] {
			if c, _ := b.At(i/9, i%9); c != first {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// followPickByRead は**読んだマスと合う候補だけ**を残す（2026-10-07）。
//
// 1 マスごとの合い方（`followCellFits`）:
//
//   - **成っていない駒の候補**: 読んだ駒がその駒そのもの（種類が同じ・成っていない）なら合う
//   - **成駒の候補**: 読んだ駒が**その駒の成っていない姿でなければ**合う（読めない "?" も合う）
//
// ⚠️ **成駒を読めることを前提にしないこと**（2026-10-07。ユーザーの判断）。実際の中継では成駒は
// 滅多に出ないので学習データに入らず、読めないのが普通。**どの駒が動いたかは局面から分かる**ので、
// 行き先が「その駒の成っていない姿」と読めなければ成っている、で決める。⚠️ **将棋の見込み
// （角・飛・歩はほぼ成る、など）は使わない**（ユーザーの判断。証拠だけで決める）。
// ⚠️ **行き先が空きに読めたら、どの候補とも合わない**（手が被っているなど別の事情。81 マスを読む）。
// ⚠️ **比べるのは駒の種類と成りだけ**（先後は候補どうしで同じ —— 指した側の駒）。向きの読み違いで落とさない。
func followPickByRead(base *position.Position, since []string, rests [][]string, read []followcam.CellPiece, rotated bool) [][]string {
	var out [][]string
	for _, r := range rests {
		b := followBoard(base, since, r)
		if b == nil {
			continue
		}
		ok, used := true, false
		for _, cp := range read {
			if cp.Cell < 0 || cp.Cell >= 81 {
				continue
			}
			i := cp.Cell
			if rotated {
				i = 80 - i // 撮った画像の向き → 解析の向き
			}
			c, err := b.At(i/9, i%9)
			if err != nil || !followCellFits(c, cp.Piece) {
				ok = false
				break
			}
			used = true
		}
		if ok && used {
			out = append(out, r)
		}
	}
	return out
}

// followCellFits は候補の盤のマス c が、読んだ駒 piece（撮った画像の向きの SFEN。空は ""、読めない "?"）と合うか。
//
// 判定は `followJudge`。合うとは、駒の有無が合い、**成っていない駒の候補なら種類が合う**こと・**成駒の候補なら種類が
// 食い違わない**こと（成っていない姿と読めなければ合う。成駒と読めた・別の駒・読めない "?" は合う）。
func followCellFits(c position.Cell, piece string) bool {
	v := followJudge(c, followObsPiece(piece))
	switch {
	case v.presence != followAgree:
		return false
	case c.IsEmpty():
		return true
	case c.Promoted():
		return v.kind != followDisagree
	}
	return v.kind == followAgree
}

// FrameFits は**比べる相手の 1 枚から今の 1 枚までに変わったマスが、その間に本譜へ足した手で
// ぴったり説明できるか**を返す（2026-10-07）。説明できれば今の 1 枚は先端と合っているので、
// 比べる相手の局面を覚え直して真を返す（中継セッションは `followcam.Camera.SetCellBase` を呼ぶ）。
//
// ⚠️ **81 マスの読みの一致度には頼らない。** ゲーム画面は色付けのマスの読み違いなどで一致度 1 に
// なりにくく、それだけを条件にしていたら比べる相手が古いまま残り、速い経路がほとんど効かなかった
// （2026-10-07 に実機で踏んだ。変わったマスが 10 → 34 と増え続けた）。**画素の変化と、足した手の
// 動かすマスが合うか**で確かめる（`position.ChangedFits`）。
// cells は撮った画像の向きのマスの番号（`followcam.Shot.Cells`）。
func (f *follower) FrameFits(cells []int) bool {
	base, since, extra, ok := f.followFromBase()
	if !ok {
		return false
	}
	mask := f.cellsMask(cells)
	if len(since) == 0 {
		// 何も足していない。何も変わっていなければ（露出の揺れだけ）合っている。
		return mask.Count() == 0
	}
	to := base.Clone()
	for _, m := range since {
		if to.ApplyMove(m) != nil {
			return false
		}
	}
	if !position.ChangedFits(base.Board, to.Board, mask, extra) {
		return false
	}
	f.markBase()
	log.Follow.Info("比べる相手を今の 1 枚に替えました（足した手で変わったマスの説明が付いた）", "足した手", len(since))
	return true
}

// fastApply は速い経路で決まった手順を本譜の先に足す。atFrame は「これでこの 1 枚とぴったり合う」か。
func (f *follower) fastApply(moves []string, atFrame bool, route, why string) (FollowAuto, error) {
	f.smu.Lock()
	if f.tree() == nil {
		f.smu.Unlock()
		return FollowAuto{}, fmt.Errorf("解析タブにまだ局面がありません")
	}
	atTip := f.tree().CurrentID() == mainTipID(f.tree())
	rev, main, rootSfen := f.host.hostRev(), f.tree().MainLine(), rootSFEN(f.tree())
	f.smu.Unlock()

	text := followText(rootSfen, main, moves)
	applied, err := f.followApply(moves, rev, atTip)
	if err != nil {
		return FollowAuto{}, err
	}
	logAdded(route, len(main)+1, moves, text, true, why)
	return f.baseIf(FollowAuto{Kind: FollowUnique, Applied: true, Moves: slices.Clone(moves), Text: text,
		Added: applied.Added, Guess: true, Fit: 1, Note: applied.Note, State: applied.State,
		AtFrame: atFrame}, nil)
}

// logAdded は**追従が手を足したことを 1 行で残す**（2026-10-08。デバッグ用のまとめ）。
//
// 「▲7九玉 がおかしい」と言われたとき、どの経路で・何を根拠に足したかを、この 1 行で引けるようにする
// （それまでは前後 100 行を読んで組み立てていた）。録画の名前は撮る側が直後に「録画を残しました」で出す。
// ⚠️ **棋譜のコメントには書かない**（KIF に書き出され、棚にも残る。人の検討と混ざる）。
// route は経路（速い経路・81 マスの読み）、startPly は 1 手目の手数、why は根拠（無ければ空）。
func logAdded(route string, startPly int, moves, text []string, guess bool, why string) {
	log.Follow.Info("手を足しました（追跡）", "手数", startPly, "手", strings.Join(text, " "), "usi", strings.Join(moves, " "),
		"経路", route, "根拠", why, "推測", guess)
}

// followProbeWhy は 81 マスの読みの下見 p から、手を足した根拠を短く組み立てる（`logAdded`）。
func followProbeWhy(p FollowProbe) string {
	why := []string{fmt.Sprintf("一致度 %.3f", p.Fit)}
	switch {
	case p.Kind == FollowChoices:
		why = append(why, fmt.Sprintf("順番の決まらない %d 通りから 1 本目", len(p.Candidates)))
	case p.Guess:
		why = append(why, "候補の 1 位")
	default:
		why = append(why, "1 通りに繋がった")
	}
	if len(p.Fixed) > 0 {
		fixed := make([]string, 0, len(p.Fixed))
		for _, f := range p.Fixed {
			fixed = append(fixed, f.Square+"("+f.Was+"→"+f.Now+")")
		}
		why = append(why, "覆したマス "+strings.Join(fixed, ","))
	}
	return strings.Join(why, "・")
}

// followLookSims は成らない手の見た目の近さを「7f>7e=0.977」の形で並べる（撮った画像の向き。測れた組だけ）。
func followLookSims(looks []followcam.CellPair, promo []PromoEvidence) string {
	var out []string
	for _, l := range looks {
		for _, e := range promo {
			if e.From == l.From && e.To == l.To {
				out = append(out, fmt.Sprintf("%s>%s=%.3f", followCellName(l.From), followCellName(l.To), e.Sim))
			}
		}
	}
	return strings.Join(out, ",")
}

// followCellName は 81 マスの番号を USI のマス（"7g"）にする（ログ用）。
func followCellName(i int) string {
	if i < 0 || i > 80 {
		return "?"
	}
	return string([]byte{byte('9' - i%9), byte('a' + i/9)})
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

// pendingMoves は**今の先端から控えている手順**（無ければ nil）。
func (f *follower) pendingMoves() []string {
	tip := f.mainTip()
	f.smu.Lock()
	defer f.smu.Unlock()
	if f.pending == nil || f.pending.tip != tip {
		return nil
	}
	return slices.Clone(f.pending.moves)
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
func (f *follower) pendingStillFits(board *position.Board, cost *position.CellCost, unseen *position.CellMask) bool {
	if board == nil || cost == nil {
		return false
	}
	f.smu.Lock()
	pend := f.pending
	if pend == nil || f.tree() == nil || pend.tip != mainTipID(f.tree()) {
		f.smu.Unlock()
		return false
	}
	_, from, err := f.tree().MainTip()
	f.smu.Unlock()
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
	tip   int
	moves []string
	// why は速い経路で決めた根拠（確かめの周で足すときにまとめの 1 行へ出す。2026-10-08）。
	why     string
	board   *position.Board
	cost    *position.CellCost
	unseen  *position.CellMask
	rotated bool
}

func (f *follower) setPending(p *followPending) {
	f.smu.Lock()
	f.pending = p
	f.smu.Unlock()
}

// takePendingPrefix は控え（今の先端からのもの）と今の答えが**頭から何手揃っているか**を返す。
// 1 手でも揃っていれば控えを取り出す（0 なら何もしない）。
//
// ⚠️ **完全一致を求めないこと**（2026-10-06）。2 枚目が 1 手先まで進んでいると
// 控え直しが続き、速い手が続くあいだ 1 手も足せなかった（`followConfirmed`）。
func (f *follower) takePendingPrefix(moves []string) int {
	tip := f.mainTip()
	f.smu.Lock()
	defer f.smu.Unlock()
	if f.pending == nil || f.pending.tip != tip {
		return 0
	}
	k := 0
	for k < len(moves) && k < len(f.pending.moves) && moves[k] == f.pending.moves[k] {
		k++
	}
	if k > 0 {
		f.pending = nil
	}
	return k
}

// mainTip は本譜の先端の節点 id（局面が無ければ -1）。
func (f *follower) mainTip() int {
	f.smu.Lock()
	defer f.smu.Unlock()
	if f.tree() == nil {
		return -1
	}
	return mainTipID(f.tree())
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
func (f *follower) followFrom(board *position.Board, cost *position.CellCost, unseen *position.CellMask, rotated bool) (a FollowAuto, err error) {
	// ⚠️ **先端を見ていたなら付いていく**（`tail -f` と同じ）。
	// 戻って読んでいるなら**動かさない**（`mergeReloadLocked` と同じ約束）。
	// ⚠️ **判定は繰り返しに入る前の 1 回だけ** —— 自分で足した手で先端が動くので、
	// 中で見ると**2 手目以降は必ず「先端ではない」**になる。
	f.smu.Lock()
	atTip := f.tree() != nil && f.tree().CurrentID() == mainTipID(f.tree())
	f.smu.Unlock()

	// unexplained は 1 周目の下見が「駒が来たのに説明が付かない」だったか（`looksFlipped` を呼ぶ条件）。
	unexplained := false
	// ⚠️ **1 枚から据える答えは 1 つだけ**（2026-10-10。判断の単位は 1 枚）。2 回目の下見は、据えた先端が
	// この 1 枚とぴったり合うか（`AtFrame`）を見るためだけに回し、**そこから手を継ぎ足さない**。
	// 以前は足せる限り 12 回まで繰り返したので、`Connect` の約束（深さ 4・修復の予算・一意性）が 1 回ごとにしか
	// 効かず、空きを覆した 4 手の修復に、2五 の歩を桂に覆した 2 手の修復を継いで 6 手を取り違えた（中継で踏んだ）。
	// 何手も写った 1 枚は `Connect` が 1 回で取る（ぴったり合う手順は `followDeepDepth` まで探す）。
	for i := 0; i < 2; i++ {
		p, e := f.followProbe(board, cost, unseen, cost != nil, rotated)
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
			// ⚠️ **見えないマスがある 1 枚は相手にしないこと**（2026-10-07 に実機で踏んだ。ShogiHome の
			// ダイアログが盤の右半分に被った 1 枚が、残りのマスでぴったり合ったので相手になり、閉じたあと
			// 33〜54 マスが変わって見えて速い経路が二度と使えなくなった）。
			// ⚠️ **駒の種類の読み違いは問わない**（`KindOnly`。比べる相手に要るのは駒の有無と先後で、
			// 成った駒を少し読み違えるだけで相手になれず、速い経路が止まっていた）。
			a.AtFrame = p.Kind == FollowSame && (cost == nil || p.KindOnly) && unseen.Count() == 0
			break
		}
		if i > 0 {
			// **据えた答えでは、この 1 枚を説明しきれていない。** 残りは継ぎ足さない（推測の上に推測を積まない）。
			// この 1 枚は先端と合っていないので、速い経路の比べる相手にもしない（`AtFrame` は偽のまま）。
			log.Follow.Info("据えた答えのあとにも指された跡が残りますが、同じ 1 枚からは継ぎ足しません",
				"moves", p.Candidates[0].Moves, "kind", p.Kind)
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
		if p.Kind == FollowChoices {
			if p.More || p.Unsettled {
				break
			}
			a.Guess = true
		}
		moves := p.Candidates[0].Moves
		f.smu.Lock()
		ply := len(f.tree().MainLine()) + 1
		f.smu.Unlock()
		applied, aerr := f.followApply(moves, p.Rev, atTip)
		if aerr != nil {
			if a.Applied {
				break
			}
			return a, aerr
		}
		a.Applied = true
		logAdded("81 マスの読み", ply, moves, p.Candidates[0].Text, p.Guess || p.Kind == FollowChoices, followProbeWhy(p))
		a.Moves = append(a.Moves, moves...)
		a.Text = append(a.Text, p.Candidates[0].Text...)
		a.Added += applied.Added
		// ⚠️ **推測が 1 つでも混じったら印を付ける**（`Node.Guess` は手ごとに付く）。
		a.Guess = a.Guess || p.Guess
		a.Fit, a.Fixed, a.Mismatch = p.Fit, p.Fixed, p.Mismatch
		a.Note, a.State = applied.Note, applied.State
	}
	if !a.Applied {
		a.State = f.State()
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
			a.Flipped, a.FlipMove = f.looksFlipped(board, cost, unseen, a.Fit)
		}
	}
	if a.Added > 1 {
		log.Follow.Info("1 枚から追いつきました", "moves", a.Moves, "guess", a.Guess)
	}
	return a, nil
}

// followDeepDepth は**認識とぴったり合う手順と、修復の答えより厳密に安く合う手順**を探す手数の上限
// （`position.ConnectOptions.DeepDepth`。2026-10-10）。
//
// 修復の最初の答えは `position.DefaultMaxDepth`（4 手）までで探すが、それよりよく合う手順は深くても探す。
// 大盤のあいだに 6 手進んだ中継の 1 枚で、4 手の修復が通って取り違えた（ぴったり合う 6 手は 5ms で見つかる。
// 1 マス読み違えた 1 枚では修復より安い側で約 190ms。`position.BenchmarkConnectDeep`）。
// ⚠️ **当て推量**。深くしても `MaxWaste`（下界 + 2 手）で切れるので、効くのは本当に隔たりが大きい 1 枚だけ。
const followDeepDepth = 8

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
	// Rewound は**推測で足した末尾の手を差し替えた**か（2026-10-08。`followRewindApply`）。真なら Note に
	// 何を何に差し替えたかが入る（中継セッションは帯に出す）。Moves / Text は新しく本譜に入った手（取り消しだけなら空）。
	Rewound bool `json:"rewound"`
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
	// 真なら、この 1 枚を**速い経路の比べる相手**にしてよい（中継セッションが `followcam.Camera.SetCellBase` を呼ぶ）。
	// ⚠️ **ぴったり（一致度 1）のときだけ立てること。** 行き先の駒が読めていないだけの 1 枚
	// （一致度 0.9965）を比べる相手にすると、その手のマスの変化が二度と見えなくなる。
	AtFrame bool `json:"atFrame"`
	// ReadCells は**速い経路で、読めば決まる・確かめられるマス**（撮った画像の向きの番号。2026-10-07）。
	// 手が分かれたときの違いの出るマスと、決まった手順の行き先（指した側の駒が来たかを確かめる）。
	// 空でなければ中継セッションは `followcam.Camera.ReadCells` で読み、結果を添えて `FollowCells` をもう一度呼ぶ。
	ReadCells []int `json:"readCells"`
	// Compare は**成・不成を見た目で確かめたい手の、動く前と動いた先のマス**（撮った画像の向き。2026-10-07。
	// `followPromoPairs`）。空でなければ中継セッションは `followcam.Camera.ComparePieces` で測り、結果を
	// `PromoEvidence` にして同じ 1 枚でもう一度呼ぶ（`FollowAuto` / `FollowCells`）。⚠️ **これを返した周は
	// 局面も控えも 1 つも動かしていない**（もう一度呼ばれて初めて決める）。
	Compare []followcam.CellPair `json:"compare"`
	// NoMoves は**速い経路で、変わったマスを説明できる手順が 1 つも無かった**か（2026-10-08）。
	// ゲーム画面で駒を掴むと、その駒が浮いて行き先が光り、考えているあいだ同じ組が続く。そこで 81 マスを
	// 読むと 1 周が 1〜5 秒塞がり、そのあいだに指した手と相手の応手がまとめて遅れた（1 組 10 秒前後）。
	// 真なら中継セッションはすぐ 81 マスを読まず、`followcam.Camera.CellsUnclear` で組が変わるのを待たせる
	// （ふるいが待つ上限で読む）。⚠️ **成・不成や打った駒で決まらなかった周には立てない**（手はある。すぐ読む）。
	NoMoves bool `json:"noMoves"`
	// LooksOnly は**速い経路で、変わったのは空きのマスだけで、駒は動いていない**か（2026-10-08。`followGlowOnly`）。
	// 駒台の駒をつまんで、打てるマスが光っている 1 枚。真なら中継セッションは 81 マスを読まず、`followcam.Camera.CellsUnmoved` で
	// その組を覚えさせる（ふるいは同じ組を「変わっていない」に数え、保険の 1 枚は残る）。
	LooksOnly bool `json:"looksOnly"`
	// DropChoices は**打った駒の種類が決まらず、人に聞く候補**（2026-10-09。`offerDrop`）。空でなければ中継セッションは枠の下の帯に
	// 並べ、選ばれたら `StudyService.FollowChoose` に `Moves` を渡す。
	DropChoices []FollowChoice `json:"dropChoices,omitempty"`
}

// PromoEvidence は followcam.CellPair を測った**見た目の近さ**（`ikkyoku.PieceSimilarity`。-1〜1。2026-10-07）。
// 中継セッションは測れなかった組（ちょうど -1）を渡さない（2026-10-08 から、負の近さは渡す —— 似ていない駒どうしは負になりうる）。
type PromoEvidence struct {
	From int     `json:"from"`
	To   int     `json:"to"`
	Sim  float64 `json:"sim"`
	// Same は測った組の `followcam.CellPair.Same`（打った駒の比べ。成・不成の証拠には使わない）。
	Same bool `json:"same,omitempty"`
}

// 成・不成を見た目で決めるしきい値（2026-10-07）。⚠️ **仮の値**（実機で詰めること）。
//
// 録画の全部（今日のゲーム画面と 10/05〜10/06 の中継。約 580 手）を、動く前のマスと動いた先のマスの
// 正規化相互相関で測った: 成った 39 手（中継の 6 手を含む。重複を除くと約 15 手）は 0.19〜0.55、
// 成っていない約 540 手のうち 516 手は 0.9 以上（中継の 1 局だけ 0.77〜0.88）。龍を飛と読んで不成で
// 記録した 2 手（▲2三飛成 を 2f2c、△8七飛成 を 8f8g）は 0.552 と 0.555。
// 計り直しはスキル `ikkyoku-follow` の `references/promo_similarity.py`。
const (
	// promoChangedBelow より低ければ**見た目が変わった ＝ 成った**。
	promoChangedBelow = 0.65
	// promoSameFrom 以上なら**見た目が同じ ＝ 成っていない**。間は証拠にしない（今までどおりの決め方）。
	promoSameFrom = 0.80
)

// promoVerdict は近さ sim から成ったかを返す。decided が偽なら証拠にならない（間の値・測れなかった）。
func promoVerdict(sim float64) (promoted, decided bool) {
	switch {
	case sim < 0:
		return false, false
	case sim < promoChangedBelow:
		return true, true
	case sim >= promoSameFrom:
		return false, true
	}
	return false, false
}

// usiSquare は USI のマス（"7g"）を盤の座標にする。
func usiSquare(sq string) (rank, file int, ok bool) {
	if len(sq) != 2 || sq[0] < '1' || sq[0] > '9' || sq[1] < 'a' || sq[1] > 'i' {
		return 0, 0, false
	}
	return int(sq[1] - 'a'), 9 - int(sq[0]-'0'), true
}

// usiFromTo は盤の上を動く手の元と行き先を、解析の向きの 81 マスの番号で返す（打つ手・読めない手は ok が偽）。
func usiFromTo(mv string) (from, to int, ok bool) {
	if len(mv) < 4 || mv[1] == '*' {
		return 0, 0, false
	}
	fr, ff, ok1 := usiSquare(mv[0:2])
	tr, tf, ok2 := usiSquare(mv[2:4])
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return fr*9 + ff, tr*9 + tf, true
}

// usiTouches は手 mv がマス i（解析の向き）を動かすか（元か行き先）。
func usiTouches(mv string, i int) bool {
	if m := usiCells(mv); m != nil {
		return m[i/9][i%9]
	}
	return false
}

// followPromoPairs は**成・不成を見た目で確かめられる手**の、動く前と動いた先のマス（撮った画像の向き）を返す
// （2026-10-07）。
//
// base は速い経路の比べる相手の局面（`followFromBase`）、since はそこから本譜の先端までの手、moves は
// 先端から足そうとしている手順。拾うのは次の 3 つを満たす手だけ:
//
//   - **その時点で成りも不成も合法**（どちらか一方しか指せないなら見た目に頼る必要が無い）
//   - **動く前のマスを since と、それより前の手が 1 度も動かしていない**（比べる相手の 1 枚のそのマスに、
//     指した駒がそのまま写っている）
//   - **動いた先のマスを、それより後の手が動かしていない**（今の 1 枚のそのマスに、指した駒が写っている）
//
// ⚠️ **将棋の見込み（敵陣に入った飛車はほぼ成る、など）は使わない**（ユーザーの判断）。ここは
// 「比べられるか」を決めるだけで、成ったかどうかは測った見た目で決める。
func followPromoPairs(base *position.Position, since, moves []string, rotated bool) []followcam.CellPair {
	p := base.Clone()
	for _, m := range since {
		if p.ApplyMove(m) != nil {
			return nil
		}
	}
	var out []followcam.CellPair
	for j, mv := range moves {
		from, to, ok := usiFromTo(mv)
		plain := strings.TrimSuffix(mv, "+")
		if ok && followBothLegal(p, plain) && !slices.ContainsFunc(since, func(m string) bool { return usiTouches(m, from) }) &&
			!slices.ContainsFunc(moves[:j], func(m string) bool { return usiTouches(m, from) }) &&
			!slices.ContainsFunc(moves[j+1:], func(m string) bool { return usiTouches(m, to) }) {
			pair := followcam.CellPair{From: from, To: to}
			if rotated {
				pair = followcam.CellPair{From: 80 - from, To: 80 - to} // 解析の向き → 撮った画像の向き
			}
			if !slices.Contains(out, pair) {
				out = append(out, pair)
			}
		}
		if p.ApplyMove(mv) != nil {
			break
		}
	}
	return out
}

// followLookPairs は手順 moves のうち**成らない手**（打つ手を除く）の、動く前と動いた先のマス（撮った画像の向き）を返す
// （2026-10-08。`followLooksDiffer`）。比べてよいのは、動く前のマスを since とその手より前の手が動かしておらず、
// 動いた先のマスをその手より後の手が動かしていない手だけ（比べる相手の 1 枚と今の 1 枚に、指した駒がそれぞれ写っている）。
// 条件は `followPromoPairs` と同じで、成りも不成も指せるかは問わない。
func followLookPairs(since, moves []string, rotated bool) []followcam.CellPair {
	var out []followcam.CellPair
	for j, mv := range moves {
		if strings.HasSuffix(mv, "+") {
			continue // 成る手は見た目が変わる（`followPromoPairs` の受け持ち）
		}
		from, to, ok := usiFromTo(mv)
		if !ok ||
			slices.ContainsFunc(since, func(m string) bool { return usiTouches(m, from) }) ||
			slices.ContainsFunc(moves[:j], func(m string) bool { return usiTouches(m, from) }) ||
			slices.ContainsFunc(moves[j+1:], func(m string) bool { return usiTouches(m, to) }) {
			continue
		}
		pair := followcam.CellPair{From: from, To: to}
		if rotated {
			pair = followcam.CellPair{From: 80 - from, To: 80 - to} // 解析の向き → 撮った画像の向き
		}
		if !slices.Contains(out, pair) {
			out = append(out, pair)
		}
	}
	return out
}

// followBackedByLooks は、手順 moves の行き先（`followArrivals`）が**どれも、読みか見た目で「指した側の駒が来た」と
// 言えるか**を返す（2026-10-08）。読みで言えないマスは、成らない手の組 looks で動いた先がそのマスのもの（撮った画像の
// 向き）に、近さ `promoSameFrom` 以上の証拠があるときだけ来たと見なす。
//
// ⚠️ **見た目で救うのは「空き・読めない」と読めたマスだけ**（相手の駒と読めたマスは救わない —— 読みが違うと言っている）。
// ⚠️ **証拠が無い・間の値なら救わない**（今までどおり 81 マスを読む）。
func followBackedByLooks(base *position.Position, since, moves []string, read []followcam.CellPiece, looks []followcam.CellPair,
	promo []PromoEvidence, rotated bool) bool {
	final := followBoard(base, since, moves)
	if final == nil {
		return false
	}
	for _, i := range followArrivals(base, since, moves) {
		c, err := final.At(i/9, i%9)
		if err != nil {
			return false
		}
		cp, found := followReadAt(read, i, rotated)
		up, readable := cellPieceUp(cp)
		if found && readable {
			if (up != rotated) != c.Black() {
				return false // 相手の駒と読めた
			}
			continue
		}
		img := i
		if rotated {
			img = 80 - i // 解析の向き → 撮った画像の向き
		}
		same := false
		for _, l := range looks {
			if l.To != img {
				continue
			}
			for _, e := range promo {
				if e.From == l.From && e.To == l.To && e.Sim >= promoSameFrom {
					same = true
				}
			}
		}
		if !same {
			return false
		}
	}
	return true
}

// followLooksDiffer は、成らない手の組 looks のどれかで、**行き先の見た目が動く前の駒と違う**（近さが
// `promoChangedBelow` より低い）かを返す。測れなかった組（promo に無い）は証拠にしない。
func followLooksDiffer(looks []followcam.CellPair, promo []PromoEvidence) (followcam.CellPair, float64, bool) {
	for _, l := range looks {
		for _, e := range promo {
			if e.From == l.From && e.To == l.To && e.Sim < promoChangedBelow {
				return l, e.Sim, true
			}
		}
	}
	return followcam.CellPair{}, 0, false
}

// followPromoOnly は、下見の候補が**同じ手順で、成るか成らないかだけが違う**なら、その手順（1 本目）を返す（2026-10-08）。
// そうでなければ nil。⚠️ **並べきれなかった（`More`）なら nil**（見えていない候補がある）。
func followPromoOnly(p FollowProbe) []string {
	if len(p.Candidates) < 2 || p.More {
		return nil
	}
	first := p.Candidates[0].Moves
	split := false
	for _, c := range p.Candidates[1:] {
		if len(c.Moves) != len(first) {
			return nil
		}
		for i, m := range c.Moves {
			if strings.TrimSuffix(m, "+") != strings.TrimSuffix(first[i], "+") {
				return nil
			}
			split = split || m != first[i]
		}
	}
	if !split {
		return nil
	}
	return first
}

// followBothLegal は局面 p で、成らない手 plain と成る手 plain+"+" の両方が合法か。
func followBothLegal(p *position.Position, plain string) bool {
	sfenStr, err := p.SFEN()
	if err != nil {
		return false
	}
	moves, err := legal.Moves(sfenStr)
	if err != nil {
		return false
	}
	var bare, promo bool
	for _, m := range moves {
		switch m.USI {
		case plain:
			bare = true
		case plain + "+":
			promo = true
		}
	}
	return bare && promo
}

// followApplyPromo は**成・不成の証拠で、読んだ盤の動いた先のマスを書き換える**（2026-10-07）。
//
// 証拠ごとに、動いた先のマスに**指した側の駒**（比べる相手の局面の、動く前のマスの駒と同じ先後）が
// 読めていれば、近さが `promoChangedBelow` より低ければその駒の成った姿に、`promoSameFrom` 以上なら
// 成っていない姿に置き換える。費用表があればそのマスは確信度 1 にする（覆させない）。
// ⚠️ **空き・相手の駒・間の値なら触らない**（今までどおり読んだ盤で決める）。
// ⚠️ **下見の前に盤を書き換えること** —— `Rank` / `Connect`・控え（`followPending.board`）・確かめ
// （`FollowConfirm`）が同じ盤を見るので、2 枚目でも同じ答えになる。
// board と cost は解析の向き（`followFrame` が回したあと）、証拠は撮った画像の向き。
func (f *follower) followApplyPromo(board *position.Board, cost *position.CellCost, promo []PromoEvidence, rotated bool) {
	if board == nil || len(promo) == 0 {
		return
	}
	base, _, _, ok := f.followFromBase()
	if !ok {
		log.Follow.Info("成・不成の見た目の証拠を使えません（比べる相手の局面がありません）", "証拠", len(promo))
		return
	}
	for _, ev := range promo {
		if ev.Same {
			continue // 打った駒の比べ（`followDropLooks`）
		}
		promoted, decided := promoVerdict(ev.Sim)
		from, to := ev.From, ev.To
		if rotated {
			from, to = 80-from, 80-to // 撮った画像の向き → 解析の向き
		}
		if !decided || from < 0 || from >= 81 || to < 0 || to >= 81 {
			continue
		}
		mover, err := base.Board.At(from/9, from%9)
		if err != nil || mover.IsEmpty() {
			continue
		}
		got, err := board.At(to/9, to%9)
		if err != nil || got.IsEmpty() || got.Black() != mover.Black() {
			continue
		}
		c, err := position.NewCell(mover.Piece(), mover.Black(), promoted)
		if err != nil {
			continue // 成れない駒（金・玉）
		}
		if board.Set(to/9, to%9, c) != nil {
			continue
		}
		if cost != nil {
			cost[to/9][to%9] = 1
		}
		log.Follow.Info("見た目を比べて成・不成を決めました", "元", squareText(from/9, from%9), "先", squareText(to/9, to%9),
			"近さ", fmt.Sprintf("%.3f", ev.Sim), "成り", promoted, "読んだ駒", got.Mark())
	}
}

// followPickByPromo は**成・不成の証拠と食い違う候補を落とす**（速い経路。2026-10-07）。
//
// 候補の手順に、証拠と同じ元と行き先の手があり、その成・不成が証拠と違えば落とす（そういう手を含まない
// 候補は残す）。全部落ちたら空を返す（呼ぶ側は決めない）。
func followPickByPromo(rests [][]string, promo []PromoEvidence, rotated bool) [][]string {
	out := rests
	for _, ev := range promo {
		promoted, decided := promoVerdict(ev.Sim)
		if !decided {
			continue
		}
		from, to := ev.From, ev.To
		if rotated {
			from, to = 80-from, 80-to
		}
		var keep [][]string
		for _, r := range out {
			ok := true
			for _, mv := range r {
				if f, t, isMove := usiFromTo(mv); isMove && f == from && t == to && strings.HasSuffix(mv, "+") != promoted {
					ok = false
					break
				}
			}
			if ok {
				keep = append(keep, r)
			}
		}
		if len(keep) < len(out) {
			log.Follow.Info("見た目を比べて成・不成を決めました（速い経路）", "元", squareText(from/9, from%9),
				"先", squareText(to/9, to%9), "近さ", fmt.Sprintf("%.3f", ev.Sim), "成り", promoted, "候補", len(keep))
		}
		out = keep
	}
	return out
}

// followPromoSplit は**成・不成だけで分かれた候補**の、成・不成を見た目で確かめられる手のマスを返す（速い経路）。
// 候補のどれかに成る手 mv+"+" があり、別の候補の同じ位置に成らない mv があるもの。確かめられるかの条件は
// `followPromoPairs` と同じ（成りも不成も合法かは、候補に両方あることで分かっている）。
func followPromoSplit(since []string, rests [][]string, rotated bool) []followcam.CellPair {
	var out []followcam.CellPair
	for _, r := range rests {
		for j, mv := range r {
			if !strings.HasSuffix(mv, "+") {
				continue
			}
			plain := strings.TrimSuffix(mv, "+")
			if !slices.ContainsFunc(rests, func(o []string) bool { return len(o) > j && o[j] == plain }) {
				continue
			}
			from, to, ok := usiFromTo(mv)
			if !ok || slices.ContainsFunc(since, func(m string) bool { return usiTouches(m, from) }) ||
				slices.ContainsFunc(r[:j], func(m string) bool { return usiTouches(m, from) }) ||
				slices.ContainsFunc(r[j+1:], func(m string) bool { return usiTouches(m, to) }) {
				continue
			}
			pair := followcam.CellPair{From: from, To: to}
			if rotated {
				pair = followcam.CellPair{From: 80 - from, To: 80 - to}
			}
			if !slices.Contains(out, pair) {
				out = append(out, pair)
			}
		}
	}
	return out
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
func (f *follower) looksFlipped(board *position.Board, cost *position.CellCost, unseen *position.CellMask, fit float64) (bool, string) {
	if board == nil {
		return false, ""
	}
	f.smu.Lock()
	if f.tree() == nil {
		f.smu.Unlock()
		return false, ""
	}
	_, from, err := f.tree().MainTip()
	main, rootSfen := f.tree().MainLine(), rootSFEN(f.tree())
	f.smu.Unlock()
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
	log.Follow.Info("盤を回すと 1 手で説明が付きます（目線が逆かもしれません）",
		"moves", top.Moves, "fit", top.Fit, "margin", got.Margin, "今の向きの一致度", fit)
	return true, text
}
