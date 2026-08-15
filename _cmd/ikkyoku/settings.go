package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/analyze"
	"github.com/ShinteLab/ikkyoku/training"
)

// AppSettings はメイン画面の設定タブに出す設定。
//
// **ikkyoku.Config そのものを返していない。** あちらは CLI 用の項目(領域・ディスプレイ番号)
// まで含む「設定ファイルの形」で、画面に出すものとは範囲が違う。ここは
// 「設定タブが読み書きするもの」だけを持つ。
type AppSettings struct {
	// FitOnStartup は起動時に盤面を探してガイド枠を合わせるか。
	FitOnStartup bool `json:"fitOnStartup"`
	// Training は訂正した局面を suteme へ登録する設定。
	Training TrainingSettings `json:"training"`
	// Engines は登録した USI エンジンの一覧（登録順）。
	//
	// ⚠️ **1 つに絞らない**（2026-08-11）。「解析に使う」を付けたものが
	// **同時に走って結果が並ぶ**ので、ここは常に一覧で扱う。
	Engines []EngineSettings `json:"engines"`
	// EngineColors は折れ線の色として**画面から選べる**色の一覧。
	//
	// ⚠️ **フロントに色の表を書かないこと**（既定色の解決も Go 側なので、
	// 2 つ持つと「選べる色」と「既定で付く色」が食い違う）。
	EngineColors []ikkyoku.EngineColorOption `json:"engineColors"`
	// AnalyzeSeconds は解析タブの「考える秒数」（**0 は無制限**）。
	//
	// **既定値（3）は解決済みで返る**（`ikkyoku.Config.ThinkSeconds`）。
	// ⚠️ **フロントに既定値を書かないこと。**
	AnalyzeSeconds int `json:"analyzeSeconds"`
	// PonanzaConstant は評価値 → 勝率の変換に使う定数（解析タブの勝率バー）。
	//
	// **既定値（1500）は解決済みで返る**（`analyze.PonanzaConstantOr`）。
	// ⚠️ **フロントに既定値を書かないこと** —— training の Host/Port と同じで、
	// 2 か所に持つと既定を変えたときに食い違う。
	PonanzaConstant float64 `json:"ponanzaConstant"`
	// Path は設定ファイルの場所。**表示のためだけ。** 手で編集したくなったときに
	// 探さずに済むよう出しておく(学習データの置き場所もこのファイルにある)。
	Path string `json:"path"`
}

// TrainingSettings は「訂正盤面を suteme に登録する」の設定。
//
// **ikkyoku.TrainingConfig をそのまま返していない。** 画面に出すのは
// 「実際に送る先(Target)」まで組み立てた形で、Host/Port の既定値の解決を
// フロントに持たせないため(既定は training パッケージが持つ)。
type TrainingSettings struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
	// Target は上の設定から組み立てた接続先("http://host:port")。**表示用。**
	Target string `json:"target"`
}

// EngineSettings は登録した USI エンジン 1 つ（設定タブの 1 行）。
//
// **「外部エンジンを使うか」の真偽値は持たない。** パスが空なら同梱のエンジン、
// 入っていれば外部エンジン。2 つ持つと「パスが入っているのに無効」という
// 食い違いが起きる（ikkyoku.EngineEntry の注記と同じ）。
type EngineSettings struct {
	// ID は一覧の中でこのエンジンを指す識別子（操作のときに渡す）。
	ID string `json:"id"`
	// Name は画面に出す名前。**空欄なら Go 側が解決した既定の名前が入る**
	// （パスのファイル名 / 「同梱エンジン」）。フロントで組み立てないこと。
	Name string `json:"name"`
	// Custom は名前を人が付けたか（false なら Name は既定の解決結果）。
	//
	// 入力欄に既定の名前を書き込んでしまうと、パスを変えても名前が追従しなく
	// なるので、フロントは Custom のときだけ Name を欄に入れる。
	Custom bool `json:"custom"`
	// Path は USI エンジンの実行ファイル。空なら同梱。
	Path string `json:"path"`
	// Builtin は同梱のエンジンを使う状態か（Path が空）。**表示用。**
	// フロントで `path === ""` を判定させないため（判断の基準を Go 側に置く）。
	Builtin bool `json:"builtin"`
	// OptionCount は既定から変えた setoption の数。**表示用**（折りたたみの見出し）。
	OptionCount int `json:"optionCount"`
	// Options はこのエンジンの設定項目（宣言順 → 宣言に無いものの順）。
	//
	// **エンジンに繋ぐまでは空**（宣言は繋がないと分からない）。`OptionsKnown` が
	// その区別で、⚠️ **フロントで `options.length === 0` を「宣言が無い」と
	// 読まないこと** —— 「まだ確かめていない」と「宣言が 1 つも無い（同梱エンジン）」は
	// 別の状態で、画面に出す文言も違う。
	Options []EngineOptionSettings `json:"options"`
	// OptionsKnown は option の宣言を読み込み済みか（「接続を確認」で入る）。
	OptionsKnown bool `json:"optionsKnown"`
	// MultiPV は候補手の本数（**解析タブのエンジンの見出しで変える**）。
	//
	// ⚠️ **`Options` の一覧には出さない**（編集口を 2 つにしない。`engineOptionSettings`）。
	// **既定は解決済みで返る**（`ikkyoku.DefaultMultiPV`）。
	MultiPV int `json:"multiPv"`
	// Enabled は解析に使うか。**外した登録も残る。**
	Enabled bool `json:"enabled"`
	// Color は評価値グラフの折れ線の色（`#rrggbb`）。
	//
	// **常に解決済みで返る**（未設定なら登録順の既定色）。⚠️ **フロントで
	// 「空なら既定」を書かないこと** —— Name / Host / Port と同じで、
	// 既定を 2 か所に持つと変えたときに食い違う。
	Color string `json:"color"`
}

// EngineOptionSettings は USI の option 1 つを画面に出す形にしたもの。
//
// **入力欄の形は Type で決まる**（check → チェックボックス、spin → 数値、
// combo → 選択、string/filename → テキスト、button → 押すだけ）。
// ⚠️ **フロントで型ごとの既定値や範囲を組み立てないこと** —— 宣言はエンジンごとに
// 違うので、写しを持つと必ず食い違う。
type EngineOptionSettings struct {
	Name string `json:"name"`
	// Type は "check" / "spin" / "combo" / "button" / "string" / "filename"。
	//
	// ⚠️ **宣言が無い（設定ファイルに手で書いた）ものは "string" で返す。**
	// 型が分からないだけで、値としては正当（宣言していない option を受け付ける
	// エンジンがある）。⚠️ **黙って捨てないこと。**
	Type string `json:"type"`
	// Value は今の値（設定に無ければ宣言された既定値）。
	Value string `json:"value"`
	// Default は宣言された既定値（**「既定に戻す」で戻る先**）。
	Default string `json:"default"`
	// Custom は人が既定から変えたか（＝設定ファイルに書いてあるか）。
	Custom bool `json:"custom"`
	// Known はエンジンが宣言している option か。
	//
	// false は「設定ファイルに書いてあるが、エンジンは宣言していない」。
	// **送りはする**（`core/usi/client.plannedOptions` が拾う）ので、
	// 画面でもそう出す（消す口だけ用意する）。
	Known bool `json:"known"`
	// Min / Max は spin の範囲（Has* が false なら宣言が無かった）。
	Min    int  `json:"min"`
	Max    int  `json:"max"`
	HasMin bool `json:"hasMin"`
	HasMax bool `json:"hasMax"`
	// Vars は combo の選択肢。
	Vars []string `json:"vars"`
}

// engineOptionSettings は 1 つの登録の option を画面に出す順に並べる。
//
// 並びは**エンジンが宣言した順**、そのあとに**宣言に無い値**（設定ファイルに
// 手で書いたもの・実行ファイルを差し替えて宣言だけ捨てたもの）を名前順で。
// ⚠️ **宣言の順を並べ替えないこと**（`EngineInfo.Declared` の注記）。
// ⚠️ **MultiPV はこの一覧に出さない**（2026-08-15）。**入口は解析タブの
// エンジンの見出し**（候補手を見ながら増やすものなので）。ここにも欄を置くと
// **同じ値の編集口が 2 つ**になり、どちらが今の値か分からなくなる。
// ⚠️ **値そのものは `Options` に入っている**（`EngineEntry.MultiPV`）。
// 「出さない」だけで「持たない」ではない。
func engineOptionSettings(e ikkyoku.EngineEntry) []EngineOptionSettings {
	out := make([]EngineOptionSettings, 0, len(e.OptionSpecs)+len(e.Options))
	seen := map[string]bool{ikkyoku.MultiPVOption: true}
	for _, o := range e.OptionSpecs {
		if o.Name == ikkyoku.MultiPVOption {
			continue
		}
		seen[o.Name] = true
		value, custom := e.OptionValue(o)
		out = append(out, EngineOptionSettings{
			Name: o.Name, Type: o.Type, Value: value, Default: o.Default,
			Custom: custom, Known: true,
			Min: o.Min, Max: o.Max, HasMin: o.HasMin, HasMax: o.HasMax, Vars: o.Vars,
		})
	}
	rest := make([]string, 0, len(e.Options))
	for name := range e.Options {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		out = append(out, EngineOptionSettings{
			Name: name, Type: "string", Value: e.Options[name], Custom: true,
		})
	}
	return out
}

// engineSettings は設定ファイルのエントリを画面に出す形にする。
//
// i は**一覧の中での位置**（未設定の色を登録順で決めるのに要る）。
func engineSettings(e ikkyoku.EngineEntry, i int) EngineSettings {
	opts := engineOptionSettings(e)
	custom := 0
	for _, o := range opts {
		if o.Custom {
			custom++
		}
	}
	return EngineSettings{
		ID:           e.ID,
		Name:         e.DisplayName(),
		Custom:       e.Name != "",
		Path:         e.Path,
		Builtin:      e.Path == "",
		OptionCount:  custom,
		Options:      opts,
		OptionsKnown: len(e.OptionSpecs) > 0,
		MultiPV:      e.MultiPV(),
		Enabled:      e.Enabled,
		Color:        e.DisplayColor(i),
	}
}

// SettingsService は設定ファイル(config.json)の読み書きを Wails にバインドする Service。
//
// CaptureService と分けてあるのは、あちらが「撮る」ことの責務だから。設定は
// 起動時にも読むので、**main() が最初に作って、読み込み済みの Config を配る**役でもある。
type SettingsService struct {
	logger *slog.Logger
	app    *application.App

	mu   sync.Mutex
	path string
	cfg  ikkyoku.Config
}

// NewSettingsService は設定を読み込んだ状態で作る。
// 読めなければゼロ値で続ける(設定ファイルが無いのは正常な状態)。
func NewSettingsService(logger *slog.Logger) *SettingsService {
	s := &SettingsService{logger: logger}
	path, err := ikkyoku.DefaultConfigPath()
	if err != nil {
		logger.Warn("設定ファイルの場所を決められませんでした", "error", err)
		return s
	}
	s.path = path
	cfg, err := ikkyoku.LoadConfig(path)
	if err != nil {
		logger.Warn("設定を読み込めませんでした", "path", path, "error", err)
		return s
	}
	s.cfg = cfg
	return s
}

func (s *SettingsService) bind(app *application.App) { s.app = app }

// Config は読み込み済みの設定を返す(起動シーケンス用。フロントには公開しない形)。
func (s *SettingsService) config() ikkyoku.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// Settings は設定タブに出す値を返す。
func (s *SettingsService) Settings() AppSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings()
}

// settings はロックを取った状態で呼ぶこと。
func (s *SettingsService) settings() AppSettings {
	list := s.cfg.EngineList()
	engines := make([]EngineSettings, 0, len(list))
	for i, e := range list {
		engines = append(engines, engineSettings(e, i))
	}
	return AppSettings{
		FitOnStartup:    s.cfg.FitOnStartup,
		Training:        trainingSettings(s.cfg.Training),
		Engines:         engines,
		EngineColors:    ikkyoku.EngineColors,
		AnalyzeSeconds:  s.cfg.ThinkSeconds(),
		PonanzaConstant: analyze.PonanzaConstantOr(s.cfg.PonanzaConstant),
		Path:            s.path,
	}
}

// enabledEngines は解析に使うエンジンを返す(AnalyzeService が使う)。
//
// ⚠️ **「空なら同梱」の解決はここではなく `ikkyoku.Config.EngineList`。**
// 判断を 2 か所に持たない。
func (s *SettingsService) enabledEngines() []ikkyoku.EngineEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.EnabledEngines()
}

// engineEntry は ID で 1 つ引く（「接続を確認」がこれ 1 つを繋ぐ）。
func (s *SettingsService) engineEntry(id string) (ikkyoku.EngineEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entryLocked(id)
}

// entryLocked は engineEntry の中身。**ロックを取った状態で呼ぶこと。**
func (s *SettingsService) entryLocked(id string) (ikkyoku.EngineEntry, bool) {
	for _, e := range s.cfg.EngineList() {
		if e.ID == id {
			return e, true
		}
	}
	return ikkyoku.EngineEntry{}, false
}

// editEngines は一覧を書き換えて保存する共通処理。**ロックを取った状態で呼ぶこと。**
//
// ⚠️ **書き換える前に `EngineList()` を通す**（＝登録が空なら同梱エンジン 1 件に
// 実体化してから触る）。そうしないと、既定の同梱エンジンの「解析に使う」を外した
// つもりが**何も保存されず、次に開くとまた有効に見える**。
func (s *SettingsService) editEngines(apply func([]ikkyoku.EngineEntry) []ikkyoku.EngineEntry) (AppSettings, error) {
	return s.save(func(cfg *ikkyoku.Config) {
		cfg.Engines = apply(cfg.EngineList())
	})
}

// checkEnginePath は指定された実行ファイルを検分して正規化する。
//
// ⚠️ **起動して繋がるかは確かめない。** 設定を保存する操作と、実際に繋がるかを見る
// 操作(AnalyzeService.CheckEngine)は別にしてある。まだ置いていないパスを先に
// 書いておく、という順序が普通にあるため(接続設定と同じ考え方)。
//
// 存在の確認だけはする。**打ち間違いは「解析を押したら繋がらない」より、
// ここで分かるほうが早い。**
func checkEnginePath(path string) (string, error) {
	p := strings.TrimSpace(strings.Trim(strings.TrimSpace(path), `"`))
	if p == "" {
		return "", nil
	}
	if info, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("その実行ファイルが見つかりません: %s", p)
	} else if info.IsDir() {
		return "", fmt.Errorf("ディレクトリではなく実行ファイルを指定してください: %s", p)
	}
	return p, nil
}

// AddEngine は一覧にエンジンを 1 つ足す（**空のパスなら同梱エンジン**）。
//
// **同じ実行ファイルを 2 つ登録できる。** option（置換表・スレッド数）を変えて
// 比べるのは正当な使い方なので、パスの重複では断らない。
func (s *SettingsService) AddEngine(path string) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := checkEnginePath(path)
	if err != nil {
		return s.settings(), err
	}
	return s.editEngines(func(list []ikkyoku.EngineEntry) []ikkyoku.EngineEntry {
		// 足したものは**そのまま使える状態**にする（追加したのに解析に出てこないと、
		// なぜ出ないのかを探すことになる）。
		return append(list, ikkyoku.EngineEntry{
			ID: ikkyoku.NextEngineID(list), Path: p, Enabled: true,
		})
	})
}

// pickEngineFile は実行ファイルを選ぶダイアログを出す（取り消したら空文字）。
//
// **パスを手で打たせない。** 将棋エンジンは深いディレクトリに置かれることが多く、
// 打ち間違いが一番起きやすい入口。startDir はダイアログの開き始めの場所。
func (s *SettingsService) pickEngineFile(startDir string) (string, error) {
	if s.app == nil {
		return "", fmt.Errorf("ダイアログを開けません")
	}
	dlg := s.app.Dialog.OpenFile()
	dlg.SetTitle("USI エンジンの実行ファイルを選ぶ")
	dlg.CanChooseFiles(true)
	dlg.CanChooseDirectories(false)
	if startDir != "" {
		dlg.SetDirectory(startDir)
	}
	if runtime.GOOS == "windows" {
		dlg.AddFilter("実行ファイル", "*.exe")
	}
	picked, err := dlg.PromptForSingleSelection()
	if err != nil {
		return "", fmt.Errorf("ファイルを選べませんでした: %w", err)
	}
	return picked, nil
}

// BrowseEngine は実行ファイルを選ぶダイアログを出し、選ばれたものを一覧に**足す**。
//
// 取り消したら何もしない。
func (s *SettingsService) BrowseEngine() (AppSettings, error) {
	// 既に登録があれば、最後に足したものの場所から開く(辿り直さずに済む)。
	start := ""
	if list := s.config().Engines; len(list) > 0 {
		if cur := list[len(list)-1].Path; cur != "" {
			start = filepath.Dir(cur)
		}
	}
	picked, err := s.pickEngineFile(start)
	if err != nil {
		return s.Settings(), err
	}
	if picked == "" {
		return s.Settings(), nil // 取り消し。**何も変えない。**
	}
	return s.AddEngine(picked)
}

// BrowseEngineFor は既存の登録の実行ファイルを**選び直す**（一覧の行の「参照…」）。
//
// ⚠️ **BrowseEngine（追加）と混ぜないこと。** 差し替えのつもりで押したら
// 登録が増えていた、という事故になる。
func (s *SettingsService) BrowseEngineFor(id string) (AppSettings, error) {
	entry, ok := s.engineEntry(id)
	if !ok {
		return s.Settings(), fmt.Errorf("そのエンジンの登録が見つかりません")
	}
	start := ""
	if entry.Path != "" {
		start = filepath.Dir(entry.Path)
	}
	picked, err := s.pickEngineFile(start)
	if err != nil {
		return s.Settings(), err
	}
	if picked == "" {
		return s.Settings(), nil // 取り消し。**何も変えない。**
	}
	return s.SetEnginePath(id, picked)
}

// SetEnginePath は登録済みエンジンの実行ファイルを差し替える。
//
// **空にするとその登録は同梱のエンジンになる**（登録そのものは消えない）。
func (s *SettingsService) SetEnginePath(id, path string) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := checkEnginePath(path)
	if err != nil {
		return s.settings(), err
	}
	return s.editEngines(func(list []ikkyoku.EngineEntry) []ikkyoku.EngineEntry {
		for i := range list {
			if list[i].ID != id || list[i].Path == p {
				continue
			}
			list[i].Path = p
			// ⚠️ **控えてある option の宣言は捨てる**（別のエンジンの宣言なので、
			// 残すと**違うエンジンの入力欄を出す**ことになる）。
			// **値のほうは捨てない** —— 置き場所を移しただけのことがあるうえ、
			// 人が書いた値を黙って消さない（宣言に無い値として画面に残る）。
			list[i].OptionSpecs = nil
		}
		return list
	})
}

// setEngineOptionSpecs はエンジンが宣言した option を控える（「接続を確認」の後）。
//
// ⚠️ **公開しない**（Service の公開メソッドはフロントの API になる）。宣言は
// **繋いで初めて分かる**ので、入口は `AnalyzeService.CheckEngine` の 1 つだけ。
//
// ⚠️ **控えるのは宣言だけで、値（`Options`）には触らない。** 人が書いた値を
// 繋ぎ直しただけで消さない（宣言に無い値は画面でもそう出る）。
func (s *SettingsService) setEngineOptionSpecs(id string, specs []ikkyoku.EngineOption) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.editEngines(func(list []ikkyoku.EngineEntry) []ikkyoku.EngineEntry {
		for i := range list {
			if list[i].ID == id {
				list[i].OptionSpecs = specs
			}
		}
		return list
	}); err != nil {
		s.logger.Warn("エンジンの option を控えられませんでした", "id", id, "error", err)
	}
}

// SetEngineOption は `setoption` で送る値を 1 つ決める（設定タブのエンジンの行）。
//
// **空文字にすると設定から消え、エンジンが宣言した既定値に戻る**（＝送られるのは
// 既定値。`core/usi/client.plannedOptions`）。⚠️ **既定と同じ値を書き込んだときも
// 消す** —— 書き残すと「エンジンの既定に従う」という指定ができなくなり、
// **エンジンのバージョンが上がって既定が変わっても古い値で固まる**（しかも
// 画面では気づけない）。
//
// ⚠️ **値の検分は宣言があるときだけ。** 宣言に無い名前も受ける（宣言していない
// option を受け付けるエンジンがあり、**設定ファイルを手で編集する経路を塞がない**）。
//
// ⚠️ **`isready` の前にしか効かない option なので、繋ぎ直しが要る。** 判断は
// `AnalyzeService.engineKey`（パス + options の指紋）が持っているので、
// **ここで接続を触らないこと。**
func (s *SettingsService) SetEngineOption(id, name, value string) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := strings.TrimSpace(name)
	if n == "" {
		return s.settings(), fmt.Errorf("option 名が空です")
	}
	entry, ok := s.entryLocked(id)
	if !ok {
		return s.settings(), fmt.Errorf("そのエンジンの登録が見つかりません")
	}
	v := strings.TrimSpace(value)
	spec, known := entry.OptionSpec(n)
	if known {
		var err error
		if v, err = checkOptionValue(spec, v); err != nil {
			return s.settings(), err
		}
	}
	return s.editEngines(func(list []ikkyoku.EngineEntry) []ikkyoku.EngineEntry {
		for i := range list {
			if list[i].ID != id {
				continue
			}
			// **既定に戻すときは行ごと消す**（上の注記）。
			if v == "" || (known && v == spec.Default) {
				delete(list[i].Options, n)
				if len(list[i].Options) == 0 {
					list[i].Options = nil
				}
				continue
			}
			if list[i].Options == nil {
				list[i].Options = map[string]string{}
			}
			list[i].Options[n] = v
		}
		return list
	})
}

// checkOptionValue は宣言に照らして値を検分する（通れば送る形に正規化して返す）。
//
// **弾くのは「エンジンが必ず断る値」だけ。** 範囲外の spin や、選択肢に無い combo は
// 送っても無視されるうえ、**`setoption` には応答が返らないので画面では気づけない**。
// 入れた瞬間に断るほうが早い。
func checkOptionValue(o ikkyoku.EngineOption, v string) (string, error) {
	switch o.Type {
	case "button":
		// ⚠️ **button は値を持たない**（送ること自体が「押した」という動作）。
		// 設定として保存する対象ではない。
		return "", fmt.Errorf("%s は押すだけの項目で、値を設定できません", o.Name)
	case "check":
		if v == "" {
			return "", nil
		}
		switch strings.ToLower(v) {
		case "true":
			return "true", nil
		case "false":
			return "false", nil
		}
		return "", fmt.Errorf("%s は true / false で指定してください: %s", o.Name, v)
	case "spin":
		if v == "" {
			return "", nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("%s は整数で指定してください: %s", o.Name, v)
		}
		if o.HasMin && n < o.Min {
			return "", fmt.Errorf("%s は %d 以上で指定してください: %d", o.Name, o.Min, n)
		}
		if o.HasMax && n > o.Max {
			return "", fmt.Errorf("%s は %d 以下で指定してください: %d", o.Name, o.Max, n)
		}
		return strconv.Itoa(n), nil
	case "combo":
		if v == "" || len(o.Vars) == 0 {
			return v, nil
		}
		if slices.Contains(o.Vars, v) {
			return v, nil
		}
		return "", fmt.Errorf("%s に選べない値です: %s", o.Name, v)
	}
	// string / filename と、型の分からないもの。**そのまま通す。**
	return v, nil
}

// SetEngineMultiPV は候補手の本数を決める（解析タブのエンジンの見出し）。
//
// **入口をそこに置いてあるのが要点**（2026-08-15。以前は解析の行に**全エンジン
// 共通**の欄が 1 つあった）。本数を変えたくなるのは**候補手を読んでいる最中**で、
// しかも**どれくらい出すかはエンジンごとに変えたい**（速いエンジンは多めに、
// 重いエンジンは 1 本、など）。
//
// ⚠️ **保存先は `Options["MultiPV"]`**（エンジンが宣言している option そのもの）。
// 専用の欄を作らないのは、**同じ値の置き場所を 2 つ持たない**ため。
//
// ⚠️ **繋ぎ直しは要らない。** これは探索ごとに送る option なので、
// `AnalyzeService.engineKey`（接続の指紋）からは外してある。
func (s *SettingsService) SetEngineMultiPV(id string, n int) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n < 1 {
		return s.settings(), fmt.Errorf("候補手の本数は 1 以上で指定してください: %d", n)
	}
	entry, ok := s.entryLocked(id)
	if !ok {
		return s.settings(), fmt.Errorf("そのエンジンの登録が見つかりません")
	}
	// エンジンが上限を宣言しているなら、それを超える値は断る（送っても無視されるうえ、
	// **`setoption` には応答が返らないので画面では気づけない**）。
	if spec, known := entry.OptionSpec(ikkyoku.MultiPVOption); known && spec.HasMax && n > spec.Max {
		return s.settings(), fmt.Errorf("このエンジンの候補手は %d 本までです: %d", spec.Max, n)
	}
	return s.editEngines(func(list []ikkyoku.EngineEntry) []ikkyoku.EngineEntry {
		for i := range list {
			if list[i].ID != id {
				continue
			}
			if list[i].Options == nil {
				list[i].Options = map[string]string{}
			}
			// ⚠️ **既定（`DefaultMultiPV`）と同じでも書き残す。** ほかの option と
			// 違って、ここでの既定は**エンジンの宣言ではなくアプリの都合**なので、
			// 消すと「エンジンの既定に従う」ではなく「アプリの既定に戻る」になる。
			// **選んだ値がそのまま残るほうが素直。**
			list[i].Options[ikkyoku.MultiPVOption] = strconv.Itoa(n)
		}
		return list
	})
}

// ResetEngineOptions は設定した値を全部捨てて、エンジンの既定に戻す。
//
// **宣言（`OptionSpecs`）は捨てない** —— 入力欄が作れなくなるので、
// 戻す先が画面から消えてしまう。
//
// ⚠️ **候補手の本数（`MultiPV`）も残す。** このボタンが並んでいるのは
// 設定タブの option の一覧で、**そこに MultiPV は出ていない**（入口は解析タブ）。
// 出ていないものを巻き添えで戻すと、**押した本人に何が起きたか分からない。**
func (s *SettingsService) ResetEngineOptions(id string) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.editEngines(func(list []ikkyoku.EngineEntry) []ikkyoku.EngineEntry {
		for i := range list {
			if list[i].ID != id {
				continue
			}
			keep := list[i].Options[ikkyoku.MultiPVOption]
			list[i].Options = nil
			if keep != "" {
				list[i].Options = map[string]string{ikkyoku.MultiPVOption: keep}
			}
		}
		return list
	})
}

// SetEngineName は表示名を付ける（空にすると既定の名前に戻る）。
//
// **同じ exe を option 違いで 2 つ登録したときに、どちらか分かるようにするため。**
// エンジンが `id name` で名乗る名前は繋がないと分からないので、それとは別に要る。
func (s *SettingsService) SetEngineName(id, name string) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := strings.TrimSpace(name)
	return s.editEngines(func(list []ikkyoku.EngineEntry) []ikkyoku.EngineEntry {
		for i := range list {
			if list[i].ID == id {
				list[i].Name = n
			}
		}
		return list
	})
}

// SetEngineColor は評価値グラフの折れ線の色を決める（空にすると既定色に戻る）。
//
// **入口は解析タブのエンジンの見出し**（候補手を出しているところ）。設定タブでは
// なくそこに置いてあるのは、**色を変えたくなるのは折れ線と結果を見比べている
// 最中**だから。
//
// ⚠️ **色は登録に紐づく**（一覧の何番目か、ではない）。並べ替えたり 1 つ
// 外したりしても色が動かないので、**前に見ていた線と同じ色が別のエンジンを
// 指すことがない**。
func (s *SettingsService) SetEngineColor(id, color string) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := strings.ToLower(strings.TrimSpace(color))
	if c != "" && !isHexColor(c) {
		return s.settings(), fmt.Errorf("色は #rrggbb で指定してください: %s", color)
	}
	return s.editEngines(func(list []ikkyoku.EngineEntry) []ikkyoku.EngineEntry {
		for i := range list {
			if list[i].ID == id {
				list[i].Color = c
			}
		}
		return list
	})
}

// isHexColor は `#rrggbb` かどうか。
//
// ⚠️ **選べる色の一覧（`ikkyoku.EngineColors`）に限定しないこと** —— 設定ファイルは
// 手で編集する前提で、一覧の外の色を書くのは正当。画面から選べる範囲と、
// 受け付ける範囲は別物。
func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, r := range s[1:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// SetEngineEnabled は「解析に使う」を切り替える。
//
// **外しても登録は消えない。** エンジンを入れ替えて比べる作業では、外したものを
// また戻すことが多い。
func (s *SettingsService) SetEngineEnabled(id string, enabled bool) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.editEngines(func(list []ikkyoku.EngineEntry) []ikkyoku.EngineEntry {
		for i := range list {
			if list[i].ID == id {
				list[i].Enabled = enabled
			}
		}
		return list
	})
}

// MoveEngine は一覧の中で登録の位置を 1 つ動かす（delta は -1 で上、+1 で下）。
//
// **並び順は表示の順序そのもの。** 解析タブのエンジンごとの結果も、評価値グラフの
// 折れ線も、勝率バーが最初に出すエンジンも、この一覧の順で決まる。**同時に走らせて
// 比べる**のが複数登録の目的なので、**よく見るものを上に置けること**は要る。
//
// ⚠️ **並び順は「優先度」ではない。** 上のエンジンが正しいという意味も、
// 先に走るという意味も無い（**解析は全部同時に走る**）。
//
// ⚠️ **「解析に使う」を外した登録も含めた一覧の中で動かす。** 走るものだけを
// 詰めて数えると、**チェックを外した瞬間に見えている順番と食い違う**。
//
// 端から先へは動かさない（**回り込ませない**）。押しても何も起きないのは、
// 一番上のものが一番下へ飛ぶより分かりやすい。
func (s *SettingsService) MoveEngine(id string, delta int) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.editEngines(func(list []ikkyoku.EngineEntry) []ikkyoku.EngineEntry {
		from := -1
		for i, e := range list {
			if e.ID == id {
				from = i
				break
			}
		}
		to := from + delta
		if from < 0 || to < 0 || to >= len(list) {
			return list
		}
		list[from], list[to] = list[to], list[from]
		return list
	})
}

// RemoveEngine は登録を消す。
//
// **全部消すと同梱エンジン 1 つに戻る**（`ikkyoku.Config.EngineList`）。
// 解析できない状態に落とし込まないため。
func (s *SettingsService) RemoveEngine(id string) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.editEngines(func(list []ikkyoku.EngineEntry) []ikkyoku.EngineEntry {
		out := list[:0]
		for _, e := range list {
			if e.ID != id {
				out = append(out, e)
			}
		}
		return out
	})
}

// trainingSettings は設定ファイルの値を、画面に出す形(既定値を解決した接続先つき)にする。
func trainingSettings(c ikkyoku.TrainingConfig) TrainingSettings {
	host := c.Host
	if host == "" {
		host = training.DefaultHost
	}
	port := c.Port
	if port <= 0 {
		port = training.DefaultPort
	}
	target, err := training.ParseBase(c.Host, c.Port)
	if err != nil {
		target = "" // 組み立てられない設定。画面側で「宛先が不正」と出す
	}
	return TrainingSettings{
		Enabled: c.Enabled,
		Host:    host,
		Port:    port,
		Token:   c.Token,
		Target:  target,
	}
}

// trainingConfig は今の接続設定を返す(TrainingService が使う)。
func (s *SettingsService) trainingConfig() ikkyoku.TrainingConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Training
}

// SetTraining は「訂正盤面を suteme に登録する」の設定を保存する。
//
// **接続の確認はしない。** 設定を保存する操作と、相手が受け付けているかを見る操作
// (TrainingService.Status)は別。書けないサーバでも設定は保存できたほうがよい
// (先に設定を入れてから suteme を起動する、という順序が普通にある)。
func (s *SettingsService) SetTraining(enabled bool, host string, port int, token string) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := ikkyoku.TrainingConfig{
		Enabled: enabled,
		Host:    strings.TrimSpace(host),
		Port:    port,
		Token:   strings.TrimSpace(token),
	}
	// 既定値そのものは書き残さない(既定が変わったときに追従できるように)。
	if next.Host == training.DefaultHost {
		next.Host = ""
	}
	if next.Port == training.DefaultPort {
		next.Port = 0
	}
	if next.Port < 0 || next.Port > 65535 {
		return s.settings(), fmt.Errorf("ポート番号が範囲外です: %d", port)
	}

	return s.save(func(cfg *ikkyoku.Config) { cfg.Training = next })
}

// ponanzaConstant は評価値 → 勝率の変換に使う定数を返す（AnalyzeService が使う）。
//
// **設定ファイルの生の値をそのまま返す**（0 = 未設定）。既定値の解決は
// `analyze.PonanzaConstantOr` の 1 か所で行うので、ここでは倒さない。
func (s *SettingsService) ponanzaConstant() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.PonanzaConstant
}

// SetPonanzaConstant は勝率の変換に使う定数を保存する（解析タブの勝率バー）。
//
// **0 以下なら既定に戻す**（設定ファイルからも消える）。欄を空にしたときの
// 素直な意味が「既定でよい」なので、そこでエラーにしない。
func (s *SettingsService) SetPonanzaConstant(v float64) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v < 0 {
		return s.settings(), fmt.Errorf("ポナンザ定数は正の数で指定してください: %v", v)
	}
	// **既定値そのものは書き残さない**（既定が変わったときに追従できるように。
	// training の Host/Port と同じ扱い）。
	if v == analyze.DefaultPonanzaConstant {
		v = 0
	}
	return s.save(func(cfg *ikkyoku.Config) { cfg.PonanzaConstant = v })
}

// SetAnalyzeSeconds は「考える秒数」を保存する（解析タブの選択。**0 は無制限**）。
//
// ⚠️ **0 も保存すること**（「無制限」は正当な選択）。`Config.AnalyzeSeconds` が
// ポインタなのはこのため —— 値で持って省略すると、**次の起動で既定に戻る**。
//
// ⚠️ **連続モードのチェックとは扱いが違う**（あちらは起動のたびに入で始まる
// その場かぎりの操作）。秒数は待ち時間を決める値で、連続解析では
// 「手数 × 秒数」がそのまま所要時間になるので、**選び直しを毎回やらせない。**
func (s *SettingsService) SetAnalyzeSeconds(v int) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v < 0 {
		return s.settings(), fmt.Errorf("考える秒数は 0 以上で指定してください（0 は無制限）: %d", v)
	}
	return s.save(func(cfg *ikkyoku.Config) { cfg.AnalyzeSeconds = &v })
}

// SetFitOnStartup は「起動時に盤面を探す」を切り替えて保存する。
func (s *SettingsService) SetFitOnStartup(v bool) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(func(cfg *ikkyoku.Config) { cfg.FitOnStartup = v })
}

// save は設定を 1 項目書き換えて保存する共通処理。**ロックを取った状態で呼ぶこと。**
//
// **保存の前にファイルを読み直す。** 設定ファイルは手で編集する前提でもあり、
// アプリ起動中に足された項目(学習データの置き場所など)を、こちらが抱えている
// 古い内容で上書きしてしまわないようにするため。
// editConfig は設定を書き換えて保存し、**保存後の設定そのもの**を返す。
//
// `save` が返すのは設定タブの形（`AppSettings`）だが、駒フォント（`FontService`）は
// 自分の形（`FontState`）を組み立てる。**AppSettings に駒フォントの項目を
// 足していないのはそのため** —— あちらは設定タブの 1 枚岩で、
// 焼いた data URL のような「画面に出さない値」を混ぜたくない。
func (s *SettingsService) editConfig(apply func(*ikkyoku.Config)) (ikkyoku.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.save(apply); err != nil {
		return s.cfg, err
	}
	return s.cfg, nil
}

func (s *SettingsService) save(apply func(*ikkyoku.Config)) (AppSettings, error) {
	if s.path == "" {
		// 保存先が決められない環境。今回限りの変更にする。
		apply(&s.cfg)
		return s.settings(), nil
	}
	cfg, err := ikkyoku.LoadConfig(s.path)
	if err != nil {
		s.logger.Warn("設定を読み直せませんでした。読み込み済みの内容に書きます", "error", err)
		cfg = s.cfg
	}
	apply(&cfg)
	if err := ikkyoku.SaveConfig(s.path, cfg); err != nil {
		s.logger.Error("設定を保存できませんでした", "path", s.path, "error", err)
		return s.settings(), err
	}
	s.cfg = cfg
	s.logger.Info("設定を保存しました", "path", s.path)
	return s.settings(), nil
}
