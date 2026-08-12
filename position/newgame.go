package position

// 何もないところから対局を始める入口（入力タブの「新しく対局を始める」）。
//
// ⚠️ **画像を知らない層のまま。** 新規対局には画像も認識結果も無いので、
// `recognize` を通らずに直接ここへ入る。訂正タブを経由しないのも `FromKIF` と同じで、
// **初期局面は手合割で一意に決まる**（直すものが無い）。
//
// ⚠️ **初期局面の定義を ikkyoku に持たない。** 手合割 → 盤面は将棋の**仕様**なので
// `core/kifu.StartSFEN` の 1 か所だけが持つ（KIF を読むときと同じ表を引くので、
// 「棋譜から読んだ平手」と「新規で作った平手」が食い違わない）。
// **ここに盤面文字列を書き写さないこと。**

import (
	"fmt"
	"strings"

	"github.com/ShinteLab/core/kifu"
)

// Hirate は平手の手合割名。**呼び出し側が "平手" と書かないため**の名前
// （表記が揺れると `core/kifu` の表を引けない）。
const Hirate = kifu.HirateHandicap

// NewGame は手合割から新しい対局を作る（根＝初期局面・手順は空）。
//
// **手合割の名前で受けるのが要点。** 今のところ UI に出しているのは平手だけだが、
// 駒落ちは `core/kifu` が既に全部持っている（香落ち〜十枚落ち）ので、
// **ここは 1 行も変えずに増やせる**。詰将棋だけは初期局面が無い（人が並べる）ので、
// 別の入口になる —— そちらは訂正タブ側の仕事で、ここには入らない。
//
// ⚠️ **知らない手合割を平手に倒さないこと**（`core/kifu.StartSFEN` もそうしている）。
// 別の初期配置で始めた対局は、そこから先の手が全部ずれる。
//
// ⚠️ **駒落ちは上手（後手）が初手を指す**ので、返る局面の手番は後手番になる
// （`w` + 手数 1 は正当な局面）。手番を先手に直さないこと。
func NewGame(handicap string) (*Study, error) {
	name := strings.TrimSpace(handicap)
	if name == "" {
		name = Hirate
	}
	start, err := kifu.StartSFEN(name)
	if err != nil {
		return nil, err
	}
	// ⚠️ **FromFullSFEN で読む**（FromBoardSFEN ではない）。持ち駒まで書いてある
	// 局面なので、駒台は書いてあるとおりに読む（`HandsFixed`）。駒落ちは盤にも
	// 駒台にも無い駒がある局面なので、逆算すると未決が消えず SFEN が組み上がらない。
	root, err := FromFullSFEN(start)
	if err != nil {
		return nil, fmt.Errorf("ikkyoku/position: 初期局面が読めません(%s): %w", name, err)
	}
	return NewStudy(root), nil
}
