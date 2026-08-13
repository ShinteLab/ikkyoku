package position

import (
	"strings"
	"testing"
)

// 平手で始めたときに、**確定した局面**（＝解析にそのまま渡せる形）が返ること。
// ここが崩れると「新しく始めたのに解析できない」になる。
func TestNewGameHirate(t *testing.T) {
	s, err := NewGame(Hirate)
	if err != nil {
		t.Fatalf("NewGame: %v", err)
	}
	got, err := s.Root().SFEN()
	if err != nil {
		t.Fatalf("根の SFEN が組み上がりません: %v", err)
	}
	const want = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL b - 1"
	if got != want {
		t.Errorf("初期局面が違います\n got: %s\nwant: %s", got, want)
	}
	if s.Ply() != 0 || len(s.Nodes()) != 0 {
		t.Errorf("手順が空ではありません: ply=%d moves=%d", s.Ply(), len(s.Nodes()))
	}
	// 初期局面から指せる手は 30 通り。**盤を作っただけで指せる状態**であること
	// （出せないと、始めた直後に駒を押しても何も光らない）。
	moves, err := s.Legal()
	if err != nil {
		t.Fatalf("合法手: %v", err)
	}
	if len(moves) != 30 {
		t.Errorf("初期局面の合法手が %d 手です(30 手のはず)", len(moves))
	}
}

// 手合割を空で渡したら平手（UI が何も選ばなかったときの既定）。
func TestNewGameEmptyIsHirate(t *testing.T) {
	a, err := NewGame("")
	if err != nil {
		t.Fatalf("NewGame(\"\"): %v", err)
	}
	b, err := NewGame(Hirate)
	if err != nil {
		t.Fatalf("NewGame(平手): %v", err)
	}
	x, _ := a.Root().SFEN()
	y, _ := b.Root().SFEN()
	if x != y {
		t.Errorf("空と平手が食い違います\n%s\n%s", x, y)
	}
}

// ⚠️ **駒落ちは「これから」ではなく、既に通る**（UI に出していないだけ）。
// **上手＝後手が初手**なので手番は後手番。ここを先手に倒すと、
// 二枚落ちを始めた瞬間に下手が 2 回続けて指す形になる。
func TestNewGameHandicap(t *testing.T) {
	s, err := NewGame("二枚落ち")
	if err != nil {
		t.Fatalf("NewGame(二枚落ち): %v", err)
	}
	got, err := s.Root().SFEN()
	if err != nil {
		// ⚠️ HandsFixed を落とすとここで落ちる（駒台の逆算が未決を残す）。
		t.Fatalf("根の SFEN が組み上がりません: %v", err)
	}
	if !strings.Contains(got, " w ") {
		t.Errorf("駒落ちの手番が後手ではありません: %s", got)
	}
	if s.Root().Turn != TurnWhite {
		t.Errorf("Turn が %v です(後手番のはず)", s.Root().Turn)
	}
}

// ⚠️ **知らない手合割を平手に倒さない。** 倒すと、別の初期配置で始めたつもりの
// 対局が黙って平手になる（画面では気づけない）。
func TestNewGameUnknownHandicap(t *testing.T) {
	if _, err := NewGame("そんな手合割は無い"); err == nil {
		t.Fatal("知らない手合割がエラーになりません")
	}
}
