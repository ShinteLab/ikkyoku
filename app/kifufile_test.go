package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"

	"github.com/ShinteLab/kicho"
)

// writeKifuFile は棋譜ファイルを書いて、それを選ぶダイアログの代わりを返す。
func writeKifuFile(t *testing.T, name string, body []byte) (string, FilePicker) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, func(string, string, ...string) (string, error) { return path, nil }
}

// **Shift_JIS の .kif を開くと UTF-8 にして返し、同じ本文のまま登録すれば
// 元の文字コード（shift_jis）が棚に残る**（2026-11-15）。
func TestOpenKifuFileShiftJISKeepsEncoding(t *testing.T) {
	svc, _ := newTestKifuService(t)
	// ⚠️ 改行は CRLF（画面の入力欄は LF にして返してくる。それでも「同じ本文」と見ること）。
	sjis, err := japanese.ShiftJIS.NewEncoder().String(strings.ReplaceAll(testKIF, "\n", "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, pick := writeKifuFile(t, "game.kif", []byte(sjis))
	svc.PickFile = pick

	got, err := svc.OpenKifuFile()
	if err != nil {
		t.Fatalf("OpenKifuFile: %v", err)
	}
	if got.Name != "game.kif" || got.Encoding != kicho.EncodingShiftJIS || !strings.Contains(got.Text, "先手太郎") {
		t.Errorf("KifuFile = %+v", got)
	}

	// 画面は textarea の値（LF・trim 済み）を送ってくる。
	rec, err := svc.ImportKIF(strings.TrimSpace(strings.ReplaceAll(got.Text, "\r\n", "\n")))
	if err != nil {
		t.Fatalf("ImportKIF: %v", err)
	}
	d, err := svc.Get(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Encoding != kicho.EncodingShiftJIS {
		t.Errorf("Encoding = %q, want shift_jis", d.Encoding)
	}

	// ⚠️ **書き換えた本文には元の文字コードを付けない**（貼り付けと同じ utf-8）。
	rec, err = svc.ImportKIF(testKIF + "   3 ２六歩(27)\n")
	if err != nil {
		t.Fatalf("ImportKIF: %v", err)
	}
	if d, _ := svc.Get(rec.ID); d.Encoding != kicho.EncodingUTF8 {
		t.Errorf("書き換えた本文の Encoding = %q, want utf-8", d.Encoding)
	}
}

// **CSA のファイルも開ける**（読み取りは貼り付けと同じ経路）。
func TestOpenKifuFileCSA(t *testing.T) {
	svc, _ := newTestKifuService(t)
	_, svc.PickFile = writeKifuFile(t, "game.csa", []byte("V2.2\nPI\n+\n+7776FU\n-3334FU\n"))
	got, err := svc.OpenKifuFile()
	if err != nil {
		t.Fatalf("OpenKifuFile: %v", err)
	}
	if got.Encoding != kicho.EncodingUTF8 || !strings.HasPrefix(got.Text, "V2.2") {
		t.Errorf("KifuFile = %+v", got)
	}
}

// **棚が開けていなくても開ける**（設計原則3。読むのは手元のファイルだけ）。
func TestOpenKifuFileWithoutShelf(t *testing.T) {
	svc := NewKifuService(NewStudyService(NewPositionService()))
	_, svc.PickFile = writeKifuFile(t, "game.kif", []byte(testKIF))
	if got, err := svc.OpenKifuFile(); err != nil || got.Text == "" {
		t.Errorf("OpenKifuFile = %+v, %v", got, err)
	}
}

// 取り消しはエラーではない・空のファイルと大きすぎるファイルは断る。
func TestOpenKifuFileCancelEmptyAndLarge(t *testing.T) {
	svc := NewKifuService(NewStudyService(NewPositionService()))
	svc.PickFile = func(string, string, ...string) (string, error) { return "", nil }
	if got, err := svc.OpenKifuFile(); err != nil || got != (KifuFile{}) {
		t.Errorf("取り消し = %+v, %v", got, err)
	}
	_, svc.PickFile = writeKifuFile(t, "empty.kif", []byte("  \r\n"))
	if _, err := svc.OpenKifuFile(); err == nil {
		t.Error("空のファイルが通った")
	}
	_, svc.PickFile = writeKifuFile(t, "large.kif", make([]byte, kifuFileMaxBytes+1))
	if _, err := svc.OpenKifuFile(); err == nil {
		t.Error("大きすぎるファイルが通った")
	}
}

// **落とされたファイルも「ファイルを開く…」と同じ扱い**（読むのは最初の 1 つ・元の文字コードを覚える）。
func TestDropKifuFiles(t *testing.T) {
	svc, _ := newTestKifuService(t)
	sjis, err := japanese.ShiftJIS.NewEncoder().String(testKIF)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := writeKifuFile(t, "first.kif", []byte(sjis))
	second, _ := writeKifuFile(t, "second.csa", []byte("PI\n+\n"))
	got := svc.DropKifuFiles([]string{first, second})
	if got.Error != "" || got.Name != "first.kif" || got.Skipped != 1 || got.Encoding != kicho.EncodingShiftJIS {
		t.Fatalf("KifuDrop = %+v", got)
	}
	if enc := svc.encodingFor(got.Text); enc != kicho.EncodingShiftJIS {
		t.Errorf("落としたファイルの文字コードを覚えていない: %q", enc)
	}
	// 読めなければ理由を返す（パニックしない・前に開いた本文は覚えたまま）。
	bad := svc.DropKifuFiles([]string{filepath.Join(t.TempDir(), "missing.kif")})
	if bad.Error == "" || bad.Text != "" {
		t.Errorf("読めないファイル = %+v", bad)
	}
	if empty := svc.DropKifuFiles(nil); empty.Error == "" {
		t.Error("空のドロップに理由が無い")
	}
	// ⚠️ **画像を落とされても文字化けした本文にしない**（Shift_JIS としては読めてしまう）。
	png, _ := writeKifuFile(t, "board.png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	if img := svc.DropKifuFiles([]string{png}); img.Error == "" || img.Text != "" {
		t.Errorf("画像が棋譜として通った: %+v", img)
	}
}

// **開いたファイルはカードになって並ぶので、後から別のファイルを開いても前のカードの文字コードは落ちない**
// （2026-11-18。それまでは最後に開いた 1 つだけを覚えていた）。
func TestEncodingForRemembersSeveralFiles(t *testing.T) {
	svc, _ := newTestKifuService(t)
	sjis, err := japanese.ShiftJIS.NewEncoder().String(testKIF)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := writeKifuFile(t, "first.kif", []byte(sjis))
	second, _ := writeKifuFile(t, "second.csa", []byte("V2.2\nPI\n+\n+7776FU\n"))
	a, err := svc.adoptKifuFile(first, "開きました")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.adoptKifuFile(second, "開きました"); err != nil {
		t.Fatal(err)
	}
	if enc := svc.encodingFor(a.Text); enc != kicho.EncodingShiftJIS {
		t.Errorf("前に開いたファイルの文字コード = %q, want shift_jis", enc)
	}
	// 貼った本文（開いていない）は utf-8。
	if enc := svc.encodingFor(testKIF + "   3 ２六歩(27)\n"); enc != kicho.EncodingUTF8 {
		t.Errorf("貼った本文の文字コード = %q, want utf-8", enc)
	}
}

// **貼り付けたカードは棚が開けていなくても作れる**（2026-11-18。設計原則3）・
// **同じ本文なら改行が違っても同じ鍵**（貼り直してもカードを増やさない）・**棋譜でなければ断る**。
func TestPreviewPaste(t *testing.T) {
	svc := NewKifuService(NewStudyService(NewPositionService()))
	got, err := svc.PreviewPaste(testKIF)
	if err != nil {
		t.Fatalf("PreviewPaste: %v", err)
	}
	if got.Key == "" || got.Game.KIF != testKIF || got.Game.Moves == 0 || got.Game.SourceID != "" {
		t.Errorf("PastedKifu = %+v", got)
	}
	again, err := svc.PreviewPaste(strings.ReplaceAll(testKIF, "\n", "\r\n"))
	if err != nil || again.Key != got.Key {
		t.Errorf("改行だけ違う本文の鍵 = %q, want %q (%v)", again.Key, got.Key, err)
	}
	if _, err := svc.PreviewPaste("こんにちは"); err == nil {
		t.Error("棋譜でない文字列をカードにした")
	}
}
