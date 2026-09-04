package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

const sampleKIF = "先手：先手太郎\n後手：後手花子\n手数----指手---------消費時間--\n" +
	"   1 ７六歩(77)\n   2 ３四歩(33)\n"

func shiftJIS(t *testing.T, s string) []byte {
	t.Helper()
	b, _, err := transform.Bytes(japanese.ShiftJIS.NewEncoder(), []byte(s))
	if err != nil {
		t.Fatalf("Shift_JIS へ変換できません: %v", err)
	}
	return b
}

// 中継ページ（HTML）の URL からでも棋譜が読めること。
//
// ⚠️ **これが `ikkyoku/kifuweb` を `kicho/scrape` に寄せた理由そのもの。**
// 連盟の中継の `source_url` は**中継ページ（HTML）の URL**（.kif ではない）なので、
// 辿れないと**棚から解析タブへ送った中継棋譜を「再読み込み」で追えない**
// （kifuweb は HTML を「棋譜ではない」と断っていた）。
func TestFetchKIFFollowsBroadcastPage(t *testing.T) {
	kif := shiftJIS(t, sampleKIF)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oui/kifu/67/sample.html":
			// 中継ページ。**一次情報は JS の定数**（拡張子の付け替えではない）。
			_, _ = io.WriteString(w, `<html><head><script>
const KIF_FILE_NAME = "/oui/kifu/67/sample.kif";
</script></head><body></body></html>`)
		case "/oui/kifu/67/sample.kif":
			_, _ = w.Write(kif)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	pageURL := srv.URL + "/oui/kifu/67/sample.html"

	got, err := fetchKIF(context.Background(), pageURL)
	if err != nil {
		t.Fatalf("fetchKIF: %v", err)
	}
	if !strings.Contains(got.Text, "７六歩") {
		t.Errorf("棋譜が取れていません: %q", got.Text)
	}
	// Shift_JIS を UTF-8 に寄せたこと（＝対局者が化けていないこと）。
	if !strings.Contains(got.Text, "先手太郎") {
		t.Errorf("文字が化けています: %q", got.Text)
	}
	if got.Encoding != "shift_jis" {
		t.Errorf("文字コードの判別が違います: %q", got.Encoding)
	}
	// ⚠️ **辿った先（.kif）ではなく、指定された URL を返すこと。**
	// 中継ページの `KIF_FILE_NAME` が一次情報で .kif のパスは変わりうるので、
	// 取り直すときも人が貼ったページのほうから辿り直す。
	if got.URL != pageURL {
		t.Errorf("URL を .kif に書き換えています: %q（期待 %q）", got.URL, pageURL)
	}
}

// .kif を直に指したときは今までどおり（HTML を辿る経路に入らない）。
func TestFetchKIFDirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sampleKIF)
	}))
	defer srv.Close()

	got, err := fetchKIF(context.Background(), srv.URL+"/sample.kif")
	if err != nil {
		t.Fatalf("fetchKIF: %v", err)
	}
	if got.Encoding != "utf-8" || !strings.Contains(got.Text, "７六歩") {
		t.Errorf("棋譜が取れていません: %+v", got)
	}
}

// ⚠️ **http / https 以外を受けないこと。**
// `file://` を通すと、URL 欄から手元のファイルを読めてしまう。
func TestFetchKIFRejectsNonHTTP(t *testing.T) {
	for _, raw := range []string{"file:///C:/Windows/win.ini", "ftp://example.com/a.kif", "a.kif"} {
		if _, err := fetchKIF(context.Background(), raw); err == nil {
			t.Errorf("%q を受け付けてしまいました", raw)
		}
	}
}

// 中継ページの URL から読み込んだあと、**同じ URL で取り直せること**。
//
// 棚から解析タブへ送った中継棋譜（`source_url` は中継ページ）を
// 「再読み込み」で追う経路そのもの。
func TestStudyServiceReloadFromBroadcastPage(t *testing.T) {
	moves := "   1 ７六歩(77)\n   2 ３四歩(33)\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".html") {
			_, _ = io.WriteString(w, `<html><script>const KIF_FILE_NAME = "/live.kif";</script></html>`)
			return
		}
		_, _ = w.Write(shiftJIS(t,
			"先手：先手太郎\n後手：後手花子\n手数----指手---------消費時間--\n"+moves))
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewStudyService(logger, NewPositionService(logger))
	if _, err := s.LoadKifuURL(srv.URL + "/live.html"); err != nil {
		t.Fatalf("LoadKifuURL: %v", err)
	}

	// 中継が 1 手進んだ。
	moves += "   3 ２六歩(27)\n"
	load, err := s.ReloadKifu()
	if err != nil {
		t.Fatalf("ReloadKifu: %v", err)
	}
	if len(load.State.Nodes) != 3 {
		t.Fatalf("伸びた手が載っていません: %d 手", len(load.State.Nodes))
	}
	// ⚠️ **見ている位置は動かない**（取り直しは「URL の側を正にする」だけ）。
	if load.State.Ply != 0 {
		t.Errorf("見ている位置が動いています: ply=%d", load.State.Ply)
	}
}
