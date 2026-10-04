package recognize

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ShinteLab/suteme"
)

// 認識器を**どこから読むか**（2026-10-04 に 3 段にした）。
//
//	1. 学習データ   … suteme の学習ディレクトリ（`SutemeDataDir`）。自分で育てたもの（LoadDir）
//	2. 配布モデル   … 置いた配布セット（`SutemeModelDir`。LoadPack）
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

// 配布セット（2 段目と 3 段目）は **suteme の配布用の書き出しそのもの**（2026-10-04）:
//
//	training_data_v8.bin(.gz)   駒種の推論器（suteme.DefaultDataFile。版は exe に入っている suteme が決める）
//	strip_data_v1.bin(.gz)      盤の縁の判定器（suteme.DefaultStripFile）
//	export.json                 書き出しの記録（日時・件数。suteme の training.ExportInfo）
//
// `go run ./_cmd/suteme-training -export -gzip -out <場所>` が作る。**ikkyoku 独自の名前や形を
// 持たない** —— 持つと、suteme の書き出しを ikkyoku の形へ変える手順が要る（以前はそうだった）。
// ⚠️ **読めるファイル名は exe に入っている suteme の版で決まる**（`suteme.DefaultDataFile`）。
// 新しい版の書き出しを古い exe に置いても名前が合わず、読めない段として下へ落ちる（版合わせ）。

// ExportInfoFile は書き出しの記録の名前（suteme の training.ExportInfoFile と同じ）。
const ExportInfoFile = "export.json"

// ErrNoPack は配布セットが置かれていない（学習データのファイルが 1 つも無い）。
// **読めない（壊れている・版が違う）とは区別する** —— 既定の置き場所に何も無いのは
// 普通の状態で、⚠ に出すことではない。
var ErrNoPack = errors.New("配布モデルが置かれていません")

// Set は 1 つの段から読んだ認識器の 1 組。読んだだけでは効かない（`Use` で差し替える）。
type Set struct {
	// Source は出所（ディレクトリのパス、または焼き込みのラベル）。画面とログに出す。
	Source string
	// Date は書き出した日時（`export.json`）。読めなければゼロ。
	// **焼き込みより古い配布モデルを使わない**ための比較に使う。
	Date time.Time
	// Samples は駒種の推論器のサンプル数（`export.json`。読めなければ 0）。
	Samples int
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

// LoadPack は置いた配布セット（suteme の配布用の書き出し）から 1 組を読む。
// 置かれていなければ ErrNoPack。
func LoadPack(dir string) (*Set, error) {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoPack
	}
	s, err := LoadPackFS(os.DirFS(dir), "model")
	if err != nil {
		return nil, err
	}
	s.Source = dir
	return s, nil
}

// LoadPackFS は fsys の直下に置いた配布セットから 1 組を読む（置いたものと焼き込みで共用）。
// kind は suteme に名乗るラベルの頭（`Result.Debug` に出る）。
//
// ⚠️ **形式の版が exe と合わなければ読めずにエラーになる**（名前が合わない・suteme の読み込みが断る）。
// 呼び出し側はそれを「読めなかった段」として下の段へ落とす。
func LoadPackFS(fsys fs.FS, kind string) (*Set, error) {
	info := readExportInfo(fsys)
	s := &Set{Date: info.Date, Samples: info.Samples}
	label := kind
	if !info.Date.IsZero() {
		label += ":" + info.Date.Format("2006-01-02")
	}

	r, closeFn, err := openPackFile(fsys, suteme.DefaultDataFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if others := packDataFiles(fsys); len(others) > 0 {
				// 学習データはあるが、この exe が読む版ではない。
				return nil, fmt.Errorf("ikkyoku/recognize: この exe が読める %s がありません（%s があります。書き出した suteme とこの exe の版が合っていません）",
					suteme.DefaultDataFile, strings.Join(others, " / "))
			}
			return nil, ErrNoPack
		}
		return nil, fmt.Errorf("ikkyoku/recognize: %w", err)
	}
	p, err := suteme.PredictorFrom(r, label+":predictor")
	closeFn()
	if err != nil {
		return nil, fmt.Errorf("ikkyoku/recognize: %w", err)
	}
	s.predictor = p

	sr, closeStrip, err := openPackFile(fsys, suteme.DefaultStripFile)
	if err != nil {
		s.StripErr = fmt.Errorf("ikkyoku/recognize: 盤の縁の判定データ（%s）がありません", suteme.DefaultStripFile)
		return s, nil
	}
	defer closeStrip()
	j, err := suteme.StripJudgeFrom(sr, label+":strip")
	if err != nil {
		s.StripErr = fmt.Errorf("ikkyoku/recognize: %w", err)
		return s, nil
	}
	s.strip = j
	s.StripSamples = j.Samples()
	return s, nil
}

// openPackFile は name.gz（圧縮した書き出し）か name（圧縮しない書き出し）を開く。
// **.gz を先に見る**（焼き込み向けに suteme が -gzip で書いたもの）。
func openPackFile(fsys fs.FS, name string) (io.Reader, func(), error) {
	if f, err := fsys.Open(name + ".gz"); err == nil {
		zr, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("%s.gz が読めません: %w", name, err)
		}
		return zr, func() { zr.Close(); f.Close() }, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}

// packDataFiles は置いてある学習データらしいファイルの名前（版違いの案内に使う）。
func packDataFiles(fsys fs.FS) []string {
	names, _ := fs.Glob(fsys, "training_data_v*")
	sort.Strings(names)
	return names
}

// exportInfo は suteme の training.ExportInfo のうち読むものだけ。
type exportInfo struct {
	Date    time.Time `json:"date"`
	Samples int       `json:"samples"`
}

// readExportInfo は書き出しの記録を読む。無い・読めないならゼロ（**比べられないだけで、
// 読めないとは見なさない**）。
func readExportInfo(fsys fs.FS) exportInfo {
	var info exportInfo
	b, err := fs.ReadFile(fsys, ExportInfoFile)
	if err != nil {
		return info
	}
	_ = json.Unmarshal(b, &info)
	return info
}

// Label は画面とログに出す短い説明（「2026-10-04 の書き出し・9233 サンプル」）。
func (s *Set) Label() string {
	var parts []string
	if !s.Date.IsZero() {
		parts = append(parts, s.Date.Format("2006-01-02 15:04")+" の書き出し")
	}
	if s.Samples > 0 {
		parts = append(parts, fmt.Sprintf("%d サンプル", s.Samples))
	}
	return strings.Join(parts, "・")
}

// OlderThan は書き出した日時が other より前か。**どちらかの日時が読めなければ false**
// （比べられないものを古いと決めつけて捨てない）。
func (s *Set) OlderThan(other *Set) bool {
	if s == nil || other == nil || s.Date.IsZero() || other.Date.IsZero() {
		return false
	}
	return s.Date.Before(other.Date)
}
