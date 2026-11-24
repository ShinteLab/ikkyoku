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

// BenchmarkMoves は `legal.Moves` 1 回の費用を測る。
//
// ⚠️ **`position.Connect`（盤面の差分を手順に復元する探索）の上限がこの数字で決まる。**
// あちらは 1 節点につきここを 1 回呼ぶので、**深さ 4 で数千〜数万回**になる。
// 遅くなったら直すのはこちら側（盤を作り直さない口を足す）であって、探索側ではない。
func BenchmarkMoves(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := legal.Moves(hirate); err != nil {
			b.Fatal(err)
		}
	}
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

// Parse は局面を見ずに座標だけ返す（解析タブの「直前の手」用。2026-10-07）。
// ⚠️ **打ちは移動元が -1**（盤の外）。画面はそれを見て移動元を塗らない。
func TestParse(t *testing.T) {
	m, ok := legal.Parse("P*5e")
	if !ok || m.FromRank != -1 || m.FromFile != -1 || m.ToRank != 4 || m.ToFile != 4 || !m.Drops() {
		t.Fatalf("Parse(P*5e) = %+v, %v", m, ok)
	}
	m, ok = legal.Parse("8h2b+")
	if !ok || m.FromRank != 7 || m.FromFile != 1 || m.ToRank != 1 || m.ToFile != 7 || !m.Promote {
		t.Fatalf("Parse(8h2b+) = %+v, %v", m, ok)
	}
	if _, ok := legal.Parse("resign"); ok {
		t.Fatal("resign が手として読めました")
	}
}

// **手番の側が王手されているか**（2026-10-09。連続王手の千日手を見分ける）。
// ⚠️ **手番の側**を見ること —— 逆を見ると、王手をかけた側とかけられた側が入れ替わり、
// 反則負けの側を取り違える。
func TestInCheck(t *testing.T) {
	for _, tc := range []struct {
		sfen string
		want bool
	}{
		{hirate, false},
		{"4R3k/9/9/9/9/9/9/9/K8 w - 1", true},  // △玉に▲飛の王手。手番は△
		{"4R3k/9/9/9/9/9/9/9/K8 b - 1", false}, // 同じ盤でも手番が▲なら▲は王手されていない
	} {
		got, err := legal.InCheck(tc.sfen)
		if err != nil {
			t.Fatalf("InCheck(%s): %v", tc.sfen, err)
		}
		if got != tc.want {
			t.Errorf("InCheck(%s) = %v, want %v", tc.sfen, got, tc.want)
		}
	}
}

// Piece は指したあとに行き先に居る駒の字（2026-11-25。成る / 成らずを駒の字で聞くため）。
// ⚠️ **成る手と成らない手で字が分かれること**（同じ字だと画面で見分けが付かない）。
// 後手の駒も大文字・成駒が動く手は成った字のまま・打ちは打つ駒の字。
func TestMovesPiece(t *testing.T) {
	// 先手の銀が 3 段目へ入れる（4d → 4c）。後手の歩を 1 枚持ち、先手の龍が 9 筋にいる。
	const s = "4k4/9/9/5S3/9/9/9/+R8/4K4 b P 1"
	ms, err := legal.Moves(s)
	if err != nil {
		t.Fatalf("Moves: %v", err)
	}
	want := map[string]string{
		"4d4c+": "+S",
		"4d4c":  "S",
		"9h9g":  "+R",
		"P*5e":  "P",
	}
	got := map[string]string{}
	for _, m := range ms {
		if _, ok := want[m.USI]; ok {
			got[m.USI] = m.Piece
		}
	}
	for usi, w := range want {
		if g, ok := got[usi]; !ok {
			t.Errorf("%s が合法手に入っていません", usi)
		} else if g != w {
			t.Errorf("%s の Piece = %q, want %q", usi, g, w)
		}
	}

	// 後手の手番でも大文字（駒のフォントは大小で同じ字。向きは画面が決める）。
	const w = "4k4/9/9/9/9/3s5/9/9/4K4 w - 1"
	ms, err = legal.Moves(w)
	if err != nil {
		t.Fatalf("Moves: %v", err)
	}
	found := false
	for _, m := range ms {
		if m.USI != "6f6g+" {
			continue
		}
		found = true
		if m.Piece != "+S" {
			t.Errorf("後手の 6f6g+ の Piece = %q, want \"+S\"", m.Piece)
		}
	}
	if !found {
		t.Error("6f6g+ が合法手に入っていません")
	}
}
