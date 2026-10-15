package position

// ⚠️ **SideChangedMoves / SideFits の歯止め**（2026-10-09。推測で足した手の差し替えを速い経路で見る）:
//   - **種類だけ違う行き先は「変わっていない」と見ること**（△3二角打 を推測で足したが本当は △3二金。3二 の見た目は変わらない）
//   - **どちらの盤でも空きのマスは、変わっていてよいこと**（金の動けるマスが光った）
//   - **先後か駒の有無が変わったのに変わったマスに入っていなければ合わないこと**（見えていない手は採らない）
//   - **駒のあるマスが光っただけ（先後も有無も変わらない）では合わないこと**

import (
	"slices"
	"testing"
)

// guessLine は △3二角打 を推測で足す前の局面（▲7六歩 △3四歩 ▲2二角成 △同銀 ▲2六歩。後手は角を持っている）。
var guessLine = []string{"7g7f", "3c3d", "8h2b+", "3a2b", "2g2f"}

func TestSideChangedMovesReplacesDropWithMove(t *testing.T) {
	anc := posAfter(t, guessLine...)
	base := posAfter(t, append(slices.Clone(guessLine), "B*3b")...) // 比べる相手の 1 枚の局面（推測の手のあと）
	// 4一 が空き、金の動ける 3一・4二・5二 が光った 1 枚。3二 は角打ちの絵のまま（実は金）なので入らない。
	changed := maskOf("4a", "3a", "4b", "5b")
	got, err := SideChangedMoves(anc, base.Board, changed, maskOf("3b"), maskOf("3b"), 2)
	if err != nil {
		t.Fatalf("SideChangedMoves: %v", err)
	}
	if len(got) != 1 || !slices.Equal(got[0], []string{"4a3b"}) {
		t.Fatalf("got %v, want [[4a3b]]", got)
	}
	// 種類まで比べる `ChangedMoves` では合わない（3二 の中身が角から金に変わるのに、変わったマスに入っていない）。
	if strict, _ := ChangedMoves(anc, changed, maskOf("3b"), 2); len(strict) != 0 {
		t.Fatalf("前提が崩れています: 種類まで比べても合いました %v", strict)
	}
}

func TestSideFits(t *testing.T) {
	base := posAfter(t, append(slices.Clone(guessLine), "B*3b")...)
	moved := posAfter(t, append(slices.Clone(guessLine), "4a3b")...)
	if !SideFits(base.Board, moved.Board, maskOf("4a", "4b"), nil) {
		t.Error("種類だけ違う 3二 と光った空きの 4二 は、変わっていてよい")
	}
	if SideFits(base.Board, moved.Board, maskOf("4b"), nil) {
		t.Error("4一 が空いたのに変わったマスに入っていない（見えていない手は採らない）")
	}
	if SideFits(base.Board, moved.Board, maskOf("4a", "5a"), nil) {
		t.Error("駒のある 5一（玉）が光っただけでは合わない")
	}
	if SideFits(base.Board, base.Board, maskOf("4b"), nil) {
		t.Error("先後も駒の有無も 1 マスも変わらなければ偽")
	}
}
