package followcam

// 追う盤（マスタ）の決め方（`PickAnchor`）の歯止め（2026-10-09）。`CaptureService.SetFollowShot` / `noteShot` の分は
// `_cmd/ikkyoku` の `anchor_test.go`。
//
// ⚠️ **ここが守っているのは 3 つ。**
//   - **訂正してから追うなら、押した瞬間の 1 枚ではなく訂正した 1 枚をマスタにすること**
//     （押したときに棋士の手が盤に被っていて、初手から「別の盤」になった。実機で踏んだ）
//   - ⚠️ **訂正した 1 枚は今の根を採った 1 枚のときだけ使うこと**（棋譜・新規対局で根を入れ替えたあとに、
//     前の 1 枚の盤を追わない）
//   - ⚠️ **撮ったあとに枠が動いていたら使わないこと**（記述子は画像の中の座標なので、
//     使うと本物の盤を「別の盤」として見送り続ける）

import (
	"image"
	"testing"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/recognize"
)

var (
	anchorRegion = ikkyoku.Region{X: 10, Y: 20, Width: 400, Height: 300}
	anchorShot   = ShotAnchor{
		Sig:     recognize.Signature{Frame: image.Rect(0, 0, 400, 300), Board: gateBoard, Color: 180},
		Region:  anchorRegion,
		Path:    "shot.png",
		Session: "s1",
	}
	// anchorLive は押した瞬間の 1 枚（手が被って盤の矩形がずれた、のつもり）。
	anchorLive = recognize.Signature{Frame: image.Rect(0, 0, 400, 300), Board: image.Rect(40, 10, 360, 290), Color: 120}
)

func TestPickAnchorPrefersCorrectedShot(t *testing.T) {
	got, fromShot, err := PickAnchor(anchorShot, "s1", anchorLive, true, anchorRegion)
	if err != nil || !fromShot || got != anchorShot.Sig {
		t.Fatalf("訂正した 1 枚があるなら、押した瞬間の 1 枚を見ずにそれをマスタにすること: got=%v fromShot=%v err=%v", got, fromShot, err)
	}
	// 押した瞬間に盤が映っていなくても始められる。
	if _, fromShot, err := PickAnchor(anchorShot, "s1", recognize.Signature{}, false, anchorRegion); err != nil || !fromShot {
		t.Fatalf("盤が映っていなくても、訂正した 1 枚で始めること: fromShot=%v err=%v", fromShot, err)
	}
}

func TestPickAnchorFallsBackToLive(t *testing.T) {
	moved := anchorRegion
	moved.X++
	cases := []struct {
		name    string
		shot    ShotAnchor
		session string
		region  ikkyoku.Region
	}{
		{"訂正した 1 枚が無い", ShotAnchor{}, "s1", anchorRegion},
		{"根を入れ替えた（検討の id が違う）", anchorShot, "s2", anchorRegion},
		{"撮ったあとに枠が動いた", anchorShot, "s1", moved},
	}
	for _, c := range cases {
		got, fromShot, err := PickAnchor(c.shot, c.session, anchorLive, true, c.region)
		if err != nil || fromShot || got != anchorLive {
			t.Errorf("%s: 押した瞬間の 1 枚で決めること: got=%v fromShot=%v err=%v", c.name, got, fromShot, err)
		}
		if _, _, err := PickAnchor(c.shot, c.session, recognize.Signature{}, false, c.region); err == nil {
			t.Errorf("%s: 盤も映っていなければ、始めずに理由を返すこと", c.name)
		}
	}
}
