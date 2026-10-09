package position

// ⚠️ **打つ手の行き先の種類（`dropsBacked`）の歯止め**（2026-10-09）:
//   - **打つ手の行き先がはっきり別の種類に読めたら、Rank の候補にも Connect の解にもしないこと**
//     （後手の持ち駒が角だけの局面で、7二 を「後手の金」と読んだ 1 枚から △7二角打 を足した）
//   - **打った駒の種類に読めれば今までどおり候補にすること**
//   - **読みの弱いマスでは止めないこと**（種類は認識が一番外すところ）

import (
	"context"
	"slices"
	"testing"

	"github.com/ShinteLab/core/sfen"
)

// dropFrame は guessLine のあと 3二 に後手の piece が載って見える 1 枚（ほかは △3二角打 のあとの盤と同じ）。
func dropFrame(t *testing.T, piece int) *Board {
	t.Helper()
	b := *posAfter(t, append(slices.Clone(guessLine), "B*3b")...).Board
	c, err := NewCell(piece, false, false)
	if err != nil {
		t.Fatalf("NewCell: %v", err)
	}
	if err := b.Set(1, 6, c); err != nil { // 3二
		t.Fatalf("Set: %v", err)
	}
	return &b
}

func evenCellCost(v float64) *CellCost {
	var c CellCost
	for r := range c {
		for f := range c[r] {
			c[r][f] = v
		}
	}
	return &c
}

func hasCandidate(r RankResult, moves ...string) bool {
	for _, c := range r.Candidates {
		if slices.Equal(c.Moves, moves) {
			return true
		}
	}
	return false
}

func TestRankSkipsDropReadAsOtherKind(t *testing.T) {
	from := posAfter(t, guessLine...)
	r, err := Rank(from, dropFrame(t, sfen.Gold), ConnectOptions{Cost: evenCellCost(0.9)})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if hasCandidate(r, "B*3b") {
		t.Fatalf("3二 を金と読んだ 1 枚で角打ちを候補にしました: %+v", r.Candidates)
	}
	// 角と読めれば今までどおり候補（1 位）。
	r, err = Rank(from, dropFrame(t, sfen.Bishop), ConnectOptions{Cost: evenCellCost(0.9)})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(r.Candidates) == 0 || !slices.Equal(r.Candidates[0].Moves, []string{"B*3b"}) {
		t.Fatalf("角と読めた 1 枚で角打ちが 1 位になりません: %+v", r.Candidates)
	}
	// 読みの弱いマス（0.3）なら、金と読めても止めない。
	cost := evenCellCost(0.9)
	cost[1][6] = 0.3
	r, err = Rank(from, dropFrame(t, sfen.Gold), ConnectOptions{Cost: cost})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if !hasCandidate(r, "B*3b") {
		t.Fatalf("読みの弱いマスで角打ちを落としました: %+v", r.Candidates)
	}
}

func TestConnectSkipsDropReadAsOtherKind(t *testing.T) {
	from := posAfter(t, guessLine...)
	r, err := Connect(context.Background(), from, dropFrame(t, sfen.Gold),
		ConnectOptions{Cost: evenCellCost(0.9), Tolerance: DefaultTolerance})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if r.Stop == StopFound && len(r.Moves) > 0 && r.Moves[0] == "B*3b" {
		t.Fatalf("3二 を金と読んだ 1 枚で角打ちに繋ぎました: %+v", r)
	}
}
