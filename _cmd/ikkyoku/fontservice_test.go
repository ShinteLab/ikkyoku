package main

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	ikkyoku "github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/piecefont"
)

// newTestFonts は保存先を一時ディレクトリに向けた FontService を作る。
// （`newTestSettings` と同じ理由で `NewSettingsService` は使えない。）
func newTestFonts(t *testing.T) *FontService {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := ikkyoku.SaveConfig(path, ikkyoku.Config{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	settings := &SettingsService{logger: logger, path: path}
	return NewFontService(logger, settings)
}

// ⚠️ **端末に駒の字を持つフォントが無ければ skip する**（`piecefont` と同じ理由）。
func anyUsableFont(t *testing.T) piecefont.Font {
	t.Helper()
	fonts, err := piecefont.List()
	if err != nil {
		t.Skipf("フォントを列挙できない: %v", err)
	}
	for _, f := range fonts {
		if f.Usable() {
			return f
		}
	}
	t.Skip("駒の字を持つフォントが端末に無い")
	return piecefont.Font{}
}

// 登録が無いときは同梱。**Face が null で返ること**が「既定に戻す」の合図。
func TestFontStateBuiltinByDefault(t *testing.T) {
	st := newTestFonts(t).State()
	if st.Current != "" {
		t.Errorf("既定で %q が選ばれている", st.Current)
	}
	if st.Face != nil {
		t.Errorf("同梱なのにフォントを返した: %+v", st.Face)
	}
	if len(st.Fonts) != 0 {
		t.Errorf("登録が無いのに %d 件返った", len(st.Fonts))
	}
	// **既定の名前と要る字は Go 側が解決する**（フロントに書かせない）。
	if st.BuiltinName == "" || st.Required == "" {
		t.Errorf("既定の名前 / 要る字が空: %+v", st)
	}
}

// 登録すると、そのまま使う状態になり、焼いたフォントが返ること。
//
// **登録しただけで使わない、という状態を作らない**（登録は「この字で見たい」）。
func TestFontAddUsesIt(t *testing.T) {
	f := anyUsableFont(t)
	s := newTestFonts(t)

	st, err := s.Add(f.Path, f.Index)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Fonts) != 1 {
		t.Fatalf("登録数 = %d, want 1", len(st.Fonts))
	}
	if st.Current != st.Fonts[0].ID {
		t.Errorf("登録したのに使われていない: current=%q id=%q", st.Current, st.Fonts[0].ID)
	}
	if st.Note != "" {
		t.Errorf("焼けたはずなのに理由が出ている: %q", st.Note)
	}
	if st.Face == nil {
		t.Fatal("焼いたフォントが返っていない")
	}
	// ⚠️ **family は同梱と必ず違うこと**（同名で上書きすると同梱に戻せなくなる）。
	if st.Face.Family == "ShogiSFEN" {
		t.Errorf("同梱と同じ family: %q", st.Face.Family)
	}
	if !strings.HasPrefix(st.Face.DataURL, "data:font/ttf;base64,") {
		t.Errorf("data URL の形が違う: %.40q", st.Face.DataURL)
	}
	// 行には元フォントの名前と、使えることが出ている。
	row := st.Fonts[0]
	if !row.OK || row.Note != "" || row.Missing != "" {
		t.Errorf("使えるはずの行に理由が出ている: %+v", row)
	}
	if row.Name == "" || row.File == "" {
		t.Errorf("行の中身が埋まっていない: %+v", row)
	}
	if row.Custom {
		t.Error("名前を付けていないのに Custom が立っている")
	}

	// 同じ書体をもう一度足しても増えないこと（**同じ字を 2 つ登録しても意味が無い**）。
	again, err := s.Add(f.Path, f.Index)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Fonts) != 1 {
		t.Errorf("同じ書体で登録が増えた: %d 件", len(again.Fonts))
	}
}

// 同梱へ戻せること、戻したあとも登録は残ること。
func TestFontUseBuiltin(t *testing.T) {
	f := anyUsableFont(t)
	s := newTestFonts(t)
	if _, err := s.Add(f.Path, f.Index); err != nil {
		t.Fatal(err)
	}

	st, err := s.Use("")
	if err != nil {
		t.Fatal(err)
	}
	if st.Current != "" || st.Face != nil {
		t.Errorf("同梱に戻っていない: current=%q face=%+v", st.Current, st.Face)
	}
	if len(st.Fonts) != 1 {
		t.Errorf("同梱に戻したら登録まで消えた: %d 件", len(st.Fonts))
	}

	// 選び直せること。
	st, err = s.Use(st.Fonts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Face == nil {
		t.Error("選び直したのにフォントが返らない")
	}
	// 無い登録は断る（**黙って同梱に落とさない** —— 押した操作が
	// 何も起きなかったように見える）。
	if _, err := s.Use("font-999"); err == nil {
		t.Error("無い登録を選べてしまった")
	}
}

// ⚠️ **使っていた登録を消したら同梱に戻すこと。**
// 戻さないと「一覧に無いものを選んでいる」状態になる。
func TestFontRemoveFallsBackToBuiltin(t *testing.T) {
	f := anyUsableFont(t)
	s := newTestFonts(t)
	st, err := s.Add(f.Path, f.Index)
	if err != nil {
		t.Fatal(err)
	}
	id := st.Fonts[0].ID

	st, err = s.Remove(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Fonts) != 0 {
		t.Errorf("消えていない: %d 件", len(st.Fonts))
	}
	if st.Current != "" || st.Face != nil {
		t.Errorf("同梱に戻っていない: current=%q face=%+v", st.Current, st.Face)
	}
	if _, err := s.Remove(id); err == nil {
		t.Error("無い登録を消せてしまった")
	}
}

// 名前を付け替えられること、空にすると元フォントの名前へ戻ること。
func TestFontRename(t *testing.T) {
	f := anyUsableFont(t)
	s := newTestFonts(t)
	st, err := s.Add(f.Path, f.Index)
	if err != nil {
		t.Fatal(err)
	}
	id, def := st.Fonts[0].ID, st.Fonts[0].Name

	st, err = s.Rename(id, "楷書")
	if err != nil {
		t.Fatal(err)
	}
	if st.Fonts[0].Name != "楷書" || !st.Fonts[0].Custom {
		t.Errorf("付けた名前が反映されていない: %+v", st.Fonts[0])
	}
	// ⚠️ **名前を変えても使う字は変わらない**（family は ID から作るため）。
	if st.Face == nil {
		t.Error("名前を変えたらフォントが外れた")
	}

	st, err = s.Rename(id, "   ")
	if err != nil {
		t.Fatal(err)
	}
	if st.Fonts[0].Name != def || st.Fonts[0].Custom {
		t.Errorf("空にしても既定の名前へ戻らない: %+v", st.Fonts[0])
	}
}

// 消えたフォントを指していても、**同梱に落ちるだけで盤は描けること**（設計原則3）。
func TestFontStateMissingFile(t *testing.T) {
	s := newTestFonts(t)
	_, err := s.settings.editConfig(func(cfg *ikkyoku.Config) {
		cfg.PieceFonts = []ikkyoku.PieceFontEntry{{
			ID: "font-1", Path: filepath.Join(t.TempDir(), "消えた.ttf"), Source: "消えた書体",
		}}
		cfg.PieceFont = "font-1"
	})
	if err != nil {
		t.Fatal(err)
	}

	st := s.State()
	if st.Face != nil {
		t.Error("読めないフォントを焼いて返した")
	}
	if st.Note == "" {
		t.Error("同梱で描いている理由が出ていない")
	}
	// **登録は残す**（フォントを入れ直せば戻る）。行に理由が出るだけ。
	if len(st.Fonts) != 1 || st.Fonts[0].OK || st.Fonts[0].Note == "" {
		t.Errorf("登録が消えたか、理由が出ていない: %+v", st.Fonts)
	}
	// ⚠️ **選択は残すこと。** 一覧にその登録はあるので、同梱を選んでいるように
	// 見せると**どれを選んだのか分からなくなる**（入れ直せばそのまま戻る）。
	if st.Current != "font-1" {
		t.Errorf("選択まで捨てている: %q", st.Current)
	}
}

// ⚠️ **一覧に無い ID を指しているときだけは同梱として返すこと。**
// 設定ファイルは手で編集する前提でもあり、指し先が無いまま返すと
// 画面が「一覧に無いものを選んでいる」状態になる。
func TestFontStateDanglingSelection(t *testing.T) {
	s := newTestFonts(t)
	if _, err := s.settings.editConfig(func(cfg *ikkyoku.Config) {
		cfg.PieceFont = "font-9"
	}); err != nil {
		t.Fatal(err)
	}
	st := s.State()
	if st.Current != "" || st.Face != nil {
		t.Errorf("同梱として返っていない: current=%q face=%+v", st.Current, st.Face)
	}
	if st.Note != "" {
		t.Errorf("同梱そのものなので理由は要らない: %q", st.Note)
	}
}

// 駒の字が足りないフォントは登録できないこと（**理由に足りない字を出す**）。
func TestFontAddRejectsIncomplete(t *testing.T) {
	fonts, err := piecefont.List()
	if err != nil {
		t.Skipf("フォントを列挙できない: %v", err)
	}
	var bad piecefont.Font
	for _, f := range fonts {
		if !f.Usable() {
			bad = f
			break
		}
	}
	if bad.Path == "" {
		t.Skip("駒の字が足りないフォントが端末に無い")
	}

	s := newTestFonts(t)
	_, err = s.Add(bad.Path, bad.Index)
	if err == nil {
		t.Fatal("字が足りないのに登録できた")
	}
	if !strings.Contains(err.Error(), "足りません") {
		t.Errorf("理由が分からない文言: %v", err)
	}
	if len(s.State().Fonts) != 0 {
		t.Error("断ったのに登録された")
	}
}

// 一覧は**字が足りないものも返す**（選べない見た目にするだけ）。
// ⚠️ **消すと、探しているのか対象外なのかが画面から分からない。**
func TestFontScanKeepsUnusable(t *testing.T) {
	s := newTestFonts(t)
	scan, err := s.Scan()
	if err != nil {
		t.Skipf("フォントを列挙できない: %v", err)
	}
	if len(scan.Fonts) == 0 {
		t.Skip("端末にフォントが 1 つも無い")
	}
	if len(scan.Dirs) == 0 {
		t.Error("探した場所を返していない")
	}
	usable, unusable := 0, 0
	for _, f := range scan.Fonts {
		if f.Missing == "" {
			usable++
		} else {
			unusable++
		}
	}
	if usable != scan.Usable {
		t.Errorf("Usable = %d, 実際に使えるのは %d", scan.Usable, usable)
	}
	if unusable == 0 {
		t.Log("字が足りないフォントが 1 つも無い端末（普通は大量にある）")
	}
}

// 登録済みの書体には印が付くこと（同じものをもう一度足させない）。
func TestFontScanMarksRegistered(t *testing.T) {
	f := anyUsableFont(t)
	s := newTestFonts(t)
	if _, err := s.Add(f.Path, f.Index); err != nil {
		t.Fatal(err)
	}
	scan, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range scan.Fonts {
		if c.Path == f.Path && c.Index == f.Index {
			if !c.Registered {
				t.Error("登録済みなのに印が付いていない")
			}
			return
		}
	}
	t.Error("登録した書体が一覧に出てこない")
}

// プレビューは登録せずに焼けること、**family が毎回変わること**。
//
// ⚠️ 同じ名前で焼き直すと、どちらが当たるかがブラウザ任せになり、
// **プレビューが前のフォントのまま**に見えることがある。
func TestFontPreview(t *testing.T) {
	f := anyUsableFont(t)
	s := newTestFonts(t)

	a, err := s.Preview(f.Path, f.Index)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Preview(f.Path, f.Index)
	if err != nil {
		t.Fatal(err)
	}
	if a.Family == b.Family {
		t.Errorf("プレビューの family が使い回されている: %q", a.Family)
	}
	if a.DataURL == "" || b.DataURL == "" {
		t.Error("プレビューが空")
	}
	// 登録はされないこと。
	if len(s.State().Fonts) != 0 {
		t.Error("プレビューしただけで登録された")
	}
}
