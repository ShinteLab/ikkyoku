package main

// 「追う」を押した瞬間に盤が映っていないときのマスタ（`fallbackAnchor` / `noteShot`）の歯止め（2026-10-09）。
//
// ⚠️ **ここが守っているのは 2 つ。**
//   - **撮った 1 枚の盤があれば、押すタイミングを選ばせないこと**（CM・解説者のあいだに押しても始められる）
//   - ⚠️ **撮ったあとに枠が動いていたら使わないこと**（記述子は画像の中の座標なので、
//     使うと本物の盤を「別の盤」として見送り続ける）

import (
	"image"
	"testing"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/recognize"
)

func TestFallbackAnchor(t *testing.T) {
	shot := recognize.Signature{Frame: image.Rect(0, 0, 400, 300), Board: gateBoard, Color: 180}
	region := ikkyoku.Region{X: 10, Y: 20, Width: 400, Height: 300}

	got, err := fallbackAnchor(shot, region, region)
	if err != nil || got != shot {
		t.Fatalf("撮った盤があって枠も同じなら、それをマスタにすること: got=%v err=%v", got, err)
	}
	if _, err := fallbackAnchor(recognize.Signature{}, region, region); err == nil {
		t.Fatal("撮った盤が無ければ、始めずに理由を返すこと")
	}
	moved := region
	moved.X++
	if _, err := fallbackAnchor(shot, region, moved); err == nil {
		t.Fatal("撮ったあとに枠が動いていたら、撮った盤を使わないこと")
	}
}

// ⚠️ **盤が取れなかった 1 枚で前の控えを消さないこと**（枠が同じなら前の盤はまだ使える）。
func TestNoteShotKeepsPreviousWithoutBoard(t *testing.T) {
	s := &CaptureService{}
	shot := recognize.Signature{Board: gateBoard}
	region := ikkyoku.Region{Width: 400, Height: 300}
	s.shotAnchor, s.shotRegion = shot, region

	s.noteShot(gateFrame(), ikkyoku.Region{Width: 500, Height: 300}, nil)
	if s.shotAnchor != shot || s.shotRegion != region {
		t.Fatalf("盤の無い 1 枚で控えが変わった: %v %v", s.shotAnchor, s.shotRegion)
	}
}
