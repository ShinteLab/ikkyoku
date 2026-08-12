package analyze

import (
	"math"
	"testing"

	coreusi "github.com/ShinteLab/core/usi"
)

// 勝率の式そのもの（1/(1+exp(-評価値/定数))）。
//
// **評価値 0 でちょうど 5 割**、正なら 5 割超、負なら 5 割未満。
// ⚠️ **左右が入れ替わると画面上は「後手が優勢のときに先手側が伸びる」**という
// 壊れ方になるので、向きをここで固定しておく。
func TestWinRate(t *testing.T) {
	tests := []struct {
		name string
		s    Score
		k    float64
		want float64
	}{
		{"互角", Score{CP: 0}, 0, 0.5},
		{"既定の定数で +1500", Score{CP: 1500}, 0, 1 / (1 + math.Exp(-1))},
		{"符号が逆なら 1 との差が同じ", Score{CP: -1500}, 0, 1 - 1/(1+math.Exp(-1))},
		{"定数を指定できる", Score{CP: 600}, 600, 1 / (1 + math.Exp(-1))},
		// 詰みは手数によらず振り切る（詰んでいることに変わりはない）。
		{"先手の詰み", Score{Mate: 3}, 0, 1},
		{"後手の詰み", Score{Mate: -11}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WinRate(tt.s, tt.k); math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("WinRate(%+v, %v) = %v, want %v", tt.s, tt.k, got, tt.want)
			}
		})
	}
}

// ⚠️ **既定値の解決は 1 か所**（設定タブの表示も解析の指定もここを通る）。
func TestPonanzaConstantOr(t *testing.T) {
	if got := PonanzaConstantOr(0); got != DefaultPonanzaConstant {
		t.Errorf("PonanzaConstantOr(0) = %v, want %v", got, DefaultPonanzaConstant)
	}
	if got := PonanzaConstantOr(-1); got != DefaultPonanzaConstant {
		t.Errorf("PonanzaConstantOr(-1) = %v, want %v", got, DefaultPonanzaConstant)
	}
	if got := PonanzaConstantOr(600); got != 600 {
		t.Errorf("PonanzaConstantOr(600) = %v, want 600", got)
	}
}

// ⚠️ **勝率も評価値と同じで先手視点。** 後手番の info をそのまま勝率に直すと、
// **後手番のときだけバーが逆に伸びる**（評価値の符号だけ直して勝率を直し忘れる、
// というのが一番ありがちな壊れ方）。
func TestScoreWinRateIsBlackOriented(t *testing.T) {
	in := coreusi.Info{ScoreCP: 1500, HasScore: true}
	black := newScore(in, true, 0)
	white := newScore(in, false, 0)
	if black.WinRate <= 0.5 {
		t.Errorf("先手番で有利なのに勝率 %v", black.WinRate)
	}
	if white.WinRate >= 0.5 {
		t.Errorf("後手番で有利（＝先手不利）なのに勝率 %v", white.WinRate)
	}
	if math.Abs((black.WinRate+white.WinRate)-1) > 1e-9 {
		t.Errorf("先後の勝率の和が 1 になりません: %v + %v", black.WinRate, white.WinRate)
	}
}

// 設定した定数が解析の結果まで届くこと。**届かないと、定数を変えても
// バーが動かない**（画面では「効いていない」としか分からない）。
func TestOptionsPonanzaConstant(t *testing.T) {
	in := coreusi.Info{ScoreCP: 600, HasScore: true, Depth: 1, PV: []string{"7g7f"}}
	acc := &accumulator{black: true, ponanza: 600}
	acc.lines = map[int]Line{}
	if _, ok := acc.add(in); !ok {
		t.Fatal("info が取り込まれませんでした")
	}
	got := acc.snapshot().Lines[0].Score.WinRate
	want := 1 / (1 + math.Exp(-1))
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("WinRate = %v, want %v（定数 600 が効いていない）", got, want)
	}
}
