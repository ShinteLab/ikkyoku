package position

// 千日手（同じ局面が 4 回）で対局を終わらせる（2026-10-09）。
//
// **ルールから決まる終局**なので、人の投了と違って推測ではない。本譜を根から辿り、
// **盤・持ち駒・手番が同じ局面が 4 回目に現れた手**で対局が終わる。
//
//   - 4 回のあいだ**一方が王手をかけ続けていた**なら、王手をかけた側の反則負け
//     （連続王手の千日手）。KIF の行は**手番の側から見た結果**なので、4 回目を作った
//     手を誰が指したかで「反則勝ち」（手番の側＝かけられた側の勝ち）と
//     「反則負け」（手番の側＝かけ続けた側の負け）に分かれる
//   - 両方が王手をかけ続けていた（逃げる手がそのまま王手になる応酬）なら、
//     **どちらかに決められないので普通の千日手**として扱う
//
// ⚠️ **数えるのは根から後だけ**（設計原則1）。撮った中盤の局面が根なら、それより前の
// 繰り返しは分からない —— **少なく数えるだけで、誤って千日手にはしない。**
// ⚠️ **本譜だけを見る**（終局は本譜に 1 つ。枝の千日手には印を付けない）。
// ⚠️ **指せる手を減らさない** —— 4 回目を作る手も合法手（`legal.Moves` は履歴を知らない）。
// ルールでも「指せない手」ではなく、指した時点で対局が終わる。

import (
	"strings"

	"github.com/ShinteLab/ikkyoku/legal"
)

const (
	markRepetition = "千日手"
	markIllegalWin = "反則勝ち"
	markIllegalLos = "反則負け"
)

// settleRepetition は本譜を根から辿り、**同じ局面が 4 回目に現れた手**で対局を終わらせる。
//
// ⚠️ **既に終局しているなら何もしない**（棋譜の終局・人の投了を上書きしない）。
// ⚠️ **呼ぶのは本譜が伸びうる操作の後だけ**（`Play` / `PlayLine` / `Graft` / `Promote` と
// `FromKIF` の最後）。本譜を丸ごと辿るので、`FromKIF` の 1 手ごとに呼ぶと手数の 2 乗になる。
func (s *Study) settleRepetition() {
	if s.end != 0 {
		return
	}
	pos := s.root.Clone()
	first, err := pos.SFEN()
	if err != nil {
		return
	}
	sfens := []string{first}
	seen := map[string][]int{repetitionKey(first): {0}}
	for n := s.mainNext(s.top); n != nil; n = s.mainNext(n) {
		if err := pos.ApplyMove(n.usi); err != nil {
			return
		}
		cur, err := pos.SFEN()
		if err != nil {
			return
		}
		sfens = append(sfens, cur)
		k := repetitionKey(cur)
		seen[k] = append(seen[k], len(sfens)-1)
		if at := seen[k]; len(at) >= 4 {
			mark, note := repetitionResult(sfens, at[0], len(sfens)-1)
			s.end, s.endMark, s.endHand, s.endNote = n.id, mark, false, note
			return
		}
	}
}

// repetitionKey は**同じ局面かを比べる鍵**（盤・手番・持ち駒。手数は落とす）。
func repetitionKey(sfenStr string) string {
	f := strings.Fields(sfenStr)
	if len(f) > 3 {
		f = f[:3]
	}
	return strings.Join(f, " ")
}

// repetitionResult は 1 回目（from）から 4 回目（to）までの手を見て、終局の種別と説明を返す。
//
// sfens[j] は j 手目を指したあとの局面。j 手目を指したのは sfens[j] の手番の**反対側**で、
// その局面で手番の側が王手されていれば、j 手目は王手だった。
func repetitionResult(sfens []string, from, to int) (mark, note string) {
	plain := "同じ局面が 4 回現れました（千日手）"
	blackAll, whiteAll := true, true
	for j := from + 1; j <= to; j++ {
		check, err := legal.InCheck(sfens[j])
		if err != nil {
			// 王手かどうか分からないなら、反則と決めつけない。
			return markRepetition, plain
		}
		if turnOf(sfens[j]) == "w" { // 指したのは先手
			blackAll = blackAll && check
		} else {
			whiteAll = whiteAll && check
		}
	}
	if blackAll == whiteAll {
		// どちらも王手をかけ続けていない（普通の千日手）か、両方がかけ続けた（決められない）。
		return markRepetition, plain
	}
	checker, sym := "b", "▲"
	if whiteAll {
		checker, sym = "w", "△"
	}
	note = "連続王手の千日手（" + sym + "の反則負け）"
	if turnOf(sfens[to]) == checker {
		return markIllegalLos, note
	}
	return markIllegalWin, note
}

// turnOf は SFEN の手番の欄（"b" / "w"）。
func turnOf(sfenStr string) string {
	f := strings.Fields(sfenStr)
	if len(f) < 2 {
		return "b"
	}
	return f[1]
}
