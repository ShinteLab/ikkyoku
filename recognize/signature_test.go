package recognize_test

import (
	"image"
	"testing"

	"github.com/ShinteLab/ikkyoku/recognize"
	"github.com/ShinteLab/suteme"
)

func sig(x, y, w, h int, color uint8) recognize.Signature {
	return recognize.Signature{
		Frame: image.Rect(0, 0, 800, 800),
		Board: image.Rect(x, y, x+w, y+h),
		Color: color,
	}
}

// ⚠️ **中継の大盤を追いかけないための歯止め**（2026-09-15）。
//
// 大盤は**将棋の局面としては矛盾しない**ので、盤面だけを見ていては弾けない。
// **画面に占める大きさと位置と地色**で「別の盤」と判断する。
func TestSignatureRejectsDifferentBoard(t *testing.T) {
	master := sig(100, 100, 360, 392, 180)
	cases := []struct {
		name string
		got  recognize.Signature
	}{
		{"大きさが違う（大盤に切り替わった）", sig(40, 40, 700, 763, 180)},
		{"位置が違う（別の場所に映っている）", sig(400, 100, 360, 392, 180)},
		{"地色が違う（別の盤）", sig(100, 100, 360, 392, 90)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, why := master.Matches(c.got)
			if ok {
				t.Fatal("別の盤なのに同じと判断しました")
			}
			if why == "" {
				t.Error("理由が空です（画面に何も出せない）")
			}
		})
	}
}

// ⚠️ **本物の盤の揺れでは落とさないこと。** 寄りや露出で多少は動く。
// **落としすぎると、追っているのに何も進まない**（理由も出ないので気づけない）。
func TestSignatureAllowsSmallDrift(t *testing.T) {
	master := sig(100, 100, 360, 392, 180)
	cases := []struct {
		name string
		got  recognize.Signature
	}{
		{"同じ", sig(100, 100, 360, 392, 180)},
		{"少し寄った", sig(94, 102, 384, 418, 180)},
		{"少しずれた", sig(112, 108, 360, 392, 180)},
		{"露出が変わった", sig(100, 100, 360, 392, 160)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if ok, why := master.Matches(c.got); !ok {
				t.Fatalf("同じ盤のはずが落とされました: %s", why)
			}
		})
	}
}

// ⚠️ **判断できないときは「同じ」に倒すこと**（設計原則3）。
//
// 記述子が取れないのは**盤が映っていないとき**で、それは一致度が既に落としている。
// ここで重ねて落とすと、**理由が 2 か所から出て画面が分からなくなる。**
func TestSignatureUnknownPasses(t *testing.T) {
	master := sig(100, 100, 360, 392, 180)
	if ok, _ := master.Matches(recognize.Signature{}); !ok {
		t.Error("取れなかった記述子で落としました")
	}
	if ok, _ := (recognize.Signature{}).Matches(master); !ok {
		t.Error("マスタが無いのに落としました")
	}
}

// 観測値から記述子が取れること（取れなければ false）。
func TestSignatureOf(t *testing.T) {
	d := &suteme.Debug{
		ImageBounds: image.Rect(0, 0, 800, 800),
		Region:      image.Rect(10, 20, 370, 412),
		BoardColor:  177,
	}
	got, ok := recognize.SignatureOf(d)
	if !ok {
		t.Fatal("記述子が取れませんでした")
	}
	if got.Board != d.Region || got.Color != d.BoardColor {
		t.Errorf("記述子 = %+v", got)
	}
	if _, ok := recognize.SignatureOf(nil); ok {
		t.Error("nil から取れてしまいました")
	}
	if _, ok := recognize.SignatureOf(&suteme.Debug{}); ok {
		t.Error("盤が取れていないのに記述子が返りました")
	}
}
