package recognize

import (
	"fmt"
	"image"

	"github.com/ShinteLab/suteme"
)

// MinRegionConfidence は盤面の矩形を採用する最低信頼度(suteme.ValidateBoard の値)。
//
// suteme が領域を採用する基準と同じ値にしてある。**これより上げないこと**を目的に
// 決めた数字ではなく、「suteme が盤だと認めた矩形は ikkyoku も盤として扱う」という
// 一貫性のための値。ここを独自に厳しくすると、認識(FromImage)は通るのに
// ガイド枠は合わせられない、という説明の付かない状態が生まれる。
const MinRegionConfidence = 0.5

// BoardRegion は画像の中で盤面と判定した矩形と、その信頼度。
//
// **画像の座標系そのまま。** 呼び出し側(キャプチャ領域へ戻す側)が原点を足す。
type BoardRegion struct {
	Rect       image.Rectangle `json:"rect"`
	Confidence float64         `json:"confidence"`
}

// DetectRegion は画像の中から盤面の矩形を探す。**駒種は見ない。**
//
// ガイド枠の自動フィット(撮った 1 枚から盤の位置を割り出し、枠をそこへ寄せる)のための
// 入口。FromImage と違って駒種推論器を必要としないので、学習データが無い状態でも動く。
//
// **suteme.Recognize ではなく DetectBoard を直に呼んでいる。** Recognize は
// 検出の信頼度が足りないとき「画像全体が盤面」へフォールバックするが、
// それは枠を合わせる用途では「合わせる先が元の枠のまま」という無意味な答えになる。
// ここが欲しいのはグリッド検出が出した矩形そのもの。
//
// 信頼度が MinRegionConfidence に届かなければエラーを返す。**枠の位置はユーザーが
// 手で合わせたもの**なので、怪しい検出結果で勝手に動かさない。
func DetectRegion(img image.Image) (BoardRegion, error) {
	if img == nil {
		return BoardRegion{}, fmt.Errorf("ikkyoku/recognize: 画像がありません")
	}

	br := suteme.DetectBoard(img)
	if br == nil {
		return BoardRegion{}, fmt.Errorf("ikkyoku/recognize: 盤面を検出できませんでした")
	}
	conf := suteme.ValidateBoard(img, br)
	region := BoardRegion{Rect: br.Bounds, Confidence: conf}
	if conf < MinRegionConfidence {
		return region, fmt.Errorf("ikkyoku/recognize: 盤面らしい矩形が見つかりませんでした(信頼度 %.2f)", conf)
	}
	return region, nil
}
