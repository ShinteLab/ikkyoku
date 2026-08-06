package main

import (
	"image"
	"testing"

	"github.com/ShinteLab/ikkyoku"
)

// fitGeometry は「撮った画像の中で盤がどこにあったか」を枠ウィンドウの座標に戻す。
// **符号を 1 つ間違えると枠が逆方向へ飛ぶ**が、それに気づく手段が実機しか無いので
// ここで固定しておく。
func TestFitGeometry(t *testing.T) {
	// 枠ウィンドウ(DIP)と、その内側のキャプチャ領域(物理px)。
	// 差分だけを使うので、両者の原点が一致している必要はない。
	cur := windowState{X: 100, Y: 200, Width: 480, Height: 420}
	region := ikkyoku.Region{X: 402, Y: 936, Width: 476, Height: 384}

	tests := []struct {
		name  string
		board image.Rectangle
		scale float64
		want  windowState
		moved bool
	}{
		{
			// 盤が右下に 10px ずれていて、20px 小さい。
			// 枠は右下へ 10 動き、幅と高さが 20 縮む。
			name:  "等倍",
			board: image.Rect(10, 10, 466, 374),
			scale: 1.0,
			want:  windowState{X: 110, Y: 210, Width: 460, Height: 400},
			moved: true,
		},
		{
			// 盤が左上に寄っている(画像の原点より手前には無いので、
			// 左上に寄る = 原点のまま盤が小さい)ケース。
			name:  "原点は同じで盤が小さい",
			board: image.Rect(0, 0, 400, 300),
			scale: 1.0,
			want:  windowState{X: 100, Y: 200, Width: 404, Height: 336},
			moved: true,
		},
		{
			// 150% スケーリング。物理 30px のずれは DIP では 20。
			// サイズは外側に倒すので、割り切れない縮小は 1px 控えめになる。
			name:  "150%",
			board: image.Rect(30, 30, 446, 354),
			scale: 1.5,
			want:  windowState{X: 120, Y: 220, Width: 440, Height: 380},
			moved: true,
		},
		{
			// fitSlopPx 以内のずれでは動かさない(押すたびに 1px 動くのを避ける)。
			name:  "ずれが小さければ動かさない",
			board: image.Rect(2, 1, 476, 384),
			scale: 1.0,
			want:  cur,
			moved: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, moved := fitGeometry(cur, region, image.Point{}, tt.board, tt.scale)
			if moved != tt.moved {
				t.Errorf("moved = %v, want %v", moved, tt.moved)
			}
			if got != tt.want {
				t.Errorf("fitGeometry() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// 画像の原点が 0,0 とは限らない(image.Image の Bounds().Min)。
// 原点を引き忘れると、そのぶんだけ枠がずれる。
func TestFitGeometryOrigin(t *testing.T) {
	cur := windowState{X: 100, Y: 200, Width: 480, Height: 420}
	region := ikkyoku.Region{X: 0, Y: 0, Width: 476, Height: 384}
	origin := image.Point{X: 50, Y: 60}

	// 原点から 10px ずれた位置に、キャプチャ領域と同じ大きさの盤がある。
	board := image.Rect(60, 70, 60+476, 70+384)
	got, moved := fitGeometry(cur, region, origin, board, 1.0)
	if !moved {
		t.Fatal("moved = false, want true")
	}
	want := windowState{X: 110, Y: 210, Width: 480, Height: 420}
	if got != want {
		t.Errorf("fitGeometry() = %+v, want %+v", got, want)
	}
}
