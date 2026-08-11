// Package legal は「この局面で指せる手」を返す層（Phase 5 の「手を進める UI」用）。
//
// ⚠️ **合法手生成のためだけに `engine` を import する。** Phase 4 で解析を USI に
// 移して消したはずの依存がここで戻るが、**用途が違う**（手の検証であって解析ではない）。
// **解析をこちら経由に戻さないこと** —— 棋力の問題を設定の問題にした意味が無くなる
// （`analyze` は USI クライアントのまま）。
//
// **このパッケージは状態を持たない。** 渡された局面 1 つに対して答えるだけで、
// 履歴も前の局面も知らない（設計原則1）。手順を持つのは `position.Study`。
//
// **画像を知らない**（`position` と同じ側）。
package legal

import (
	"fmt"

	"github.com/ShinteLab/core/sfen"
	coreusi "github.com/ShinteLab/core/usi"
	shogi "github.com/ShinteLab/engine"
)

// Move は指せる手 1 つ。**盤の座標は position.Board と同じ数え方**
// （rank=0 が一段目、file=0 が 9 筋）なので、UI 側で読み替えないこと。
type Move struct {
	// USI は手文字列（"7g7f" / "7g7f+" / "P*5e"）。**エンジンに渡すのはこれ。**
	USI string `json:"usi"`
	// FromRank / FromFile は移動元。**打ちのときは -1。**
	FromRank int `json:"fromRank"`
	FromFile int `json:"fromFile"`
	// Drop は打つ駒のベース駒コード（`sfen.Pawn` など）。**移動のときは -1。**
	//
	// ⚠️ **駒台のどの駒を掴んだかを突き合わせるのはこの値。** 駒台の駒は
	// マスを持たないので、From では表せない。
	Drop int `json:"drop"`
	// ToRank / ToFile は移動先（打ち先）。
	ToRank int `json:"toRank"`
	ToFile int `json:"toFile"`
	// Promote は成るか。
	//
	// ⚠️ **同じ移動先に成りと不成の 2 つが並ぶことがある。** どちらを指すかは
	// 人が決めることなので、**片方に丸めないこと**（UI が聞く）。
	Promote bool `json:"promote"`
}

// Drops は打ちかを返す。
func (m Move) Drops() bool { return m.Drop >= 0 }

// Moves は局面 SFEN での合法手をすべて返す。
//
// positionSFEN は**局面全体の SFEN**（盤面・手番・持ち駒・手数）。
// `position.Position.SFEN()` が返すものをそのまま渡す。
//
// ⚠️ **玉の欠けた局面では合法手を出せない**（`engine` の合法手生成が玉の位置を
// 前提にしている）。詰将棋のような「玉が 1 枚しかない局面」は**確定できるのが
// 訂正 UI の要件**なので、ここで返すのはエラーであって、盤を見ることも訂正することも
// 止めない（設計原則3・4。`analyze.ensurePlayable` と同じ線引き）。
func Moves(positionSFEN string) (out []Move, err error) {
	// ⚠️ **panic を握る。** 想定外の局面（訂正 UI は不正な局面も確定できる）で
	// 合法手生成が落ちても、**アプリごと道連れにしないこと**（設計原則3）。
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("この局面の合法手を出せませんでした: %v", r)
		}
	}()

	b, err := shogi.NewBoard("sfen " + positionSFEN)
	if err != nil {
		return nil, fmt.Errorf("この局面を読めませんでした: %w", err)
	}

	cands := b.Candidate()
	out = make([]Move, 0, len(cands))
	for _, a := range cands {
		if a == nil {
			continue
		}
		m, ok := parse(a.String())
		if !ok {
			continue // 読めない手は落とす（出せる手が減るだけで、嘘は出さない）
		}
		out = append(out, m)
	}
	return out, nil
}

// parse は USI の手文字列を Move にする。**座標の読み替えはここ 1 か所。**
func parse(text string) (Move, bool) {
	mv, ok := coreusi.ParseMove(text)
	if !ok || mv.Resign {
		return Move{}, false
	}
	m := Move{
		USI:      text,
		FromRank: -1,
		FromFile: -1,
		Drop:     -1,
		ToRank:   rankOf(mv.ToY),
		ToFile:   fileOf(mv.ToX),
		Promote:  mv.Promote,
	}
	if m.ToRank < 0 || m.ToFile < 0 {
		return Move{}, false
	}
	if mv.Drop != 0 {
		base, _ := sfen.ParsePieceLetter(mv.Drop)
		if base == sfen.NotFound {
			return Move{}, false
		}
		m.Drop = base
		return m, true
	}
	m.FromRank, m.FromFile = rankOf(mv.FromY), fileOf(mv.FromX)
	if m.FromRank < 0 || m.FromFile < 0 {
		return Move{}, false
	}
	return m, true
}

// rankOf は USI の段（1..9。a=1）を position.Board の rank（0 が一段目）にする。
func rankOf(y int) int {
	if y < 1 || y > 9 {
		return -1
	}
	return y - 1
}

// fileOf は core/usi の内部 x を position.Board の file（0 が 9 筋）にする。
//
// ⚠️ **core/usi の x は「筋番号」ではない**（`FormatSquare` が `10-x` を書くので、
// 7 筋は x=3）。x=1 が 9 筋で、core/sfen の file の数え方とそのまま一致する。
func fileOf(x int) int {
	if x < 1 || x > 9 {
		return -1
	}
	return x - 1
}

// USI は position.Board の座標を USI のマス文字列にする。
// **UI から来た「どこからどこへ」を手文字列に組み立てるのに使う。**
func USI(fromRank, fromFile, toRank, toFile int, promote bool) (string, error) {
	from, err := square(fromRank, fromFile)
	if err != nil {
		return "", err
	}
	to, err := square(toRank, toFile)
	if err != nil {
		return "", err
	}
	if promote {
		return from + to + "+", nil
	}
	return from + to, nil
}

// DropUSI は「駒台の駒をこのマスに打つ」手文字列を組み立てる。
func DropUSI(piece, toRank, toFile int) (string, error) {
	letter := sfen.Letter(piece)
	if letter == "None" {
		return "", fmt.Errorf("ikkyoku/legal: 駒コードが不正です: %d", piece)
	}
	to, err := square(toRank, toFile)
	if err != nil {
		return "", err
	}
	return letter + "*" + to, nil
}

func square(rank, file int) (string, error) {
	if rank < 0 || rank > 8 || file < 0 || file > 8 {
		return "", fmt.Errorf("ikkyoku/legal: 盤の外です: rank=%d file=%d", rank, file)
	}
	return coreusi.FormatSquare(file+1, rank+1), nil
}
