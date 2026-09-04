package guide

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
	cur := Window{X: 100, Y: 200, Width: 480, Height: 420}
	region := ikkyoku.Region{X: 402, Y: 936, Width: 476, Height: 384}

	tests := []struct {
		name  string
		board image.Rectangle
		scale float64
		want  Window
		moved bool
	}{
		{
			// 盤がキャプチャ領域より右下に 10px ずれていて、20px 小さい。
			// 枠は右下へ 10 動き、幅と高さが 20 縮む。
			name:  "等倍",
			board: image.Rect(412, 946, 412+456, 946+364),
			scale: 1.0,
			want:  Window{X: 110, Y: 210, Width: 460, Height: 400},
			moved: true,
		},
		{
			// 盤がキャプチャ領域の外(左上)にある。画面全体から探すので普通に起きる。
			// 枠は左上へ大きく動く。
			name:  "枠の外で見つかった",
			board: image.Rect(102, 236, 102+476, 236+384),
			scale: 1.0,
			want:  Window{X: -200, Y: -500, Width: 480, Height: 420},
			moved: true,
		},
		{
			// 150% スケーリング。物理 30px のずれは DIP では 20。
			// サイズは外側に倒すので、割り切れない縮小は 1px 控えめになる。
			name:  "150%",
			board: image.Rect(432, 966, 432+416, 966+324),
			scale: 1.5,
			want:  Window{X: 120, Y: 220, Width: 440, Height: 380},
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
			got, moved := Geometry(cur, region, tt.board, 2, tt.scale)
			if moved != tt.moved {
				t.Errorf("moved = %v, want %v", moved, tt.moved)
			}
			if got != tt.want {
				t.Errorf("Geometry() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// 検出結果は毎回 数px 揺れる(実測で 5〜10px)。そこまで直しにいくと押すたびに
// 枠が少し動くので、slop 以内は動かさない。
func TestFitGeometrySlop(t *testing.T) {
	cur := Window{X: 100, Y: 200, Width: 480, Height: 420}
	region := ikkyoku.Region{X: 0, Y: 0, Width: 476, Height: 384}
	board := image.Rect(5, 5, 476+5, 384+5) // 5px ずれているが大きさは同じ

	if _, moved := Geometry(cur, region, board, 7, 1.0); moved {
		t.Error("slop=7 で 5px のずれを直しにいっている")
	}
	if _, moved := Geometry(cur, region, board, 2, 1.0); !moved {
		t.Error("slop=2 では 5px のずれを直すはず")
	}
}

// ⚠️ **slop に余白そのものを渡さないこと。** 余白と同じ幅のずれを許すと、
// ずれた向きの余白がちょうど 0 になるところまで見逃す(＝盤の外枠線が画像の端に
// 来て、suteme が検出を外す)。余白を広げた意味が無くなる。
func TestFitSlopIsSmallerThanMargin(t *testing.T) {
	for _, board := range []image.Rectangle{
		image.Rect(100, 200, 730, 830), // マス 70px
		image.Rect(0, 0, 900, 900),     // マス 100px
		image.Rect(0, 0, 81, 81),       // 下限に落ちる小さい盤
	} {
		m, s := Margin(board), Slop(board)
		if s >= m {
			t.Errorf("board=%v: slop %d が余白 %d 以上", board, s, m)
		}
	}
}

// **盤ぴったりに合わせると次のキャプチャが認識しづらくなる**ので、
// マスの大きさに比例した余白を残す。ここが 0 に戻るとフィットが逆効果になる。
//
// 比率は 0.3(suteme の見解: 外枠線を含めたうえで、さらに外側へ 0.3 マス程度・
// 最低 8px)。**0.1 に戻さないこと** —— 実機で「合わせた枠で撮ると認識できず、
// 少し広げると拾う」が出た値。
func TestWithFitMargin(t *testing.T) {
	tests := []struct {
		name  string
		board image.Rectangle
		want  image.Rectangle
	}{
		{
			// マス 70px → 余白 21px。
			name:  "マスの3割",
			board: image.Rect(100, 200, 730, 830),
			want:  image.Rect(79, 179, 751, 851),
		},
		{
			// 縦長の矩形では狭いほう(横 45px)に合わせる → 余白 14px(13.5 の四捨五入)。
			name:  "縦横で違えば狭いほう",
			board: image.Rect(0, 0, 405, 630),
			want:  image.Rect(-14, -14, 419, 644),
		},
		{
			// 小さすぎる盤でも下限は残す(マス 9px → 2.7px → 8px)。
			name:  "下限",
			board: image.Rect(0, 0, 81, 81),
			want:  image.Rect(-8, -8, 89, 89),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WithMargin(tt.board); got != tt.want {
				t.Errorf("WithMargin(%v) = %v, want %v", tt.board, got, tt.want)
			}
		})
	}
}

// TestInsideClickThrough は**素通しにする範囲**を固定する。
//
// ⚠️ **撮る範囲そのものにしないこと。** Wails のリサイズ判定は
// 「クライアント領域の端 5px（角は +10px）」で、ガイド枠（2px）より内側まで
// 食い込む。そこまで素通しにすると**左右と下の縁から枠をリサイズできなくなり、
// 素通しを切るまで大きさを変えられない**。
//
// DPI が上がると詰める幅も物理ピクセルで広がること（CSS px 指定なので）も見ている。
func TestInsideClickThrough(t *testing.T) {
	region := ikkyoku.Region{X: 100, Y: 200, Width: 400, Height: 300}

	tests := []struct {
		name  string
		scale float64
		x, y  int
		want  bool
	}{
		{"真ん中は素通し", 1.0, 300, 350, true},
		{"左の縁は押せる(リサイズを潰さない)", 1.0, 104, 350, false},
		{"右の縁は押せる", 1.0, 495, 350, false},
		{"下の縁は押せる", 1.0, 300, 495, false},
		{"上の縁は押せる", 1.0, 300, 204, false},
		{"内側へ入れば素通し", 1.0, 111, 350, true},
		{"範囲の外は押せる(ツールバー側)", 1.0, 300, 100, false},
		// 150% では詰める幅も 15px になるので、等倍なら素通しだった点が縁に入る。
		{"150%では詰め幅も広がる", 1.5, 111, 350, false},
		{"150%でも内側は素通し", 1.5, 130, 350, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InsideClickThrough(region, tt.scale, tt.x, tt.y); got != tt.want {
				t.Errorf("InsideClickThrough(%d,%d) = %v, want %v", tt.x, tt.y, got, tt.want)
			}
		})
	}
}
