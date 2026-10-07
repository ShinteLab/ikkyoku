package position

import (
	"reflect"
	"testing"
)

// ⚠️ **`copyFrom` が `Clone` と同じものを写すこと**（2026-10-07）。
//
// `Connect` の探索は子を作るたびに `Clone` せず、深さごとの作業用の局面へ `copyFrom` で写す。
// `Clone` にだけフィールドを足すと、**探索の中でだけ**そのフィールドが前の節点のまま残る
// （`HandsFixed` なら駒落ちの局面が探索の中で逆算に戻る）。
func TestCopyFromMatchesClone(t *testing.T) {
	p, err := FromFullSFEN("ln1g3nl/1ks3p2/1pp1s2p1/p2p1b1lp/5p1P1/P1P1p4/1PNP1PSRP/2KGGS3/L7+b w RGPnp 31")
	if err != nil {
		t.Fatalf("FromFullSFEN: %v", err)
	}
	p.MateProblem = true

	// 前の中身が残らないことも見るため、別の局面を写してから上書きする。
	dst := &Position{Board: &Board{}, handBlack: map[int]int{}, handWhite: map[int]int{}}
	other, err := FromFullSFEN("lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL b S2Pb 1")
	if err != nil {
		t.Fatalf("FromFullSFEN: %v", err)
	}
	dst.copyFrom(other)
	dst.copyFrom(p)

	if want := p.Clone(); !reflect.DeepEqual(dst, want) {
		t.Fatalf("copyFrom と Clone が食い違います:\n copyFrom=%+v\n Clone   =%+v", dst, want)
	}
}
