package ikkyoku

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	display := 1
	want := Config{
		Display: &display,
		Region:  &Region{X: 0, Y: 0, Width: 1920, Height: 1080},
		OutDir:  filepath.Join(dir, "captures"),
	}

	if err := SaveConfig(path, want); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got.Display == nil || *got.Display != *want.Display {
		t.Errorf("Display = %v, want %v", got.Display, *want.Display)
	}
	if got.Region == nil || *got.Region != *want.Region {
		t.Errorf("Region = %v, want %v", got.Region, *want.Region)
	}
	if got.OutDir != want.OutDir {
		t.Errorf("OutDir = %q, want %q", got.OutDir, want.OutDir)
	}
}

// 勝率バーのポナンザ定数が往復すること。
//
// ⚠️ **既定値（1500）をここに書き残さないこと。** 0 は「未設定」で、既定への
// 解決は `analyze.PonanzaConstantOr` の 1 か所（設定ファイルに 1500 を焼くと、
// 既定を変えたときにその設定ファイルだけ追従しない）。
func TestConfigPonanzaConstantRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(path, Config{PonanzaConstant: 600}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got.PonanzaConstant != 600 {
		t.Errorf("PonanzaConstant = %v, want 600", got.PonanzaConstant)
	}

	// 未設定は 0 のまま（既定への倒しはここではしない）。
	bare := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(bare, Config{}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	if got, err := LoadConfig(bare); err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	} else if got.PonanzaConstant != 0 {
		t.Errorf("PonanzaConstant = %v, want 0（未設定）", got.PonanzaConstant)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil (ファイル無しはエラーにしない)", err)
	}
	// Config は map（Engine.Options）を含むので == で比べられない。
	if !reflect.DeepEqual(got, Config{}) {
		t.Errorf("LoadConfig() = %+v, want zero value", got)
	}
}

// エンジンの設定が往復すること。**設定ファイルは手で編集する前提**でもあるので、
// 書いたものがそのまま読めることを固定しておく。
func TestConfigEngineRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	want := Config{Engines: []EngineEntry{
		{ID: "engine-1", Name: "やねうら王", Path: filepath.Join(dir, "engine.exe"),
			Options: map[string]string{"USI_Hash": "1024", "Threads": "4"}, Enabled: true},
		// **外した登録も残ること。** 入れ替えて比べる作業では戻すことが多い。
		{ID: "engine-2", Path: filepath.Join(dir, "other.exe")},
	}}
	if err := SaveConfig(path, want); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !reflect.DeepEqual(got.Engines, want.Engines) {
		t.Errorf("Engines = %+v, want %+v", got.Engines, want.Engines)
	}
	if en := got.EnabledEngines(); len(en) != 1 || en[0].ID != "engine-1" {
		t.Errorf("EnabledEngines() = %+v, want engine-1 だけ", en)
	}
}

// エンジンが宣言した option（`optionSpecs`）が設定ファイルに残ること。
//
// **控えておかないと、設定タブを開くたびにエンジンを起こす**ことになる
// （NNUE の読み込みで数秒かかるものがある）。⚠️ **値（`options`）とは別物**なので、
// 両方が往復することを見ている。
func TestConfigEngineOptionSpecsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	want := Config{Engines: []EngineEntry{{
		ID: "engine-1", Path: filepath.Join(dir, "engine.exe"), Enabled: true,
		Options: map[string]string{"USI_Hash": "1024"},
		OptionSpecs: []EngineOption{
			{Name: "USI_Hash", Type: "spin", Default: "256", Min: 1, Max: 33554432, HasMin: true, HasMax: true},
			{Name: "USI_Ponder", Type: "check", Default: "false"},
			{Name: "EvalDir", Type: "string", Default: "eval"},
			{Name: "BookMoves", Type: "combo", Default: "no_book", Vars: []string{"no_book", "standard_book"}},
			{Name: "Clear Hash", Type: "button"},
		},
	}}}
	if err := SaveConfig(path, want); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !reflect.DeepEqual(got.Engines, want.Engines) {
		t.Fatalf("Engines = %+v, want %+v", got.Engines, want.Engines)
	}

	e := got.Engines[0]
	// 設定してある値はその値、していない値は**宣言された既定値**。
	// ⚠️ **「空なら既定」を呼び出し側に書かせないための解決**なので、両方見る。
	spec, ok := e.OptionSpec("USI_Hash")
	if !ok {
		t.Fatal("OptionSpec(USI_Hash) が引けない")
	}
	if v, custom := e.OptionValue(spec); v != "1024" || !custom {
		t.Errorf("OptionValue(USI_Hash) = %q, %v, want \"1024\", true", v, custom)
	}
	spec, _ = e.OptionSpec("EvalDir")
	if v, custom := e.OptionValue(spec); v != "eval" || custom {
		t.Errorf("OptionValue(EvalDir) = %q, %v, want 既定の \"eval\", false", v, custom)
	}
	if _, ok := e.OptionSpec("NoSuchOption"); ok {
		t.Error("宣言に無い名前が引けてしまった")
	}
	// button は「押すだけ」。値を持つ項目と混ぜないための印。
	if spec, _ := e.OptionSpec("Clear Hash"); !spec.IsButton() {
		t.Error("Clear Hash が button と判定されない")
	}
}

// 登録が 1 つも無いときは**同梱エンジン 1 つ**として振る舞うこと。
// 設定ファイルを作っていない状態でも解析できる、という既定の挙動。
func TestEngineListDefaultsToBuiltin(t *testing.T) {
	got := Config{}.EngineList()
	if len(got) != 1 || got[0].Path != "" || !got[0].Enabled {
		t.Fatalf("EngineList() = %+v, want 同梱エンジン 1 つ（有効）", got)
	}
	if name := got[0].DisplayName(); name != BuiltinEngineName {
		t.Errorf("DisplayName() = %q, want %q", name, BuiltinEngineName)
	}
	if len(Config{}.EnabledEngines()) != 1 {
		t.Errorf("EnabledEngines() が空。既定で同梱エンジンが使えなくなっている")
	}
}

// 旧形式（engine.path / engine.options）が読み込み時に engines へ移ること。
//
// **移行を落とすと、設定してあった外部エンジンが黙って同梱に戻る**（画面には
// 何も出ない）ので、ここで固定しておく。
func TestLoadConfigMigratesLegacyEngine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	legacy := `{"engine":{"path":"C:/shogi/YaneuraOu.exe","options":{"Threads":"4"}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got.Engine != nil {
		t.Errorf("Engine = %+v, want nil（移したら旧欄は捨てる）", got.Engine)
	}
	if len(got.Engines) != 1 {
		t.Fatalf("Engines = %+v, want 1 件", got.Engines)
	}
	e := got.Engines[0]
	if e.Path != "C:/shogi/YaneuraOu.exe" || e.Options["Threads"] != "4" || !e.Enabled || e.ID == "" {
		t.Errorf("Engines[0] = %+v, want 旧設定をそのまま引き継いだ有効なエントリ", e)
	}

	// 保存し直したら、ファイルからも旧欄が消えること。
	if err := SaveConfig(path, got); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(b), `"engine"`) {
		t.Errorf("保存後も旧欄が残っている: %s", b)
	}
}

// engines が既にあれば旧欄は無視すること（手で両方書いたときは新しいほうが正）。
func TestLoadConfigPrefersEngines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	both := `{"engine":{"path":"old.exe"},"engines":[{"id":"engine-1","path":"new.exe","enabled":true}]}`
	if err := os.WriteFile(path, []byte(both), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(got.Engines) != 1 || got.Engines[0].Path != "new.exe" {
		t.Errorf("Engines = %+v, want new.exe だけ", got.Engines)
	}
}

// エンジンの ID を**発行する**こと（2026-09-16）。
//
// ⚠️ **空いた番号を使い回さないこと**がここの要点。以前は空きを順に探していたので
// **`engine-2` を消して別のエンジンを足すとまた `engine-2`** になった。
// **検討の控えが評価値をこの ID で指す**ようになったので、使い回すと
// **前のエンジンの評価値が別のエンジンのものとして読まれる**（画面を見ても
// 気づけない壊れ方）。
func TestNextEngineID(t *testing.T) {
	engines := []EngineEntry{{ID: "engine-1"}, {ID: "engine-3"}}
	got := NextEngineID(engines)
	// ⚠️ **空いている "engine-2" を拾わないこと。**
	if got == "engine-2" {
		t.Fatalf("空いた番号を使い回しています: %q", got)
	}
	for _, e := range engines {
		if got == e.ID {
			t.Fatalf("既存とぶつかっています: %q", got)
		}
	}
	// 続けて発行しても重ならないこと。
	seen := map[string]bool{got: true}
	for i := 0; i < 50; i++ {
		id := NextEngineID(engines)
		if seen[id] {
			t.Fatalf("同じ ID を 2 回発行しています: %q", id)
		}
		seen[id] = true
	}
}

func TestDefaultConfigPath(t *testing.T) {
	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath() error = %v", err)
	}
	if filepath.Base(path) != "config.json" {
		t.Errorf("DefaultConfigPath() = %q, want basename %q", path, "config.json")
	}
}

// TestEngineDisplayColor は**折れ線の色の既定の解決**を固定する（2026-08-14）。
//
// ⚠️ **「空なら登録順の既定色」を呼び出し側に書かせない**（DisplayName と同じ）。
// 2 か所に持つと、画面から選べる色と既定で付く色が食い違う。
func TestEngineDisplayColor(t *testing.T) {
	if got := (EngineEntry{Color: "#123456"}).DisplayColor(3); got != "#123456" {
		t.Errorf("指定した色 = %q, want #123456", got)
	}
	if got := (EngineEntry{}).DisplayColor(1); got != EngineColors[1].Value {
		t.Errorf("既定色 = %q, want %q", got, EngineColors[1].Value)
	}
	// 一覧を超えたら回す（登録の数に上限は無い）。
	n := len(EngineColors)
	if got := (EngineEntry{}).DisplayColor(n); got != EngineColors[0].Value {
		t.Errorf("%d 番目の既定色 = %q, want %q", n, got, EngineColors[0].Value)
	}
}

// 「考える秒数」が設定ファイルに残ること（2026-08-15）。
//
// ⚠️ **一番の要点は「無制限（0）」が消えないこと。** 値で持って `omitempty` を
// 付けると、**0 を選んだ設定がファイルから消えて次の起動で既定（3 秒）に戻る**
// （しかも画面では「設定が効いていない」としか分からない）。だからポインタ。
func TestConfigAnalyzeSecondsRoundTrip(t *testing.T) {
	dir := t.TempDir()

	for _, tt := range []struct {
		name string
		set  *int
		want int
	}{
		{"未設定なら既定", nil, DefaultAnalyzeSeconds},
		{"選んだ秒数が残る", ptr(7), 7},
		{"無制限(0)も残る", ptr(0), 0},
		{"壊れた値は無制限に倒す", ptr(-5), 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".json")
			if err := SaveConfig(path, Config{AnalyzeSeconds: tt.set}); err != nil {
				t.Fatalf("SaveConfig() error = %v", err)
			}
			got, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if n := got.ThinkSeconds(); n != tt.want {
				t.Errorf("ThinkSeconds() = %d, want %d", n, tt.want)
			}
		})
	}
}

func ptr(v int) *int { return &v }

// 駒フォントの登録が往復すること。
//
// **端末のフォントから焼く駒の字**（`piecefont`）の設定。焼いた TTF は
// 持たないので、残るのは「元フォントのどれを使うか」だけ。
func TestConfigPieceFontRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	want := Config{
		PieceFonts: []PieceFontEntry{
			{ID: "font-1", Path: `C:\Windows\Fonts\msmincho.ttc`, Index: 1, Source: "ＭＳ Ｐ明朝"},
			{ID: "font-2", Name: "楷書", Path: `C:\Users\me\fonts\kaisho.ttf`},
		},
		PieceFont: "font-2",
	}
	if err := SaveConfig(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.PieceFonts, want.PieceFonts) {
		t.Errorf("PieceFonts = %+v, want %+v", got.PieceFonts, want.PieceFonts)
	}
	// ⚠️ **TTC の書体番号が落ちると、別の書体で焼く**（msmincho.ttc の
	// MS 明朝と MS P明朝が入れ替わる）。しかも画面では気づきにくい。
	if got.PieceFonts[0].Index != 1 {
		t.Errorf("書体番号が落ちた: %d", got.PieceFonts[0].Index)
	}
	if got.PieceFont != "font-2" {
		t.Errorf("選択が残っていない: %q", got.PieceFont)
	}
}

// 選択の解決。**空なら同梱**で、消えた登録を指していても同梱に落ちるだけ。
func TestCurrentPieceFont(t *testing.T) {
	c := Config{PieceFonts: []PieceFontEntry{{ID: "font-1", Path: "a.ttf"}}}

	if _, ok := c.CurrentPieceFont(); ok {
		t.Error("空なら同梱のはず")
	}
	c.PieceFont = "font-1"
	got, ok := c.CurrentPieceFont()
	if !ok || got.Path != "a.ttf" {
		t.Errorf("選択を引けない: %+v ok=%v", got, ok)
	}
	// ⚠️ **無い ID を指していてもエラーにしない**（設定ファイルは手で編集する
	// 前提でもある。指し先が無いだけで盤が描けなくなるのは行き過ぎ）。
	c.PieceFont = "font-9"
	if _, ok := c.CurrentPieceFont(); ok {
		t.Error("無い登録を指しているのに引けてしまった")
	}
}

// 表示名は「空なら元フォントの名前」。**この解決は 1 か所だけ。**
func TestPieceFontDisplayName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry PieceFontEntry
		want  string
	}{
		{"付けた名前が優先", PieceFontEntry{Name: "楷書", Source: "玉ねぎ楷書"}, "楷書"},
		{"空なら元フォントの名前", PieceFontEntry{Source: "玉ねぎ楷書"}, "玉ねぎ楷書"},
		{"それも無ければファイル名", PieceFontEntry{Path: `C:\fonts\kaisho.ttf`}, "kaisho.ttf"},
		{"空白だけの名前は付けていない扱い", PieceFontEntry{Name: "  ", Source: "游明朝"}, "游明朝"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.entry.DisplayName(); got != tc.want {
				t.Errorf("DisplayName = %q, want %q", got, tc.want)
			}
		})
	}
}

// family 名は登録ごとに違い、**同梱の ShogiSFEN とも必ず違う**こと。
//
// ⚠️ **同名で上書き登録すると同梱に戻せなくなる**（どれが当たるかは
// ブラウザ任せになる）。
func TestPieceFontFamily(t *testing.T) {
	a, b := PieceFontFamily("font-1"), PieceFontFamily("font-2")
	if a == b {
		t.Errorf("登録が違うのに family が同じ: %q", a)
	}
	for _, f := range []string{a, b} {
		if f == "ShogiSFEN" || !strings.Contains(f, "font-") {
			t.Errorf("family が不適切: %q", f)
		}
	}
}

// ⚠️ **エンジンと違って、空のときに既定を差し込まないこと。**
// 登録が無くても同梱で描けるので、「登録が 1 つある」ように見せる理由が無い。
func TestPieceFontListStaysEmpty(t *testing.T) {
	if got := (Config{}).PieceFontList(); len(got) != 0 {
		t.Errorf("空の設定で %d 件返った", len(got))
	}
}

func TestNextPieceFontID(t *testing.T) {
	if got := NextPieceFontID(nil); got != "font-1" {
		t.Errorf("NextPieceFontID(nil) = %q", got)
	}
	fonts := []PieceFontEntry{{ID: "font-1"}, {ID: "font-3"}}
	if got := NextPieceFontID(fonts); got != "font-2" {
		t.Errorf("空いている番号を使っていない: %q", got)
	}
}

// TestNormalizeEvalAxis は評価値グラフの縦軸の語彙を固定する（2026-09-13）。
//
// ⚠️ **知らない値も空も「素の評価値」に倒すこと。** 打ち間違いや将来の版で
// 書かれた値を読んだときに勝率や圧縮へ倒れると、**設定を触っていないのに
// 縦軸が変わって見える**（折れ線の形そのものが変わる）。
//
// ⚠️ **綴りはフロントの `EvalMode` と同じ**（`evalgraph.ts`）。設定に入る
// 文字列なので、片方だけ変えると保存された軸が読めずに既定へ戻る。
func TestNormalizeEvalAxis(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", EvalAxisEval},
		{EvalAxisEval, EvalAxisEval},
		{EvalAxisScaled, EvalAxisScaled},
		{EvalAxisWinRate, EvalAxisWinRate},
		{"winRate", EvalAxisEval},
		{"あるはずのない軸", EvalAxisEval},
	} {
		if got := NormalizeEvalAxis(tc.in); got != tc.want {
			t.Errorf("NormalizeEvalAxis(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestConfigEvalAxisRoundTrip は選んだ縦軸が保存されて戻ること（2026-09-13）。
//
// ⚠️ **残ること自体が要点**（切り離しや帯の設定と同じ。画面の組み方の好みなので、
// 次の起動でも同じ軸で始まってほしい）。
func TestConfigEvalAxisRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(path, Config{EvalGraphAxis: EvalAxisScaled}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EvalGraphAxis != EvalAxisScaled {
		t.Errorf("EvalGraphAxis = %q, want %q", cfg.EvalGraphAxis, EvalAxisScaled)
	}
}

// TestNormalizeTheme は配色の指定の倒し方を固定する（2026-10-02）。
//
// ⚠️ **知らない値・空はダーク。** ライトを足す前の設定ファイルには項目が無いので、
// ここが崩れると**更新しただけで画面の色が変わる**。
func TestNormalizeTheme(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ThemeDark},
		{ThemeDark, ThemeDark},
		{ThemeLight, ThemeLight},
		{ThemeSystem, ThemeSystem},
		{"Light", ThemeDark},
		{"あるはずのない配色", ThemeDark},
	} {
		if got := NormalizeTheme(tc.in); got != tc.want {
			t.Errorf("NormalizeTheme(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// 選択肢の一覧は、どれも正規化して自分自身に戻ること（画面で選べるのに
	// 保存すると別の値になる、を防ぐ）。
	for _, o := range ThemeOptions {
		if got := NormalizeTheme(o.Value); got != o.Value {
			t.Errorf("ThemeOptions の %q が NormalizeTheme で %q になる", o.Value, got)
		}
	}
}

// TestConfigThemeRoundTrip は選んだ配色が保存されて戻ること（2026-10-02）。
func TestConfigThemeRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(path, Config{Theme: ThemeLight}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme != ThemeLight {
		t.Errorf("Theme = %q, want %q", cfg.Theme, ThemeLight)
	}
}

// 王/玉の解決。⚠️ **先後を分けられるのは玉だけ**（左馬は盤全体）。
func TestGyokuFor(t *testing.T) {
	for _, tc := range []struct {
		gyoku                string
		wantBlack, wantWhite bool
	}{
		{GyokuNone, false, false},
		{GyokuBlack, true, false},
		{GyokuWhite, false, true},
		{GyokuBoth, true, true},
		// ⚠️ **知らない値は「王のまま」。** `<shogi-board>` の属性は
		// 「知らない値なら両方」だが、あちらは属性が付いている時点で
		// 「玉を使う」と言っている。こちらは**使うかどうかも含めて表す欄**なので、
		// 打ち間違いで盤の字が勝手に変わってはいけない。
		{"gyoku", false, false},
		{"BLACK", false, false},
	} {
		t.Run(tc.gyoku, func(t *testing.T) {
			if got := GyokuFor(tc.gyoku, true); got != tc.wantBlack {
				t.Errorf("先手 = %v, want %v", got, tc.wantBlack)
			}
			if got := GyokuFor(tc.gyoku, false); got != tc.wantWhite {
				t.Errorf("後手 = %v, want %v", got, tc.wantWhite)
			}
		})
	}
}

// 選択肢の値は `<shogi-board>` の `gyoku` 属性の語彙そのままであること。
//
// ⚠️ **別の語彙にすると変換表を挟むことになり、片方だけ直したときに黙って食い違う。**
func TestGyokuOptions(t *testing.T) {
	seen := map[string]bool{}
	for _, o := range GyokuOptions {
		if o.Label == "" {
			t.Errorf("%q に文言が無い", o.Value)
		}
		if seen[o.Value] {
			t.Errorf("値が重複している: %q", o.Value)
		}
		seen[o.Value] = true
		if got := NormalizeGyoku(o.Value); got != o.Value {
			t.Errorf("選択肢 %q が正規化で %q に変わる", o.Value, got)
		}
	}
	for _, v := range []string{GyokuNone, GyokuBlack, GyokuWhite, GyokuBoth} {
		if !seen[v] {
			t.Errorf("%q が選択肢に無い", v)
		}
	}
}

// 王/玉・左馬の設定が往復すること。
func TestConfigGyokuRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(path, Config{Gyoku: GyokuWhite, HidariUma: true}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gyoku != GyokuWhite || !got.HidariUma {
		t.Errorf("往復していない: gyoku=%q hidariUma=%v", got.Gyoku, got.HidariUma)
	}
}

// 駒の字の色。⚠️ **色と濃さは 1 つの値にまとめて返すこと** ——
// 別々に配ると、HTML で駒を描く側は element の opacity を使うことになり
// **駒の背景（木地）ごと透ける**。
func TestPieceInk(t *testing.T) {
	for _, tc := range []struct {
		name    string
		color   string
		opacity float64
		want    string
	}{
		// そのままの濃さなら 16 進のまま（設定ファイルにも画面にも読みやすい）。
		{"既定", "", 0, "#1a1a1a"},
		{"色だけ", "#3b2a1a", 0, "#3b2a1a"},
		{"薄くする", "#1a1a1a", 0.75, "rgba(26, 26, 26, 0.75)"},
		{"色と濃さ", "#804000", 0.5, "rgba(128, 64, 0, 0.5)"},
		{"大文字も読む", "#ABCDEF", 0, "#abcdef"},
		{"#rgb も読む", "#abc", 0, "#aabbcc"},
		// ⚠️ **読めない色は既定に戻す**（弾いて空を返さない。駒が消える）。
		{"読めない色", "まっくろ", 0, "#1a1a1a"},
		{"# が無い", "1a1a1a", 0, "#1a1a1a"},
		// ⚠️ **範囲外は丸める。** 0 まで許すと駒が消えて盤が壊れたようにしか
		// 見えず、戻し方も分からなくなる。
		{"薄すぎ", "#1a1a1a", 0.01, "rgba(26, 26, 26, 0.2)"},
		{"濃すぎ", "#1a1a1a", 5, "#1a1a1a"},
		{"負", "#1a1a1a", -1, "#1a1a1a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PieceInk(tc.color, tc.opacity); got != tc.want {
				t.Errorf("PieceInk(%q, %v) = %q, want %q", tc.color, tc.opacity, got, tc.want)
			}
		})
	}
}

// 既定は core/web の `--shogi-piece-color` の既定と同じであること。
//
// ⚠️ **食い違うと、設定を触っていないのに駒の色が変わる**（ikkyoku が
// 変数を当てた瞬間に別の色になる）。**向こうを変えたらここも直す。**
func TestDefaultPieceColorMatchesWeb(t *testing.T) {
	if DefaultPieceColor != "#1a1a1a" {
		t.Errorf("core/web の既定と違う: %q", DefaultPieceColor)
	}
	if got := PieceInk("", 0); got != DefaultPieceColor {
		t.Errorf("既定の解決が %q になっている", got)
	}
}

// 駒の字の色と濃さが往復すること。
func TestConfigPieceInkRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(path, Config{PieceColor: "#3b2a1a", PieceOpacity: 0.7}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.PieceColor != "#3b2a1a" || got.PieceOpacity != 0.7 {
		t.Errorf("往復していない: color=%q opacity=%v", got.PieceColor, got.PieceOpacity)
	}
}

// SutemeSourceOr は**空・未知の値を auto に倒す**(設定ファイルは手で編集する前提で、
// 綴り間違いでアプリが認識できなくなるより既定へ倒す)。
func TestConfigSutemeSourceOr(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", SutemeSourceAuto},
		{"auto", SutemeSourceAuto},
		{"dir", SutemeSourceDir},
		{"model", SutemeSourceModel},
		{"embed", SutemeSourceEmbed},
		{"EMBED", SutemeSourceAuto},
		{"でたらめ", SutemeSourceAuto},
	}
	for _, tt := range tests {
		c := Config{SutemeSource: tt.in}
		if got := c.SutemeSourceOr(); got != tt.want {
			t.Errorf("Config{SutemeSource: %q}.SutemeSourceOr() = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// 配布モデルの置き場所（2026-10-04）。**空なら既定、指定があればそれ**で、既定は
// **exe の隣ではない**こと（Program Files には書けない）・設定の %APPDATA% とも別の
// %LOCALAPPDATA% の下であること。⚠️ ここが崩れると、置いたモデルを黙って読まなくなる。
func TestConfigModelDir(t *testing.T) {
	def, err := DefaultModelDir()
	if err != nil {
		t.Skip(err)
	}
	cache, _ := os.UserCacheDir()
	if filepath.Dir(filepath.Dir(def)) != cache || filepath.Base(def) != "model" {
		t.Fatalf("既定 = %q（%s の下の ikkyoku/model であること）", def, cache)
	}
	if got, _ := (Config{}).ModelDir(); got != def {
		t.Fatalf("空なら既定: %q", got)
	}
	if got, _ := (Config{SutemeModelDir: "D:/models"}).ModelDir(); got != "D:/models" {
		t.Fatalf("指定が効いていない: %q", got)
	}
}

// 棋譜データベースの場所。**「空なら既定」の解決は Config.KifuDB の 1 か所。**
//
// ⚠️ **既定は kicho アプリのものとは別**（os.UserConfigDir()/ikkyoku/kicho.db）。
// 同じ SQLite ファイルを 2 プロセスから書くと `database is locked` になりうるので、
// **共用はユーザーが設定で指定したときだけ**にしてある。
func TestConfigKifuDB(t *testing.T) {
	// 指定してあればそのまま。
	c := Config{KifuDBPath: `D:\shogi\kicho.db`}
	got, err := c.KifuDB()
	if err != nil {
		t.Fatalf("KifuDB: %v", err)
	}
	if got != `D:\shogi\kicho.db` {
		t.Errorf("指定したパスが使われていません: %q", got)
	}

	// 空なら既定（ikkyoku 配下）。
	def, err := Config{}.KifuDB()
	if err != nil {
		t.Fatalf("KifuDB(既定): %v", err)
	}
	if filepath.Base(def) != "kicho.db" || filepath.Base(filepath.Dir(def)) != "ikkyoku" {
		t.Errorf("既定の場所が違います: %q", def)
	}
	// ⚠️ **kicho アプリの既定を指さないこと**（同じ DB を 2 プロセスから開かない）。
	if filepath.Base(filepath.Dir(def)) == "kicho" {
		t.Errorf("kicho の既定を指しています: %q", def)
	}

	// 空白だけのときも既定へ倒す（設定ファイルは手で編集する前提）。
	blank, err := Config{KifuDBPath: "  "}.KifuDB()
	if err != nil {
		t.Fatalf("KifuDB(空白): %v", err)
	}
	if blank != def {
		t.Errorf("空白が既定に倒れていません: %q", blank)
	}
}

// ⚠️ **表示名の優先順位** —— 人が付けた名前 → エンジンが名乗った名前 →
// exe のファイル名 → 「同梱エンジン」。
//
// 名乗りを既定にしてあるのは、`YaneuraOu_NNUE-tournament-clang++-avx2.exe` より
// エンジン自身の名乗りのほうが読めるから。⚠️ **人が付けた名前より前に出さないこと**
// —— 同じ exe を option 違いで 2 つ登録すると名乗りは同じになるので、
// **見分けが付くのは人が付けた名前だけ**。
func TestEngineDisplayNamePrefersReportedNameOverExe(t *testing.T) {
	exe := filepath.Join("D:", "ShogiEngine", "YaneuraOu_NNUE-tournament.exe")

	for _, tc := range []struct {
		label string
		e     EngineEntry
		want  string
	}{
		{"人が付けた名前が最優先",
			EngineEntry{Name: "水匠5(D12)", EngineName: "Suisho5", Path: exe}, "水匠5(D12)"},
		{"付けていなければ名乗り",
			EngineEntry{EngineName: "Suisho5", Path: exe}, "Suisho5"},
		{"名乗りも無ければファイル名",
			EngineEntry{Path: exe}, "YaneuraOu_NNUE-tournament.exe"},
		{"同梱は名乗りがあればそちら",
			EngineEntry{EngineName: "ikkyoku (engine 同梱)"}, "ikkyoku (engine 同梱)"},
		{"同梱で名乗りが無ければ既定の文言",
			EngineEntry{}, BuiltinEngineName},
	} {
		if got := tc.e.DisplayName(); got != tc.want {
			t.Errorf("%s: DisplayName = %q, want %q", tc.label, got, tc.want)
		}
	}
}

// 名乗りは設定ファイルに残る（繋がないと分からないので、消えると二度と出てこない）。
func TestEngineNameRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := Config{Engines: []EngineEntry{
		{ID: "engine-1", Path: "D:/e.exe", EngineName: "Suisho5", Enabled: true},
	}}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.EngineList()[0].EngineName != "Suisho5" {
		t.Fatalf("名乗りが残っていません: %+v", got.EngineList()[0])
	}
}

// 検討の控えの置き場（2026-09-16）。
//
// ⚠️ **棚（`KifuDB`）と同じ場所に倒さないこと。** あちらは「棋譜」の置き場で、
// こちらは**棋譜になる前の検討**も含めて残す場所。**同じ入れ物にすると、
// 中継を撮っている最中の検討が置けない**（棚の主キーは取得元）。
func TestConfigStudyDir(t *testing.T) {
	// 指定してあればそのまま。
	c := Config{StudyDirPath: `D:\shogi\studies`}
	got, err := c.StudyDir()
	if err != nil {
		t.Fatalf("StudyDir: %v", err)
	}
	if got != `D:\shogi\studies` {
		t.Errorf("指定したパスが使われていません: %q", got)
	}

	// 空なら既定（ikkyoku 配下の studies）。
	def, err := Config{}.StudyDir()
	if err != nil {
		t.Fatalf("StudyDir(既定): %v", err)
	}
	if filepath.Base(def) != "studies" || filepath.Base(filepath.Dir(def)) != "ikkyoku" {
		t.Errorf("既定の場所が違います: %q", def)
	}

	// 空白だけのときも既定へ倒す（設定ファイルは手で編集する前提）。
	blank, err := Config{StudyDirPath: "  "}.StudyDir()
	if err != nil {
		t.Fatalf("StudyDir(空白): %v", err)
	}
	if blank != def {
		t.Errorf("空白が既定に倒れていません: %q", blank)
	}

	// ⚠️ **棚と同じファイルを指さないこと。**
	db, err := Config{}.KifuDB()
	if err != nil {
		t.Fatalf("KifuDB: %v", err)
	}
	if def == db || def == filepath.Dir(db) {
		t.Errorf("棚と同じ場所を指しています: studies=%q db=%q", def, db)
	}
}

// 棋譜の自動更新の間隔（2026-09-26）。**画面には出さず設定ファイルで書くだけ**なので、
// 書いていない・壊れている値は既定（5 分）に倒すこと。
func TestConfigKifuFollowInterval(t *testing.T) {
	for _, tt := range []struct {
		minutes, want int
	}{
		{0, DefaultKifuFollowMinutes},
		{-3, DefaultKifuFollowMinutes},
		{1, 1},
		{10, 10},
	} {
		c := Config{KifuFollowMinutes: tt.minutes}
		if got := c.KifuFollowInterval(); got != tt.want {
			t.Errorf("KifuFollowInterval(%d) = %d, want %d", tt.minutes, got, tt.want)
		}
	}
	if DefaultKifuFollowMinutes != 5 {
		t.Errorf("既定が 5 分ではありません: %d", DefaultKifuFollowMinutes)
	}
}
