package position

// 盤を 180 度回す。**取り込みの向き（撮った画像が後手目線だったとき）の受け皿。**
//
// ⚠️ **これは「表示視点」ではない。** 画面のどちら側から眺めるかは
// `<shogi-board>` の `flip` 属性の話で、SFEN は 1 文字も変わらない。
// こちらは**盤面そのものが上下逆に写っていた**という別の事実で、
// 盤・先後・駒台・手番をまとめて入れ替える。
//
// ⚠️ **取り込みでも訂正でも回さないこと**（AGENTS.md「認識も訂正も先後・手番を
// 意識しない」）。学習サンプルは（PNG・SFEN の盤面・盤面矩形）の 3 点組で、
// **ラベルは画素と一致していなければならない**。回すのは**確定した局面を
// 解析側へ渡す境界の 1 回だけ**で、元の Position は画像の向きのまま置いておく。

// Rotate180 は盤を 180 度回した**新しい盤**を返す（元は変えない）。
//
// マスの位置を入れ替えるだけでなく、**駒の先後も入れ替える**（回すと相手の駒に
// なるのが「向きが逆だった」ということそのもの）。成/不成は変わらない。
func (b *Board) Rotate180() *Board {
	out := NewBoard()
	for rank := 0; rank < 9; rank++ {
		for file := 0; file < 9; file++ {
			c := b.cells[rank][file]
			if c.filled {
				c.black = !c.black
			}
			out.cells[8-rank][8-file] = c
		}
	}
	return out
}

// Rotate180 は局面を 180 度回した**新しい局面**を返す（元は変えない）。
//
// 入れ替わるのは 4 つで、**どれか 1 つでも落とすと局面が壊れる**:
//
//   - 盤（マスの位置と駒の先後）
//   - 駒台の割り振り（先手の持ち駒 ⇄ 後手の持ち駒）
//   - 手番（先手番 ⇄ 後手番。**未決は未決のまま**。設計原則5）
//   - ……手数と HandsFixed は向きに関係しないのでそのまま
//
// ⚠️ **未決の駒台は回しても未決のまま。** 逆算（HandTotal）は盤上の枚数から出るので
// 回した盤でも同じ枚数になり、割り振っていないぶんは割り振っていないままになる。
// **ここで片側へ寄せないこと**（決めていないことを決めない）。
func (p *Position) Rotate180() *Position {
	c := p.Clone()
	c.Board = p.Board.Rotate180()
	c.handBlack, c.handWhite = c.handWhite, c.handBlack
	c.Turn = c.Turn.flip()
	return c
}

// Rotate180 は費用の表も同じ向きに回す（`Board.Rotate180` と対で使う）。
//
// ⚠️ **盤を回したら費用も回すこと。** 忘れると**別のマスの確信度で判断する**ことに
// なり、認識器が自信を持っていたマスを平気で覆す（しかも画面からは気づけない）。
// ⚠️ **中身は入れ替えない**（先後の区別が無い数なので、位置だけ移す）。
func (c *CellCost) Rotate180() *CellCost {
	if c == nil {
		return nil
	}
	out := &CellCost{}
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			out[8-r][8-f] = c[r][f]
		}
	}
	return out
}

// flip は手番を入れ替える。**未決は未決のまま**（決めていないことを決めない）。
func (t Turn) flip() Turn {
	switch t {
	case TurnBlack:
		return TurnWhite
	case TurnWhite:
		return TurnBlack
	}
	return TurnUnknown
}
