package position

import (
	"testing"

	"github.com/ShinteLab/core/sfen"
)

// 盤を回すと、マスの位置と駒の先後の**両方**が入れ替わる。
// 片方だけだと「相手の駒が上下逆に並んだ盤」という別物になるので、
// 盤面 SFEN そのもので固定してある。
func TestBoardRotate180(t *testing.T) {
	const src = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"
	b, err := FromSFEN(src)
	if err != nil {
		t.Fatalf("FromSFEN: %v", err)
	}
	got := b.Rotate180().SFEN()
	if got != src {
		// 平手の初期局面は 180 度回しても同じ形（対称なので）。
		t.Fatalf("初期局面を回した結果が違う\n got=%s\nwant=%s", got, src)
	}
	if b.SFEN() != src {
		t.Fatalf("元の盤を書き換えている: %s", b.SFEN())
	}

	// 対称でない盤で、位置と先後が入れ替わることを見る。
	one, err := FromSFEN("9/9/9/9/9/9/9/9/8P") // 1九に先手の歩
	if err != nil {
		t.Fatalf("FromSFEN: %v", err)
	}
	if got := one.Rotate180().SFEN(); got != "p8/9/9/9/9/9/9/9/9" {
		t.Fatalf("1九の先手歩を回すと 9一の後手歩になるはず: %s", got)
	}

	// 成駒は成ったまま（向きの話であって、成/不成の話ではない）。
	pro, err := FromSFEN("9/9/9/9/9/9/9/9/8+P")
	if err != nil {
		t.Fatalf("FromSFEN: %v", err)
	}
	if got := pro.Rotate180().SFEN(); got != "+p8/9/9/9/9/9/9/9/9" {
		t.Fatalf("成りが落ちている: %s", got)
	}
}

// 局面を回すと、盤・駒台・手番がまとめて入れ替わる。
// ⚠️ **どれか 1 つでも落とすと、画面では気づけない形で局面が壊れる。**
func TestPositionRotate180(t *testing.T) {
	p, err := FromBoardSFEN("9/9/9/9/9/9/9/9/8P")
	if err != nil {
		t.Fatalf("FromBoardSFEN: %v", err)
	}
	p.Turn = TurnBlack
	p.MoveNumber = 41
	if err := p.SetHand(sfen.Rook, true, 1); err != nil {
		t.Fatalf("SetHand: %v", err)
	}
	// 残りは全部先手に寄せて確定させる（回した結果を SFEN で見たいので）。
	for base, n := range p.Unassigned() {
		b, _ := p.assigned(base)
		if err := p.SetHand(base, true, b+n); err != nil {
			t.Fatalf("SetHand: %v", err)
		}
	}
	beforeBlack, beforeWhite := p.Hands()
	before, err := p.SFEN()
	if err != nil {
		t.Fatalf("SFEN: %v", err)
	}
	if len(beforeWhite) != 0 {
		t.Fatalf("前提が崩れている（後手の駒台が空でない）: %v", beforeWhite)
	}

	r := p.Rotate180()
	got, err := r.SFEN()
	if err != nil {
		t.Fatalf("回した局面の SFEN: %v", err)
	}
	if got == before {
		t.Fatalf("回しても何も変わっていない: %s", got)
	}
	// 盤・手番・持ち駒がすべて後手側へ移る。手数はそのまま。
	want := "p8/9/9/9/9/9/9/9/9 w " + sfen.FormatHands(map[int]int{}, beforeBlack) + " 41"
	if got != want {
		t.Fatalf("回した局面が違う\n got=%s\nwant=%s", got, want)
	}
	black, white := r.Hands()
	if len(black) != 0 {
		t.Fatalf("先手の駒台が空になっていない: %v", black)
	}
	if white[sfen.Rook] != beforeBlack[sfen.Rook] {
		t.Fatalf("飛車が後手の駒台に移っていない: %v", white)
	}
	if r.MoveNumber != 41 {
		t.Fatalf("手数が変わっている: %d", r.MoveNumber)
	}

	// 元は 1 文字も変わらない（**画像の向きのまま置いておく**）。
	if after, _ := p.SFEN(); after != before {
		t.Fatalf("元の局面を書き換えている\n before=%s\n after =%s", before, after)
	}

	// 2 回回すと元に戻る。
	if back, _ := r.Rotate180().SFEN(); back != before {
		t.Fatalf("2 回回して戻らない\n got=%s\nwant=%s", back, before)
	}
}

// 手番が未決なら、回しても未決のまま（決めていないことを決めない。設計原則5）。
func TestRotate180KeepsUnknownTurn(t *testing.T) {
	p, err := FromBoardSFEN("9/9/9/9/9/9/9/9/8P")
	if err != nil {
		t.Fatalf("FromBoardSFEN: %v", err)
	}
	if got := p.Rotate180().Turn; got != TurnUnknown {
		t.Fatalf("未決の手番が %v になった", got)
	}
	// 未決の駒台も未決のまま（回しても逆算の結果は同じ）。
	if len(p.Rotate180().Unassigned()) == 0 {
		t.Fatal("回したら駒台の未決が消えた（どちらかに寄せている）")
	}
}

// HandsFixed は向きに関係しないので写す。
// ⚠️ **落とすと駒落ちの局面で逆算が復活し、SFEN が組み上がらなくなる。**
func TestRotate180KeepsHandsFixed(t *testing.T) {
	p, err := FromFullSFEN("lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL b - 1")
	if err != nil {
		t.Fatalf("FromFullSFEN: %v", err)
	}
	if !p.Rotate180().HandsFixed {
		t.Fatal("HandsFixed が落ちている")
	}
}
