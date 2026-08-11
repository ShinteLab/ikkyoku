package legal_test

import (
	"testing"

	"github.com/ShinteLab/core/sfen"
	"github.com/ShinteLab/ikkyoku/legal"
)

const hirate = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL b - 1"

// 初期局面の合法手は 30 手（engine の perft と同じ値）。
// **ここが崩れると「動かせる位置」がそのまま嘘になる**ので固定しておく。
func TestMovesHirate(t *testing.T) {
	ms, err := legal.Moves(hirate)
	if err != nil {
		t.Fatalf("Moves: %v", err)
	}
	if len(ms) != 30 {
		t.Fatalf("合法手の数 = %d, want 30", len(ms))
	}
	// ７六歩（7g7f）が入っていること。座標の読み替えを間違えると
	// **見た目は動くのに 1 マスずれた手を指す**ので、座標まで見る。
	var found *legal.Move
	for i := range ms {
		if ms[i].USI == "7g7f" {
			found = &ms[i]
		}
	}
	if found == nil {
		t.Fatal("7g7f が合法手に入っていません")
	}
	// 7g = 7 筋 7 段 → file = 9-7 = 2, rank = 7-1 = 6
	if found.FromRank != 6 || found.FromFile != 2 {
		t.Errorf("移動元 = (%d,%d), want (6,2)", found.FromRank, found.FromFile)
	}
	// 7f = 7 筋 6 段 → rank = 5
	if found.ToRank != 5 || found.ToFile != 2 {
		t.Errorf("移動先 = (%d,%d), want (5,2)", found.ToRank, found.ToFile)
	}
	if found.Drops() || found.Promote {
		t.Errorf("7g7f が打ち/成りになっています: %+v", found)
	}
}

// 打ちは Drop にベース駒コードが入り、From は -1 のまま。
// **駒台の駒はマスを持たない**ので、From では表せない。
func TestMovesDrop(t *testing.T) {
	// 先手が歩を 1 枚持っている局面（1 筋の歩を抜いて持ち駒にした）。
	const s = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/1PPPPPPPP/1B5R1/LNSGKGSNL b P 1"
	ms, err := legal.Moves(s)
	if err != nil {
		t.Fatalf("Moves: %v", err)
	}
	drops := 0
	for _, m := range ms {
		if !m.Drops() {
			continue
		}
		drops++
		if m.Drop != sfen.Pawn {
			t.Errorf("打つ駒 = %d, want %d(歩)", m.Drop, sfen.Pawn)
		}
		if m.FromRank != -1 || m.FromFile != -1 {
			t.Errorf("打ちなのに移動元が入っています: %+v", m)
		}
	}
	if drops == 0 {
		t.Fatal("歩を持っているのに打ちの手が 1 つも出ていません")
	}
}

// ⚠️ **玉の欠けた局面でも落ちないこと。** 詰将棋のような「玉が 1 枚しかない局面」は
// 訂正 UI では確定できる（それが要件）ので、ここに来ること自体はある。
// 合法手が出せなくてもよいが、**アプリごと道連れにしないこと**（設計原則3）。
func TestMovesWithoutKingDoesNotPanic(t *testing.T) {
	const s = "9/9/9/9/4P4/9/9/9/9 b - 1"
	// 返り値は問わない（出ても出なくてもよい）。panic しないことが要件。
	_, _ = legal.Moves(s)
}

// 座標 → USI の組み立て（UI から来た「どこからどこへ」を手にする側）。
// **Moves の読み替えと逆向きなので、往復で固定しておく。**
func TestUSIRoundTrip(t *testing.T) {
	got, err := legal.USI(6, 2, 5, 2, false)
	if err != nil {
		t.Fatalf("USI: %v", err)
	}
	if got != "7g7f" {
		t.Errorf("USI = %q, want %q", got, "7g7f")
	}
	if got, err := legal.USI(2, 1, 1, 1, true); err != nil || got != "8c8b+" {
		t.Errorf("USI(成り) = %q, %v, want %q", got, err, "8c8b+")
	}
	if got, err := legal.DropUSI(sfen.Pawn, 4, 4); err != nil || got != "P*5e" {
		t.Errorf("DropUSI = %q, %v, want %q", got, err, "P*5e")
	}
	if _, err := legal.USI(0, 0, 9, 0, false); err == nil {
		t.Error("盤の外なのにエラーになりませんでした")
	}
}
