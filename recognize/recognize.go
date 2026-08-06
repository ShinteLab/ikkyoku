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
	"errors"
	"fmt"
	"image"

	"github.com/ShinteLab/suteme"
)

// Option は認識の設定。suteme の関数オプションをそのまま通す。
//
// **ikkyoku 側で薄いラッパを被せ直さない。** チェックの種類も持ち駒の割り振りも
// 将棋の仕様側(core/sfen)の語彙なので、包み直すと同じ選択肢を二重に定義することになる。
// 使う側は `recognize.FromImage(img, suteme.WithErrorOn(sfen.CheckKing))` と書く。
type Option = suteme.Option

// Debug は認識1回分の観測情報(盤面と判定した矩形・その決め方・使った推論器・
// マスごとの分類と確信度)。**Option と同じくエイリアスで、包み直さない。**
// 矩形もクラスも suteme が出した値そのものなので、ikkyoku 側で同じ形の型を
// 定義し直すと変換のぶんだけ嘘が入る余地が増える。
//
// 用途は「認識が外れたときの切り分け」。撮った画像に Region と Cells[].Rect を
// 重ねれば、座標がずれているのか駒種を外しているのかが一目で分かる。
// **認識の判断には使わない**(あくまで観測用)。
type Debug = suteme.Debug

// Board は 1 枚の画像から割り出した盤面。
//
// 1 回の認識は他の認識と完全に独立している(履歴に依存しない)。
type Board struct {
	// SFEN は盤面部分だけの SFEN。手番・持ち駒・手数は含まない。
	// **手番は盤面からは決まらない**(設計原則5)ので、ここには入れられない。
	SFEN string `json:"sfen"`

	// Confidence は盤面検出の信頼度(0.0〜1.0)。低い値は「盤を映していない画面を
	// 撮ったかもしれない」の目安になる。認識結果を疑う入口として UI に出せる。
	Confidence float64 `json:"confidence"`

	// Warnings は成立していない点(駒数・玉・二歩・行き所のない駒)。
	// 「ここが怪しい」の提示にそのまま使える。**警告があっても認識は成功扱い。**
	Warnings []string `json:"warnings"`

	// HandTotal は盤上の駒数から逆算した駒台の枚数。
	// **先後の割り振りは付かない**(どちらの持ち駒かは局面からは決まらない)。
	// 手番と同じく、UI で人間に決めてもらうのが最も安い。
	HandTotal map[string]int `json:"handTotal"`

	// Black / White は盤上に見えている駒数(成駒はベース駒に合算)。
	// HandTotal の逆算根拠であり、訂正 UI で「どの駒を数え間違えたか」を
	// 突き合わせるのに要る。
	Black map[string]int `json:"black"`
	White map[string]int `json:"white"`

	// Debug は「その答えをどう出したか」の観測情報。**表示用**であって、
	// ここまでの各フィールドのように局面の内容を表すものではない。
	// 撮った画像に重ねて「盤をどこだと思ったか」を見せるのが主な使い道
	// (Confidence が低いときに、座標の問題か認識器の問題かを切り分けられる)。
	//
	// suteme が埋めなかった場合は nil。**無くても動くこと**
	// (設計原則3。デバッグ情報が欠けても盤は描ける)。
	Debug *Debug `json:"debug,omitempty"`
}

// FromImage は画像から盤面を割り出す。
//
// **既定では盤面がおかしくてもエラーにしない**(設計原則3「段階的に劣化すること」)。
// 駒数が合わない・玉が無いといった点は Warnings に載るだけで、盤は描ける。
// エラーにしたい呼び出し側は suteme.WithErrorOn / suteme.WithStrict を渡す。
//
// エラーを返すのは「盤そのものが取れなかった」ときと、上記のオプションで
// 明示的にエラーにした違反が見つかったとき。**後者では Board も埋めて返す**ので、
// 呼び出し側は理由を出しつつ認識結果を見せられる。
// **どちらもキャプチャの失敗として扱わないこと。**
func FromImage(img image.Image, opts ...Option) (Board, error) {
	if img == nil {
		return Board{}, fmt.Errorf("ikkyoku/recognize: 画像がありません")
	}

	// 持ち駒の先後は割り振らない(suteme.HandNone。既定のまま)。
	// **どちらの持ち駒かは盤面からは決まらない**ので、ここで先手に寄せるような
	// 便宜的な決め打ちはしない。決めるのは UI(人間)の仕事。
	r, err := suteme.Recognize(img, opts...)
	if r == nil {
		// 盤が取れていない。理由はそのまま呼び出し側(UI)に見せる。文言は
		// 「盤面を認識できませんでした: <ここ>」の形で使われるので、ここでは重ねない。
		if err == nil {
			err = errors.New("suteme が結果を返しませんでした")
		}
		return Board{}, fmt.Errorf("ikkyoku/recognize: %w", err)
	}

	b := Board{
		SFEN:       r.Board,
		Confidence: r.Confidence,
		Warnings:   r.Warnings(),
		HandTotal:  nonNil(r.HandTotal),
		Black:      nonNil(r.Black),
		White:      nonNil(r.White),
		Debug:      r.Debug,
	}
	if err != nil {
		// WithErrorOn / WithStrict でエラー扱いにした違反。Board は埋めたまま返す。
		return b, fmt.Errorf("ikkyoku/recognize: %w", err)
	}
	if b.SFEN == "" {
		return Board{}, fmt.Errorf("ikkyoku/recognize: suteme が空の結果を返しました")
	}
	return b, nil
}

// nonNil は nil マップを空マップにする(JSON で null ではなく {} にするため)。
func nonNil(m map[string]int) map[string]int {
	if m == nil {
		return map[string]int{}
	}
	return m
}
