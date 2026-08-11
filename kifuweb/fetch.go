// Package kifuweb は棋譜（.kif）を URL から取ってくる。
//
// **やるのは「取る」と「文字コードを UTF-8 に直す」の 2 つだけ。**
// KIF の解釈は `core/kifu`、局面の組み立ては `ikkyoku/position` の仕事で、
// ここには書かない。
//
// ⚠️ **kicho の `scrape` にほぼ同じものがある**（`DecodeKIF` / `ReadLimited`）。
// **依存の向き（ikkyoku → core / suteme / engine）に kicho は入っていない**ので
// 共有できず、必要なぶんだけこちらに持っている。**3 つめの利用側が出たら
// core へ寄せること**（そのときは kicho も向き先を変える）。
//
// ⚠️ **中継ページ（HTML）から .kif を辿る処理は持たない**（kicho にはある）。
// ここが受けるのは **.kif の URL そのもの**で、HTML が返ってきたら
// 「棋譜ではない」と言って断る。曖昧に推測して別のものを読み込むより、
// どこを直せばよいかが分かるほうがよい。
package kifuweb

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// MaxBytes は 1 回の取得で読む上限。棋譜 1 局は大きくても数十 KB なので、
// 誤って巨大なファイルを掴んだときの保険。
const MaxBytes = 4 << 20 // 4MiB

// 文字コードの名前（画面に「何として読んだか」を出すため）。
const (
	EncodingUTF8     = "utf-8"
	EncodingShiftJIS = "shift_jis"
)

// Result は取得した棋譜。
type Result struct {
	// Text は UTF-8 に直した本文。
	Text string
	// Encoding は元の文字コード（"utf-8" / "shift_jis"）。
	Encoding string
	// URL は実際に取りに行った URL。
	URL string
}

// Fetch は URL から棋譜を取ってくる。
//
// 打ち切りは ctx で行う（呼び出し側がタイムアウトを決める）。
func Fetch(ctx context.Context, rawURL string) (Result, error) {
	target := strings.TrimSpace(rawURL)
	if target == "" {
		return Result{}, fmt.Errorf("URL が空です")
	}
	u, err := url.Parse(target)
	if err != nil {
		return Result{}, fmt.Errorf("URL を読めません: %w", err)
	}
	// ⚠️ **http / https 以外は受けない。** file:// を通すと、URL 欄に打った
	// パスで手元のファイルを読めてしまう（読み込みの口としては別に作るべきもの）。
	if u.Scheme != "http" && u.Scheme != "https" {
		return Result{}, fmt.Errorf("http / https の URL を指定してください: %s", target)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Result{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("取得に失敗しました: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("取得に失敗しました(HTTP %d): %s", resp.StatusCode, target)
	}

	b, err := readLimited(resp.Body)
	if err != nil {
		return Result{}, err
	}
	text, enc, err := Decode(b)
	if err != nil {
		return Result{}, err
	}
	if looksLikeHTML(text) {
		return Result{}, fmt.Errorf(
			"棋譜ではなく HTML が返りました。.kif ファイルの URL を指定してください: %s", target)
	}
	return Result{Text: text, Encoding: enc, URL: target}, nil
}

// Decode は棋譜のバイト列を UTF-8 文字列にして、元の文字コード名を返す。
//
// **世に出回っている .kif は Shift_JIS が多い**（Kifu for Windows 等の既定。
// 日本将棋連盟の棋譜中継もそう）。UTF-8 として妥当ならそのまま、
// そうでなければ Shift_JIS として解釈する。
func Decode(b []byte) (text, encoding string, err error) {
	if utf8.Valid(b) {
		return string(b), EncodingUTF8, nil
	}
	out, _, err := transform.Bytes(japanese.ShiftJIS.NewDecoder(), b)
	if err != nil {
		return "", "", fmt.Errorf(
			"文字コードを判別できませんでした(UTF-8 でも Shift_JIS でもありません): %w", err)
	}
	return string(out), EncodingShiftJIS, nil
}

// readLimited は本文を MaxBytes まで読む。
func readLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("読み込みに失敗しました: %w", err)
	}
	if len(b) > MaxBytes {
		return nil, fmt.Errorf("内容が大きすぎます(%d バイト超)", MaxBytes)
	}
	return b, nil
}

// looksLikeHTML は中身が HTML かを見る（中継ページの URL を渡したときの判別）。
func looksLikeHTML(text string) bool {
	head := strings.ToLower(strings.TrimSpace(text))
	if len(head) > 512 {
		head = head[:512]
	}
	return strings.HasPrefix(head, "<!doctype html") ||
		strings.HasPrefix(head, "<html") ||
		strings.Contains(head, "<head")
}
