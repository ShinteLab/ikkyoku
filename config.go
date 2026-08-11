package ikkyoku

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config は永続化する設定（ディスプレイ番号・領域・保存先）。
// CLI のフラグが指定された場合はフラグを優先し、Config はあくまで既定値の置き場所として使う。
type Config struct {
	Display *int    `json:"display,omitempty"`
	Region  *Region `json:"region,omitempty"`
	OutDir  string  `json:"outDir,omitempty"`
	Hotkey  string  `json:"hotkey,omitempty"`

	// SutemeDataDir は suteme の駒種推論器の学習データ
	// (training_data_v2.json / model_v2.json)を置いたディレクトリ。
	//
	// 空なら suteme 既定の探索(カレントディレクトリ → 実行ファイルのディレクトリ)に任せる。
	// 指定できるようにしてあるのは、**学習データが 3.5MB 級で、しかも育て続けるもの**
	// だから。実行ファイルの隣にコピーを置く運用にすると、更新のたびにコピーし直す必要が
	// あり、古いデータで認識してしまう事故が起きる。開発中は suteme のリポジトリを
	// 直接指しておけば、データを更新した結果がそのまま反映される。
	SutemeDataDir string `json:"sutemeDataDir,omitempty"`

	// FitOnStartup は起動時に盤面を探してガイド枠を合わせるか。
	//
	// **既定は false(探さない)。** 枠の位置はユーザーが手で合わせたものなので、
	// 起動のたびに勝手に動かすのを既定の挙動にはしない。有効にすると毎回の起動で
	// 一度だけ探す(GUI の設定タブから切り替える)。
	//
	// omitempty を付けていないのは、**切ってあること自体を設定ファイルに残す**ため。
	// このファイルは手で編集する前提でもあるので、キーが消えると存在に気づけない。
	FitOnStartup bool `json:"fitOnStartup"`

	// Training は訂正した局面を suteme の学習用サーバへ送る設定。
	Training TrainingConfig `json:"training"`

	// Engines は登録した USI エンジンの一覧。
	//
	// **1 つに絞らない**（2026-08-11）。検討ツールとして実用になるかは繋ぐエンジンの
	// 棋力で決まるが、**どのエンジンが正しいかは局面によって違う**。同じ局面を
	// 複数のエンジンに読ませて評価値を並べられることが「別の選択も一つの局として
	// 辿る」という構想に効く（`Enabled` を付けたものが同時に走る）。
	//
	// **空なら同梱のエンジン 1 つ**として扱う（`EngineList`）。設定ファイルを
	// 作っていない状態でも解析できる、という既定の挙動を変えないため。
	Engines []EngineEntry `json:"engines,omitempty"`

	// Engine は**旧形式**（単一エンジン）の設定。
	//
	// ⚠️ **読み込んだ時点で Engines へ移し、この欄は捨てる**（`LoadConfig`）。
	// 次に保存したときにファイルからも消える。**新しいコードはここを読まないこと。**
	Engine *EngineConfig `json:"engine,omitempty"`
}

// EngineEntry は登録した USI エンジン 1 つ。
//
// **繋ぎ先は「USI を話すプロセス」なら何でもよい**（やねうら王・水匠・prokishi.exe）。
// 検討ツールとして実用になるかは繋ぐエンジンの棋力で決まるので、そこを差し替え
// られるようにしてある（`_docs/phase4-engine-usi.md`）。
type EngineEntry struct {
	// ID は一覧の中でこのエンジンを指す識別子。**設定ファイルの中だけで通じればよい。**
	//
	// パスを鍵にしないのは、**同じ実行ファイルを別の option で 2 つ登録する**のが
	// 正当な使い方だから（置換表やスレッド数を変えて比べる）。
	ID string `json:"id"`

	// Name は画面に出す名前。空ならパスのファイル名（同梱なら「同梱エンジン」）。
	//
	// ⚠️ **エンジンが `id name` で名乗る名前とは別物。** あちらは繋いで初めて分かるので、
	// 繋いでいないあいだの表示と、同じ exe を 2 つ登録したときの区別にこちらが要る。
	Name string `json:"name,omitempty"`

	// Path は USI エンジンの実行ファイル。
	//
	// **空なら同梱のエンジンを使う。** 「外部エンジンを使うかどうか」の真偽値は
	// 別に持たない —— 2 つ持つと、パスが入っているのに無効、という食い違いが起きる。
	Path string `json:"path,omitempty"`

	// Options は接続時に `setoption` で送る値（option 名 → 値）。
	//
	// ⚠️ **設定ファイルを手で編集する前提。画面には出していない。** USI の option は
	// エンジンごとに名前も型も既定値も違うので、汎用の設定 UI を作り込むと重い。
	// 素通しにしておけば、必要な人が必要なものだけ書ける。
	//
	// 例: `{"USI_Hash": "1024", "Threads": "4", "EvalDir": "eval"}`
	//
	// ⚠️ **`isready` の前に送られる**（置換表の確保や評価関数の読み込みに間に合わせるため。
	// `core/usi/client.Open` の注記）。探索ごとに変えるもの（MultiPV）はここではない。
	Options map[string]string `json:"options,omitempty"`

	// Enabled は解析のときに使うか。**外した登録は消さずに残る**
	// （エンジンを入れ替えて比べる作業では、外したものをまた戻すことが多い）。
	//
	// omitempty を付けないのは、**外してあること自体を設定ファイルに残す**ため。
	Enabled bool `json:"enabled"`
}

// EngineConfig は**旧形式**の単一エンジン設定（`Config.Engine`）。
//
// ⚠️ **移行のためだけに残してある。** 今の設定は `Config.Engines`。
type EngineConfig struct {
	Path    string            `json:"path,omitempty"`
	Options map[string]string `json:"options,omitempty"`
}

// BuiltinEngineName は同梱エンジンの表示名（パスが空のエントリ）。
const BuiltinEngineName = "同梱エンジン"

// DisplayName は画面に出す名前を返す（Name が空ならパスのファイル名）。
func (e EngineEntry) DisplayName() string {
	if e.Name != "" {
		return e.Name
	}
	if e.Path == "" {
		return BuiltinEngineName
	}
	return filepath.Base(e.Path)
}

// EngineList は登録済みのエンジン一覧を返す。
//
// ⚠️ **空なら同梱エンジン 1 つを返す。** 設定ファイルを作っていない状態でも
// 解析できる、という既定の挙動をここで担保している。**呼び出し側が
// 「空だったら同梱」を書かないこと**（2 か所に散る）。
func (c Config) EngineList() []EngineEntry {
	if len(c.Engines) == 0 {
		return []EngineEntry{{ID: DefaultEngineID, Enabled: true}}
	}
	out := make([]EngineEntry, len(c.Engines))
	copy(out, c.Engines)
	return out
}

// EnabledEngines は解析に使うエンジンだけを返す（順番は登録順）。
func (c Config) EnabledEngines() []EngineEntry {
	var out []EngineEntry
	for _, e := range c.EngineList() {
		if e.Enabled {
			out = append(out, e)
		}
	}
	return out
}

// DefaultEngineID は同梱エンジンを既定で登録したときの ID。
const DefaultEngineID = "builtin"

// NextEngineID は既存の一覧とぶつからない ID を作る。
func NextEngineID(engines []EngineEntry) string {
	used := make(map[string]bool, len(engines))
	for _, e := range engines {
		used[e.ID] = true
	}
	for i := 1; ; i++ {
		id := fmt.Sprintf("engine-%d", i)
		if !used[id] {
			return id
		}
	}
}

// migrateEngines は旧形式（`engine`）の設定を `engines` へ移す。
//
// **読み込みの一度きり。** 移したら旧欄は捨てるので、次に保存した時点で
// ファイルからも消える。`engines` が既にあれば旧欄は無視する（手で両方書いた
// ときに、新しいほうを正とする）。
func migrateEngines(c *Config) {
	old := c.Engine
	c.Engine = nil
	if old == nil || len(c.Engines) > 0 {
		return
	}
	if old.Path == "" && len(old.Options) == 0 {
		return
	}
	c.Engines = []EngineEntry{{
		ID:      NextEngineID(nil),
		Path:    old.Path,
		Options: old.Options,
		Enabled: true,
	}}
}

// TrainingConfig は訂正済みの局面を suteme に登録するための接続設定。
//
// **既定は無効。** 訂正結果の還元は 2026-08-07 に決めた方針だが、
// **自動では送らない**（人間が直したのは 1 マスで残り 80 マスは推論結果のまま、
// という「自分の出力を正解として食う」形になるため）。設定で有効にしたうえで、
// 局面ごとにボタンを押したときだけ送る。
type TrainingConfig struct {
	// Enabled は「訂正盤面を suteme に登録する」を使うか。
	// **これは送信ボタンを出すかどうかであって、自動送信のスイッチではない。**
	//
	// omitempty を付けないのは FitOnStartup と同じ理由（切ってあること自体を残す）。
	Enabled bool `json:"enabled"`
	// Host は suteme の学習用サーバのホスト。空なら 127.0.0.1。
	Host string `json:"host,omitempty"`
	// Port は同ポート。0 なら 8080（suteme-training の既定）。
	Port int `json:"port,omitempty"`
	// Token は Bearer トークン。**同じマシンで動かすなら不要**
	// （suteme はループバックからのアクセスを認証免除にしている）。
	// 別のマシンへ送るときだけ、suteme の APIタブで発行したものを入れる。
	Token string `json:"token,omitempty"`
}

// DefaultConfigPath は既定の設定ファイルパスを返す（os.UserConfigDir()/ikkyoku/config.json）。
func DefaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("ikkyoku: 設定ディレクトリの取得に失敗しました: %w", err)
	}
	return filepath.Join(dir, "ikkyoku", "config.json"), nil
}

// LoadConfig は path から設定を読み込む。ファイルが存在しない場合はゼロ値の Config を
// エラー無しで返す（初回起動時に設定ファイルが無いのは正常な状態のため）。
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("ikkyoku: 設定の読み込みに失敗しました: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("ikkyoku: 設定の解析に失敗しました: %w", err)
	}
	// 旧形式（単一エンジン）をここで吸収する。**読み込みの入口 1 か所だけ**で行い、
	// これより上のコードは新しい形（Engines）しか知らない。
	migrateEngines(&c)
	return c, nil
}

// SaveConfig は設定を path に保存する。親ディレクトリが無ければ作成する。
func SaveConfig(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ikkyoku: 設定ディレクトリの作成に失敗しました: %w", err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("ikkyoku: 設定のエンコードに失敗しました: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("ikkyoku: 設定の書き込みに失敗しました: %w", err)
	}
	return nil
}
