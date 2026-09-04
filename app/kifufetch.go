package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ShinteLab/kicho/scrape"
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

// fetchKIF は URL から棋譜を取ってくる（解析タブの「棋譜の URL」と「再読み込み」）。
//
// **中身の解釈は `kicho/scrape` に寄せてある**（2026-09-04 に `ikkyoku/kifuweb` を
// 畳んだ）。文字コードの判別（`DecodeKIF`）・上限つきの読み取り（`ReadLimited`）・
// **HTML から .kif を辿る**（`LooksLikeHTML` / `KifURLFromHTML`）はどれも向こうの実装。
//
// ⚠️ **`kicho.Library` を経由していないのが要点。** あちらは棋譜データベースを
// 開けていないと使えないが、**URL から棋譜を読むのは棚に依らない操作**
// （設計原則3「段階的に劣化すること」）。DB が壊れていても貼った棋譜は解析できる。
//
// ⚠️ **HTML を辿れるようになったのは移行の副産物。** 連盟の中継の `source_url` は
// **中継ページ（HTML）の URL** なので、辿れないと棚から解析タブへ送った中継棋譜を
// 「再読み込み」で追えない（`ikkyoku/kifuweb` はここで断っていた）。
func fetchKIF(ctx context.Context, rawURL string) (kifuFetch, error) {
	u, err := parseKifuURL(rawURL)
	if err != nil {
		return kifuFetch{}, err
	}

	b, err := getLimited(ctx, u)
	if err != nil {
		return kifuFetch{}, err
	}
	// HTML が返ってきたら、そこに書かれた .kif を辿る。
	// **ページから対局内容を読み取るわけではない**（取り込むのは .kif の原本）。
	if scrape.LooksLikeHTML(b) {
		kifURL, err := scrape.KifURLFromHTML(u, b)
		if err != nil {
			return kifuFetch{}, err
		}
		if b, err = getLimited(ctx, kifURL); err != nil {
			return kifuFetch{}, err
		}
	}

	text, encoding, err := scrape.DecodeKIF(b)
	if err != nil {
		return kifuFetch{}, err
	}
	// ⚠️ **辿った先ではなく、指定された URL を返す**（上のコメント）。
	return kifuFetch{Text: text, Encoding: encoding, URL: u.String()}, nil
}

// parseKifuURL は入力を http/https の URL として読む。
//
// ⚠️ **http / https 以外を受けないこと。** `file://` を通すと、URL 欄から
// 手元のファイルを読めてしまう（ファイルの読み込みは口として別に作るもの）。
func parseKifuURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("URL の形式が正しくありません: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("http または https の URL を指定してください: %s", rawURL)
	}
	return u, nil
}

// getLimited は URL の中身を上限つきで読む。
//
// 上限は `scrape.MaxKifuBytes`（4MiB）。**棋譜 1 局は数十 KB** なので、
// これで足りないのは相手が棋譜を返していないとき。
func getLimited(ctx context.Context, u *url.URL) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("取得できませんでした: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("取得できませんでした（HTTP %d）: %s", resp.StatusCode, u)
	}
	return scrape.ReadLimited(resp.Body)
}
