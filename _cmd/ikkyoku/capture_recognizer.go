package main

// CaptureService のうち、**認識器の読み込み（3 段）**（2026-11-13 に captureservice.go から分けた）。
//
// 学習データ → 配布モデル → 焼き込み。理由は `_docs/design-capture.md` の「決定: 認識器は 3 段」、
// 歯止めは `recognizersource_test.go`。

import (
	"errors"
	"fmt"

	"github.com/ShinteLab/ikkyoku/config"
	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// RecognizerStatus は suteme のデータ(SutemeDataDir)の読み込み状況。
//
// **駒種推論器と帯の判定器を別々に持つ。** 前者は無ければ駒種が読めない(致命的)が、
// 後者は無くても検出は動く(1マス滑りを直せなくなるだけ)。ひとつの Ready / Error に
// まとめると、帯データを置き忘れているのに「認識器: OK」と出て気づけない。
type RecognizerStatus struct {
	// Source は実際に読んだ出所(学習データ・配布モデルはディレクトリのパス、焼き込みは
	// 書き出しの日時と件数)。読めていなければ空。
	Source string `json:"source"`
	Ready  bool   `json:"ready"`
	// Error は**どの段も読めなかった**ときの理由(このとき盤面は読めない)。
	Error string `json:"error"`

	// Mode は実際にどの段から読んだか(`config.SutemeSourceDir` / `SutemeSourceModel` /
	// `SutemeSourceEmbed` / 空＝どれも読めていない)。**設定の値そのものではない** ——
	// 設定は「どの段から見始めるか」で、読めなかった段は飛ばされる。**画面にはこちらを出すこと。**
	Mode string `json:"mode"`
	// Skipped は**置いてあるのに使えなかった段**とその理由(2026-10-04)。読めた段より
	// 上にあったものが入る。**置かれていない段は入れない**(既定の置き場所が空なのは普通)。
	// 空でなければ画面の ⚠ に「指定した認識器を使えない」と出る(issues.go)。
	Skipped []string `json:"skipped"`
	// ModelDir は配布モデルを探した場所(設定が空なら既定の置き場所)。設定タブに出す。
	ModelDir string `json:"modelDir"`
	// EmbedAvailable はこのビルドに認識器が焼き込まれているか
	// (`-tags embedmodel`)。設定タブが「焼き込み」を選べるかの判断に使う。
	EmbedAvailable bool `json:"embedAvailable"`

	// StripSamples は盤の縁の帯の判定器のサンプル数。0 なら読めていない。
	StripSamples int `json:"stripSamples"`
	// StripError は帯の判定器が読めなかった理由。**Error とは別**で、
	// これが埋まっていても認識自体は動く(盤の位置が 1マス滑ることがある)。
	StripError string `json:"stripError"`
}

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

// loadRecognizer は設定に従って認識器を 1 組読み、差し替える（2026-10-04 に 3 段にした）。
//
//  1. 学習データ（SutemeDataDir）  2. 配布モデル（SutemeModelDir）  3. 焼き込み
//
// **見る順は recognizerOrder**（設定 SutemeSource は「どの段から見始めるか」）。
// **置かれていない段は黙って飛ばし、置いてあるのに読めなかった段は Skipped に残して
// 下へ落とす**（画面の ⚠ に出る。issues.go）。どの段も読めなければ認識器を外す
// （recognize.Clear。⚠️ suteme 既定の探索には落とさない）。
//
// ⚠️ **駒種の推論器と盤の縁の判定器は同じ段から 1 組で差し替える**（recognize.Set.Use）。
// 段ごとに別々に読むと「駒種は最新の学習データ、盤の位置合わせは学習前」になる。
//
// ⚠️ **自動（auto / dir）のときは、焼き込みより古い配布モデルを使わない**（export.json の
// 日付で比べる）。exe を更新したのに、昔置いたモデルが優先されて精度が下がるのを防ぐ。
// 配布モデルから見始める設定（model）にしているときは、古くても使う（意思表示なので）。
func (s *CaptureService) loadRecognizer() RecognizerStatus {
	s.mu.Lock()
	dir, modelSetting, pref := s.recognizerDir, s.recognizerModelDir, s.recognizerSource
	s.mu.Unlock()

	modelDir, err := config.Config{SutemeModelDir: modelSetting}.ModelDir()
	if err != nil {
		log.Warn("配布モデルの置き場所を決められませんでした", "error", err)
	}
	st := RecognizerStatus{EmbedAvailable: recognize.EmbeddedAvailable(), ModelDir: modelDir}

	// 焼き込みは配布モデルとの新旧の比較でも読むので、読んだら使い回す。
	var embedded *recognize.Set
	var embeddedErr error
	embeddedLoaded := false
	loadEmbedded := func() (*recognize.Set, error) {
		if !embeddedLoaded {
			embedded, embeddedErr = recognize.LoadEmbedded()
			embeddedLoaded = true
		}
		return embedded, embeddedErr
	}

	for _, mode := range recognizerOrder(pref) {
		var set *recognize.Set
		switch mode {
		case config.SutemeSourceDir:
			if dir == "" {
				continue
			}
			if set, err = recognize.LoadDir(dir); err != nil {
				st.Skipped = append(st.Skipped, fmt.Sprintf("学習データ（%s）を読めません: %v", dir, err))
				log.Warn("学習データを読めませんでした", "dir", dir, "error", err)
				continue
			}
		case config.SutemeSourceModel:
			if modelDir == "" {
				continue
			}
			set, err = recognize.LoadPack(modelDir)
			if errors.Is(err, recognize.ErrNoPack) {
				// 既定の置き場所が空なのは普通の状態。**場所を指定したのに無いときだけ**残す。
				if modelSetting != "" {
					st.Skipped = append(st.Skipped, fmt.Sprintf("配布モデル（%s）が置かれていません", modelDir))
				}
				continue
			}
			if err != nil {
				st.Skipped = append(st.Skipped, fmt.Sprintf("配布モデル（%s）を読めません: %v", modelDir, err))
				log.Warn("配布モデルを読めませんでした", "dir", modelDir, "error", err)
				continue
			}
			if pref != config.SutemeSourceModel && recognize.EmbeddedAvailable() {
				if emb, err := loadEmbedded(); err == nil && set.OlderThan(emb) {
					st.Skipped = append(st.Skipped, fmt.Sprintf("配布モデル（%s）は焼き込み（%s）より古いので使っていません",
						set.Date.Format("2006-01-02"), emb.Date.Format("2006-01-02")))
					log.Info("配布モデルは焼き込みより古いので使いません", "model", set.Date, "embed", emb.Date)
					continue
				}
			}
		case config.SutemeSourceEmbed:
			if !recognize.EmbeddedAvailable() {
				continue
			}
			if set, err = loadEmbedded(); err != nil {
				st.Skipped = append(st.Skipped, fmt.Sprintf("焼き込んだ認識器を読めません: %v", err))
				log.Warn("焼き込んだ認識器を読めませんでした", "error", err)
				continue
			}
		}

		set.Use()
		st.Mode, st.Source, st.Ready = mode, set.Source, true
		st.StripSamples = set.StripSamples
		if set.StripErr != nil {
			st.StripError = set.StripErr.Error()
			log.Warn("盤の縁の帯の判定器を読み込めませんでした(盤の位置が 1マス滑ることがあります)",
				"mode", mode, "error", set.StripErr)
		}
		// NOTICE（常に残す。2026-10-11）: どの認識器で読んだかが分からないと、読み違いを追えない。
		log.Notice("認識器を読み込みました", "mode", mode, "source", set.Source, "strip", set.StripSamples)
		return st
	}

	recognize.Clear()
	st.Error = "学習データ・配布モデル・焼き込みのどれも読めていません"
	log.Warn("認識器がありません（撮った画像から盤面を読めません）", "skipped", st.Skipped)
	return st
}

// recognizerOrder は認識器の段を見る順を返す（2026-10-04）。
//
// 設定は「どの段から見始めるか」で、**見始めた段から下へ、そのあと上の段**の順。
// 上の段へも回るのは、**焼き込みの無いビルドで「焼き込み」にしていても動くように**
// （設定ファイルは `wails3 dev` と配る exe で共用なので、普通に起きる）。
//
// ⚠️ **auto を「焼き込み優先」にしないこと。** 焼き込みは配布用に固定したデータ、
// 学習データは育て続けるデータ。開発中（学習データを指している状態）に焼き込みへ
// 倒れると、**学習データを更新しても反映されない**という最も気づきにくい事故になる。
// 置いたもの・指定したもののほうがユーザーの意思表示なので、上の段を先に見る。
func recognizerOrder(pref string) []string {
	all := []string{config.SutemeSourceDir, config.SutemeSourceModel, config.SutemeSourceEmbed}
	start := 0
	switch pref {
	case config.SutemeSourceModel:
		start = 1
	case config.SutemeSourceEmbed:
		start = 2
	}
	order := make([]string, 0, len(all))
	order = append(order, all[start:]...)
	return append(order, all[:start]...)
}
