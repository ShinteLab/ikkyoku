package main

import "testing"

// ⚠️ **ファイルの並びが手順の順になること**（0 詰め）と、
// **推測かどうかが名前から読めること**。
//
// **後から見たいのは推測で足した手**（中継には**棋士の手が映り込む**ので、
// 誤認識はまずそちらに出る）。名前で選り分けられないと、
// 1 局ぶん 100 枚以上を全部開くことになる。
func TestFollowFrameName(t *testing.T) {
	tests := []struct {
		name   string
		number int
		moves  []string
		guess  bool
		want   string
	}{
		{"1 手", 42, []string{"7g7f"}, false, "042-7g7f.png"},
		{"推測", 42, []string{"7g7f"}, true, "042-7g7f-guess.png"},
		// ⚠️ **1 枚で 2 手ぶん足すことがある**（CM を挟んだとき）。
		// **その事実自体が手掛かり**なので名前に残す。
		{"2 手", 7, []string{"7g7f", "3c3d"}, false, "007-7g7f_3c3d.png"},
		// ⚠️ **0 詰めが 3 桁あること** —— 2 桁だと 100 手目で並びが崩れる。
		{"3 桁", 128, []string{"2b3c+"}, false, "128-2b3c+.png"},
		{"打つ手", 30, []string{"P*5e"}, false, "030-P*5e.png"},
		// 手が空でも名前は成り立つこと（呼び出し側が壊れていても保存は通す）。
		{"手が無い", 5, nil, false, "005.png"},
		// ⚠️ **パス区切りを作らないこと**（保存先の外へ書けてしまう）。
		{"危ない字", 3, []string{"../evil"}, false, "003-evil.png"},
		{"負の手数", -1, []string{"7g7f"}, false, "000-7g7f.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := followFrameName(tt.number, tt.moves, tt.guess); got != tt.want {
				t.Errorf("followFrameName() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ⚠️ **繋げなかった周の名前**。採用した画像と並ぶので、**名前で区別が付くこと**と、
// **同じ手数で何周も失敗しても上書きされないこと**が要点。
func TestFollowMissName(t *testing.T) {
	tests := []struct {
		number int
		kind   string
		seq    int
		want   string
	}{
		{42, "choices", 0, "x042-choices-1.png"},
		{42, "choices", 1, "x042-choices-2.png"},
		{7, "unreachable", 0, "x007-unreachable-1.png"},
		// 種類が読めなくても名前は成り立つこと。
		{5, "", 0, "x005-miss-1.png"},
		{5, "../evil", 0, "x005-evil-1.png"},
	}
	for _, tt := range tests {
		if got := followMissName(tt.number, tt.kind, tt.seq); got != tt.want {
			t.Errorf("followMissName(%d, %q, %d) = %q, want %q",
				tt.number, tt.kind, tt.seq, got, tt.want)
		}
	}
}
