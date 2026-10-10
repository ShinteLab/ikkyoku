package recognize

import (
	"math"
	"testing"

	"github.com/ShinteLab/suteme"
)

// ⚠️ **空きと読んだマスは被覆率から確かさを作り、駒のマスは推論の確信度のまま**（2026-10-11。`CellConfidence`）。
// 歯止めは: はっきり空いたマスを 0 にしない（安く覆せてしまう）・線に近い空きは安い・測れない空きは 0・
// 並び順ではなく row/col で置く。
func TestCellConfidence(t *testing.T) {
	d := &Debug{EmptyCover: 0.10, Cells: []suteme.CellDebug{
		{Row: 0, Col: 0, Piece: "", Cover: 0.0},  // はっきり空き
		{Row: 0, Col: 1, Piece: "", Cover: 0.09}, // 線のすぐ下（字画の細い「と」を空きと読んだ形）
		{Row: 0, Col: 2, Piece: "", Cover: -1},   // 測れない（色付け）
		{Row: 0, Col: 3, Piece: "", Cover: 0.12}, // 線より上なのに空き（ずらした窓で空にした）
		{Row: 8, Col: 8, Piece: "P", Confidence: 0.8, Cover: 0.3},
	}}
	got := CellConfidence(d)
	if len(got) != 81 {
		t.Fatalf("len = %d, want 81", len(got))
	}
	want := map[int]float64{0: 1, 1: 0.1, 2: 0, 3: 0, 80: 0.8}
	for i, w := range want {
		if math.Abs(got[i]-w) > 1e-9 {
			t.Errorf("マス %d = %v, want %v", i, got[i], w)
		}
	}
	if CellConfidence(nil) != nil || CellConfidence(&Debug{}) != nil {
		t.Errorf("マスが無いのに nil 以外を返しました")
	}
}
