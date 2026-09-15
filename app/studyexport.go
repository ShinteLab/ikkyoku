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

	// ⚠️ **根が手合割の初期局面と一致していること**（2026-09-16）。
	//
	// **KIF は「手合割 ＋ 初手からの指し手」でしか局面を表せない**（`core/kifu`
	// は盤面図を書かない）。撮った中盤の局面を根にした検討をそのまま書き出すと、
	// **その局面を初期局面と取り違えた棋譜**ができる —— 開き直すと平手の初形に
	// 手順だけが乗った、**まったく別の対局**になる。
	//
	// ⚠️ **黙って平手として出さないこと。** 嘘の棋譜が棚に入るのが一番たちが悪い
	// （`TODO.md`「本譜のロック」が守りたいのはまさにそこ）。
	start, err := kifu.StartSFEN(handicap)
	if err != nil {
		return "", fmt.Errorf("手合割が分かりません（%s）: %w", handicap, err)
	}
	if root != start {
		return "", errStudyNotFromStart
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

// errStudyNotFromStart は**根が初期局面ではない**ので KIF にできないこと。
//
// ⚠️ **これは今のところ直しようがない**（`core/kifu` が盤面図を書かない）。
// 撮った中盤の局面から始めた検討は棚に入れられない —— **控え
// （`ikkyoku/studies/`）には今までどおり残る**ので、失うものは無い。
var errStudyNotFromStart = fmt.Errorf(
	"この検討は初期局面から始まっていないので、棋譜（KIF）にできません" +
		"（KIF は手合割と初手からの指し手でしか局面を表せません）。" +
		"検討そのものは控えてあるので、次の起動でも続きから開けます")

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
