package app

import (
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// 二歩の盤（8 筋に先手の歩が 2 枚。7 筋の歩を読み違えたつもり）。駒は 40 枚そろっているので未決は無い。
const nifuBoard = "lnsgkgsnl/1r5b1/ppppppppp/9/9/1P7/PP1PPPPPP/1B5R1/LNSGKGSNL"

// 反則のある局面を確定させない（`foulOf`。2026-10-09）の歯止め。
//
// ⚠️ **ここが守っているのは 2 つ。**
//   - **駒落ちでも詰将棋でもない局面の反則は確定させないこと**（二歩の局面から中継を追い始めた。実機で踏んだ）
//   - ⚠️ **駒落ち・詰将棋の属性が付いていれば今までどおり確定させること**（玉が 1 枚・駒数が合わないのが
//     正しい局面がある。止めると詰将棋が確定できなくなる）
func TestAdoptRejectsFoulWithoutAttribute(t *testing.T) {
	s := edited(t, nifuBoard)
	if _, err := s.SetTurn(int(position.TurnBlack)); err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	if st := s.State(); st.SFEN == "" {
		t.Fatal("前提が崩れています: 未決が残っていて SFEN が組み上がりません")
	}
	study := NewStudyService(s)
	_, err := study.Adopt()
	if err == nil || !strings.Contains(err.Error(), "反則") {
		t.Fatalf("二歩の局面を確定させてはいけない: err=%v", err)
	}
	if study.State().Loaded {
		t.Error("断ったのに解析タブに局面が入りました")
	}
}

func TestAdoptAcceptsFoulWithAttribute(t *testing.T) {
	cases := []struct {
		name string
		set  func(s *PositionService) error
	}{
		{"詰将棋", func(s *PositionService) error { _, err := s.SetMateProblem(true); return err }},
		{"駒落ち", func(s *PositionService) error { _, err := s.SetHandsFixed(true); return err }},
	}
	for _, c := range cases {
		s := edited(t, nifuBoard)
		if _, err := s.SetTurn(int(position.TurnBlack)); err != nil {
			t.Fatalf("%s: SetTurn: %v", c.name, err)
		}
		if err := c.set(s); err != nil {
			t.Fatalf("%s: 属性を付けられません: %v", c.name, err)
		}
		if _, err := NewStudyService(s).Adopt(); err != nil {
			t.Errorf("%s: 属性が付いていれば今までどおり確定させること: %v", c.name, err)
		}
	}
}
