// Package recognize は撮った画像から盤面を割り出す層。
//
// **画像 → 盤面の認識そのものは suteme が担当する。** ここがやるのは呼び出しと、
// アプリが扱いやすい形へのまとめだけ。認識器をこちらに書かないこと(責務の線引きは
// ルートの CLAUDE.md 参照)。逆に、認識結果をどう扱うか(矯正・訂正 UI)は ikkyoku の仕事で、
// Phase 3 の局面矯正層はこのパッケージに入る想定。
//
// **認識できないことは異常ではない。** 中継が盤を映していない瞬間に撮ることもあれば、
// 認識器が外すこともある。設計原則「段階的に劣化すること」に従い、呼び出し側が
// 「キャプチャは成功・認識だけ失敗」を区別できるようにしてある。
package recognize

import (
	"fmt"
	"image"

	"github.com/ShinteLab/suteme"
)

// Board は 1 枚の画像から割り出した盤面。
//
// 1 回の認識は他の認識と完全に独立している(履歴に依存しない)。
type Board struct {
	// SFEN は盤面部分だけの SFEN。手番・持ち駒・手数は含まない。
	// **手番は盤面からは決まらない**(設計原則5)ので、ここには入れられない。
	SFEN string `json:"sfen"`

	// Warnings は駒数保存則に反する点。「ここが怪しい」の提示にそのまま使える。
	Warnings []string `json:"warnings"`

	// HandTotal は盤上の駒数から逆算した駒台の枚数。
	// **先後の割り振りは付かない**(どちらの持ち駒かは局面からは決まらない)。
	// 手番と同じく、UI で人間に決めてもらうのが最も安い。
	HandTotal map[string]int `json:"handTotal"`
}

// FromImage は画像から盤面を割り出す。
//
// 認識できなければエラーを返す。**呼び出し側はこれをキャプチャの失敗として扱わないこと。**
func FromImage(img image.Image) (Board, error) {
	if img == nil {
		return Board{}, fmt.Errorf("ikkyoku/recognize: 画像がありません")
	}

	// 認識できなかった理由はそのまま呼び出し側(UI)に見せる。文言は
	// 「盤面を認識できませんでした: <ここ>」の形で使われるので、ここでは重ねない。
	sfenBoard, err := suteme.LoadSFEN(img)
	if err != nil {
		return Board{}, fmt.Errorf("ikkyoku/recognize: %w", err)
	}
	if sfenBoard == "" {
		return Board{}, fmt.Errorf("ikkyoku/recognize: suteme が空の結果を返しました")
	}

	b := Board{
		SFEN:      sfenBoard,
		Warnings:  []string{},
		HandTotal: map[string]int{},
	}

	// 駒数保存則の検証。盤上の駒数から駒台にあるはずの枚数まで逆算してくれるので、
	// **駒台を画像認識しなくても持ち駒が埋まる。**
	if v := suteme.ValidatePieces(sfenBoard); v != nil {
		if len(v.Warnings) > 0 {
			b.Warnings = v.Warnings
		}
		if len(v.HandTotal) > 0 {
			b.HandTotal = v.HandTotal
		}
	}
	return b, nil
}
