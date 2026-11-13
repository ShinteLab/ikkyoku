package app

// 81 マスの読みを**比べる相手の局面から説明する**（2026-10-11。追従の作り直し。理由は `_docs/design-follow.md`）。
//
// **比べる相手の局面**は、本譜の先端とぴったり合った最後の 1 枚の局面（`markBase` / `followFromBase`）。
// そこから先端までに追従が足した手（足し済みの手）は、どれも 1 枚とぴったり合ったことの無い推測の手。
// 以前の 81 マスの読みは、先端から足す下見（`Connect`）と、足し済みの手を戻す差し替え（比べる相手の局面まで戻る
// 候補）を別々に探していたので、同じ 1 枚でも**どちらの道を通ったかで答えが変わりえた**（観点 1）。
// 速い経路（`FollowCells`）と同じく、**比べる相手の局面から 1 回だけ探し**、
//
//   - 足し済みの手で始まる答え → 残りを先端から足す（今までの「先端から足す」）
//   - 足し済みの手の途中で分かれる答え → 分かれたところから差し替える（`followRewindProbe` の関門にかける）
//
// を 1 つの答えから決める。⚠️ **足し済みの手が無い（比べる相手＝先端）ときは使わない**（今までどおり先端から探す）。
// ⚠️ **差し替えの関門は変えない**（先後と駒の有無で比べる・推測の手だけ・2 枚続けて同じ差し替えで据える）。

import (
	"context"
	"slices"

	"github.com/ShinteLab/ikkyoku/position"
)

// followBaseAnswer は比べる相手の局面から撮った 1 枚を説明した答え（`followExplainFromBase`）。
type followBaseAnswer struct {
	// r は比べる相手の局面からの `Connect` の答え（手順は比べる相手の局面から）。
	r position.ConnectResult
	// rests は**足し済みの手で始まる答え**の残り（先端から足す手順。重なりは除く）。無ければ nil。
	rests [][]string
	// rewind は**足し済みの手の途中で分かれる答え**（rests が無いときだけ。差し替えの候補）。
	// ⚠️ **据えてよいかはまだ決まっていない**（`followRewindProbe` の関門にかける）。
	rewind *followRewind
}

// followBaseFrom は**81 マスの読みを比べる相手の局面から説明するか**と、その局面・足し済みの手・本譜を返す。
//
// 使うのは、比べる相手の局面を覚えていて、そこから先端までに足し済みの手があり、それが**全部推測の手**のときだけ
// （印の無い手まで差し替えの候補にしない。`position.Study.MainGuessTail`）。
func (f *follower) followBaseFrom() (base *position.Position, since, line []string, ok bool) {
	base, since, _, ok = f.followFromBase()
	if !ok || len(since) == 0 {
		return nil, nil, nil, false
	}
	f.smu.Lock()
	defer f.smu.Unlock()
	if f.tree() == nil || len(since) > len(f.tree().MainGuessTail()) {
		return nil, nil, nil, false
	}
	return base, since, f.tree().MainLine(), true
}

// followExplainFromBase は撮った盤 board を**比べる相手の局面 base から**説明する（`Connect` は 1 回だけ）。
//
// 探す深さは足し済みの手のぶん深くする（先端から探していたときと同じだけ先まで届くように）。
// ⚠️ **足し済みの手で始まる答えが 1 つでもあれば、分かれる答えは差し替えの候補にしない**（速い経路と同じ）。
// ⚠️ **分かれる答えは、一意か、行き着く局面が揃うときだけ候補にする**（`followSameEnd`。一意でないのに選ばない）。
// 揃うときは、足し済みの手といちばん長く揃う答え（いちばん浅く戻る差し替え）を採る。
func followExplainFromBase(ctx context.Context, base *position.Position, since, line []string, board *position.Board, opt position.ConnectOptions) (followBaseAnswer, error) {
	o := opt
	o.MaxDepth = len(since) + position.DefaultMaxDepth
	o.DeepDepth = len(since) + followDeepDepth
	r, err := position.Connect(ctx, base, board, o)
	if err != nil {
		return followBaseAnswer{}, err
	}
	ans := followBaseAnswer{r: r}
	keep0 := len(line) - len(since)
	switch r.Stop {
	case position.StopSame:
		// 撮った盤は比べる相手の局面そのもの（足し済みの手を全部取り消す答え）。
		ans.rewind = &followRewind{line: slices.Clone(line), keep: keep0, why: "比べる相手の局面から説明した"}
		return ans, nil
	case position.StopFound:
	default:
		return ans, nil
	}
	for _, sol := range r.Solutions {
		if len(sol) < len(since) || !slices.Equal(sol[:len(since)], since) {
			continue
		}
		rest := sol[len(since):]
		if !slices.ContainsFunc(ans.rests, func(x []string) bool { return slices.Equal(x, rest) }) {
			ans.rests = append(ans.rests, slices.Clone(rest))
		}
	}
	if len(ans.rests) > 0 || r.More || (!r.Unique && !followSameEnd(base, r.Solutions)) {
		return ans, nil
	}
	best, bj := []string(nil), -1
	for _, sol := range r.Solutions {
		j := 0
		for j < len(sol) && j < len(since) && sol[j] == since[j] {
			j++
		}
		if j > bj {
			best, bj = sol, j
		}
	}
	ans.rewind = &followRewind{line: slices.Clone(line), keep: keep0 + bj, moves: slices.Clone(best[bj:]),
		why: "比べる相手の局面から説明した"}
	return ans, nil
}

// fits は**足し済みの手で始まる答えが、撮った盤とぴったり合い、下見がそのまま採れるか**（認識を覆していない・
// 並べきれている・一意か行き着く局面が揃う）。偽なら「比べる相手の局面から合う答えが無い」側に数える
// （`followRewindProbe` が比べる相手より前へ戻る差し替えを試す）。tip は本譜の先端の局面。
//
// ⚠️ **修復の答え（認識を覆して合わせた答え）は「合う」に数えないこと**。比べる相手の局面は種類の読み違いを問わずに
// 決めている（`KindOnly`）ので、その前の推測の手の種類が違っていることがある（△7二金 を △7二角打 と取り違えた中継）。
// 修復でしか合わないなら、比べる相手より前へ戻る差し替えも関門にかける。
func (a followBaseAnswer) fits(tip *position.Position) bool {
	if len(a.rests) == 0 || a.r.More || a.r.Cost > 0 {
		return false
	}
	return len(a.rests) == 1 || followSameEnd(tip, a.rests)
}

// tipResult は足し済みの手で始まる答えを、**先端からの `Connect` の答え**の形に直す（`followProbe` がそのまま使う）。
// rests が無ければ ok は偽。
func (a followBaseAnswer) tipResult() (position.ConnectResult, bool) {
	if len(a.rests) == 0 {
		return position.ConnectResult{}, false
	}
	r := a.r
	r.Solutions = a.rests
	r.Depth = len(a.rests[0])
	r.Unique = len(a.rests) == 1 && !r.More
	// ⚠️ **覆したマス（`Fixed`）は比べる相手からの答えが 1 本のときだけ埋まる**。比べる相手からは複数で、
	// 足し済みの手で始まるのが 1 本のときは空のまま（その 1 本の分は分からない）。
	r.Moves = nil
	if r.Unique {
		r.Moves = a.rests[0]
	} else {
		r.Fixed = nil
	}
	if r.Depth == 0 {
		// 足し済みの手だけで撮った盤になる（先端そのもの）。
		r.Stop, r.Solutions, r.Moves = position.StopSame, nil, nil
	}
	return r, true
}
