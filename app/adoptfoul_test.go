package app

import (
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// 二歩の盤（8 筋に先手の歩が 2 枚。7 筋の歩を読み違えたつもり）。駒は 40 枚そろっているので未決は無い。
const nifuBoard = "lnsgkgsnl/1r5b1/ppppppppp/9/9/1P7/PP1PPPPPP/1B5R1/LNSGKGSNL"

// 局面として成り立っていない局面を確定させない（`foulOf`。2026-10-09）の歯止め。
//
// ⚠️ **属性ごとに許すものは 1 つずつ**（ユーザーの指示）:
//   - 平手 … 何も許さない
//   - 駒落ち … **駒が少ないことだけ**（二歩は駄目）
//   - 詰将棋 … **攻方の玉が無いことだけ**（玉方の玉は要る。二歩は駄目）

// adoptWith は盤を訂正タブに入れ、属性を付けて（set が nil なら平手）、解析タブへ採る。
func adoptWith(t *testing.T, board string, set func(s *PositionService) error) error {
	t.Helper()
	s := edited(t, board)
	if _, err := s.SetTurn(int(position.TurnBlack)); err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	if set != nil {
		if err := set(s); err != nil {
			t.Fatalf("属性を付けられません: %v", err)
		}
	}
	_, err := NewStudyService(s).Adopt()
	return err
}

func asHandicap(s *PositionService) error { _, err := s.SetHandsFixed(true); return err }
func asMate(s *PositionService) error     { _, err := s.SetMateProblem(true); return err }

func TestAdoptRejectsDoubledPawnEverywhere(t *testing.T) {
	for name, set := range map[string]func(*PositionService) error{
		"平手": nil, "駒落ち": asHandicap, "詰将棋": asMate,
	} {
		if err := adoptWith(t, nifuBoard, set); err == nil {
			t.Errorf("%s: 二歩の局面を確定させてはいけない", name)
		}
	}
}

func TestAdoptHandicapAllowsOnlyFewerPieces(t *testing.T) {
	if err := adoptWith(t, nimaiBoard, asHandicap); err != nil {
		t.Errorf("駒落ちは駒が少なくても確定させること: %v", err)
	}
	// 駒落ちでも玉は 2 枚要る（先手の玉を抜いた二枚落ち）。
	if err := adoptWith(t, "lnsgkgsnl/9/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSG1GSNL", asHandicap); err == nil {
		t.Error("駒落ちでも玉が無い局面は確定させてはいけない")
	}
}

func TestAdoptMateAllowsOnlyMissingAttackerKing(t *testing.T) {
	if err := adoptWith(t, "4k4/9/4G4/9/9/9/9/9/9", asMate); err != nil {
		t.Errorf("詰将棋は攻方の玉が無くても確定させること: %v", err)
	}
	// 玉方の玉が無い・どちらの玉も無いのは駄目。
	for name, board := range map[string]string{
		"玉方の玉が無い":  "9/9/4G4/9/9/9/9/9/4K4",
		"どちらの玉も無い": "9/9/4G4/9/9/9/9/9/9",
	} {
		if err := adoptWith(t, board, asMate); err == nil {
			t.Errorf("%s: 確定させてはいけない", name)
		}
	}
}
