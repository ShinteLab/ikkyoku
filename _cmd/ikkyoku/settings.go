package main

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku"
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
	return AppSettings{
		FitOnStartup: s.cfg.FitOnStartup,
		Training:     trainingSettings(s.cfg.Training),
		Path:         s.path,
	}
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
