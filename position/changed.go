package position

// 「撮った盤のうち、どのマスが変わったか」から指された手を割り出す層（2026-10-07）。
//
// **追従の速い経路。** 81 マスの推論（2.1 秒）を回さず、前に確かめた 1 枚とのマスごとの
// 画素の差（数ミリ秒）だけで手を決める。変わったマスの組は、合法手がどのマスを動かすかと
// **ほぼ 1 対 1 に対応する**（移動と取る手は元と行き先の 2 マス、打つ手は 1 マス）ので、
// 合法手を総当たりして**動かすマスの組がぴったり一致する手順**を探せば足りる。
//
// ⚠️ **画像を知らない**（`position` の約束）。受け取るのは 81 個の真偽だけ。
// ⚠️ **駒の種類は見ない。** 変わったマスの組が同じなら、成・不成も、打った駒の種類も
// 区別できない —— そのときは複数の答えを返し、**決めるのは呼び出し側**（81 マスを読む）。

import (
	"fmt"

	"github.com/ShinteLab/ikkyoku/legal"
)

// ChangedMoves は from から、**変わったマスの組（changed）とぴったり合う手順**を返す（最大 maxDepth 手）。
//
// 合うとは、手順を指したときに中身が変わるマスがすべて changed に入っていて、
// changed のうち中身の変わらないマスが extra に入っていること。
// extra は**中身が変わらなくても見た目が変わってよいマス**（ゲーム画面の、直前の手のマスの色付け。
// 次の手で色が消える）。中継のように色付けの無い画面では、そこが変わらないだけで害は無い。
//
// 返すのは**いちばん短い手数で合う手順のすべて**。1 つでなければ呼び出し側は決め打ちしないこと。
// 何も合わなければ空。
//
// ⚠️ **ただし、いちばん短い答えが色付けのマス（extra）の変化を「見た目だけ」で済ませたときは、
// そのマスの中身を変える、より長い手順も合わせて返す**（2026-10-08）。色付けのマスに本当に手が指されることが
// ある —— 直前の手（△同歩 2c2d）の元のマスへ歩を打たれた ▲2四飛 △2三歩 の 1 枚で、短い [2h2d] が
// 2c の変化を色付けとして飲み込み、△2三歩 を落として順序と持ち駒が狂った（ゲーム画面）。
// 長さの違う答えが並んだら、呼び出し側は違いの出るマス（ここでは 2c）を読んで決める。
func ChangedMoves(from *Position, changed, extra *CellMask, maxDepth int) ([][]string, error) {
	if from == nil || from.Board == nil {
		return nil, fmt.Errorf("ikkyoku/position: 割り出す元の局面がありません")
	}
	if from.Turn == TurnUnknown {
		return nil, fmt.Errorf("ikkyoku/position: 手番が決まっていないので割り出せません")
	}
	if changed.Count() == 0 {
		return nil, nil
	}
	// allowed は動かしてよいマス（変わったマスと、色付けのマス）。
	allowed := func(r, f int) bool { return changed.at(r, f) || extra.at(r, f) }

	// written は**いちばん短い答えが「見た目だけ」で済ませた色付けのマス**（中身を変えずに変わったマス）。
	// nil のあいだは、まだ答えが見つかっていない。
	var found [][]string
	var written *CellMask
	for depth := 1; depth <= maxDepth; depth++ {
		var out [][]string
		var walk func(p *Position, line []string) error
		walk = func(p *Position, line []string) error {
			if len(line) == depth {
				// ⚠️ **より長い答えは、短い答えが見た目だけで済ませたマスの中身を変えるものだけ採る**
				// （そうでない長い答えは、同じ変化を遠回りで説明しているだけ）。
				if changedFits(from.Board, p.Board, changed, extra) &&
					(written == nil || touches(from.Board, p.Board, written)) {
					out = append(out, append([]string(nil), line...))
				}
				return nil
			}
			sfenStr, err := p.SFEN()
			if err != nil {
				return err
			}
			moves, err := legal.Moves(sfenStr)
			if err != nil {
				return err
			}
			for _, m := range moves {
				// ⚠️ **動かすマスが「動かしてよいマス」の外なら、その手は入らない**（枝を刈る）。
				// 刈らないと 2 手で 1 万通りを総当たりする。
				if m.FromRank >= 0 && !allowed(m.FromRank, m.FromFile) {
					continue
				}
				if !allowed(m.ToRank, m.ToFile) {
					continue
				}
				next := p.Clone()
				if err := next.ApplyMove(m.USI); err != nil {
					continue
				}
				if err := walk(next, append(line, m.USI)); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(from, nil); err != nil {
			return nil, err
		}
		if len(out) == 0 {
			continue
		}
		if written != nil {
			// ⚠️ **長い答えも、いちばん短い手数のものだけ**（その先は空想の手順が増えるだけ）。
			return append(found, out...), nil
		}
		found = out
		written = writtenOff(from, out, changed, extra)
		if written == nil {
			return found, nil
		}
	}
	return found, nil
}

// writtenOff は、答え sols のどれかが**中身を変えずに済ませた、変わった色付けのマス**を返す（無ければ nil）。
func writtenOff(from *Position, sols [][]string, changed, extra *CellMask) *CellMask {
	m := &CellMask{}
	hit := false
	for _, sol := range sols {
		p := from.Clone()
		for _, mv := range sol {
			if p.ApplyMove(mv) != nil {
				return nil
			}
		}
		for r := 0; r < 9; r++ {
			for f := 0; f < 9; f++ {
				if changed.at(r, f) && extra.at(r, f) && from.Board.cells[r][f] == p.Board.cells[r][f] {
					m[r][f], hit = true, true
				}
			}
		}
	}
	if !hit {
		return nil
	}
	return m
}

// touches は from → to で、マスの組 m のどれかの中身が変わるか。
func touches(from, to *Board, m *CellMask) bool {
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			if m.at(r, f) && from.cells[r][f] != to.cells[r][f] {
				return true
			}
		}
	}
	return false
}

// ChangedFits は**盤 from から盤 to への変化が、変わったマスの組とぴったり合うか**を返す（2026-10-07）。
//
// 「比べる相手の 1 枚から今の 1 枚までに変わったマスを、その間に足した手で説明できるか」を確かめる
// （説明できれば、今の 1 枚は本譜の先端と合っている）。合うの意味は `ChangedMoves` と同じ。
// ⚠️ **何も変わっていなければ（from と to が同じ）偽**。そちらは呼び出し側が「変わったマスが無い」で見る。
func ChangedFits(from, to *Board, changed, extra *CellMask) bool {
	if from == nil || to == nil {
		return false
	}
	return changedFits(from, to, changed, extra)
}

// changedFits は from → to で中身の変わるマスが、変わったマスの組とぴったり合うか。
func changedFits(from, to *Board, changed, extra *CellMask) bool {
	seen := false
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			moved := from.cells[r][f] != to.cells[r][f]
			switch {
			case moved && !changed.at(r, f):
				// ⚠️ **中身が変わったのに見た目が変わっていないマスがあれば合わない**（見えていない手は採らない）。
				return false
			case !moved && changed.at(r, f) && !extra.at(r, f):
				return false
			}
			seen = seen || moved
		}
	}
	return seen
}
