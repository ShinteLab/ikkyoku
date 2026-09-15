package position

// 「この局面から指せる手のうち、撮った盤面を一番よく説明するのはどれか」を選ぶ層
// （2026-09-15）。
//
// ⚠️ **`Connect`（厳密一致 + 修復）とは問いが違う。** あちらは
// **「認識した盤面に到達できるか」**を聞くので、**認識が数マス外すと落ちる**
// ——「認識器が 100% でないと動かない」という実機の指摘はそこから来た。
//
// こちらが聞くのは**「どの候補が一番よく合うか」**。前の局面が分かっている以上、
// 次にあり得る盤面は**合法手のぶん（約 100 通り）しかない**。しかも
// **候補どうしは 2〜4 マスしか違わない**ので、**残りのマスの認識ミスは
// 全候補に等しく乗って相殺される**。だから**認識が完璧でなくても選べる。**
//
// ⚠️ **絶対値ではなく差で判断すること。** 「何マス合っているか」は認識の出来に
// 引きずられるが、**1 位と 2 位の差**は「その手を指したと言い切れるか」を直接表す。
//
// ⚠️ **それでも 2 つの歯止めが要る:**
//
//	Fit    … そもそも盤として読めているか（CM の画面なら全部外れるので低くなる）
//	Margin … 1 位と 2 位に差があるか（似た候補が並ぶなら、決め打ちしない）
//
// **前者が低ければ「盤が映っていない」、後者が小さければ「決められない」。**
// ⚠️ **どちらも「人が直す」の前に出す答え**であって、直させるのは最後の手段。

import (
	"fmt"
	"sort"

	"github.com/ShinteLab/ikkyoku/legal"
)

// 判断の既定値。⚠️ **実測で決めた数ではない**（2026-09-15 時点）。
// **実際の中継で当たり具合を見て調整すること。**
const (
	// DefaultMinFit は「盤として読めている」と見なす最低の一致度（0〜1）。
	//
	// これを下回ったら**候補を選ばない** —— 解説の画面や CM を撮ったときは
	// どの候補とも合わないので、ここで落ちる。
	DefaultMinFit = 0.80
	// DefaultMinMargin は 1 位と 2 位の費用差の最低値。
	//
	// ⚠️ **小さいと「似た候補のどちらか」を決め打ちする**ことになる。
	// マス 1 つぶんの重みがだいたい 0.15〜1.0 なので、**1 マスぶんの確信**に当たる。
	DefaultMinMargin = 0.6
	// DefaultRankCandidates は返す候補の数。
	DefaultRankCandidates = 5
)

// Candidate は「この手を指したとしたら」の 1 通り。
type Candidate struct {
	// Moves は手順（USI）。**0 手＝何も指していない**も候補に含まれる。
	Moves []string
	// Cost は撮った盤面との食い違いの費用（**小さいほどよく合う**）。
	Cost float64
	// Fit は 0〜1 の一致度（**大きいほどよく合う**）。`1 - Cost/総量`。
	Fit float64
	// Fixed は撮った盤面と食い違うマス（＝この手だとすると認識が外していた所）。
	Fixed []Fix
}

// RankResult は候補を良い順に並べたもの。
type RankResult struct {
	// Candidates は良い順（先頭が 1 位）。
	Candidates []Candidate
	// Margin は 1 位と 2 位の費用差（**大きいほど言い切れる**）。候補が 1 つなら +Inf 相当の大きな値。
	Margin float64
	// Total は費用の総量（全マスぶん）。`Fit` の分母。
	Total float64
}

// Decided は**1 位を採ってよいか**を返す（一致度と差の両方を満たすか）。
//
// ⚠️ **満たさない理由を混ぜないこと。** 「盤として読めていない」と
// 「どれか決められない」は**次にすることが違う**（撮り直す / 人に聞く）。
func (r RankResult) Decided(minFit, minMargin float64) bool {
	if len(r.Candidates) == 0 {
		return false
	}
	return r.Candidates[0].Fit >= minFit && r.Margin >= minMargin
}

// Readable は**そもそも盤として読めているか**（1 位の一致度が最低値以上か）。
func (r RankResult) Readable(minFit float64) bool {
	return len(r.Candidates) > 0 && r.Candidates[0].Fit >= minFit
}

// Rank は from から**1 手で**行ける局面（と、何も指さない場合）を、
// 撮った盤面をどれだけ説明できるかで並べる。
//
// ⚠️ **深さは 1 だけ。** 中継が 1 手進むのが普通で、**候補の数が深さで爆発する**
// （2 手なら 1 万通り）。CM を挟んで飛んだときは `Connect` の側で拾う。
//
// ⚠️ **必ず何かを返す**（「繋がりません」が無い）。決め打ってよいかは
// `Decided` / `Readable` で呼び出し側が判断する。**そこが `Connect` との違い。**
func Rank(from *Position, target *Board, opt ConnectOptions) (RankResult, error) {
	if from == nil || from.Board == nil {
		return RankResult{}, fmt.Errorf("ikkyoku/position: 並べる元の局面がありません")
	}
	if target == nil {
		return RankResult{}, fmt.Errorf("ikkyoku/position: 撮った盤面がありません")
	}
	if from.Turn == TurnUnknown {
		return RankResult{}, fmt.Errorf("ikkyoku/position: 手番が決まっていないので並べられません")
	}
	sfenStr, err := from.SFEN()
	if err != nil {
		return RankResult{}, err
	}

	cost := opt.Cost
	out := RankResult{Total: totalCost(cost)}

	// **何も指していない**も候補（中継がまだ進んでいないことは普通にある）。
	add := func(moves []string, board *Board) {
		d := newDiff(board, target, cost, 0)
		c := Candidate{Moves: moves, Cost: d.cost, Fixed: d.fixes(board, target)}
		if out.Total > 0 {
			c.Fit = 1 - c.Cost/out.Total
		}
		out.Candidates = append(out.Candidates, c)
	}
	add(nil, from.Board)

	// ⚠️ **合法手が出せなくても候補 0 にしないこと**（玉の欠けた局面など）。
	// 「何も指していない」だけでも並べられる（設計原則3）。
	if moves, err := legal.Moves(sfenStr); err == nil {
		for _, m := range moves {
			next := from.Clone()
			if err := next.ApplyMove(m.USI); err != nil {
				continue
			}
			add([]string{m.USI}, next.Board)
		}
	}

	sort.SliceStable(out.Candidates, func(i, j int) bool {
		return out.Candidates[i].Cost < out.Candidates[j].Cost
	})
	if len(out.Candidates) >= 2 {
		out.Margin = out.Candidates[1].Cost - out.Candidates[0].Cost
	} else {
		out.Margin = out.Total // 比べる相手が居ない ＝ 迷いようが無い
	}
	if n := opt.rankCandidates(); len(out.Candidates) > n {
		out.Candidates = out.Candidates[:n]
	}
	return out, nil
}

func (o ConnectOptions) rankCandidates() int {
	if o.MaxSolutions <= 0 {
		return DefaultRankCandidates
	}
	return o.MaxSolutions
}

// totalCost は全マスぶんの費用（`Fit` の分母）。
func totalCost(c *CellCost) float64 {
	sum := 0.0
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			sum += c.at(r, f)
		}
	}
	return sum
}
