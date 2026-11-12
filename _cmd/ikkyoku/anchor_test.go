package main

// 追う盤（マスタ）にする 1 枚の控え方（`SetFollowShot` / `noteShot`）の歯止め（2026-10-09）。決め方（`followcam.PickAnchor`）の分は
// `followcam` の `anchor_test.go`。
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
	"github.com/ShinteLab/ikkyoku/followcam"
	"github.com/ShinteLab/ikkyoku/recognize"
)

var (
	anchorRegion = ikkyoku.Region{X: 10, Y: 20, Width: 400, Height: 300}
	anchorShot   = followcam.ShotAnchor{
		Sig:     recognize.Signature{Frame: image.Rect(0, 0, 400, 300), Board: image.Rect(100, 60, 280, 240), Color: 180},
		Region:  anchorRegion,
		Path:    "shot.png",
		Session: "s1",
	}
)

func TestSetFollowShot(t *testing.T) {
	s := &CaptureService{studySession: func() string { return "s1" }}
	s.lastShot = anchorShot
	s.lastShot.Session = ""

	s.SetFollowShot("shot.png")
	if !s.followShot.OK() || s.followShot.Session != "s1" {
		t.Fatalf("最後に撮った 1 枚を採ったなら、今の検討の id を添えて覚えること: %+v", s.followShot)
	}
	// 画像ファイル・画像の無い局面（違うパス・空）なら忘れる。
	for _, path := range []string{"other.png", ""} {
		s.followShot = anchorShot
		s.SetFollowShot(path)
		if s.followShot.OK() {
			t.Errorf("%q: 最後に撮った 1 枚でなければ忘れること", path)
		}
	}
}

// ⚠️ **盤が取れなかった 1 枚でも控えを入れ替えること** —— その 1 枚を訂正して採っても、
// 前の 1 枚の盤をマスタにしない。
func TestNoteShotReplacesEvenWithoutBoard(t *testing.T) {
	s := &CaptureService{studySession: func() string { return "s1" }}
	s.lastShot = anchorShot

	s.noteShot(image.NewRGBA(image.Rect(0, 0, 400, 300)), anchorRegion, "noboard.png", nil)
	s.SetFollowShot("noboard.png")
	if s.followShot.OK() {
		t.Fatalf("盤の取れなかった 1 枚を採ったのに、前の 1 枚の盤を覚えた: %+v", s.followShot)
	}
}

// ⚠️ **訂正した 1 枚（`followShot`）は追う前から入るものなので、追跡を止めても捨てないこと**（2026-10-11）。
// 撮る側の持ち物（`followcam.Camera`）は止めたら丸ごと捨てるが、こちらは `CaptureService` が持っている。
// ⚠️ **撮る側には差し込み口（`Corrected`）から渡ること**（渡らないと、訂正した 1 枚をマスタにできない）。
func TestFollowShotSurvivesEndFollow(t *testing.T) {
	s := NewCaptureService("", "", "")
	s.followShot = followcam.ShotAnchor{Path: "corrected.png"}
	s.follow.BeginFollow()
	if got := s.follow.Corrected(); got.Path != "corrected.png" {
		t.Fatalf("訂正した 1 枚が撮る側に渡っていません: %+v", got)
	}
	s.follow.EndFollow()
	if s.followShot.Path != "corrected.png" {
		t.Errorf("追う前から入る訂正した 1 枚まで捨てました")
	}
}
