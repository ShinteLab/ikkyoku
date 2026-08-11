package position

// 「手を指して局面を進める」操作（Phase 5 の解析タブ）。
//
// ⚠️ **edit.go の操作とは別物。** あちらは訂正（合法性を問わない自由編集）で、
// こちらは**実際の対局と同じ進め方**:
//
//	            訂正（edit.go）              手を進める（ここ）
//	動かせる先   どこでも                     合法手だけ（判定は ikkyoku/legal）
//	手番         人が決める（未決でよい）      **1 手ごとに入れ替わる**
//	駒台         先後を人が割り振る            **取った駒が自動で載る**
//
// **合法性はここでは見ない。** 合法かどうかを言うのは `ikkyoku/legal`（engine）で、
// ここがやるのは「決まった手を盤に反映する」だけ。分けてあるのは、`position` が
// engine を知らずに済むから（依存が 1 か所に閉じる）。

import (
	"fmt"

	"github.com/ShinteLab/core/sfen"
	coreusi "github.com/ShinteLab/core/usi"
)

// ApplyMove は USI の 1 手を盤に反映する（"7g7f" / "7g7f+" / "P*5e"）。
//
// **取った駒は指した側の駒台に載る**（成駒は元の駒に戻る）。手番が入れ替わり、
// 手数が 1 進む。
//
// ⚠️ **合法性は見ない。** 呼ぶ前に `ikkyoku/legal.Moves` で確かめること。
// ここが見るのは「その手が盤の上で成立するか」（移動元に自分の駒があるか等）だけで、
// 王手放置も二歩も止めない（止める役はエンジン側にある）。
//
// ⚠️ **手番が未決の局面では指せない。** どちらが指したのかが決まらないと、
// 取った駒をどちらの駒台に載せるかも決まらない（設計原則5）。
func (p *Position) ApplyMove(move string) error {
	if p.Turn == TurnUnknown {
		return fmt.Errorf("ikkyoku/position: 手番が決まっていないので手を進められません")
	}
	mv, ok := coreusi.ParseMove(move)
	if !ok || mv.Resign {
		return fmt.Errorf("ikkyoku/position: 手を読めません: %s", move)
	}
	black := p.Turn == TurnBlack

	toRank, toFile, err := squareIndex(mv.ToX, mv.ToY)
	if err != nil {
		return err
	}

	if mv.Drop != 0 {
		if err := p.applyDrop(mv.Drop, toRank, toFile, black); err != nil {
			return err
		}
	} else if err := p.applyMove(mv, toRank, toFile, black); err != nil {
		return err
	}

	p.Turn = TurnBlack
	if black {
		p.Turn = TurnWhite
	}
	if p.MoveNumber > 0 {
		p.MoveNumber++
	}
	return nil
}

// applyDrop は駒台の駒を打つ。
func (p *Position) applyDrop(letter byte, toRank, toFile int, black bool) error {
	base, _ := sfen.ParsePieceLetter(letter)
	if base == sfen.NotFound {
		return fmt.Errorf("ikkyoku/position: 打つ駒が読めません: %c", letter)
	}
	to, err := p.Board.At(toRank, toFile)
	if err != nil {
		return err
	}
	if !to.IsEmpty() {
		return fmt.Errorf("ikkyoku/position: 打ち先に駒があります")
	}
	b, w := p.assigned(base)
	have := w
	if black {
		have = b
	}
	if have <= 0 {
		return fmt.Errorf("ikkyoku/position: %sの駒台に%sがありません",
			sideName(black), sfen.Name(base))
	}
	if err := p.Place(toRank, toFile, base, black, false); err != nil {
		return err
	}
	p.hand(black)[base] = have - 1
	return nil
}

// applyMove は盤上の駒を動かす（取ったら駒台へ）。
func (p *Position) applyMove(mv coreusi.Move, toRank, toFile int, black bool) error {
	fromRank, fromFile, err := squareIndex(mv.FromX, mv.FromY)
	if err != nil {
		return err
	}
	from, err := p.Board.At(fromRank, fromFile)
	if err != nil {
		return err
	}
	if from.IsEmpty() {
		return fmt.Errorf("ikkyoku/position: 移動元が空マスです")
	}
	if from.Black() != black {
		return fmt.Errorf("ikkyoku/position: %s番に%sの駒は動かせません",
			sideName(black), sideName(from.Black()))
	}
	to, err := p.Board.At(toRank, toFile)
	if err != nil {
		return err
	}
	if !to.IsEmpty() {
		if to.Black() == black {
			return fmt.Errorf("ikkyoku/position: 移動先に自分の駒があります")
		}
		if to.Piece() == sfen.King {
			return fmt.Errorf("ikkyoku/position: 玉は取れません")
		}
		// **成駒はベース駒に戻って駒台へ**（と金を取れば歩）。Cell がベース駒コードで
		// 持っているので自然にそうなる。
		p.hand(black)[to.Piece()]++
	}
	promoted := from.Promoted() || mv.Promote
	moved, err := NewCell(from.Piece(), black, promoted)
	if err != nil {
		return err
	}
	if err := p.Board.Set(toRank, toFile, moved); err != nil {
		return err
	}
	return p.Board.Set(fromRank, fromFile, Cell{})
}

// squareIndex は core/usi の内部座標を Board の (rank, file) にする。
// **rank=0 が一段目、file=0 が 9 筋**（core/sfen の並びそのもの）。
//
// ⚠️ **core/usi の x は「筋番号」ではない**（`FormatSquare` が `10-x` を書くので、
// 7 筋は x=3）。x=1 が 9 筋なので、file はそのまま x-1。
func squareIndex(x, y int) (rank, file int, err error) {
	if x < 1 || x > 9 || y < 1 || y > 9 {
		return 0, 0, fmt.Errorf("ikkyoku/position: 盤の外です: %d,%d", x, y)
	}
	return y - 1, x - 1, nil
}
