// Package piecefont は**端末に入っているフォントから駒の字を焼く**。
//
// なぜ端末で焼くのか:
//
// 駒の字に使えるフォントを同梱して配るには、**派生物の作成と再配布を認める
// ライセンス**でなければならない（`core/web/README.md`）。ところが実際に
// それを認めている日本語フォントは少なく、手元にある「これで駒を表示したい」
// フォントの大半は配れない。**だが「自分の端末に入っているフォントを、
// 自分の端末で表示に使う」だけなら再配布ではない。**
//
//	端末のフォント ──Faces──> 一覧（駒の字が揃っているか）
//	                └─Build──> 駒フォント（19 グリフ・約 10KB）──> webview の @font-face
//
// ⚠️ **焼いたフォントはディスクに残さない。** 残す理由が無い（10KB なので
// 起動のたびに焼き直しても一瞬）うえ、残すと**元フォントを入れ替えたのに
// 古い字で描く**という、画面からは気づけない事故が起きる。
//
// ⚠️ **焼いたフォントを配らないこと。** そちらには元フォントの条項が効く。
// このパッケージが扱うのは「その端末で表示する」までで、書き出す口は持たない。
//
// ⚠️ **TTF を組み立てるコードはここに書かない**（`core/shogifont`）。
// 字形の生成は将棋の表示の仕様側なので core に一本化する。ここがやるのは
// 「端末のどこにフォントがあるか」という、アプリ側の話だけ。
package piecefont

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/ShinteLab/core/shogifont"
)

// Font は端末に入っているフォントの書体 1 つ分。
//
// **TTC は 1 ファイルに複数の書体が入っている**ので、`Path` だけでは指せない
// （`msmincho.ttc` に MS 明朝と MS P明朝が同居している）。`Path` と `Index` の
// 組が 1 つの書体を指す。
type Font struct {
	Path  string `json:"path"`
	File  string `json:"file"` // ファイル名。**同じ名前の書体を見分ける手掛かり**
	Index int    `json:"index"`

	// Name は画面に出す名前（日本語名があればそちら）。
	Name      string `json:"name"`
	Family    string `json:"family"` // 英語の family 名
	SubFamily string `json:"subFamily,omitempty"`

	// 元フォントの権利表記。⚠️ **空でも「制約が無い」ではない。**
	// 画面に出すのは、**何に由来する字なのかを利用者が判断できるようにする**ため。
	Copyright  string `json:"copyright,omitempty"`
	Trademark  string `json:"trademark,omitempty"`
	License    string `json:"license,omitempty"`
	LicenseURL string `json:"licenseUrl,omitempty"`

	// Missing は駒に要る字のうち、このフォントに無いもの。**空なら焼ける。**
	//
	// ⚠️ **足りないものを一覧から消さないこと**（選べない見た目にするだけ）。
	// 消すと「入れたはずのフォントが出てこない」になり、探しているのか
	// 対象外なのかが画面から分からない。
	Missing string `json:"missing,omitempty"`
}

// Usable は駒の字が揃っていて焼けるか。
func (f Font) Usable() bool { return f.Missing == "" }

// Required は駒を描くのに要る字（画面に「何が足りないか」を出すため）。
//
// ⚠️ **ikkyoku 側で数え直さないこと**（`core/shogifont` が持っている）。
func Required() string { return string(shogifont.Required()) }

// 拡張子。**大文字のファイルもある**（`HGRSKP.TTF`）ので比較は小文字で行う。
var fontExts = map[string]bool{
	".ttf": true, ".ttc": true, ".otf": true, ".otc": true,
}

// Dirs はフォントを探すディレクトリを返す。
//
// Windows では**システムとユーザーの 2 か所**。⚠️ **ユーザー側を落とさないこと** ——
// 後から入れたフォント（まさに「駒に使いたいフォント」）はそちらに入る
// （Windows 10 以降、管理者権限なしでインストールするとこちら）。
func Dirs() []string {
	var dirs []string
	add := func(parts ...string) {
		for _, p := range parts {
			if p == "" {
				return
			}
		}
		dirs = append(dirs, filepath.Join(parts...))
	}
	switch runtime.GOOS {
	case "windows":
		add(os.Getenv("SystemRoot"), "Fonts")
		add(os.Getenv("LOCALAPPDATA"), "Microsoft", "Windows", "Fonts")
	case "darwin":
		home, _ := os.UserHomeDir()
		dirs = append(dirs, "/System/Library/Fonts", "/Library/Fonts")
		add(home, "Library", "Fonts")
	default:
		home, _ := os.UserHomeDir()
		dirs = append(dirs, "/usr/share/fonts", "/usr/local/share/fonts")
		add(home, ".local", "share", "fonts")
		add(home, ".fonts")
	}
	return dirs
}

// List は端末に入っているフォントの書体を全部返す（名前順）。
//
// **駒の字が無いものも返す**（`Missing` に何が足りないか入る）。実測では
// 190 ファイル・577MB を読んで 1 秒弱で、そのうち焼けるのは 85 書体だった。
// **押したときだけ呼ぶこと**（起動のたびに走らせる類の処理ではない）。
func List() ([]Font, error) {
	var fonts []Font
	seen := map[string]bool{}
	found := false
	for _, dir := range Dirs() {
		ents, err := os.ReadDir(dir)
		if err != nil {
			// **無いディレクトリは飛ばす**（ユーザー側は 1 つも入れていなければ無い）。
			continue
		}
		found = true
		for _, e := range ents {
			if e.IsDir() || !fontExts[strings.ToLower(filepath.Ext(e.Name()))] {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if seen[strings.ToLower(path)] {
				continue
			}
			seen[strings.ToLower(path)] = true
			fs, err := read(path)
			if err != nil {
				// ⚠️ **読めない 1 つで一覧ごと落とさない。** 端末には壊れた
				// フォントも、フォントでないファイルも入っている。
				continue
			}
			fonts = append(fonts, fs...)
		}
	}
	if !found {
		return nil, fmt.Errorf("フォントのディレクトリが見つかりません (%s)", strings.Join(Dirs(), ", "))
	}
	sortFonts(fonts)
	return fonts, nil
}

// Read は 1 つの書体だけを読み直す。
//
// **登録したフォントが今も同じ場所にあるか**を確かめるのに使う
// （アンインストールされることも、更新で書体が入れ替わることもある）。
func Read(path string, index int) (Font, error) {
	fs, err := read(path)
	if err != nil {
		return Font{}, err
	}
	for _, f := range fs {
		if f.Index == index {
			return f, nil
		}
	}
	return Font{}, fmt.Errorf("%s に書体 %d はありません", filepath.Base(path), index)
}

// Build は駒フォントを焼いて TTF のバイト列を返す。
//
// ⚠️ **family は登録ごとに変えること**（呼び出し側の責任）。同じ名前で
// 複数登録すると、どれが当たるかブラウザ任せになって切り替えが効かなくなる。
func Build(path string, index int, family string) ([]byte, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("フォントを読めません: %w", err)
	}
	out, err := shogifont.Build(src, shogifont.Options{Family: family, Index: index})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return out, nil
}

func read(path string) ([]Font, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	faces, err := shogifont.Faces(src)
	if err != nil {
		return nil, err
	}
	out := make([]Font, 0, len(faces))
	for _, f := range faces {
		out = append(out, Font{
			Path:       path,
			File:       filepath.Base(path),
			Index:      f.Index,
			Name:       displayName(f),
			Family:     f.Family,
			SubFamily:  f.SubFamily,
			Copyright:  f.Copyright,
			Trademark:  f.Trademark,
			License:    f.License,
			LicenseURL: f.LicenseURL,
			Missing:    string(f.Missing),
		})
	}
	return out, nil
}

// regularNames は「書体名に足しても情報が増えない」サブファミリ名。
var regularNames = map[string]bool{
	"regular": true, "標準": true, "レギュラー": true, "r": true, "": true,
}

// displayName は画面に出す名前を組む。**日本語名があればそちらを使う**
// （「玉ねぎ楷書激無料版v7改」を "Tamanegi Kaisho Geki FreeVer 7" と
// 出されても、利用者は自分が入れたフォントだと分からない）。
func displayName(f shogifont.Face) string {
	name := f.LocalFamily
	if name == "" {
		name = f.Family
	}
	sub := f.LocalSubFamily
	if sub == "" {
		sub = f.SubFamily
	}
	if name == "" {
		// 名前が 1 つも読めないフォントもある。**空の行を出さないこと。**
		return "(名前なし)"
	}
	if !regularNames[strings.ToLower(strings.TrimSpace(sub))] {
		name += " " + sub
	}
	return name
}

// sortFonts は名前順に並べる。
//
// ⚠️ **焼けるものを先に詰めないこと。** 探すときは名前で探すので、
// 使える/使えないで前後に分かれていると**同じ名前のフォントが離れて並ぶ**。
// 使えるかどうかは行の見た目が言う。
func sortFonts(fonts []Font) {
	sort.SliceStable(fonts, func(i, j int) bool {
		a, b := fonts[i], fonts[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Index < b.Index
	})
}
