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
