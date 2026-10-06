package position

// ⚠️ **ChangedMoves の歯止め**（2026-10-07。追従の速い経路）:
//   - 変わったマスの組から、**1 手・取る手・同じ側で取り返す 2 手**が 1 通りに決まること
//   - **成・不成、打った駒の種類のように見分けられないものは、1 通りに決めないこと**（複数返す）
//   - **変わったマスが合法手のどれとも合わなければ、何も返さないこと**（手や頭が被った 1 枚）
//   - **色付けのマス（extra）は変わっても変わらなくてもよいこと**

import (
	"slices"
	"testing"
)

const hirateFull = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL b - 1"

// maskOf は USI のマス（"7g" など）の集まりを CellMask にする。
func maskOf(squares ...string) *CellMask {
	m := &CellMask{}
	for _, s := range squares {
		m[int(s[1]-'a')][9-int(s[0]-'0')] = true
	}
	return m
}

// posAfter は平手から moves を指した局面。
func posAfter(t *testing.T, moves ...string) *Position {
	t.Helper()
	p, err := FromFullSFEN(hirateFull)
	if err != nil {
		t.Fatalf("FromFullSFEN: %v", err)
	}
	for _, m := range moves {
		if err := p.ApplyMove(m); err != nil {
			t.Fatalf("ApplyMove(%q): %v", m, err)
		}
	}
	return p
}

func TestChangedMovesOneMove(t *testing.T) {
	got, err := ChangedMoves(posAfter(t), maskOf("7g", "7f"), nil, 2)
	if err != nil {
		t.Fatalf("ChangedMoves: %v", err)
	}
	if len(got) != 1 || !slices.Equal(got[0], []string{"7g7f"}) {
		t.Fatalf("got %v, want [[7g7f]]", got)
	}
}

// 指して 1 秒で取り返された 1 枚（▲2四歩 △同歩）。
func TestChangedMovesExchange(t *testing.T) {
	from := posAfter(t, "2g2f", "8c8d", "2f2e", "8d8e")
	got, err := ChangedMoves(from, maskOf("2e", "2d", "2c"), nil, 2)
	if err != nil {
		t.Fatalf("ChangedMoves: %v", err)
	}
	if len(got) != 1 || !slices.Equal(got[0], []string{"2e2d", "2c2d"}) {
		t.Fatalf("got %v, want [[2e2d 2c2d]]", got)
	}
}

// 同じ側の別の駒で取り返した 2 手（△同歩 ▲同飛）。
func TestChangedMovesRecaptureBySameSide(t *testing.T) {
	from := posAfter(t, "2g2f", "8c8d", "2f2e", "8d8e", "2e2d")
	got, err := ChangedMoves(from, maskOf("2c", "2d", "2h"), nil, 2)
	if err != nil {
		t.Fatalf("ChangedMoves: %v", err)
	}
	if len(got) != 1 || !slices.Equal(got[0], []string{"2c2d", "2h2d"}) {
		t.Fatalf("got %v, want [[2c2d 2h2d]]", got)
	}
}

// ⚠️ 成るか成らないかは、変わったマスの組では見分けられない。
func TestChangedMovesPromotionIsAmbiguous(t *testing.T) {
	// ▲7六歩 △3四歩 のあと、角が 2二 の角を取る（成・不成の両方が指せる）。
	from := posAfter(t, "7g7f", "3c3d")
	got, err := ChangedMoves(from, maskOf("8h", "2b"), nil, 2)
	if err != nil {
		t.Fatalf("ChangedMoves: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("成・不成の 2 通りを返すはず: %v", got)
	}
}

// ⚠️ 手や頭が被った 1 枚（変わったマスが合法手のどれとも合わない）からは何も返さない。
func TestChangedMovesNothingFits(t *testing.T) {
	got, err := ChangedMoves(posAfter(t), maskOf("5e", "4e", "5f", "4f"), nil, 2)
	if err != nil {
		t.Fatalf("ChangedMoves: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("合う手は無いはず: %v", got)
	}
}

// 色付けのマス（直前の手）は、変わっていても手の邪魔をしない。
func TestChangedMovesAllowsHighlight(t *testing.T) {
	from := posAfter(t, "7g7f") // 直前の手は 7七→7六
	extra := maskOf("7g", "7f")
	got, err := ChangedMoves(from, maskOf("3c", "3d", "7g"), extra, 2)
	if err != nil {
		t.Fatalf("ChangedMoves: %v", err)
	}
	if len(got) != 1 || !slices.Equal(got[0], []string{"3c3d"}) {
		t.Fatalf("got %v, want [[3c3d]]", got)
	}
	// 色付けのマス以外の余計な変化は許さない。
	if got, _ := ChangedMoves(from, maskOf("3c", "3d", "5e"), extra, 2); len(got) != 0 {
		t.Fatalf("余計なマスが変わっているのに合いました: %v", got)
	}
}
