package position

// KIF テキストから検討（根 + 手順）を組み立てる入口（Phase 5 の「棋譜を貼り付ける」）。
//
// ⚠️ **画像を知らない層のまま。** 貼り付けた棋譜には画像も座標系も無いので、
// `recognize` を通らずに直接ここへ入る。訂正タブを経由しないのは、
// **棋譜の局面は既に確定している**（初期局面 + 手順で一意に決まる）から。
//
// **KIF の読み取りも、指し手 → USI の変換も `core/kifu`。** ここに書かないこと
// （将棋の**仕様**は core に一本化する）。ここがやるのは
// 「読めた手を 1 手ずつ Study に指させる」というアプリ側の組み立てだけ。

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/core/sfen"
)

// KIFLoad は KIF を読み込んだ結果の内訳（何手載ったか・どこで止まったか）。
type KIFLoad struct {
	// Game は対局の素性（対局者・棋戦・日時）。**ikkyoku で定義し直さない**
	// （`Game` は `core/kifu.Document` のエイリアス）。
	Game Game
	// RootSFEN は初期局面（手合割から決まる）。
	RootSFEN string
	// Total は KIF に載っていた指し手の数（投了などの終局印は数えない）。
	Total int
	// Loaded は実際に盤へ進められた手数。**Total より少ないことがある。**
	Loaded int
	// Note は全部は載らなかった理由（載ったなら空）。
	//
	// ⚠️ **これはエラーではない**（設計原則3）。途中まででも並べたほうが使えるので、
	// 読めなくなったところで止めて、そこまでの手順を持った Study を返す。
	Note string
}

// FromKIF は KIF テキストを読んで検討を組み立てる。
//
// 手順は**合法手として 1 手ずつ指す**（`Study.Play`）ので、棋譜が壊れていれば
// そこで止まる。⚠️ **止まってもエラーにしない** —— そこまでの手順は正しいので、
// 読めたところまでの局面を解析できるほうがよい（設計原則3）。理由は `Note` に出す。
//
// error を返すのは「棋譜として 1 手も成立しなかった」ときだけ。
func FromKIF(text string) (*Study, KIFLoad, error) {
	doc, err := kifu.Parse(text)
	if err != nil {
		return nil, KIFLoad{}, err
	}
	start, err := doc.StartSFEN()
	if err != nil {
		return nil, KIFLoad{}, err
	}
	root, err := FromFullSFEN(start)
	if err != nil {
		return nil, KIFLoad{}, err
	}

	load := KIFLoad{Game: Game(doc), RootSFEN: start}
	// ⚠️ **変換に失敗しても、そこまでの手は返ってくる。** 理由は Note に回す。
	moves, decodeErr := kifu.DecodeMoves(doc.Moves)
	load.Total = len(moves)
	if decodeErr != nil {
		load.Note = decodeErr.Error()
	}

	study := NewStudy(root)
	for i, mv := range moves {
		// ⚠️ **`Play` を使わないこと**（2026-08-14）。あちらは「人が盤で指した」
		// 印が付く。**棋譜の手は実際に現れた指し手**なので、自分で試しに指した手と
		// 同じ印を付けると、手順リストでどちらか分からなくなる。
		if err := study.play(mv, mark{}); err != nil {
			name := mv
			if i < len(doc.Moves) {
				name = doc.Moves[i].Name
			}
			load.Note = fmt.Sprintf("%d手目「%s」で止まりました: %v", i+1, name, err)
			break
		}
		load.Loaded++
	}
	return study, load, nil
}

// FromFullSFEN は完全形の SFEN（盤面・手番・持ち駒・手数）から局面を作る。
//
// ⚠️ **FromBoardSFEN とは別物。** あちらは認識結果（盤面だけ）の入口で、手番も
// 駒台の割り振りも未決のまま作る。こちらは**全部書いてある局面**を読むので、
// 手番も持ち駒も決まった状態で返る。
//
// 手番の欄が無ければ先手番、持ち駒が "-" なら駒台は空、手数が無ければ 1。
func FromFullSFEN(s string) (*Position, error) {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) == 0 {
		return nil, fmt.Errorf("ikkyoku/position: SFEN が空です")
	}
	p, err := FromBoardSFEN(fields[0])
	if err != nil {
		return nil, err
	}

	// ⚠️ **駒台は書いてあるとおりに読む**（逆算しない）。駒落ちは盤にも駒台にも
	// 無い駒がある局面なので、逆算すると未決が消えず SFEN() が組み上がらない。
	p.HandsFixed = true

	p.Turn = TurnBlack
	if len(fields) >= 2 && fields[1] == "w" {
		p.Turn = TurnWhite
	}
	if len(fields) >= 3 {
		if err := applyHands(p, fields[2]); err != nil {
			return nil, err
		}
	}
	p.MoveNumber = 1
	if len(fields) >= 4 {
		if n, err := strconv.Atoi(fields[3]); err == nil && n > 0 {
			p.MoveNumber = n
		}
	}
	return p, nil
}

// applyHands は SFEN の持ち駒欄（"S2Pb3p" / "-"）を駒台に割り振る。
func applyHands(p *Position, field string) error {
	if field == "-" {
		return nil
	}
	// 先に数え上げてから割り振る（同じ駒種が 2 度出ても足し込めるように）。
	tally := map[bool]map[int]int{true: {}, false: {}}
	count := 0
	for i := 0; i < len(field); i++ {
		c := field[i]
		if c >= '0' && c <= '9' {
			count = count*10 + int(c-'0')
			continue
		}
		base, black := sfen.ParsePieceLetter(c)
		if base == sfen.NotFound {
			return fmt.Errorf("ikkyoku/position: 持ち駒が読めません: %q", field)
		}
		n := count
		if n == 0 {
			n = 1
		}
		count = 0
		tally[black][base] += n
	}
	for black, m := range tally {
		for base, n := range m {
			if err := p.SetHand(base, black, n); err != nil {
				return err
			}
		}
	}
	return nil
}
