package recognize

// 「マスごとの読みがどれだけ確かか」を 81 個の数にする層（2026-10-11）。
//
// 中継を追うときの費用表（どのマスなら認識を覆してよいか）の元になる。⚠️ **ここは suteme が返した
// 観測値を読み替えるだけ**（認識器を書く場所ではない）。下限（覆すのにかかる最低の費用）を決めるのは
// 使う側（app の `minCellCost`）。

// CellConfidence は認識結果 d の**マスごとの読みの確かさ**（0〜1）を 81 個・行優先で返す。d が無い・マスが無ければ nil。
//
//   - **駒と読んだマス**: suteme の推論の確信度（`CellDebug.Confidence`）
//   - **空きと読んだマス**: `1 − 被覆率 ÷ 空きの線`（`CellDebug.Cover` / `Debug.EmptyCover`）。被覆率が空きの線から
//     遠いほど確か。⚠️ **測れなければ 0**（`Cover` が -1 —— 直前の手の色付けなど）
//
// ⚠️ **空きに確信度 0 を置かないこと**（2026-10-11 に決め直した）。suteme は空きを推論しないので確信度 0 で返し、
// 以前はそれを下限の 0.15 で数えていた。はっきり空いて写ったマスも安く覆せたので、中継で 3七・3三 の空きを覆した
// 偽の手順が通った（2026-10-10）。suteme の正解付き 239 局面で、空きと読んだ 11,134 マスのうち本当は駒だったのは
// 6 マスで、被覆率が空きの線に近いほど多い（`被覆率 ÷ 空きの線` が 0.4 未満で 10,576 マス中 1、0.6 以上で 138 マス中 3）。
// 線に近い空きは今までどおり安く覆せる。
func CellConfidence(d *Debug) []float64 {
	if d == nil || len(d.Cells) == 0 {
		return nil
	}
	out := make([]float64, 81)
	for _, c := range d.Cells {
		if c.Row < 0 || c.Row >= 9 || c.Col < 0 || c.Col >= 9 {
			continue
		}
		v := c.Confidence
		if c.Piece == "" {
			v = emptyConfidence(c.Cover, d.EmptyCover)
		}
		out[c.Row*9+c.Col] = v
	}
	return out
}

// emptyConfidence は空きと読んだマスの確かさ（被覆率 cover・空きの線 line）。測れなければ 0。
func emptyConfidence(cover, line float64) float64 {
	if cover < 0 || line <= 0 {
		return 0
	}
	return min(max(1-cover/line, 0), 1)
}
