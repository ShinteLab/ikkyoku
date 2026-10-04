package recognize

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ShinteLab/suteme"
)

// 認識器を**どこから読むか**（2026-10-04 に 3 段にした）。
//
//	1. 学習データ   … suteme の学習ディレクトリ（`SutemeDataDir`）。自分で育てたもの（LoadDir）
//	2. 配布モデル   … ダウンロードして置いた配布セット（`SutemeModelDir`。LoadPack）
//	3. 焼き込み     … exe に最初から入っている配布セット（`-tags embedmodel`。LoadEmbedded）
//
// どの段を使うかの判断は `_cmd/ikkyoku/captureservice.go` の `loadRecognizer`。
// ここは**1 つの段から 1 組を読む**ことと、**読んだ 1 組をまとめて差し替える**ことだけを持つ。
//
// ⚠️ **駒種の推論器と盤の縁の判定器は 1 組で差し替えること**（`Set.Use`）。
// 別々に差し替えると、上の段の推論器が読めずに下の段へ落ちたときに**判定器だけ
// 上の段のものが残る**（「駒種は最新の学習データ、盤の位置合わせは学習前」という
// ちぐはぐな状態。2026-08-22 に実際に踏んだ。AGENTS.md）。
//
// ⚠️ **suteme 既定の探索（カレントディレクトリ → 実行ファイルの隣）は使わない。**
// 認識器を 1 組も差し替えていなければ `FromImage` は suteme を呼ばずに断る（`Clear`）。
// exe の隣にデータを置く運用は取らない（Program Files には書けず、更新のたびに
// コピーし直すことになる）。

// 配布セットのファイル名（2026-10-04）。**焼き込み（`model/`）と、ダウンロードして置く
// 配布モデルで同じ形**にしてある —— 焼き込みは「exe に最初から入っている配布セット」で、
// 読む口が 1 本で済む。中身は suteme の「配布用に書き出す」（`training.ExportCompact`）の
// 出力を gzip したもの（`_cmd/ikkyoku/build/copy-model.ps1`）。
//
// ⚠️ **ファイル名に版を入れない**（`training_data_v8` → `predictor`）。版が上がるたびに
// go:embed の行と置き場所の案内を書き換えることになるため。どの版かは `source.txt`。
const (
	PackPredictorFile = "predictor.bin.gz"
	PackStripFile     = "strip.bin.gz"
	PackSourceFile    = "source.txt"
)

// ErrNoPack は配布セットが置かれていない（`predictor.bin.gz` が無い）。
// **読めない（壊れている・形式が違う）とは区別する** —— 既定の置き場所に何も無いのは
// 普通の状態で、⚠ に出すことではない。
var ErrNoPack = errors.New("配布モデルが置かれていません")

// Set は 1 つの段から読んだ認識器の 1 組。読んだだけでは効かない（`Use` で差し替える）。
type Set struct {
	// Source は出所（ディレクトリのパス、または焼き込みのラベル）。画面とログに出す。
	Source string
	// Date は配布セットの日付（`source.txt` の「(yyyy-mm-dd)」）。読めなければゼロ。
	// **焼き込みより古い配布モデルを使わない**ための比較に使う。
	Date time.Time
	// StripSamples は盤の縁の判定器のサンプル数（読めていなければ 0）。
	StripSamples int
	// StripErr は判定器が読めなかった理由。**推論器が読めていれば 1 組としては使う**
	// （判定器が無くても認識はできる。盤の位置が 1 マス滑ることがあるだけ）。
	StripErr error

	predictor suteme.Predictor
	strip     *suteme.StripJudge
}

var (
	useMu sync.Mutex
	// ready は ikkyoku が認識器を差し替えたか。false なら FromImage は suteme を呼ばない
	// （呼ぶと suteme 既定の探索に落ちる）。
	ready bool
)

// Use は 1 組をまとめて差し替える。⚠️ **判定器が無い組では判定器を「使わない」にする**
// （`SetStripJudge(nil)`）—— 前の組の判定器を残すとちぐはぐになり、既定の探索へ
// 戻すと exe の隣やカレントディレクトリを見に行く。
func (s *Set) Use() {
	useMu.Lock()
	defer useMu.Unlock()
	suteme.SetPredictor(s.predictor)
	suteme.SetStripJudge(s.strip)
	ready = true
}

// Clear は認識器を外す（どの段も読めなかったとき）。以後の FromImage は断る。
// 判定器も外す（盤の位置合わせ `DetectRegion` は判定器なしで動く）。
func Clear() {
	useMu.Lock()
	defer useMu.Unlock()
	suteme.SetPredictor(nil)
	suteme.SetStripJudge(nil)
	ready = false
}

// Ready は認識器を差し替えてあるか。
func Ready() bool {
	useMu.Lock()
	defer useMu.Unlock()
	return ready
}

// LoadDir は suteme の学習ディレクトリ（生の `training_data_v*.bin` / `strip_data_v1.bin`）から
// 1 組を読む。**育て続けるデータ**なので、開発中は suteme のリポジトリを直接指す。
//
// 判定器（`strip_data_v1.bin`）は**読めなくても組として返す**（`StripErr`）。
//
// 盤の縁の判定器が無いと、**盤の位置が 1マス滑ったまま信頼度 1.00 で返ることがある。**
// 盤の外枠線が画像の外に出ている（＝ガイド枠が盤の縁ぴったり、あるいは内側）と、
// 検出した窓が 1マス滑っても格子線には乗るので suteme.ValidateBoard では見分けが
// 付かない。suteme がこれを直す唯一の手立てが帯の判定器（unslipByJudge）で、
// **judge が無ければ何もしない**（滑ったまま通る）。
//
// 実測（2026-08-22 の中継キャプチャ 1203x961。盤の下端が画像の外）:
//
//	判定器なし  (223, 32)-(974,875)  信頼度 1.00  ← 1マス上へ滑っている
//	判定器あり  (223,125)-(974,968)  信頼度 1.00  ← 正しい
func LoadDir(dir string) (*Set, error) {
	p, err := suteme.LoadPredictor(dir)
	if err != nil {
		return nil, fmt.Errorf("ikkyoku/recognize: %w", err)
	}
	s := &Set{Source: dir, predictor: p}
	path := filepath.Join(dir, suteme.DefaultStripFile)
	if samples, err := suteme.LoadStripData(path); err != nil {
		s.StripErr = fmt.Errorf("ikkyoku/recognize: %w", err)
	} else if j := suteme.NewStripJudge(samples); j == nil {
		s.StripErr = fmt.Errorf("ikkyoku/recognize: 帯の教師データが空です: %s", path)
	} else {
		s.strip = j
		s.StripSamples = j.Samples()
	}
	return s, nil
}

// LoadPack はダウンロードして置いた配布セット（`predictor.bin.gz` / `strip.bin.gz` /
// `source.txt`）から 1 組を読む。置かれていなければ ErrNoPack。
//
// ⚠️ **形式の版が exe と合わなければ読めずにエラーになる**（suteme の読み込みが断る）。
// 呼び出し側はそれを「読めなかった段」として下の段へ落とす。
func LoadPack(dir string) (*Set, error) {
	pred, err := os.ReadFile(filepath.Join(dir, PackPredictorFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoPack
		}
		return nil, fmt.Errorf("ikkyoku/recognize: %w", err)
	}
	src, _ := os.ReadFile(filepath.Join(dir, PackSourceFile))
	strip, stripErr := os.ReadFile(filepath.Join(dir, PackStripFile))
	if stripErr != nil {
		strip = nil
	}
	s, err := loadPack(pred, strip, cleanSource(string(src)), "model")
	if err != nil {
		return nil, err
	}
	s.Source = dir
	if stripErr != nil {
		s.StripErr = fmt.Errorf("ikkyoku/recognize: %w", stripErr)
	}
	return s, nil
}

// loadPack は配布セットの中身（gzip のまま）から 1 組を組み立てる。焼き込みと共用。
func loadPack(predGZ, stripGZ []byte, source, kind string) (*Set, error) {
	label := kind
	if source != "" {
		label += ":" + firstLine(source)
	}
	r, err := gunzip(predGZ)
	if err != nil {
		return nil, fmt.Errorf("ikkyoku/recognize: 学習データが読めません: %w", err)
	}
	p, err := suteme.PredictorFrom(r, label+":predictor")
	if err != nil {
		return nil, fmt.Errorf("ikkyoku/recognize: %w", err)
	}
	s := &Set{Source: source, Date: sourceDate(source), predictor: p}
	if len(stripGZ) == 0 {
		s.StripErr = errors.New("ikkyoku/recognize: 帯の教師データ（" + PackStripFile + "）がありません")
		return s, nil
	}
	sr, err := gunzip(stripGZ)
	if err != nil {
		s.StripErr = fmt.Errorf("ikkyoku/recognize: 帯の教師データが読めません: %w", err)
		return s, nil
	}
	j, err := suteme.StripJudgeFrom(sr, label+":strip")
	if err != nil {
		s.StripErr = fmt.Errorf("ikkyoku/recognize: %w", err)
		return s, nil
	}
	s.strip = j
	s.StripSamples = j.Samples()
	return s, nil
}

// cleanSource は source.txt の中身を整える。⚠️ **BOM を落とすこと** —— 書き出しは
// Windows PowerShell（`build/copy-model.ps1`）で、`Set-Content -Encoding utf8` は BOM を付ける。
func cleanSource(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(s, "\ufeff"))
}

// sourceDateRe は source.txt の末尾の「(yyyy-mm-dd)」（copy-model.ps1 が書く）。
var sourceDateRe = regexp.MustCompile(`\((\d{4}-\d{2}-\d{2})\)`)

// sourceDate は source.txt から配布セットの日付を読む。読めなければゼロ
// （**比べられないときは古いと見なさない**。呼び出し側の判断）。
func sourceDate(source string) time.Time {
	m := sourceDateRe.FindAllStringSubmatch(source, -1)
	if len(m) == 0 {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", m[len(m)-1][1])
	if err != nil {
		return time.Time{}
	}
	return t
}

// OlderThan は配布セットの日付が other より前か。**どちらかの日付が読めなければ false**
// （比べられないものを古いと決めつけて捨てない）。
func (s *Set) OlderThan(other *Set) bool {
	if s == nil || other == nil || s.Date.IsZero() || other.Date.IsZero() {
		return false
	}
	return s.Date.Before(other.Date)
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
