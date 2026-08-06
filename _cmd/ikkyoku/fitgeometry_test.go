package main

import (
	"image"
	"testing"

	"github.com/ShinteLab/ikkyoku"
)

// fitGeometry は「スクリーンのどこに盤があったか」を枠ウィンドウの座標に戻す。
// **符号を 1 つ間違えると枠が逆方向へ飛ぶ**が、それに気づく手段が実機しか無いので
// ここで固定しておく。
//
// 枠ウィンドウは DIP、キャプチャ領域と盤はスクリーン座標・物理ピクセル。
// **原点が揃っていない**(枠の位置とキャプチャ領域の位置は別物)ので、
// テストでもわざと離れた値を使う。
func TestFitGeometry(t *testing.T) {
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
			// 盤がキャプチャ領域より右下に 10px ずれていて、20px 小さい。
			// 枠は右下へ 10 動き、幅と高さが 20 縮む。
			name:  "等倍",
			board: image.Rect(412, 946, 412+456, 946+364),
			scale: 1.0,
			want:  windowState{X: 110, Y: 210, Width: 460, Height: 400},
			moved: true,
		},
		{
			// 盤がキャプチャ領域の外(左上)にある。画面全体から探すので普通に起きる。
			// 枠は左上へ大きく動く。
			name:  "枠の外で見つかった",
			board: image.Rect(102, 236, 102+476, 236+384),
			scale: 1.0,
			want:  windowState{X: -200, Y: -500, Width: 480, Height: 420},
			moved: true,
		},
		{
			// 150% スケーリング。物理 30px のずれは DIP では 20。
			// サイズは外側に倒すので、割り切れない縮小は 1px 控えめになる。
			name:  "150%",
			board: image.Rect(432, 966, 432+416, 966+324),
			scale: 1.5,
			want:  windowState{X: 120, Y: 220, Width: 440, Height: 380},
			moved: true,
		},
		{
			// slop 以内のずれでは動かさない(押すたびに数px 動くのを避ける)。
			name:  "ずれが小さければ動かさない",
			board: image.Rect(404, 937, 404+476, 937+384),
			scale: 1.0,
			want:  cur,
			moved: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, moved := fitGeometry(cur, region, tt.board, 2, tt.scale)
			if moved != tt.moved {
				t.Errorf("moved = %v, want %v", moved, tt.moved)
			}
			if got != tt.want {
				t.Errorf("fitGeometry() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// 検出結果は毎回 数px 揺れる(実測で 5〜10px)。呼び出し側は余白と同じ値を slop に
// 渡しており、**余白より小さいずれは盤の収まり方を変えない**ので動かさない。
// ここが 2px 固定に戻ると、押すたびに枠が少し動く。
func TestFitGeometrySlop(t *testing.T) {
	cur := windowState{X: 100, Y: 200, Width: 480, Height: 420}
	region := ikkyoku.Region{X: 0, Y: 0, Width: 476, Height: 384}
	board := image.Rect(5, 5, 476+5, 384+5) // 5px ずれているが大きさは同じ

	if _, moved := fitGeometry(cur, region, board, 7, 1.0); moved {
		t.Error("slop=7 で 5px のずれを直しにいっている")
	}
	if _, moved := fitGeometry(cur, region, board, 2, 1.0); !moved {
		t.Error("slop=2 では 5px のずれを直すはず")
	}
}

// **盤ぴったりに合わせると次のキャプチャが認識しづらくなる**ので、
// マスの大きさに比例した余白を残す。ここが 0 に戻るとフィットが逆効果になる。
func TestWithFitMargin(t *testing.T) {
	tests := []struct {
		name  string
		board image.Rectangle
		want  image.Rectangle
	}{
		{
			// マス 70px → 余白 7px。
			name:  "マスの1割",
			board: image.Rect(100, 200, 730, 830),
			want:  image.Rect(93, 193, 737, 837),
		},
		{
			// 縦長の矩形では狭いほう(横 45px)に合わせる → 余白 5px(4.5 の四捨五入)。
			name:  "縦横で違えば狭いほう",
			board: image.Rect(0, 0, 405, 630),
			want:  image.Rect(-5, -5, 410, 635),
		},
		{
			// 小さすぎる盤でも下限は残す(マス 9px → 0.9px → 2px)。
			name:  "下限",
			board: image.Rect(0, 0, 81, 81),
			want:  image.Rect(-2, -2, 83, 83),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := withFitMargin(tt.board); got != tt.want {
				t.Errorf("withFitMargin(%v) = %v, want %v", tt.board, got, tt.want)
			}
		})
	}
}
