package app

import (
	"testing"

	"github.com/ShinteLab/ikkyoku/followcam"
	"github.com/ShinteLab/ikkyoku/position"
)

// cellOf は SFEN の駒 1 つ（"P" / "+b" など。空は ""）を盤のマスにする。
func cellOf(t *testing.T, piece string) position.Cell {
	t.Helper()
	sfen := "9/9/9/9/9/9/9/9/9"
	if piece != "" {
		sfen = piece + "8/9/9/9/9/9/9/9/9"
	}
	b, err := position.FromSFEN(sfen)
	if err != nil {
		t.Fatalf("FromSFEN(%q): %v", sfen, err)
	}
	c, err := b.At(0, 0)
	if err != nil {
		t.Fatalf("At: %v", err)
	}
	return c
}

// ⚠️ **読んだマスと候補のマスの判定の決まりは `followJudge` の 1 か所**（2026-10-11。観点 2）。ここが崩れると、
// 速い経路と 81 マスの読みで同じ読みの扱いがまたずれる。
func TestFollowJudge(t *testing.T) {
	sure := func(piece string) followObs { return followObsCell(cellOf(t, piece), 0.9) }
	weak := func(piece string) followObs { return followObsCell(cellOf(t, piece), 0.4) }
	cases := []struct {
		name               string
		cand               string
		obs                followObs
		presence, side, kd followMatch
	}{
		{"同じ駒", "P", sure("P"), followAgree, followAgree, followAgree},
		{"先後が違う", "P", sure("p"), followAgree, followDisagree, followAgree},
		{"種類が違う", "S", sure("G"), followAgree, followAgree, followDisagree},
		{"駒があるのに空き", "P", sure(""), followDisagree, followUnknown, followUnknown},
		{"空きなのに駒", "", sure("P"), followDisagree, followUnknown, followUnknown},
		// ⚠️ 弱い駒の読みは何も言わない（光の差し込んだ空きを香 0.40 と読んだ形）。
		{"弱い駒の読み", "", weak("L"), followUnknown, followUnknown, followUnknown},
		// ⚠️ 空きの読みは弱くても「駒が無い」とは言う（費用は確かさのとおり）。
		{"弱い空きの読み", "P", weak(""), followDisagree, followUnknown, followUnknown},
		// ⚠️ 成駒の候補は、成っていない姿と読めたときだけ種類が食い違う。
		{"成駒を成っていない姿と読んだ", "+B", sure("B"), followAgree, followAgree, followDisagree},
		{"成駒を別の駒と読んだ", "+B", sure("P"), followAgree, followAgree, followUnknown},
		{"成駒を成駒と読んだ", "+B", sure("+B"), followAgree, followAgree, followAgree},
		{"成っていない駒を成駒と読んだ", "B", sure("+B"), followAgree, followAgree, followDisagree},
		// 種類の読めない "?" は種類について何も言わない（先後も読めていなければ駒の有無だけ）。
		{"読めない駒", "P", followObsPiece("?"), followAgree, followUnknown, followUnknown},
		{"読んでいない", "P", followObs{}, followUnknown, followUnknown, followUnknown},
	}
	for _, c := range cases {
		v := followJudge(cellOf(t, c.cand), c.obs)
		if v.presence != c.presence || v.side != c.side || v.kind != c.kd {
			t.Errorf("%s: 有無・先後・種類 = %d %d %d, want %d %d %d", c.name, v.presence, v.side, v.kind, c.presence, c.side, c.kd)
		}
	}
}

// ⚠️ **速い経路で読んだマスは、撮った画像の向きの読みを解析の向きに直して比べること**（後手目線なら先後を入れ替える）。
// 種類が読めない "?" でも、向きが読めていれば先後を言う（成駒は種類が読めないのが普通）。
func TestFollowObsRead(t *testing.T) {
	black := cellOf(t, "+P")
	read := []followcam.CellPiece{{Cell: 0, Piece: "?", Side: "up"}, {Cell: 80, Piece: "p"}}
	if v := followJudge(black, followObsRead(read, 0, false)); v.side != followAgree {
		t.Errorf("手前向きの \"?\" が先手の駒と合いません: %+v", v)
	}
	// 後手目線: 解析の向きのマス 0 は撮った画像のマス 80（向こう向きの "p" ＝ 撮った画像の後手 ＝ 先手）。
	// と金の候補を成っていない歩と読んだので、種類は食い違う。
	if v := followJudge(black, followObsRead(read, 0, true)); v.side != followAgree || v.kind != followDisagree {
		t.Errorf("後手目線で先後を入れ替えていません: %+v", v)
	}
	if o := followObsRead(read, 40, false); o.read {
		t.Errorf("読んでいないマスを読んだことにしました: %+v", o)
	}
}
