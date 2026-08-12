package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
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
	// OptionCount は config.json に書いた setoption の数。**表示用。**
	//
	// option は画面に出していない（エンジンごとに違いすぎる）ので、
	// **書いたものが効いていることだけ**は見えるようにしておく。
	OptionCount int `json:"optionCount"`
	// Enabled は解析に使うか。**外した登録も残る。**
	Enabled bool `json:"enabled"`
}

// engineSettings は設定ファイルのエントリを画面に出す形にする。
func engineSettings(e ikkyoku.EngineEntry) EngineSettings {
	return EngineSettings{
		ID:          e.ID,
		Name:        e.DisplayName(),
		Custom:      e.Name != "",
		Path:        e.Path,
		Builtin:     e.Path == "",
		OptionCount: len(e.Options),
		Enabled:     e.Enabled,
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
	for _, e := range list {
		engines = append(engines, engineSettings(e))
	}
	return AppSettings{
		FitOnStartup:    s.cfg.FitOnStartup,
		Training:        trainingSettings(s.cfg.Training),
		Engines:         engines,
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
			if list[i].ID == id {
				list[i].Path = p
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
