package main

import (
	"image"
	"testing"
)

// ⚠️ **撮る側の持ち物は追い始めで作り直し、止めたら丸ごと捨てること**（2026-10-11。`captureFollow`）。
// 捨て損ねると、次に追ったときの録画が前の回のディレクトリに混ざり、前の回の比べる相手の 1 枚と比べてしまう。
// 訂正した 1 枚（`followShot`）は追う前から入るものなので、捨てない。
func TestFollowStateLifecycle(t *testing.T) {
	s := &CaptureService{}
	s.followShot = shotAnchor{path: "corrected.png"}
	s.BeginFollow()
	if !s.fol.following {
		t.Fatalf("追い始めたのに追っていることになっていません")
	}
	s.fol.followDir = "follow/20261011-000000"
	s.fol.cellBase = image.NewRGBA(image.Rect(0, 0, 9, 9))
	s.fol.cellGen, s.fol.gateShots, s.fol.followMisses = 3, 10, 2
	s.fol.cellsUnmoved = frameCells{cells: []int{1}, ok: true}

	s.EndFollow()
	f := s.fol
	if f.following || f.followDir != "" || f.cellBase != nil || f.cellGen != 0 || f.gateShots != 0 ||
		f.followMisses != 0 || f.cellsUnmoved.ok {
		t.Fatalf("止めたのに撮る側の持ち物が残っています: %+v", f)
	}
	if s.followShot.path != "corrected.png" {
		t.Errorf("追う前から入る訂正した 1 枚まで捨てました")
	}
	s.BeginFollow()
	if s.fol.followDir != "" || !s.fol.following {
		t.Fatalf("追い始めで作り直していません: %+v", s.fol)
	}
}
