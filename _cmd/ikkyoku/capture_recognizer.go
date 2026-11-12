package main

// CaptureService のうち、**認識器の設定と読み直しの口**（2026-11-13 に captureservice.go から分けた）。
//
// 3 段（学習データ → 配布モデル → 焼き込み）のどれを使うかを決めて読むのは recognize.LoadSources
// （同じ日に移した。Wails に触れていなかったため）。ここが持つのは設定の 3 つ・直近の結果・⚠ を合わせるフック。

import "github.com/ShinteLab/ikkyoku/recognize"

// RecognizerStatus は認識器の読み込み状況（フロントの API が返す型。中身は recognize）。
// ⚠️ **型エイリアスにしてある** —— 定義は recognize.RecognizerStatus の 1 か所（写さない）。
type RecognizerStatus = recognize.RecognizerStatus

// applyRecognizerDir は学習データの置き場所の変更をその場で効かせる
// (SettingsService.onSutemeDataDir から呼ばれる)。
func (s *CaptureService) applyRecognizerDir(dir string) {
	s.mu.Lock()
	s.recognizerDir = dir
	s.mu.Unlock()
	s.ReloadRecognizer()
}

// applyRecognizerModelDir は配布モデルの置き場所の変更をその場で効かせる
// (SettingsService.OnSutemeModelDir から呼ばれる。2026-10-04)。
// **設定の値（空なら既定の置き場所）をそのまま持つ** —— 解決は loadRecognizer が毎回する。
func (s *CaptureService) applyRecognizerModelDir(dir string) {
	s.mu.Lock()
	s.recognizerModelDir = dir
	s.mu.Unlock()
	s.ReloadRecognizer()
}

// applyRecognizerSource は設定「認識器の読み込み元」の変更をその場で効かせる
// (SettingsService.onSutemeSource から呼ばれる)。
//
// **再起動を待たせないのは、切り替えた結果を見るための設定だから** ——
// 焼き込みに切り替えて認識が落ちるなら、その場で戻せたほうがよい。
func (s *CaptureService) applyRecognizerSource(source string) {
	s.mu.Lock()
	s.recognizerSource = source
	s.mu.Unlock()
	s.ReloadRecognizer()
}

// ReloadRecognizer は駒種推論器を読み込み直し、その結果を返す。起動時にも呼ぶ。
//
// **再読み込みの入口を用意しているのは、学習データを育てながら使うため。**
// suteme は一度読み込んだ推論器をキャッシュするので、学習データを更新しても
// これを呼ぶまで(あるいは再起動するまで)反映されない。
// 訂正 → 学習データ更新 → 撮り直す、というループを回すのにアプリの再起動を
// 挟みたくない。
func (s *CaptureService) ReloadRecognizer() RecognizerStatus {
	st := s.loadRecognizer()
	s.mu.Lock()
	s.recognizerStatus = st
	hook := s.onRecognizer
	s.mu.Unlock()
	if hook != nil {
		hook(st)
	}
	return st
}

// Recognizer は直近の読み込み結果を返す。**読み込み直さない。**
// フロントが起動時に状態を表示するためだけに 3.5MB を読み直すのを避ける。
func (s *CaptureService) Recognizer() RecognizerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recognizerStatus
}

// loadRecognizer は今の設定で認識器を 1 組読み、差し替える（中身は recognize.LoadSources）。
// ⚠️ **設定はロックの中で写してから、ロックの外で読む**（読み込みは 3.5MB 級で時間がかかる）。
func (s *CaptureService) loadRecognizer() RecognizerStatus {
	s.mu.Lock()
	src := recognize.Sources{DataDir: s.recognizerDir, ModelDir: s.recognizerModelDir, Source: s.recognizerSource}
	s.mu.Unlock()
	return recognize.LoadSources(src)
}
