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
