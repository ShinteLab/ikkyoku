package main

import (
	"log/slog"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku"
)

// AppSettings はメイン画面の設定タブに出す設定。
//
// **ikkyoku.Config そのものを返していない。** あちらは CLI 用の項目(領域・ディスプレイ番号)
// まで含む「設定ファイルの形」で、画面に出すものとは範囲が違う。ここは
// 「設定タブが読み書きするもの」だけを持つ。
type AppSettings struct {
	// FitOnStartup は起動時に盤面を探してガイド枠を合わせるか。
	FitOnStartup bool `json:"fitOnStartup"`
	// Path は設定ファイルの場所。**表示のためだけ。** 手で編集したくなったときに
	// 探さずに済むよう出しておく(学習データの置き場所もこのファイルにある)。
	Path string `json:"path"`
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
	return AppSettings{FitOnStartup: s.cfg.FitOnStartup, Path: s.path}
}

// SetFitOnStartup は「起動時に盤面を探す」を切り替えて保存する。
//
// **保存の前にファイルを読み直す。** 設定ファイルは手で編集する前提でもあり、
// アプリ起動中に足された項目(学習データの置き場所など)を、こちらが抱えている
// 古い内容で上書きしてしまわないようにするため。
func (s *SettingsService) SetFitOnStartup(v bool) (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.path == "" {
		return AppSettings{FitOnStartup: v}, nil // 保存先が決められない環境。今回限りの変更にする
	}
	cfg, err := ikkyoku.LoadConfig(s.path)
	if err != nil {
		s.logger.Warn("設定を読み直せませんでした。読み込み済みの内容に書きます", "error", err)
		cfg = s.cfg
	}
	cfg.FitOnStartup = v
	if err := ikkyoku.SaveConfig(s.path, cfg); err != nil {
		s.logger.Error("設定を保存できませんでした", "path", s.path, "error", err)
		return AppSettings{FitOnStartup: s.cfg.FitOnStartup, Path: s.path}, err
	}
	s.cfg = cfg
	s.logger.Info("設定を保存しました", "fitOnStartup", v, "path", s.path)
	return AppSettings{FitOnStartup: v, Path: s.path}, nil
}
