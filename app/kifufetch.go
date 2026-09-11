package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ShinteLab/kicho"
)

// kifuFetch は URL から取ってきた棋譜。
type kifuFetch struct {
	// Text は KIF 本文（UTF-8 に寄せてある）。
	Text string
	// Encoding は元の文字コード（"utf-8" / "shift_jis"）。**画面に出す。**
	//
	// URL を打ち間違えても「棋譜が読めません」としか出ないことがあるので、
	// **取れた側の事実**（どこから・何の文字コードで）を見せる。
	Encoding string
	// URL は**指定された URL そのもの**。
	//
	// ⚠️ **HTML を辿ったときも .kif に書き換えないこと**（kicho の `source_url` と
	// 同じ扱い）。中継ページの `KIF_FILE_NAME` が一次情報で、**.kif のパスは
	// 変わりうる**ので、取り直すときも人が貼ったページのほうから辿り直すのが正しい。
	URL string
}

// kifuFetcher は URL から棋譜を取ってくる側（**DB を持たない**）。
//
// ⚠️ **`kicho.Library` ではなく `kicho.NewFetcher()` を使うのが要点。**
// あちらは棚（SQLite）を開けていないと作れないが、**URL から棋譜を読むのは
// 棚に依らない操作**（設計原則3）。DB が壊れていても貼った URL は解析できる。
//
// ⚠️ **1 つを使い回してよい**（持っているのは HTTP クライアントだけ。読売の
// ペイロードを評価する goja の VM は 1 回ごとに作られる）。
var kifuFetcher = kicho.NewFetcher()

// fetchKIF は URL から棋譜を取ってくる（解析タブの「棋譜の URL」と「再読み込み」）。
//
// **取得元の判別も中身の解釈も `kicho.Fetcher.Fetch` に寄せてある**（2026-09-12）。
// 連盟の中継・読売（竜王戦）・それ以外のサイトの .kif を、**呼び出し側が
// 見分けずに**同じ 1 本で扱える。
//
// ⚠️ **ここに「このサイトならこちら」を書かないこと。** 取得元の知識は kicho
// （親 `CLAUDE.md`）。以前はこの関数が .kif を読むだけだったので、**読売の URL は
// 通らず、画面は「棋譜の URL」と「中継から取得」の 2 つの入力欄を持っていた。**
//
// ⚠️ **`kicho.Library` を経由していないのが要点**（上の `kifuFetcher`）。
//
// ⚠️ **HTML から .kif を辿れるのは今までどおり**（`Fetcher` の中）。連盟の中継の
// `source_url` は中継ページ（HTML）の URL なので、辿れないと棚から解析タブへ
// 送った中継棋譜を「再読み込み」で追えない。
func fetchKIF(ctx context.Context, rawURL string) (kifuFetch, error) {
	in := strings.TrimSpace(rawURL)
	// ⚠️ **http / https 以外のスキームを弾くこと。** `file://` を通すと URL 欄から
	// 手元のファイルを読めてしまう（ファイルの読み込みは別の口として作るもの）。
	// ⚠️ **スキームが無い入力は弾かない** —— 棋譜 ID として扱える（kicho が判別する）。
	if i := strings.Index(in, "://"); i >= 0 {
		switch strings.ToLower(in[:i]) {
		case "http", "https":
		default:
			return kifuFetch{}, fmt.Errorf("http または https の URL を指定してください: %s", rawURL)
		}
	}

	got, err := kifuFetcher.Fetch(ctx, in)
	if err != nil {
		return kifuFetch{}, err
	}
	if got.Empty() {
		return kifuFetch{}, fmt.Errorf("棋譜が空でした: %s", rawURL)
	}
	// ⚠️ **辿った先ではなく、指定された URL を返す**（`URL` のコメント）。
	return kifuFetch{Text: got.KIF, Encoding: got.Encoding, URL: in}, nil
}
