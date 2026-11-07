// Package training は suteme の学習用サーバ（`suteme/training`）へ訂正済みの局面を
// 登録するためのクライアント。
//
// **サンプルの作り方はここに書かない。** 学習サンプルの単位は
// （PNG・SFEN の盤面フィールド・盤面矩形）の 3 点組で、そこから 81 マスを切り出すのは
// **suteme の仕事**（2026-08-07 の決定）。ここが持つのは HTTP の口の形だけで、
// 向こうの形式が変わっても影響を受けないようにしてある。
//
// ⚠️ **送る SFEN は「手前が先手」の規約に従うこと**（suteme 側の要求）。実際の対局で
// 後手が手前に映っていても、**画像に見えているとおり**＝手前側を先手として表現した
// SFEN を送る。規約を外れた SFEN を登録すると、suteme の向き正規化
// （`samplesFromRegion` の `if !black { Rotate180 }`）がその 1 件だけ逆に働き、
// **同じ駒種に上下反転した 2 群ができる**。機械には検出できないので送信側が守る。
// ikkyoku は取り込みでも訂正でも盤を反転しない（`_docs/design-position.md`「視点」の節）ので、
// **普通に使っている限りこの規約は自然に守られる。**
package training

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png" // DecodeConfig 用（送る前に矩形が画像に収まっているか確かめる）
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultPort は suteme の学習用サーバの既定ポート（`suteme/_cmd/suteme-training`）。
// **scheme を書かずにポートも書かなかった**ときだけ補う（ParseURL）。
const DefaultPort = 8080

// DefaultURL は既定の接続先。**同じマシンで動かすのが普通の使い方。**
// suteme はループバックからのアクセスを認証免除で受けるので、この既定なら
// トークンも外部公開の設定も要らない。
const DefaultURL = "http://127.0.0.1:8080"

// Client は suteme の学習用サーバの API を叩く。ゼロ値では使わない（New で作る）。
type Client struct {
	// BaseURL はサーバを載せた場所（"http://host:port" か、`-base` で載せたなら
	// "http://host:port/st"）。末尾に / は付けない。API はこの下の "/api/..."。
	BaseURL string
	// Token は Bearer トークン。空なら付けない。
	// **ループバックからは要求されない**ので、同じマシンで動かすなら空でよい。
	Token string
	// HTTP は差し替え可能（テスト用）。nil なら既定のクライアントを使う。
	HTTP *http.Client
}

// New は接続先の URL からクライアントを作る（URL の読み方は ParseURL）。
func New(rawURL, token string) (*Client, error) {
	base, err := ParseURL(rawURL)
	if err != nil {
		return nil, err
	}
	return &Client{BaseURL: base, Token: strings.TrimSpace(token)}, nil
}

// apiSuffixes は suteme の画面に出る URL の末尾のうち、剥がしてサーバの場所に戻すもの。
// suteme の APIタブは「登録先」として `…/api/register` を出すので、**それを貼っても届くように**する。
var apiSuffixes = []string{"/api/register", "/api/status"}

// ParseURL は設定の URL を、実際に送る先（サーバを載せた場所。末尾の / なし）にする。
//
//   - 空なら DefaultURL
//   - scheme が無ければ http を補い、**ポートも無ければ DefaultPort を補う**
//     （"127.0.0.1" と打って 80 番へ行かないように。scheme を書いたときは URL のとおり ——
//     リバースプロキシ越しなら 80 / 443 が普通）
//   - パスは suteme の `-base`（"http://host:8080/st"）。末尾の `/api/register` などは剥がす
//   - ⚠️ **組み立てられないものはエラーにする**（黙って別の宛先へ送らない）
func ParseURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return DefaultURL, nil
	}
	schemeless := !strings.Contains(s, "://")
	if schemeless {
		// "::1" のような IPv6 のリテラルは [] で囲まないと URL にならない
		if ip := net.ParseIP(s); ip != nil && strings.Contains(s, ":") {
			s = "[" + s + "]"
		}
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("接続先の URL を読めません: %q", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("接続先の URL は http:// か https:// で書いてください: %q", raw)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("接続先の URL にホストがありません: %q", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("接続先の URL に ? や # や認証情報は書けません: %q", raw)
	}
	if schemeless && u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(DefaultPort))
	}
	p := strings.TrimRight(u.Path, "/")
	for _, suf := range apiSuffixes {
		if strings.HasSuffix(p, suf) {
			p = strings.TrimRight(strings.TrimSuffix(p, suf), "/")
			break
		}
	}
	u.Path, u.RawPath = p, ""
	return u.String(), nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	// 相手が居ないときに UI が固まらない程度には短く、3.5MB 級の
	// PNG を送れる程度には長く。
	return &http.Client{Timeout: 20 * time.Second}
}

// Status は `GET /api/status` の応答。
//
// **`/api/health` ではない。** 「サーバが生きているか」ではなく
// 「**今このサーバに送ってよいか**」を返す口なので、そのつもりで読むこと。
type Status struct {
	// Enabled は登録を受け付ける設定になっているか（suteme の APIタブのトグル）。
	//
	// ⚠️ **false でもループバックからは登録できる。** suteme は
	// ループバックを `withAccessControl` の入口で素通しにしており、
	// Enabled / トークンの判定はそもそも通らない。したがって
	// **これを送信の可否として使わないこと**（同じマシンなら送れるのに
	// 送れないと表示することになる）。表示に留める。
	Enabled bool `json:"enabled"`
	// Detailed は以下の項目が返ってきたか。**ループバックか、正しいトークンを
	// 持っている相手にしか返らない**（素性を明かさないため）。
	Detailed bool `json:"detailed"`
	// External はループバック以外からのアクセスを許す設定か。
	External bool `json:"external"`
	// Auth はトークンを要求する設定か。
	Auth bool `json:"auth"`
	// Entries は履歴の件数、Capacity はその上限（超えると登録が断られる）。
	Entries  int `json:"entries"`
	Capacity int `json:"capacity"`
	// DataVersion は向こうが読んでいる学習データのファイル名。
	DataVersion string `json:"dataVersion"`
}

// Status は登録を受け付けられる状態かを問い合わせる。
func (c *Client) Status(ctx context.Context) (Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/status", nil)
	if err != nil {
		return Status{}, err
	}
	c.auth(req)
	res, err := c.httpClient().Do(req)
	if err != nil {
		return Status{}, fmt.Errorf("%s につながりません: %w", c.BaseURL, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return Status{}, statusError(res.StatusCode, body)
	}

	// 返る項目は相手次第（素性を明かさない相手には enabled しか返らない）なので、
	// 生の map で受けてから詰め替える。
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return Status{}, fmt.Errorf("応答を読めません（suteme の学習用サーバではない可能性があります）: %w", err)
	}
	st := Status{Enabled: boolOf(raw["enabled"])}
	if _, ok := raw["capacity"]; ok {
		st.Detailed = true
		st.External = boolOf(raw["external"])
		st.Auth = boolOf(raw["auth"])
		st.Entries = intOf(raw["entries"])
		st.Capacity = intOf(raw["capacity"])
		st.DataVersion, _ = raw["data_version"].(string)
	}
	return st, nil
}

// Sample は登録する 1 件。
type Sample struct {
	// ImagePath は撮った PNG。**中身をそのまま送る**（再エンコードしない）。
	// suteme は画像そのもののハッシュで再送を弾くので、バイト列を変えないほうがよい。
	ImagePath string
	// SFEN は訂正した正解。盤面部分だけでも、手番・持ち駒つきの完全形でもよい
	// （suteme は学習に盤面部分しか使わないが、送ったぶんは保持される）。
	SFEN string
	// Bounds は画像の中での盤面の外枠。**必須**（無いと suteme が 400 を返す）。
	// 認識結果の `Debug.Region` をそのまま渡す。
	Bounds image.Rectangle
}

// RegisterResult は `POST /api/register` の応答。
type RegisterResult struct {
	// ID は登録された局面の ID（suteme の履歴タブに出る）。
	ID string `json:"id"`
	// Duplicate は同じ画像が既に登録されていたか。**エラーではない**
	// （再送を安全にするための仕組みで、既存の ID が返る）。
	Duplicate bool `json:"duplicate"`
	// Entries は登録後の履歴の件数。Duplicate のときは 0。
	Entries int `json:"entries"`
}

// Register は訂正済みの局面を学習データとして登録する。
//
// **入るのは「未確認」（`Verified=false`）として。** 画像と SFEN の対応は機械には
// 検証できないので、suteme の解析タブで人が一度見るまで学習には使われない。
// これは ikkyoku 側では変えられないし、変えようとしないこと。
func (c *Client) Register(ctx context.Context, s Sample) (RegisterResult, error) {
	if strings.TrimSpace(s.SFEN) == "" {
		return RegisterResult{}, fmt.Errorf("送る SFEN がありません")
	}
	raw, err := os.ReadFile(s.ImagePath)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("画像を読めません: %w", err)
	}
	// **送る前に矩形が画像に収まっているか確かめる。** 向こうも同じ検証をするが、
	// 400 が返ってから理由を読むより、こちらで座標系の食い違いに気づけるほうがよい
	// （認識結果の座標はメモリ上の画像基準で、PNG は原点が (0,0) に正規化される）。
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return RegisterResult{}, fmt.Errorf("画像を読めません: %w", err)
	}
	if s.Bounds.Dx() < 9 || s.Bounds.Dy() < 9 {
		return RegisterResult{}, fmt.Errorf("盤面の矩形が小さすぎます: %v", s.Bounds)
	}
	if s.Bounds.Min.X < 0 || s.Bounds.Min.Y < 0 ||
		s.Bounds.Max.X > cfg.Width || s.Bounds.Max.Y > cfg.Height {
		return RegisterResult{}, fmt.Errorf(
			"盤面の矩形 %v が画像 (%dx%d) からはみ出しています", s.Bounds, cfg.Width, cfg.Height)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("image", fileName(s.ImagePath))
	if err != nil {
		return RegisterResult{}, err
	}
	if _, err := part.Write(raw); err != nil {
		return RegisterResult{}, err
	}
	fields := [][2]string{
		{"sfen", strings.TrimSpace(s.SFEN)},
		{"x1", strconv.Itoa(s.Bounds.Min.X)},
		{"y1", strconv.Itoa(s.Bounds.Min.Y)},
		{"x2", strconv.Itoa(s.Bounds.Max.X)},
		{"y2", strconv.Itoa(s.Bounds.Max.Y)},
	}
	for _, f := range fields {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return RegisterResult{}, err
		}
	}
	if err := mw.Close(); err != nil {
		return RegisterResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/register", &buf)
	if err != nil {
		return RegisterResult{}, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	c.auth(req)
	res, err := c.httpClient().Do(req)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("%s につながりません: %w", c.BaseURL, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return RegisterResult{}, statusError(res.StatusCode, body)
	}
	var out RegisterResult
	if err := json.Unmarshal(body, &out); err != nil {
		return RegisterResult{}, fmt.Errorf("応答を読めません: %w", err)
	}
	return out, nil
}

func (c *Client) auth(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

// statusError は HTTP のステータスを、**何をすればよいかが分かる**日本語にする。
// suteme はエラー本文を {"error": "..."} で返すので、あればそれも添える。
func statusError(code int, body []byte) error {
	var payload struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)
	detail := strings.TrimSpace(payload.Error)

	var head string
	switch code {
	case http.StatusNotFound:
		// **公開オフのときは 404 を返す**仕様（存在自体を伏せる）。パスの間違いと
		// 見分けが付かないので、両方の可能性を書く。
		head = "受け付けていません（suteme 側で外部公開がオフか、宛先が違います）"
	case http.StatusUnauthorized:
		head = "トークンが受け付けられませんでした"
	case http.StatusServiceUnavailable:
		head = "suteme 側で登録が無効になっています（APIタブで有効にしてください）"
	case http.StatusInsufficientStorage:
		head = "suteme 側の履歴が上限です（不要な局面を削除してください）"
	case http.StatusBadRequest:
		head = "送った内容を受け付けてもらえませんでした"
	default:
		head = fmt.Sprintf("エラーが返りました (HTTP %d)", code)
	}
	if detail != "" {
		return fmt.Errorf("%s: %s", head, detail)
	}
	return fmt.Errorf("%s", head)
}

func fileName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

func intOf(v any) int {
	f, _ := v.(float64)
	return int(f)
}
