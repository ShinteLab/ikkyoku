package app

import "testing"

// 手や頭が盤に被って「見えない」マス（suteme の `CellDebug.Hidden`。2026-10-06）。
//
// 見えないマスは根拠にも減点にもせず、行き先が見えない手は足さずに待つ。
// **判断しているのは `position`**（`CellMask`）で、ここでは追従の入口から見て
// 「撮った 1 枚の印がちゃんと届いているか」を見る。

// hiddenAt は (rank, file) を見えないことにした 81 個の並びを作る（行優先）。
func hiddenAt(cells ...[2]int) []bool {
	out := make([]bool, 81)
	for _, c := range cells {
		out[c[0]*9+c[1]] = true
	}
	return out
}

// ⚠️ **行き先が見えない 1 枚からは足さず、手がどいた次の 1 枚で足すこと。**
//
// 実機の △3一歩打（髪が被った 3一 を「後手の駒」と読み、撮った盤と完全に一致していたので
// 行き先の裏付けも 2 枚の確かめもすり抜けた）と同じ形。ここでは ▲7六歩 の 7六 に手が被って
// 「先手の歩」と読まれている。**頭が動かなければ同じ 1 枚が続く**ので、何枚来ても足さないこと。
func TestFollowAutoWaitsForUnseenArrival(t *testing.T) {
	s, _ := following(t)
	board := boardAfter(t, "7g7f")
	covered := hiddenAt([2]int{5, 2}) // 7六

	for i := 0; i < 3; i++ {
		a, err := s.FollowAuto(board, evenConf(0.9), covered)
		if err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
		if a.Applied || a.Pending {
			t.Fatalf("%d 枚目: 行き先が見えないのに足そうとしました: applied=%v pending=%v moves=%v",
				i+1, a.Applied, a.Pending, a.Moves)
		}
		if a.Unseen != 1 {
			t.Errorf("Unseen = %d, want 1", a.Unseen)
		}
	}

	// 手がどいた（印が消えた）1 枚なら、今までどおり 2 枚で足す。
	if a, err := s.FollowAuto(board, evenConf(0.9), nil); err != nil || !a.Pending {
		t.Fatalf("手がどいた 1 枚目で控えていません: %+v (%v)", a, err)
	}
	a, err := s.FollowAuto(board, evenConf(0.9), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Applied || len(a.Moves) != 1 || a.Moves[0] != "7g7f" {
		t.Fatalf("手がどいたのに ▲7六歩 を足していません: %+v", a)
	}
	if a.Unseen != 0 {
		t.Errorf("Unseen = %d, want 0", a.Unseen)
	}
}

// ⚠️ **頭が盤の上辺に被っていても、反対側で指された手は入ること。**
//
// 一段目が丸ごと「空き」に化けている。見えないマスを食い違いに数えると、足した手に
// **「覆したマス」が 9 つ付く**（覆したのではなく、読めていないだけ）。
func TestFollowAutoReadsAroundUnseen(t *testing.T) {
	s, _ := following(t)
	const board = "9/1r5b1/ppppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL" // ▲7六歩 + 一段目が空き
	top := make([][2]int, 9)
	for f := range top {
		top[f] = [2]int{0, f}
	}
	hidden := hiddenAt(top...)

	if a, err := s.FollowAuto(board, evenConf(0.9), hidden); err != nil || !a.Pending {
		t.Fatalf("1 枚目で控えていません: %+v (%v)", a, err)
	}
	a, err := s.FollowAuto(board, evenConf(0.9), hidden)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Applied || len(a.Moves) != 1 || a.Moves[0] != "7g7f" {
		t.Fatalf("▲7六歩 を足していません: %+v", a)
	}
	if len(a.Fixed) != 0 {
		t.Errorf("見えないマスが「覆したマス」に出ています: %+v", a.Fixed)
	}
	if a.Guess {
		t.Errorf("見えているマスはぴったり合っているのに推測の印が付きました")
	}
	if a.Unseen != 9 {
		t.Errorf("Unseen = %d, want 9", a.Unseen)
	}
}

// ⚠️ **目線（後手が手前）で盤を回すなら、見えないマスも一緒に回すこと**（費用表と同じ）。
func TestFollowFrameRotatesUnseen(t *testing.T) {
	pos := NewPositionService()
	if _, err := pos.SetViewpoint(true); err != nil {
		t.Fatalf("SetViewpoint: %v", err)
	}
	_, _, unseen, rotated, err := pos.followFrame(hirateBoard, evenConf(0.9), hiddenAt([2]int{0, 0}))
	if err != nil {
		t.Fatalf("followFrame: %v", err)
	}
	if !rotated || unseen == nil || !unseen[8][8] || unseen[0][0] {
		t.Fatalf("見えないマスが盤と一緒に回っていません: rotated=%v unseen=%v", rotated, unseen)
	}
}

// ⚠️ **印が無い・数が合わないときは nil（＝全部見えている。今までどおり）に倒すこと。**
func TestHiddenMask(t *testing.T) {
	if hiddenMask(nil) != nil {
		t.Error("nil → nil のはず")
	}
	if hiddenMask(make([]bool, 80)) != nil {
		t.Error("数が合わなければ nil のはず")
	}
	if hiddenMask(make([]bool, 81)) != nil {
		t.Error("1 つも立っていなければ nil のはず")
	}
	m := hiddenMask(hiddenAt([2]int{3, 4}))
	if m == nil || !m[3][4] || m.Count() != 1 {
		t.Fatalf("行優先で表にしていません: %v", m)
	}
}
