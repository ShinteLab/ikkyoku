package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/kicho"
)

// 棋譜ファイルを開く（2026-11-15。入力タブの「ファイルを開く」）。
//
// **開いたファイルは貼り付け欄に入るだけ。** 行き先（「解析する」→ 解析タブ /
// 「棋譜に登録する」→ 棚）は貼り付けと同じ 2 つで、**ファイル専用の行き先を作らない**
// （入力欄を 2 つに増やさないのと同じ理由。同じ入力の入り方が 1 つ増えただけ）。
//
// ⚠️ **棚が開けていなくても開けること**（設計原則3）。読むのは手元のファイルで、
// 棚（kicho の DB）には触らない。`KifuService` に置いてあるのはダイアログと
// 「登録するときに元の文字コードを残す」の受け渡しのためで、`library()` は呼ばない。
//
// ⚠️ **文字コードは中身から判別する**（`kicho.DecodeKIF`。URL と同じ判定）。
// .kif は Shift_JIS のことが多く、UTF-8 決め打ちにすると漢数字が壊れて 1 手も読めない。

// kifuFileMaxBytes は開く棋譜ファイルの上限（URL の取得と同じ 4MiB。kicho の `scrape.MaxKifuBytes`）。
// 棋譜 1 局は大きくても数十 KB なので、誤って巨大なファイルを選んだときに貼り付け欄を固めないための保険。
const kifuFileMaxBytes = 4 << 20

// KifuFile は開いた棋譜ファイルの中身。
type KifuFile struct {
	// Name はファイル名（パスではない）。**画面に出す**（何を開いたか）。
	Name string `json:"name"`
	// Text は本文（UTF-8 に寄せてある）。空なら取り消した。
	Text string `json:"text"`
	// Encoding は元の文字コード（"utf-8" / "shift_jis"）。**画面に出す。**
	Encoding string `json:"encoding"`
}

// openedKifu は最後に開いたファイル（登録するときに元の文字コードを残すため）。
type openedKifu struct {
	text     string // 改行を "\n" に揃え、前後の空白を落としたもの（`sameKifuText`）
	encoding string
}

// OpenKifuFile は棋譜ファイル（KIF / CSA）を選んで読む。**取り消したら空の KifuFile を返す**（エラーではない）。
//
// 読んだ本文と元の文字コードを覚えておき、**同じ本文のまま「棋譜に登録する」が
// 押されたら、その文字コードで記録する**（`ImportKIF`）。⚠️ **文字コードを画面に
// 往復させないこと** —— 画面の値を信じる道を作ると、書き換えた本文に元の文字コードが付きうる。
func (s *KifuService) OpenKifuFile() (KifuFile, error) {
	if s.PickFile == nil {
		return KifuFile{}, fmt.Errorf("ダイアログを開けません")
	}
	s.mu.Lock()
	start := s.lastKifuDir
	s.mu.Unlock()
	path, err := s.PickFile("棋譜ファイルを選ぶ", start,
		"棋譜 (*.kif, *.kifu, *.csa)", "*.kif;*.kifu;*.csa",
		"すべてのファイル", "*.*")
	if err != nil {
		return KifuFile{}, fmt.Errorf("ファイルを選べませんでした: %w", err)
	}
	if path == "" {
		return KifuFile{}, nil // 取り消し
	}
	return s.adoptKifuFile(path, "開きました")
}

// KifuDropTarget は棋譜ファイルを落とせる場所（入力タブの「棋譜を貼り付ける」の群）の要素 ID（2026-11-15）。
//
// ⚠️ **画面の `data-file-drop-target` を付けた要素の id と揃えること**（`inputtab.ts` の `#kifu-drop`）。
// Wails は落ちた要素の id を一緒に渡してくるので、`_cmd/ikkyoku` はこれと同じときだけ `DropKifuFiles` を呼ぶ。
const KifuDropTarget = "kifu-drop"

// KifuDroppedEvent は落とされた棋譜ファイルを画面へ届けるイベントの名前（中身は `KifuDrop`）。
const KifuDroppedEvent = "kifu:dropped"

// KifuDrop は落とされた棋譜ファイル（`kifu:dropped` で画面へ届く）。
type KifuDrop struct {
	KifuFile
	// Error は読めなかった理由（読めたなら空）。
	Error string `json:"error"`
	// Skipped は読まなかったファイルの数（**読むのは最初の 1 つだけ**）。
	Skipped int `json:"skipped"`
}

// DropKifuFiles は落とされた棋譜ファイルを読む（2026-11-15。入力タブの「棋譜を貼り付ける」へのドラッグ＆ドロップ）。
//
// **「ファイルを開く…」で選んだのと同じ扱い**（中身を貼り付け欄に入れるだけ・開いた本文のまま
// 登録したら元の文字コードを残す）。⚠️ **読むのは最初の 1 つだけ** —— 貼り付け欄は 1 つなので、
// 2 つ目以降は読まずに数だけ返す（画面が「1 つだけ読みました」と出す）。
//
// ⚠️ **`//wails:ignore` を外さないこと。** `_cmd/ikkyoku` がドロップのイベントから呼ぶ口で、
// フロントの API ではない（外すと画面から任意のパスを読めてしまう）。
//
//wails:ignore
func (s *KifuService) DropKifuFiles(paths []string) KifuDrop {
	if len(paths) == 0 {
		return KifuDrop{Error: "ファイルがありません"}
	}
	got, err := s.adoptKifuFile(paths[0], "落とされたので読みました")
	drop := KifuDrop{KifuFile: got, Skipped: len(paths) - 1}
	if err != nil {
		drop.Error = err.Error()
	}
	return drop
}

// adoptKifuFile は棋譜ファイルを読み、**登録するときに元の文字コードを残せるよう覚える**
// （「ファイルを開く…」とドロップの合流点。ここから先はどちらから来たかで変えない）。
func (s *KifuService) adoptKifuFile(path, how string) (KifuFile, error) {
	got, err := readKifuFile(path)
	if err != nil {
		return KifuFile{}, err
	}
	s.mu.Lock()
	s.lastKifuDir = filepath.Dir(path)
	s.opened = openedKifu{text: normalizeKifuText(got.Text), encoding: got.Encoding}
	s.mu.Unlock()
	log.Info("棋譜ファイルを"+how, "path", path, "encoding", got.Encoding, "bytes", len(got.Text))
	return got, nil
}

// readKifuFile は棋譜ファイルを読んで UTF-8 に寄せる（`OpenKifuFile` の中身）。
func readKifuFile(path string) (KifuFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return KifuFile{}, fmt.Errorf("ファイルを開けません: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, kifuFileMaxBytes+1))
	if err != nil {
		return KifuFile{}, fmt.Errorf("ファイルを読めません: %w", err)
	}
	if len(b) > kifuFileMaxBytes {
		return KifuFile{}, fmt.Errorf("ファイルが大きすぎます（%d MiB まで）: %s", kifuFileMaxBytes>>20, filepath.Base(path))
	}
	text, encoding, err := kicho.DecodeKIF(b)
	if err != nil {
		return KifuFile{}, fmt.Errorf("文字コードを判別できません: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return KifuFile{}, fmt.Errorf("ファイルが空です: %s", filepath.Base(path))
	}
	return KifuFile{Name: filepath.Base(path), Text: text, Encoding: encoding}, nil
}

// encodingFor は登録する本文の元の文字コードを返す。**最後に開いたファイルと同じ本文のときだけ**
// その文字コードで、書き換えた・貼り付けた本文は utf-8（貼り付けと同じ）。
func (s *KifuService) encodingFor(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opened.encoding != "" && s.opened.text == normalizeKifuText(text) {
		return s.opened.encoding
	}
	return kicho.EncodingUTF8
}

// normalizeKifuText は本文を比べられる形にする。
//
// ⚠️ **改行を揃えること** —— 画面の入力欄（textarea）は CRLF を LF にして返すので、
// CRLF のファイルを開いてそのまま登録しても、揃えないと「書き換えた」と見なしてしまう。
// 前後の空白も落とす（画面は trim して送ってくる）。
func normalizeKifuText(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
}
