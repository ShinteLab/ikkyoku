package app

// 推測で足した末尾の手を、後の 1 枚で差し替える（2026-10-08。理由は `_docs/design-follow.md`）。
//
// **見失い方の形**: 手が被って 2〜3 手進む → 手がどいた 1 枚でまとめて繋げない → 1 手だけの答えを足す →
// 本譜の先端がずれて、それ以降の 1 枚はどれも繋がらない（2026-10-08 の中継 2 件）。入口（繋げない）を塞いでも、
// 別の理由で必ずまた起きるので、**ずれても戻れる**ようにする。
//
// ⚠️ **差し替えてよいのは、本譜の末尾から続く推測の手（橙の印）だけ**（`position.Study.MainGuessTail`）。
// 印の無い手（ぴったり一致した手・棋譜の手・人が指した手）は「本譜のロック」で守る側に残す。
// ⚠️ **元の手は消さずに枝に残す**（ユーザーの判断。評価値も残り、差し替えが間違っていても人が戻せる）。
// ⚠️ **2 枚続けて同じ差し替えで確定する**（ユーザーの判断。1 枚だけでは差し替えない）。
// ⚠️ **履歴に依存しない**（設計原則1）—— 判断に使うのは今の手の木と撮った 1 枚だけで、2 枚の確かめは
// 足す手の控え（`followPending`）と同じ範囲。

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ShinteLab/ikkyoku/followcam"
	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/position"
)

// followRewindMax は差し替えで戻る手数の上限。
//
// 戻った局面からは今の `position.Connect`（深さ 4）で探すので、それより戻っても届かない。
const followRewindMax = 3

// followSideEps は先後と駒の有無の食い違いの費用を比べるときの遊び（float の丸め対策）。
const followSideEps = 1e-9

// followRewind は**推測で足した末尾の手を差し替える答え**（1 枚目で見つけて控え、2 枚目で確かめる）。
type followRewind struct {
	// line は見つけたときの本譜。⚠️ **これが変わっていたら捨てる**（違う局面の先を差し替えないため）。
	line []string
	// keep は本譜の頭から残す手数（ここから先の推測の手を差し替える）。
	keep int
	// moves は残した局面から足す手順（空なら取り消すだけ）。
	moves []string
	// why はログに出す根拠。
	why string
	// held は**前の 1 枚で見つけたまま残している**か（2026-10-09）。そのあとの 1 枚は同じ差し替えを言っていないので、
	// ⚠️ **変わっていない 1 枚（`FollowConfirm`）では据えない**（同じ差し替えを言う 1 枚が来たら据える）。
	held bool
}

// same は同じ差し替えか（`held` は見ない）。
func (r *followRewind) same(o *followRewind) bool {
	return r != nil && o != nil && r.keep == o.keep && slices.Equal(r.line, o.line) && slices.Equal(r.moves, o.moves)
}

// followRewindCheck は撮った 1 枚で、**推測で足した末尾の手を差し替えるか**を見る（`FollowAuto` の頭で呼ぶ）。
//
// done が真なら、この周の答えはそれで終わり（差し替えを控えた／差し替えた）。偽なら今までどおり先端から足す。
//
// ⚠️ **手や頭が被った 1 枚では試さない**（読みが崩れていて、正しい末尾を壊す）。控えた差し替えも捨てずに
// 次の 1 枚を待つ。⚠️ **費用表が無ければ試さない**（どのマスの食い違いが確かかの根拠が無い）。
func (f *follower) followRewindCheck(board *position.Board, cost *position.CellCost, unseen *position.CellMask, p FollowProbe) (FollowAuto, bool, error) {
	if board == nil || cost == nil || unseen.Count() > 0 || p.Kind == FollowUnreadable {
		return FollowAuto{}, false, nil
	}
	rw := f.followRewindProbe(board, cost, p)
	if rw == nil {
		f.dropRewindUnlessFits(board, cost)
		return FollowAuto{}, false, nil
	}
	a, err := f.followRewindHold(rw, p.Fit)
	return a, true, err
}

// followRewindHold は見つけた差し替え rw を控える。前の 1 枚と同じ差し替えなら据える（2 枚目で確かめた）。
//
// ⚠️ **控えたら先端から足す手の控えは捨てる**（次の確かめ（`FollowConfirm`）でどちらを据えるのか分からなくなる）。
func (f *follower) followRewindHold(rw *followRewind, fit float64) (FollowAuto, error) {
	f.smu.Lock()
	prev := f.rewind
	f.rewind = rw
	f.smu.Unlock()
	if rw.same(prev) {
		return f.followRewindApply(rw)
	}
	f.setPending(nil)
	log.Follow.Info("推測で足した手を差し替えられそうです（次の 1 枚で確かめます）",
		"戻す手数", len(rw.line)-rw.keep, "元の手", rw.line[rw.keep:], "新しい手", rw.moves, "根拠", rw.why)
	return FollowAuto{Kind: FollowUnique, Pending: true, Reason: "確かめています",
		Guess: true, Fit: fit, State: f.State()}, nil
}

// dropRewindUnlessFits は、差し替えを言わなかった 1 枚で**控えた差し替えを捨てるか**を決める（2026-10-09）。
//
// ⚠️ **何も言えない 1 枚では捨てない**（`pendingStillFits` と同じ考え）。**実機の症状**: △7二角打 を △7二金 に差し替える
// 案が 3 回出たが、そのたびに次に読んだ 1 枚（空きのマスが光り、変わったマスが 10〜12）が「隔たりが大きすぎます」などを
// 返して案が捨てられ、差し替わるまで 64 秒かかった（2026-10-09 の中継）。その 1 枚は先端（△7二角打）を支持していない
// —— 6一 が空いて見えるぶん、むしろ差し替えた側に合っていた。
// 捨てるのは、**差し替えた盤より先端の盤のほうがよく合う**（先後と駒の有無の食い違いが少ない。`followSideCost`）1 枚。
// 残すときは `held` を立てる（変わっていない 1 枚では据えず、同じ差し替えを言う 1 枚を待つ）。
func (f *follower) dropRewindUnlessFits(board *position.Board, cost *position.CellCost) {
	f.smu.Lock()
	prev := f.rewind
	var root *position.Position
	var line []string
	if f.tree() != nil {
		root, line = f.tree().Root(), f.tree().MainLine()
	}
	f.smu.Unlock()
	if prev == nil {
		return
	}
	keep := false
	if root != nil && slices.Equal(line, prev.line) {
		tip, ok1 := followReplay(root, line)
		anc, ok2 := followReplay(root, line[:prev.keep])
		if ok1 && ok2 {
			if after, ok3 := followReplay(anc, prev.moves); ok3 {
				skip := promotedCells(tip.Board)
				keep = followSideCost(after.Board, board, cost, skip) <= followSideCost(tip.Board, board, cost, skip)+followSideEps
			}
		}
	}
	f.smu.Lock()
	defer f.smu.Unlock()
	if f.rewind != prev {
		return
	}
	if !keep {
		f.rewind = nil
		return
	}
	held := *prev
	held.held = true
	f.rewind = &held
	log.Follow.Info("控えた差し替えは、この 1 枚では打ち消されないので残します", "元の手", prev.line[prev.keep:], "新しい手", prev.moves)
}

// holdRewind は控えた差し替えに `held` を立てる（新しい 1 枚を見始めたとき。`FollowCells`）。
func (f *follower) holdRewind() {
	f.smu.Lock()
	defer f.smu.Unlock()
	if f.rewind != nil && !f.rewind.held {
		held := *f.rewind
		held.held = true
		f.rewind = &held
	}
}

// followCellsRewind は**速い経路で、推測で足した末尾の手を差し替えるか**を見る（2026-10-09）。
// done が真なら、この周の答えはそれで終わり（行き先を読むよう頼んだ／差し替えを控えた／差し替えた）。
//
// 呼ぶのは**先端から先に合う手順が 1 つも無かったとき**（`FollowCells`）。**実機の症状**（2026-10-09 の中継。手で操作する
// デジタル盤）: 金をドラッグしているあいだ、元の 6一 に金が残ったまま 7二 に金の絵が重なった 1 枚で、変わったマスが 7二 だけに
// なり △7二角打 を推測で足した。2 秒後の 1 枚で 6一 が空いた（変わったマスは 6一 と、金の動ける 5一・6二・7三 が光ったもの）が、
// 速い経路は先端から先しか探さないので「説明できる手がありません」で待ち、差し替えは 81 マスを読む周まで出なかった。
//
// 比べるのは**先後と駒の有無だけ**（`position.SideChangedMoves`）。7二 の見た目は変わっていない（金の絵のまま）ので、
// 種類まで比べると合わない。光ったマスは空きのマスなので、変わっていてよい。
// ⚠️ **差し替えてよいのは推測の手（橙）だけ・最大 3 手**（`followRewindProbe` と同じ）。
// ⚠️ **合う手順が一意のときだけ**。⚠️ **戻した手と同じ手で始まる答えは採らない**。
// ⚠️ **先端そのものが（先後と駒の有無で）合うなら戻さない**。
// ⚠️ **行き先に指した側の駒が読めてから控える**（`followBackedByRead`。読んでいなければ行き先を読むよう頼む）。
// ⚠️ **2 枚続けて同じ差し替えで据える**のは 81 マスの読みの差し替えと同じ（控えは共通。`followRewindHold`）。
func (f *follower) followCellsRewind(base *position.Position, since []string, mask, extra *position.CellMask, read []followcam.CellPiece, rotated bool) (FollowAuto, bool, error) {
	f.smu.Lock()
	if f.tree() == nil {
		f.smu.Unlock()
		return FollowAuto{}, false, nil
	}
	tail := f.tree().MainGuessTail()
	line := f.tree().MainLine()
	root := f.tree().Root()
	f.smu.Unlock()
	if len(tail) == 0 {
		return FollowAuto{}, false, nil
	}
	if tip := followBoard(base, since, nil); tip == nil || position.SideFits(base.Board, tip, mask, extra) {
		return FollowAuto{}, false, nil
	}
	for k := 1; k <= len(tail) && k <= followRewindMax; k++ {
		keep := len(line) - k
		anc, ok := followReplay(root, line[:keep])
		if !ok {
			return FollowAuto{}, false, nil
		}
		old := line[keep:]
		// 戻した手のマスは動かしてよい（行き先が変わったマスに入っていないことがある —— 上の 7二）。
		reach := &position.CellMask{}
		for _, m := range old {
			if c := usiCells(m); c != nil {
				for r := 0; r < 9; r++ {
					for f := 0; f < 9; f++ {
						reach[r][f] = reach[r][f] || c[r][f]
					}
				}
			}
		}
		sols, err := position.SideChangedMoves(anc, base.Board, mask, extra, reach, followCellsDepth)
		if err != nil {
			return FollowAuto{}, false, nil
		}
		var cands [][]string
		for _, sol := range sols {
			if len(sol) > 0 && sol[0] != old[0] {
				cands = append(cands, sol)
			}
		}
		if len(cands) == 0 {
			continue
		}
		if len(cands) > 1 {
			// ⚠️ 一意でないのに 1 本選ばない（81 マスの読みに任せる）。
			return FollowAuto{}, false, nil
		}
		moves := cands[0]
		arrivals := followArrivals(anc, nil, moves)
		// ⚠️ **消える手の行き先も読むこと**（2026-10-10。`followGone`）。
		gone := followGone(anc, old, moves)
		need := slices.Clone(arrivals)
		for _, i := range gone {
			if !slices.Contains(need, i) {
				need = append(need, i)
			}
		}
		if !followReadCovers(read, need, rotated) {
			log.Follow.Info("変わったマスなら推測で足した手を差し替えられそうです（行き先を読んで確かめます）",
				"元の手", old, "新しい手", moves)
			return FollowAuto{Kind: FollowChoices, Reason: "行き先のマスを読みます",
				ReadCells: followImageCells(need, rotated), State: f.State()}, true, nil
		}
		if ok, bad, got := followBackedByRead(anc, nil, moves, read, rotated); !ok {
			log.Follow.Info("差し替える手の行き先に指した側の駒が読めません", "新しい手", moves, "マス", bad, "読んだ駒", got)
			return FollowAuto{}, false, nil
		}
		if sq, got, seen := followGoneSeen(anc, old, gone, read, rotated); seen {
			log.Follow.Info("差し替えると消える手の駒が、まだ読めています（差し替えません）",
				"元の手", old, "新しい手", moves, "マス", sq, "読んだ駒", got)
			return FollowAuto{}, false, nil
		}
		if sq, got, kept := followKindKeepsOld(anc, old, moves, need, read, rotated); kept {
			log.Follow.Info("差し替えると種類の変わるマスが、元の手の駒に読めています（差し替えません）",
				"元の手", old, "新しい手", moves, "マス", sq, "読んだ駒", got)
			return FollowAuto{}, false, nil
		}
		rw := &followRewind{line: line, keep: keep, moves: slices.Clone(moves),
			why: "変わったマス（先後と駒の有無）を説明できる・行き先を読んだ"}
		a, err := f.followRewindHold(rw, 0)
		return a, true, err
	}
	return FollowAuto{}, false, nil
}

// followRewindProbe は、末尾の推測の手を戻した局面から撮った 1 枚が**もっとよく説明できるか**を探す。
//
// 比べるのは**先後と駒の有無の食い違い**だけ（`followSideCost`。駒の種類は見ない ——
// 種類は認識が一番外すところで、種類の読み違いに合わせて正しい手を差し替えないため）。
// 差し替えるのは次のどちらか:
//
//   - 戻した側のほうが**厳密に**食い違いが少ない（先端からでは説明しきれないマスが残る）
//   - 同じだけ説明でき、戻した側のほうが**少ない手数**で、手数の偶奇が同じ（先端からは往復して戻る
//     手を挟んで辻褄を合わせている。▲4八飛 を推測で足したあと、本当は ▲7六歩 だった 1 枚）
//
// ⚠️ **戻した局面からの答えが一意のときだけ**（一意でないのに 1 本選ばない）。
// ⚠️ **戻した手と同じ手で始まる答えは採らない**（それは戻さなくても先端から繋がる。もっと浅く戻せば済む）。
//
// 戻る候補は**比べる相手の局面からの答え**（`followExplainFromBase`。2026-10-11）と、**末尾から 1〜3 手**の 2 つの道から
// 来るが、⚠️ **関門（上の 2 つの条件）は 1 つ**（`accept`）。末尾から戻すのは、比べる相手の局面より前へ戻るときと、
// 比べる相手の局面を使わない 1 枚のときだけ。
func (f *follower) followRewindProbe(board *position.Board, cost *position.CellCost, p FollowProbe) *followRewind {
	f.smu.Lock()
	if f.tree() == nil {
		f.smu.Unlock()
		return nil
	}
	tail := f.tree().MainGuessTail()
	line := f.tree().MainLine()
	root := f.tree().Root()
	f.smu.Unlock()
	if len(tail) == 0 {
		return nil
	}
	tip, ok := followReplay(root, line)
	if !ok {
		return nil
	}

	// **戻る候補は 2 つの道から来る**（2026-10-11）:
	//   - **比べる相手の局面からの答え**（`followExplainFromBase`）。足し済みの手（比べる相手から先端まで。全部推測の手）の
	//     途中で分かれる答えがそのまま差し替えの候補になる。何手戻るかは答えが決める（中継で 6 手を取り違えたとき、
	//     正しい手順は 6 手戻らないと届かなかった。2026-10-10）
	//   - **末尾から 1〜`followRewindMax` 手**。比べる相手の局面より前へ戻るのはこちらだけで、⚠️ **比べる相手の局面から
	//     足し済みの手で始まる答えがぴったり合わないときの控え**（速い経路の `followCellsRewind` と同じ形。`fits`）。
	//     比べる相手の局面を使わない 1 枚（足し済みの手が無い・覚えていない）では、今までどおりこちらだけ
	// ⚠️ **推測の手の外へは戻らない**。
	bpos, since, bline, useBase := f.followBaseFrom()
	if useBase && !slices.Equal(bline, line) {
		useBase = false
	}
	nb := 0
	if useBase {
		nb = len(since)
	}
	ks := []int{}
	for k := nb + 1; k <= len(tail) && k <= followRewindMax; k++ {
		ks = append(ks, k)
	}
	deepest := nb
	if len(ks) > 0 {
		deepest = max(deepest, ks[len(ks)-1])
	}
	if deepest == 0 {
		return nil
	}
	// ⚠️ **種類の読みを信じてよい 1 枚か**は、いちばん深く戻る局面と比べて決める（2026-10-10。`position.KindsTrusted`）。
	// 先端と比べると、疑っている推測の手が置いた駒の種類違い（2五 の桂）まで「読み違い」に数えてしまう。
	deep, ok := followReplay(root, line[:len(line)-deepest])
	if !ok {
		return nil
	}
	trusted := position.KindsTrusted(deep.Board, board, nil)

	// **先端側の一番よい説明**（何も指していない／下見が並べた候補／先端から 1 手の候補）。同じだけ説明できるなら
	// 手数の少ないほう、それも同じなら種類の食い違いの少ないほう。
	// ⚠️ **先端の盤で成駒が載っているマスは比べない**（2026-10-09。`promotedCells`）。
	// ⚠️ **先端から 1 手の候補（`FollowProbe.ranked`）も数えること**（2026-10-11 に実機で踏んだ）。深い探索の答え
	// （`Candidates`）は「変わっていません」で終わった 1 枚では空になる。先端に △9六角打 を足せば合う 1 枚で、
	// 先端そのもの（9六 が空き）と比べて「食い違い 1.00 → 0.00」になり、△5八銀打 ▲5七角 を
	// △9六角打 ▲5七角 △5八銀打 に差し替えた（順番が逆。打った駒も人に聞いている途中だった）。
	skip := promotedCells(tip.Board)
	tipCost, tipPlies := followSideCost(tip.Board, board, cost, skip), 0
	tipKind := followKindCost(tip.Board, board, cost, skip)
	tipSide := [][]string{nil}
	for _, c := range p.Candidates {
		tipSide = append(tipSide, c.Moves)
	}
	tipSide = append(tipSide, p.ranked...)
	for _, mv := range tipSide[1:] {
		after, ok := followReplay(tip, mv)
		if !ok {
			continue
		}
		v := followSideCost(after.Board, board, cost, skip)
		if v < tipCost-followSideEps || (v <= tipCost+followSideEps && len(mv) < tipPlies) {
			tipCost, tipPlies = v, len(mv)
			tipKind = followKindCost(after.Board, board, cost, skip)
		} else if v <= tipCost+followSideEps && len(mv) == tipPlies {
			tipKind = min(tipKind, followKindCost(after.Board, board, cost, skip))
		}
	}
	// tipEnds は先端側の説明が行き着く局面（`followEndKey`）。要るときに 1 回だけ作る。
	var tipEnds map[string]bool
	reachedFromTip := func(after *position.Position) bool {
		if tipEnds == nil {
			tipEnds = map[string]bool{}
			for _, mv := range tipSide {
				if pos, ok := followReplay(tip, mv); ok {
					if key := followEndKey(pos); key != "" {
						tipEnds[key] = true
					}
				}
			}
		}
		key := followEndKey(after)
		return key != "" && tipEnds[key]
	}
	if tipCost <= followSideEps && tipPlies == 0 && (!trusted || tipKind <= followSideEps) {
		// 先端そのものがぴったり合っている（長考中はほとんどこれ）。戻す理由が無い。
		return nil
	}

	// accept は**末尾の k 手を戻して moves を指す差し替え**が関門を通るかを見る（通れば根拠を返す）。
	// ⚠️ **どちらの道から来た候補も、この 1 つの関門にかけること**（道ごとに判断を変えない）。
	accept := func(k int, moves []string) (string, bool) {
		keep := len(line) - k
		old := line[keep:]
		if len(moves) > 0 && moves[0] == old[0] {
			// 戻した手と同じ手で始まるなら、もっと浅く戻せば済む。
			return "", false
		}
		anc, ok := followReplay(root, line[:keep])
		if !ok {
			return "", false
		}
		after, ok := followReplay(anc, moves)
		if !ok {
			return "", false
		}
		alt := followSideCost(after.Board, board, cost, skip)
		better := alt < tipCost-followSideEps
		fewer := tipPlies > 0 && alt <= tipCost+followSideEps && len(moves) < k+tipPlies && (k+tipPlies-len(moves))%2 == 0
		// ⚠️ **先後と駒の有無で同点なら、種類の読みを信じてよい 1 枚に限って種類で選ぶ**（2026-10-10。`position.KindsTrusted`）。
		// 中継で取り違えた ▲2五桂 △同桂 と正しい ▲2五桂 △4五桂 ▲同歩 …… は先後と駒の有無では同じ盤で、2五 が歩（1.00）か
		// 桂かでしか分かれない。⚠️ **手数の偶奇は揃えること**（違えば手番が食い違う）。
		altKind := followKindCost(after.Board, board, cost, skip)
		kinder := trusted && !better && alt <= tipCost+followSideEps && altKind < tipKind-followSideEps &&
			(k+tipPlies-len(moves))%2 == 0
		var why string
		switch {
		case better:
			why = fmt.Sprintf("先後と駒の有無の食い違い %.2f → %.2f", tipCost, alt)
		case kinder:
			why = fmt.Sprintf("先後と駒の有無は同じ %.2f・種類の食い違い %.2f → %.2f", alt, tipKind, altKind)
		case fewer:
			why = fmt.Sprintf("同じ食い違い %.2f を %d 手 → %d 手で説明", alt, k+tipPlies, len(moves))
		default:
			return "", false
		}
		// ⚠️ **先端に手を足しても同じ局面に行き着くなら差し替えない**（2026-10-11 に実機で踏んだ）。違うのは手の順番か、
		// 取られて盤から消えた駒の成・不成だけで、撮った盤にはどちらとも書いていない。足し済みの手は撮った順に並んでいて、
		// 成・不成も見た目で決めている —— そちらを捨てる根拠が無い。△9七桂成 ▲同香 を △9七桂不成 ▲同香 △8四桂打 に
		// 差し替えた（先端に △8四桂打 を足せば同じ局面だった）。
		if reachedFromTip(after) {
			log.Follow.Info("差し替えても、先端に手を足した局面と同じになります（差し替えません）",
				"元の手", old, "新しい手", moves, "根拠", why)
			return "", false
		}
		return why, true
	}

	ctx, cancel := context.WithTimeout(context.Background(), followTimeout)
	defer cancel()
	if useBase {
		// **比べる相手の局面からの答え**。下見が深く探す段まで来ていればその答えを使い、来ていなければここで探す
		// （先端が 1 手で決まった・「変わっていない」が 1 位だった 1 枚）。
		ans := p.base
		if ans == nil {
			got, err := followExplainFromBase(ctx, bpos, since, line, board,
				position.ConnectOptions{Cost: cost, Tolerance: position.DefaultTolerance})
			if err != nil {
				return nil
			}
			ans = &got
		}
		if rw := ans.rewind; rw != nil && slices.Equal(rw.line, line) {
			if why, ok := accept(len(line)-rw.keep, rw.moves); ok {
				return &followRewind{line: line, keep: rw.keep, moves: slices.Clone(rw.moves), why: rw.why + "・" + why}
			}
		}
		if ans.fits(tip) {
			// 足し済みの手で始まる答えがぴったり合う（足し済みの手は撮った盤と矛盾しない）。比べる相手の局面より前へは戻らない。
			return nil
		}
	}
	for _, k := range ks {
		keep := len(line) - k
		anc, ok := followReplay(root, line[:keep])
		if !ok {
			return nil
		}
		r, err := position.Connect(ctx, anc, board, position.ConnectOptions{Cost: cost, Tolerance: position.DefaultTolerance})
		if err != nil {
			return nil
		}
		old := line[keep:]
		var moves []string
		switch {
		case r.Stop == position.StopSame:
			// 戻すだけで撮った盤になる（取り消し）。
		case r.Stop == position.StopFound && r.Unique:
			moves = r.Moves
		case r.Stop == position.StopFound && !r.More && followSameEnd(anc, r.Solutions):
			// ⚠️ **順番だけが決まらない答え**（行き着く局面は同じ。2026-10-10）。追従が足すときと同じく、推測として 1 本採る。
			// 戻した手と同じ手で始まらない順番を選ぶ（同じ手で始まるなら、もっと浅く戻せば済む）。
			for _, sol := range r.Solutions {
				if len(sol) > 0 && sol[0] != old[0] {
					moves = sol
					break
				}
			}
			if moves == nil {
				continue
			}
		default:
			continue
		}
		if why, ok := accept(k, moves); ok {
			return &followRewind{line: line, keep: keep, moves: slices.Clone(moves), why: why}
		}
	}
	return nil
}

// followRewindApply は控えた差し替えを据える（2 枚目で確かめたあと）。
//
// ⚠️ **元の手は枝に残す**: 差し替えは「戻り先までの本譜 + 新しい手順」を `GraftGuess`（新しい手が本譜側、
// 元の続きは枝）、取り消すだけなら末尾の先頭を `Branch`（分岐にする）。
// ⚠️ **本譜が見つけたときから変わっていたら何もしない**（違う局面の先を差し替えない）。
// ⚠️ **先端を見ていたときだけ新しい先端へ移る**（`followApply` と同じ）。
// ⚠️ **控えと速い経路の比べる相手は捨てる**（どちらも元の先端の話）。
func (f *follower) followRewindApply(rw *followRewind) (a FollowAuto, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO）。
	defer func() {
		if a.Rewound {
			f.host.publish(a.State, err)
		}
	}()
	f.smu.Lock()
	defer f.smu.Unlock()
	f.rewind = nil
	if f.tree() == nil || !slices.Equal(f.tree().MainLine(), rw.line) {
		return FollowAuto{Kind: FollowSame, Reason: "盤面は変わっていません", State: f.host.state()}, nil
	}
	tail := f.tree().MainGuessTail()
	k := len(rw.line) - rw.keep
	if k < 1 || k > len(tail) {
		return FollowAuto{Kind: FollowSame, Reason: "盤面は変わっていません", State: f.host.state()}, nil
	}
	atTip := f.tree().CurrentID() == mainTipID(f.tree())
	rootSfen := rootSFEN(f.tree())
	oldText := followText(rootSfen, rw.line[:rw.keep], rw.line[rw.keep:])
	newText := followText(rootSfen, rw.line[:rw.keep], rw.moves)

	added := 0
	if len(rw.moves) > 0 {
		g := f.host.hostGraft(append(slices.Clone(rw.line[:rw.keep]), rw.moves...), len(rw.moves))
		if g.Note != "" && g.Added == 0 {
			return FollowAuto{}, fmt.Errorf("%s", g.Note)
		}
		added = g.Added
	} else if err := f.host.hostBranch(tail[len(tail)-k]); err != nil {
		return FollowAuto{}, err
	}
	if atTip {
		f.host.hostGoTip()
	}
	f.pending = nil
	f.base = nil

	note := fmt.Sprintf("%s を %s に差し替えました（元の手は枝に残っています）",
		strings.Join(oldText, " "), strings.Join(newText, " "))
	if len(rw.moves) == 0 {
		note = fmt.Sprintf("%s を取り消しました（元の手は枝に残っています）", strings.Join(oldText, " "))
	}
	log.Follow.Info("手を差し替えました（追跡）", "手数", rw.keep+1, "戻した手数", k,
		"元の手", strings.Join(oldText, " "), "新しい手", strings.Join(newText, " "),
		"usi", strings.Join(rw.moves, " "), "根拠", rw.why)
	return FollowAuto{Kind: FollowUnique, Applied: true, Rewound: true, Guess: true,
		Moves: slices.Clone(rw.moves), Text: newText, Added: added, Note: note, State: f.host.changed()}, nil
}

// takeRewind は控えた差し替えを取り出す（`FollowConfirm` 用。本譜が変わっていたら捨てる）。
func (f *follower) takeRewind() *followRewind {
	f.smu.Lock()
	defer f.smu.Unlock()
	rw := f.rewind
	if rw != nil && rw.held {
		// ⚠️ **前の 1 枚から残している差し替えは、変わっていない 1 枚では据えない**（2026-10-09。`dropRewindUnlessFits`）。
		return nil
	}
	f.rewind = nil
	if rw == nil || f.tree() == nil || !slices.Equal(f.tree().MainLine(), rw.line) {
		return nil
	}
	return rw
}

// followEndKey は局面 p を**行き着く局面として比べる鍵**（盤・手番・駒台。手数は除く）。組めなければ空。
func followEndKey(p *position.Position) string {
	got, err := p.SFEN()
	if err != nil {
		return ""
	}
	return got[:strings.LastIndex(got, " ")]
}

// followReplay は from に手順を指した局面を返す（指せなければ ok は偽）。
func followReplay(from *position.Position, moves []string) (*position.Position, bool) {
	p := from.Clone()
	for _, m := range moves {
		if p.ApplyMove(m) != nil {
			return nil, false
		}
	}
	return p, true
}

// promotedCells は盤 b で**成駒が載っているマス**を返す（2026-10-09。差し替えの比べで数えないマス）。
//
// ⚠️ **成駒は読めないものとして扱う**（成駒は学習データにほぼ入らない。`followPickByRead` と同じ前提）。読めないだけで
// なく**先後まで外す**: 中継で 1一 の馬を、1 枚ごとに「先手の桂 0.40」「後手の歩 0.78」「先手の龍 0.40」と読み、
// 後手の歩と読んだ 2 枚続けて ▲1一角成 を取り消した（取り消すと 1一 に後手の香が戻って「合う」。2二 の角が無いことは
// 空きの読みなので 0.15 しか数えなかった）。⚠️ **比べるどの盤にも同じマスを使う**（先端の盤で決める。片方だけ外すと比べにならない）。
func promotedCells(b *position.Board) *position.CellMask {
	m := &position.CellMask{}
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			if c, err := b.At(r, f); err == nil && !c.IsEmpty() && c.Promoted() {
				m[r][f] = true
			}
		}
	}
	return m
}

// followSideCost は盤 cand を正しいとしたとき、撮った盤 read と**駒の有無か先後が食い違うマス**の費用の合計。
//
// ⚠️ **駒の種類は見ない**（`followRewindProbe`）。費用はマスごとの確信度（空きは被覆率から。`recognize.CellConfidence`）。
// skip のマスは数えない（`promotedCells`。nil なら全部数える）。
// ⚠️ **読みの弱い駒（`followArrivedMinCost` 未満）は数えない**（2026-10-08 に実機で踏んだ）。光の差し込んだ空きの 1五 を
// 「先手の香」（0.40）と読んだ 1 枚で、▲2五歩 を ▲1五角打 △2五角打 ▲同歩 に差し替えた（幻の駒を手で説明した。
// `TestFollowRewindIgnoresWeakPhantomPiece`）。駒が来た跡と見なす読み（`followArrived`）と同じ線。
func followSideCost(cand, read *position.Board, cost *position.CellCost, skip *position.CellMask) float64 {
	sum := 0.0
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			if skip != nil && skip[r][f] {
				continue
			}
			a, err := cand.At(r, f)
			if err != nil {
				continue
			}
			// 判定は `followJudge`（弱い駒の読みは数えない）。
			if v := followJudge(a, followObsBoard(read, cost, r, f)); v.presence == followDisagree || v.side == followDisagree {
				sum += v.weight
			}
		}
	}
	return sum
}

// followKindCost は盤 cand を正しいとしたとき、撮った盤 read と**先後は合って種類だけ違うマス**の費用の合計（2026-10-10）。
//
// 差し替えの比べで、先後と駒の有無が同点のときにだけ使う（`followRewindProbe`。種類の読みを信じてよい 1 枚に限る）。
// skip と読みの弱い駒の扱いは `followSideCost` と同じ。
func followKindCost(cand, read *position.Board, cost *position.CellCost, skip *position.CellMask) float64 {
	sum := 0.0
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			if skip != nil && skip[r][f] {
				continue
			}
			a, err := cand.At(r, f)
			if err != nil {
				continue
			}
			// 判定は `followJudge`（弱い駒の読みは数えない・成駒の候補は成っていない姿と読めたときだけ食い違う）。
			if v := followJudge(a, followObsBoard(read, cost, r, f)); v.side == followAgree && v.kind == followDisagree {
				sum += v.weight
			}
		}
	}
	return sum
}

// followGone は**差し替えると消える手の行き先**（解析の向きの 81 マスの番号。2026-10-10）。元の手順 old で駒が置かれ、
// 差し替える手順 moves で指した盤では空くマス。
//
// **実機の症状**（実盤の中継）: ▲7九銀打 △5九金打 ▲同玉 を足したあと、変わったマスが 7九 だけの 1 枚（銀に手か影が掛かった）で、
// 速い経路の差し替えがこの 3 手を ▲5九玉 の 1 手に替えた（7九 の銀が消えたことにすれば説明が付く）。読んだのは新しい手の
// 行き先だけで、銀が写ったままの 7九 は読んでいなかった（`TestFollowCellsRewindKeepsMoveWhosePieceIsSeen`）。
func followGone(anc *position.Position, old, moves []string) []int {
	after := followBoard(anc, nil, moves)
	if after == nil {
		return nil
	}
	var out []int
	for _, i := range followArrivals(anc, nil, old) {
		if c, err := after.At(i/9, i%9); err == nil && c.IsEmpty() {
			out = append(out, i)
		}
	}
	return out
}

// followGoneSeen は、消える手の行き先 gone のどれかに**元の手の駒（同じ側）が読めているか**。読めていれば、その手は本当に
// 指されているので差し替えない。⚠️ **先後まで見る**（`followBackedByRead` と同じ。種類は問わない）。
func followGoneSeen(anc *position.Position, old []string, gone []int, read []followcam.CellPiece, rotated bool) (sq, got string, seen bool) {
	before := followBoard(anc, nil, old)
	if before == nil {
		return "", "", false
	}
	for _, i := range gone {
		c, err := before.At(i/9, i%9)
		if err != nil || c.IsEmpty() {
			continue
		}
		if followJudge(c, followObsRead(read, i, rotated)).side == followAgree {
			cp, _ := followReadAt(read, i, rotated)
			return squareText(i/9, i%9), cp.Piece + "/" + cp.Side, true
		}
	}
	return "", "", false
}

// followKindKeepsOld は、読んだマス cells のうち**元の手順 old と差し替える手順 moves で駒の種類だけが変わるマス**（どちらも
// 同じ側の駒）で、読んだ駒が**元の盤と合い、新しい盤と合わない**ものがあるか（2026-10-10）。あれば差し替えない。
//
// **実機の症状**（実盤の中継）: ▲5九歩打 のあと、変わったマスが 5八 だけの 1 枚（△5八桂成 の手が掛かった）で、速い経路の
// 差し替えが ▲5九歩打 を ▲5九金（5八 の金が動いた）に替えた。5九 は「歩」と読めていたが、速い経路の差し替えは先後と駒の
// 有無しか比べない（`TestFollowCellsRewindKeepsDropWhoseKindIsSeen`）。⚠️ **新しい盤と合う読みなら今までどおり替える**
// （デジタル盤の △7二角打 → △7二金。7二 は金と読める。`TestFollowCellsRewindsGuessedDrop`）。合い方は `followCellFits`。
func followKindKeepsOld(anc *position.Position, old, moves []string, cells []int, read []followcam.CellPiece, rotated bool) (sq, got string, kept bool) {
	before, after := followBoard(anc, nil, old), followBoard(anc, nil, moves)
	if before == nil || after == nil {
		return "", "", false
	}
	for _, i := range cells {
		b, err1 := before.At(i/9, i%9)
		a, err2 := after.At(i/9, i%9)
		if err1 != nil || err2 != nil || b == a || b.IsEmpty() || a.IsEmpty() || b.Black() != a.Black() {
			continue
		}
		cp, found := followReadAt(read, i, rotated)
		if !found || cp.Piece == "" || cp.Piece == "?" {
			continue
		}
		if followCellFits(b, cp.Piece) && !followCellFits(a, cp.Piece) {
			return squareText(i/9, i%9), cp.Piece, true
		}
	}
	return "", "", false
}
