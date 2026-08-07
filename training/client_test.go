package training

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⚠️ ここのスタブは **suteme 側の `training/api.go` の読み方に合わせてある。**
// フィールド名（image / sfen / x1,y1,x2,y2）も、応答の形も向こうの実装が正。
// 向こうが変わったらこのテストごと直すこと（合わせるのはこちら）。

func writePNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x40, A: 0xff})
		}
	}
	path := filepath.Join(t.TempDir(), "shot.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRegisterSendsImageSFENAndBounds(t *testing.T) {
	var got struct {
		sfen, x1, y1, x2, y2 string
		imageBytes           int
		auth                 string
		path                 string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
			return
		}
		f, _, err := r.FormFile("image")
		if err != nil {
			t.Errorf("image がありません: %v", err)
			return
		}
		defer f.Close()
		var buf bytes.Buffer
		buf.ReadFrom(f)
		got.imageBytes = buf.Len()
		got.sfen = r.FormValue("sfen")
		got.x1, got.y1 = r.FormValue("x1"), r.FormValue("y1")
		got.x2, got.y2 = r.FormValue("x2"), r.FormValue("y2")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "id": "20260808-1", "entries": 3})
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "tok"}
	path := writePNG(t, 120, 130)
	res, err := c.Register(context.Background(), Sample{
		ImagePath: path,
		SFEN:      "lnsgkgsnl/9/ppppppppp/9/9/9/PPPPPPPPP/9/LNSGKGSNL b - 1",
		Bounds:    image.Rect(10, 12, 110, 122),
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if res.ID != "20260808-1" || res.Entries != 3 || res.Duplicate {
		t.Errorf("結果が違う: %+v", res)
	}
	if got.path != "/api/register" {
		t.Errorf("パスが違う: %q", got.path)
	}
	if got.auth != "Bearer tok" {
		t.Errorf("トークンが載っていない: %q", got.auth)
	}
	if !strings.HasPrefix(got.sfen, "lnsgkgsnl/") {
		t.Errorf("sfen が違う: %q", got.sfen)
	}
	// **座標は必須。** 無いと suteme が 400 を返すうえ、あっても値がずれると
	// 学習データが静かに壊れる（切り出す 81 マスがずれる）ので固定しておく。
	if got.x1 != "10" || got.y1 != "12" || got.x2 != "110" || got.y2 != "122" {
		t.Errorf("盤面座標が違う: %s,%s,%s,%s", got.x1, got.y1, got.x2, got.y2)
	}
	// PNG は再エンコードせずそのまま送る（向こうがハッシュで再送を弾くため）。
	raw, _ := os.ReadFile(path)
	if got.imageBytes != len(raw) {
		t.Errorf("画像のバイト数が変わっている: %d != %d", got.imageBytes, len(raw))
	}
}

// 矩形が画像からはみ出していたら**送る前に**止める。向こうも 400 で弾くが、
// 座標系の食い違いはこちら側の問題なので、こちらで理由を出せるほうがよい。
func TestRegisterRejectsBoundsOutsideImage(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"} // 届かないアドレス（送る前に落ちる想定）
	_, err := c.Register(context.Background(), Sample{
		ImagePath: writePNG(t, 100, 100),
		SFEN:      "9/9/9/9/9/9/9/9/9",
		Bounds:    image.Rect(0, 0, 120, 100),
	})
	if err == nil || !strings.Contains(err.Error(), "はみ出して") {
		t.Fatalf("はみ出しを弾いていない: %v", err)
	}
}

// 再送は失敗ではない。suteme は既存の ID を返すので、そのまま通す。
func TestRegisterReportsDuplicate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "id": "old-1", "duplicate": true})
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	res, err := c.Register(context.Background(), Sample{
		ImagePath: writePNG(t, 100, 100),
		SFEN:      "9/9/9/9/9/9/9/9/9",
		Bounds:    image.Rect(0, 0, 90, 90),
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !res.Duplicate || res.ID != "old-1" {
		t.Errorf("重複を伝えていない: %+v", res)
	}
}

// エラーの本文（{"error": ...}）は、そのまま人に見せる文言に混ぜる。
// 上限超過（507）は「suteme 側で消してもらう」以外に手が無いので、特に分かる必要がある。
func TestStatusErrorsAreReadable(t *testing.T) {
	cases := []struct {
		code int
		body string
		want string
	}{
		{http.StatusNotFound, ``, "外部公開がオフ"},
		{http.StatusUnauthorized, `{"error":"トークンが不正です"}`, "トークン"},
		{http.StatusServiceUnavailable, `{"error":"API 登録は無効です"}`, "APIタブ"},
		{http.StatusInsufficientStorage, `{"error":"履歴が上限（200 件）です"}`, "200 件"},
	}
	for _, tc := range cases {
		err := statusError(tc.code, []byte(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("HTTP %d: %q に %q が含まれない", tc.code, err, tc.want)
		}
	}
}

// /api/status は相手によって返す項目が変わる（素性を明かさない相手には enabled だけ）。
// **項目が欠けていてもエラーにしない**（それ自体が「詳細を返さない相手」という情報）。
func TestStatusHandlesPartialResponse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		detailed bool
		entries  int
	}{
		{"詳細つき", `{"enabled":true,"external":true,"auth":false,"entries":7,"capacity":200,"data_version":"training_data_v3.json"}`, true, 7},
		{"enabled だけ", `{"enabled":false}`, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/status" {
					t.Errorf("パスが違う: %q", r.URL.Path)
				}
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			st, err := (&Client{BaseURL: srv.URL}).Status(context.Background())
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if st.Detailed != tc.detailed || st.Entries != tc.entries {
				t.Errorf("読み取りが違う: %+v", st)
			}
		})
	}
}

func TestNewResolvesDefaults(t *testing.T) {
	if got := New("", 0, "").BaseURL; got != "http://127.0.0.1:8080" {
		t.Errorf("既定の宛先が違う: %q", got)
	}
	if got := New("::1", 9000, "").BaseURL; got != "http://[::1]:9000" {
		t.Errorf("IPv6 を囲めていない: %q", got)
	}
	// scheme まで書かれていたらそのまま使う（リバースプロキシ越しの指定）。
	if got := New("https://example.test/suteme/", 0, "").BaseURL; got != "https://example.test/suteme" {
		t.Errorf("scheme つきの指定を壊している: %q", got)
	}
}
