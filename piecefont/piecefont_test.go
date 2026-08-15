package piecefont

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShinteLab/core/shogifont"
)

// 画面に出す名前は**日本語名を優先する**。英語名しか出さないと、
// 自分で入れたフォントを利用者が見つけられない。
func TestDisplayName(t *testing.T) {
	for _, tc := range []struct {
		name string
		face shogifont.Face
		want string
	}{
		{"日本語名があればそちら",
			shogifont.Face{Family: "Yu Mincho", LocalFamily: "游明朝", SubFamily: "Regular"},
			"游明朝"},
		{"日本語名が無ければ英語名",
			shogifont.Face{Family: "Migu 1M", SubFamily: "Bold"},
			"Migu 1M Bold"},
		{"Regular は足さない",
			shogifont.Face{Family: "Migu 1M", SubFamily: "Regular"},
			"Migu 1M"},
		{"標準も足さない（日本語のサブファミリ）",
			shogifont.Face{Family: "MS Mincho", LocalFamily: "ＭＳ 明朝",
				SubFamily: "Regular", LocalSubFamily: "標準"},
			"ＭＳ 明朝"},
		{"太さは足す",
			shogifont.Face{Family: "Yu Mincho", LocalFamily: "游明朝", SubFamily: "Bold"},
			"游明朝 Bold"},
		{"名前が読めないフォントでも空行にしない",
			shogifont.Face{},
			"(名前なし)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayName(tc.face); got != tc.want {
				t.Errorf("displayName = %q, want %q", got, tc.want)
			}
		})
	}
}

// ⚠️ **焼けるものを前に詰めない。** 探すときは名前で探すので、
// 使える/使えないで分かれていると同じ名前のフォントが離れて並ぶ。
func TestSortFontsKeepsNameOrder(t *testing.T) {
	fonts := []Font{
		{Name: "游明朝", File: "yumin.ttf"},
		{Name: "ＭＳ 明朝", File: "msmincho.ttc", Index: 0},
		{Name: "Arial", File: "arial.ttf", Missing: "歩香桂"},
		{Name: "ＭＳ 明朝", File: "msmincho.ttc", Index: 1},
		{Name: "Arial", File: "arialbd.ttf", Missing: "歩香桂"},
	}
	sortFonts(fonts)
	var names []string
	for _, f := range fonts {
		names = append(names, f.Name)
	}
	// 使えない Arial が、使える書体の後ろへ回されていないこと。
	if names[0] != "Arial" || names[1] != "Arial" {
		t.Errorf("名前順になっていない: %v", names)
	}
	// 同じ名前はファイル名 → 書体番号の順（TTC の中で並びが暴れない）。
	if fonts[0].File != "arial.ttf" || fonts[1].File != "arialbd.ttf" {
		t.Errorf("同名のファイル順が崩れている: %v", fonts)
	}
	if fonts[3].Index != 0 || fonts[4].Index != 1 {
		t.Errorf("同一ファイルの書体番号順が崩れている: %v", fonts[3:])
	}
}

// 要る字は core が持っている。**ikkyoku 側で数え直さないこと**の歯止め。
func TestRequiredComesFromCore(t *testing.T) {
	if got, want := Required(), string(shogifont.Required()); got != want {
		t.Errorf("Required = %q, want %q", got, want)
	}
	if !strings.ContainsRune(Required(), '玉') {
		t.Errorf("玉 が要る字に入っていない: %q", Required())
	}
}

func TestUsable(t *testing.T) {
	if !(Font{}).Usable() {
		t.Error("Missing が空なら使える")
	}
	if (Font{Missing: "歩"}).Usable() {
		t.Error("字が足りないのに使えると答えた")
	}
}

// 探す先が 1 つも無い環境でも panic しないこと。
func TestDirs(t *testing.T) {
	if len(Dirs()) == 0 {
		t.Error("フォントを探す先が 1 つも無い")
	}
	for _, d := range Dirs() {
		if d == "" {
			t.Error("空のディレクトリが混ざっている")
		}
	}
}

// 無いファイル・フォントでないファイルは、理由の分かるエラーで断ること
// （**一覧では飛ばすが、名指しで読むときは黙って成功させない**）。
func TestReadBadInput(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.ttf")
	if err := os.WriteFile(junk, []byte("これはフォントではない"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(junk, 0); err == nil {
		t.Error("フォントでないのにエラーにならなかった")
	}
	if _, err := Read(filepath.Join(dir, "ない.ttf"), 0); err == nil {
		t.Error("無いファイルなのにエラーにならなかった")
	}
	if _, err := Build(junk, 0, "X"); err == nil {
		t.Error("フォントでないのに焼けてしまった")
	}
}

// ⚠️ **ここから下は端末に日本語フォントが入っていないと動かない。**
// 入っていなければ skip する（あるかどうかでテストが「失敗」に変わると、
// ツールの問題か環境の問題か切り分けられない）。
func usableFont(t *testing.T) Font {
	t.Helper()
	fonts, err := List()
	if err != nil {
		t.Skipf("フォントを列挙できない: %v", err)
	}
	for _, f := range fonts {
		if f.Usable() {
			return f
		}
	}
	t.Skip("駒の字を持つフォントが端末に無い")
	return Font{}
}

// 端末のフォントから焼けること、焼いたものが Read で読み直せること。
func TestListAndBuild(t *testing.T) {
	f := usableFont(t)
	if f.Path == "" || f.Name == "" {
		t.Fatalf("一覧の中身が埋まっていない: %+v", f)
	}

	// 名指しで読み直しても同じ書体が返ること（登録が今も生きているかの確認に使う）。
	again, err := Read(f.Path, f.Index)
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != f.Name {
		t.Errorf("読み直したら別の書体になった: %q → %q", f.Name, again.Name)
	}

	out, err := Build(f.Path, f.Index, "ShogiTest")
	if err != nil {
		t.Fatal(err)
	}
	// 焼いたものは 19 グリフの軽量 TTF。**元フォントより桁違いに小さいこと**が
	// 「駒だけ抜いた」の確認になる（webview へ data URL で流せる大きさ）。
	if len(out) == 0 || len(out) > 200*1024 {
		t.Errorf("焼いたフォントの大きさが想定外: %d バイト", len(out))
	}
	faces, err := shogifont.Faces(out)
	if err != nil {
		t.Fatalf("焼いたフォントを読み直せない: %v", err)
	}
	if len(faces[0].Missing) != 0 {
		t.Errorf("焼いたのに字が足りない: %q", string(faces[0].Missing))
	}
	if faces[0].Family != "ShogiTest" {
		t.Errorf("family = %q, want ShogiTest", faces[0].Family)
	}
}
