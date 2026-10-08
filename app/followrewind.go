package app

// 推測で足した末尾の手を、後の 1 枚で差し替える（2026-10-08。`TODO.md` 4「間違えたと気づいて戻す仕組み」）。
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
}

func (r *followRewind) same(o *followRewind) bool {
	return r != nil && o != nil && r.keep == o.keep && slices.Equal(r.line, o.line) && slices.Equal(r.moves, o.moves)
}

// followRewindCheck は撮った 1 枚で、**推測で足した末尾の手を差し替えるか**を見る（`FollowAuto` の頭で呼ぶ）。
//
// done が真なら、この周の答えはそれで終わり（差し替えを控えた／差し替えた）。偽なら今までどおり先端から足す。
//
// ⚠️ **手や頭が被った 1 枚では試さない**（読みが崩れていて、正しい末尾を壊す）。控えた差し替えも捨てずに
// 次の 1 枚を待つ。⚠️ **費用表が無ければ試さない**（どのマスの食い違いが確かかの根拠が無い）。
func (s *StudyService) followRewindCheck(board *position.Board, cost *position.CellCost, unseen *position.CellMask, p FollowProbe) (FollowAuto, bool, error) {
	if board == nil || cost == nil || unseen.Count() > 0 || p.Kind == FollowUnreadable {
		return FollowAuto{}, false, nil
	}
	rw := s.followRewindProbe(board, cost, p)
	s.mu.Lock()
	prev := s.rewind
	s.rewind = rw
	s.mu.Unlock()
	if rw == nil {
		return FollowAuto{}, false, nil
	}
	if rw.same(prev) {
		a, err := s.followRewindApply(rw)
		return a, true, err
	}
	// **初めて出た差し替え。** 控えて、次の 1 枚を待つ。⚠️ **先端から足す手の控えは捨てる**
	// （次の確かめ（`FollowConfirm`）でどちらを据えるのか分からなくなる）。
	s.setPending(nil)
	log.Info("推測で足した手を差し替えられそうです（次の 1 枚で確かめます）",
		"戻す手数", len(rw.line)-rw.keep, "元の手", rw.line[rw.keep:], "新しい手", rw.moves, "根拠", rw.why)
	return FollowAuto{Kind: FollowUnique, Pending: true, Reason: "確かめています",
		Guess: true, Fit: p.Fit, State: s.State()}, true, nil
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
func (s *StudyService) followRewindProbe(board *position.Board, cost *position.CellCost, p FollowProbe) *followRewind {
	s.mu.Lock()
	if s.study == nil {
		s.mu.Unlock()
		return nil
	}
	tail := s.study.MainGuessTail()
	line := s.study.MainLine()
	root := s.study.Root()
	s.mu.Unlock()
	if len(tail) == 0 {
		return nil
	}
	tip, ok := followReplay(root, line)
	if !ok {
		return nil
	}

	// **先端側の一番よい説明**（何も指していない／下見が並べた候補）。同じだけ説明できるなら手数の少ないほう。
	tipCost, tipPlies := followSideCost(tip.Board, board, cost), 0
	for _, c := range p.Candidates {
		after, ok := followReplay(tip, c.Moves)
		if !ok {
			continue
		}
		v := followSideCost(after.Board, board, cost)
		if v < tipCost-followSideEps || (v <= tipCost+followSideEps && len(c.Moves) < tipPlies) {
			tipCost, tipPlies = v, len(c.Moves)
		}
	}
	if tipCost <= followSideEps && tipPlies == 0 {
		// 先端そのものがぴったり合っている（長考中はほとんどこれ）。戻す理由が無い。
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), followTimeout)
	defer cancel()
	for k := 1; k <= len(tail) && k <= followRewindMax; k++ {
		keep := len(line) - k
		anc, ok := followReplay(root, line[:keep])
		if !ok {
			return nil
		}
		r, err := position.Connect(ctx, anc, board, position.ConnectOptions{Cost: cost, Tolerance: position.DefaultTolerance})
		if err != nil {
			return nil
		}
		var moves []string
		switch {
		case r.Stop == position.StopSame:
			// 戻すだけで撮った盤になる（取り消し）。
		case r.Stop == position.StopFound && r.Unique:
			moves = r.Moves
		default:
			continue
		}
		old := line[keep:]
		if len(moves) > 0 && moves[0] == old[0] {
			continue
		}
		after, ok := followReplay(anc, moves)
		if !ok {
			continue
		}
		alt := followSideCost(after.Board, board, cost)
		better := alt < tipCost-followSideEps
		fewer := tipPlies > 0 && alt <= tipCost+followSideEps && len(moves) < k+tipPlies && (k+tipPlies-len(moves))%2 == 0
		if !better && !fewer {
			continue
		}
		why := fmt.Sprintf("先後と駒の有無の食い違い %.2f → %.2f", tipCost, alt)
		if !better {
			why = fmt.Sprintf("同じ食い違い %.2f を %d 手 → %d 手で説明", alt, k+tipPlies, len(moves))
		}
		return &followRewind{line: line, keep: keep, moves: slices.Clone(moves), why: why}
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
func (s *StudyService) followRewindApply(rw *followRewind) (a FollowAuto, err error) {
	// ⚠️ **`publish` はロックの外で走らせること**（`defer` は LIFO）。
	defer func() {
		if a.Rewound {
			s.publish(a.State, err)
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rewind = nil
	if s.study == nil || !slices.Equal(s.study.MainLine(), rw.line) {
		return FollowAuto{Kind: FollowSame, Reason: "盤面は変わっていません", State: s.state()}, nil
	}
	tail := s.study.MainGuessTail()
	k := len(rw.line) - rw.keep
	if k < 1 || k > len(tail) {
		return FollowAuto{Kind: FollowSame, Reason: "盤面は変わっていません", State: s.state()}, nil
	}
	atTip := s.study.CurrentID() == mainTipID(s.study)
	rootSfen := rootSFEN(s.study)
	oldText := followText(rootSfen, rw.line[:rw.keep], rw.line[rw.keep:])
	newText := followText(rootSfen, rw.line[:rw.keep], rw.moves)

	added := 0
	if len(rw.moves) > 0 {
		g := s.study.GraftGuess(append(slices.Clone(rw.line[:rw.keep]), rw.moves...), len(rw.moves))
		if g.Note != "" && g.Added == 0 {
			return FollowAuto{}, fmt.Errorf("%s", g.Note)
		}
		added = g.Added
	} else if err := s.study.Branch(tail[len(tail)-k]); err != nil {
		return FollowAuto{}, err
	}
	if atTip {
		if id, _, err := s.study.MainTip(); err == nil {
			_ = s.study.GoTo(id)
		}
	}
	s.pending = nil
	s.cellBase = nil

	note := fmt.Sprintf("%s を %s に差し替えました（元の手は枝に残っています）",
		strings.Join(oldText, " "), strings.Join(newText, " "))
	if len(rw.moves) == 0 {
		note = fmt.Sprintf("%s を取り消しました（元の手は枝に残っています）", strings.Join(oldText, " "))
	}
	log.Info("手を差し替えました（追跡）", "手数", rw.keep+1, "戻した手数", k,
		"元の手", strings.Join(oldText, " "), "新しい手", strings.Join(newText, " "),
		"usi", strings.Join(rw.moves, " "), "根拠", rw.why)
	return FollowAuto{Kind: FollowUnique, Applied: true, Rewound: true, Guess: true,
		Moves: slices.Clone(rw.moves), Text: newText, Added: added, Note: note, State: s.changed()}, nil
}

// takeRewind は控えた差し替えを取り出す（`FollowConfirm` 用。本譜が変わっていたら捨てる）。
func (s *StudyService) takeRewind() *followRewind {
	s.mu.Lock()
	defer s.mu.Unlock()
	rw := s.rewind
	s.rewind = nil
	if rw == nil || s.study == nil || !slices.Equal(s.study.MainLine(), rw.line) {
		return nil
	}
	return rw
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

// followSideCost は盤 cand を正しいとしたとき、撮った盤 read と**駒の有無か先後が食い違うマス**の費用の合計。
//
// ⚠️ **駒の種類は見ない**（`followRewindProbe`）。費用はマスごとの確信度（空きは下限の 0.15）。
func followSideCost(cand, read *position.Board, cost *position.CellCost) float64 {
	sum := 0.0
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			a, err1 := cand.At(r, f)
			b, err2 := read.At(r, f)
			if err1 != nil || err2 != nil {
				continue
			}
			if a.IsEmpty() != b.IsEmpty() || (!a.IsEmpty() && a.Black() != b.Black()) {
				sum += cost[r][f]
			}
		}
	}
	return sum
}
