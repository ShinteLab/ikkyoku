package app

// 検討を KIF に書き出す（2026-09-16。Step 3）。
//
// **撮った 1 局面から始めた検討にも「棚のどの棋譜か」を持たせるための道。**
// 棚に入れれば `GameID` が付き、次からは棋譜タブから前の検討の続きを開ける
// （Step 2 の口にそのまま乗る）。
//
// ⚠️ **書き出すのは本譜だけ。** `core/kifu` はまだ木を持っていない
// （`変化：N手` を読み飛ばす。ワークスペースの `TODO.md` 1）ので、
// **枝は KIF に出ない。** ⚠️ **枝が消えるわけではない** —— 木は控え
// （`ikkyoku/studies/`）にそのまま残り、棋譜タブから開けば戻る。
//
// **撮った中盤の局面が根でも書ける**（2026-09-16。`core/kifu` に盤面図が入った）。
// ⚠️ **手数は戻らない** —— 盤面図には手数の欄が無いので、開き直すと 1 手目から
// 数え直しになる（**局面と手順は正しい**）。
//
// ⚠️ **KIF の組み立てを ikkyoku に書かないこと。** ヘッダの順も指し手の桁も
// `core/kifu` の担当で、ここがやるのは**木から手を並べて渡す**ところまで。

import (
	"fmt"
	"strings"

	"github.com/ShinteLab/core/kifu"
)

// exportKIF は今の検討の**本譜**を KIF にする。
//
// ⚠️ **公開しない**（Service の公開メソッドはフロントの API になる）。
// 棚へ入れる口は `KifuService.SaveStudy` の 1 つだけにする。
func (s *StudyService) exportKIF() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return "", fmt.Errorf("まだ局面がありません")
	}
	root, err := s.study.Root().SFEN()
	if err != nil {
		return "", fmt.Errorf("この局面は棋譜にできません: %w", err)
	}

	doc := s.game
	// ⚠️ **手合割は KIF の側の言い方に揃える**（空なら平手）。`core/kifu` が
	// 別名を吸収するので、**ここで名前の表を持たないこと。**
	handicap := strings.TrimSpace(doc.Handicap)

	// ⚠️ **根が手合割の初期局面でないなら盤面図で書く**（2026-09-16）。
	//
	// **KIF は本来「手合割 ＋ 初手からの指し手」でしか局面を表せない。** 撮った
	// 中盤の局面を根にした検討をそのまま手合割として書き出すと、**その局面を
	// 初期局面と取り違えた棋譜**になる —— 開き直すと平手の初形に手順だけが
	// 乗った、**まったく別の対局**。**黙って平手として出さないこと。**
	//
	// ⚠️ **両方は書かない**（`kifu.Document.String` が盤面図を優先する）。
	// 並べると、読み手によって別の局面になる。
	if start, err := kifu.StartSFEN(handicap); err != nil || root != start {
		doc.Start = root
	}

	moves := s.study.MainLine()
	texts, err := kifu.FormatMoves(root, moves)
	if err != nil {
		return "", fmt.Errorf("指し手を棋譜の表記にできませんでした: %w", err)
	}
	doc.Moves = make([]kifu.Move, 0, len(texts))
	for i, t := range texts {
		// ⚠️ **表記にできなかった手があったら書き出さないこと。** 名前が空のまま
		// 並べると**手数だけ合っていて中身の無い棋譜**ができ、棚に入ってから
		// 気づくことになる（設計原則3 は「黙って壊れたものを出す」ことではない）。
		if !t.OK || t.Name == "" {
			return "", fmt.Errorf("%d手目（%s）を棋譜の表記にできませんでした", i+1, t.USI)
		}
		doc.Moves = append(doc.Moves, kifu.Move{
			Num: i + 1, Name: t.Name, FromX: t.FromX, FromY: t.FromY,
		})
	}
	// ⚠️ **消費時間は出さない**（`ShowTime` を立てない）。この検討はどこからも
	// 時間を受け取っていないので、立てると **" ( 0:00/00:00:00)" が全手に並ぶ。**
	doc.ShowTime = false
	return doc.String(), nil
}

// setGameID は棚に入れた棋譜と結ぶ（`KifuService.SaveStudy` から）。
//
// ⚠️ **公開しない** —— フロントから任意の id を紐付けられると、
// **別の棋譜の控えを上書きできてしまう。**
func (s *StudyService) setGameID(id string) StudyState {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gameID = strings.TrimSpace(id)
	// ⚠️ **`changed()` を通すこと** —— 控えを書き直させる（`markDirty`）のと、
	// 別の窓に知らせるのを兼ねている。
	return s.changed()
}
