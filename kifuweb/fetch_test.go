package kifuweb_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"

	"github.com/ShinteLab/ikkyoku/kifuweb"
)

const sample = "先手：先手太郎\n後手：後手花子\n手数----指手---------消費時間--\n   1 ７六歩(77)\n"

func toShiftJIS(t *testing.T, s string) []byte {
	t.Helper()
	b, _, err := transform.Bytes(japanese.ShiftJIS.NewEncoder(), []byte(s))
	if err != nil {
		t.Fatalf("Shift_JIS へ変換できません: %v", err)
	}
	return b
}

// ⚠️ **日本将棋連盟の棋譜中継は Shift_JIS。** UTF-8 決め打ちで読むと
// 対局者名が化けたまま通り、**指し手も読めなくなる**（漢数字が壊れるため）。
func TestFetchShiftJIS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(toShiftJIS(t, sample))
	}))
	defer srv.Close()

	got, err := kifuweb.Fetch(context.Background(), srv.URL+"/oui202607290101.kif")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Text != sample {
		t.Errorf("本文が一致しません:\n got %q\nwant %q", got.Text, sample)
	}
	if got.Encoding != kifuweb.EncodingShiftJIS {
		t.Errorf("Encoding = %q, want %q", got.Encoding, kifuweb.EncodingShiftJIS)
	}
}

func TestFetchUTF8(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sample))
	}))
	defer srv.Close()

	got, err := kifuweb.Fetch(context.Background(), srv.URL+"/a.kif")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Text != sample || got.Encoding != kifuweb.EncodingUTF8 {
		t.Errorf("Fetch = %+v", got)
	}
}

// 中継ページ（HTML）の URL を渡したときは、黙って読まずに理由を返す。
func TestFetchRejectsHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<!DOCTYPE html>\n<html><head><title>棋譜中継</title></head></html>"))
	}))
	defer srv.Close()

	_, err := kifuweb.Fetch(context.Background(), srv.URL+"/index.html")
	if err == nil {
		t.Fatal("HTML を受け取ったのにエラーになりませんでした")
	}
	if !strings.Contains(err.Error(), ".kif") {
		t.Errorf("何を指定すればよいかがエラーに出ない: %v", err)
	}
}

func TestFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := kifuweb.Fetch(context.Background(), srv.URL+"/none.kif")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("HTTP の状態がエラーに出ない: %v", err)
	}
}

// ⚠️ **http / https 以外は受けない。** 通すと URL 欄から手元のファイルを読める。
func TestFetchRejectsOtherSchemes(t *testing.T) {
	for _, u := range []string{"file:///C:/secret.kif", "ftp://example.com/a.kif", ""} {
		if _, err := kifuweb.Fetch(context.Background(), u); err == nil {
			t.Errorf("%q を受け付けてしまいました", u)
		}
	}
}

// 大きすぎるものは掴まない（棋譜 1 局は数十 KB）。
func TestFetchTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, kifuweb.MaxBytes+1))
	}))
	defer srv.Close()

	if _, err := kifuweb.Fetch(context.Background(), srv.URL+"/big.kif"); err == nil {
		t.Fatal("上限を超えたのにエラーになりませんでした")
	}
}
