package main

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/piecefont"
)

// FontService は「駒の字」（設定タブ）。
//
// **端末に入っているフォントから駒の字を焼いて、webview に流す。**
// 同梱できるフォントがライセンスの都合で限られる一方、**自分の端末に入っている
// フォントを自分の端末で表示に使うのは再配布ではない**、というのがこの機能の
// 拠り所（`piecefont` のパッケージコメント）。
//
// ⚠️ **焼いたフォントをファイルに書き出す口を持たないこと。** 書き出せると
// 「その端末で表示する」を越えてしまう（元フォントの条項が効く側の話になる）。
//
// ⚠️ **盤に当てるのはフロント。** ここが返すのは family 名と data URL までで、
// どの要素に当てるかは CSS の `--shogi-font` の仕事。
type FontService struct {
	logger   *slog.Logger
	settings *SettingsService

	mu sync.Mutex
	// available は端末のフォント一覧。**探したときだけ埋まる**（1 秒近くかかる）。
	available []piecefont.Font
	scanned   bool
	// baked は焼いた結果の使い回し。鍵に**更新時刻を含める**ので、
	// 元フォントを入れ替えたら焼き直しになる。
	baked map[string]string
	// previewSeq はプレビューの family を毎回変えるための連番（下記）。
	previewSeq int
}

func NewFontService(logger *slog.Logger, settings *SettingsService) *FontService {
	return &FontService{logger: logger, settings: settings, baked: map[string]string{}}
}

// FontFace は webview に登録する 1 つ分（family 名と data URL）。
type FontFace struct {
	// Family は CSS の font-family に入れる名前。
	//
	// ⚠️ **登録ごとに違う名前であること**（`ikkyoku.PieceFontFamily`）。
	// 同名で複数登録すると、どれが当たるかがブラウザ任せになる。
	Family string `json:"family"`
	// DataURL は焼いた TTF（`data:font/ttf;base64,...`）。19 グリフで約 10KB。
	DataURL string `json:"dataUrl"`
}

// FontState は「駒の字」の設定の今の状態。
type FontState struct {
	// Fonts は登録した駒フォントの一覧（登録順）。
	Fonts []PieceFontSettings `json:"fonts"`
	// Current は今使っている登録の ID。**空なら同梱。**
	Current string `json:"current"`
	// BuiltinName は同梱を選んでいるときの表示名（フロントに書かせない）。
	BuiltinName string `json:"builtinName"`
	// Required は駒に要る字。**説明とプレビューの見本を兼ねる。**
	// ⚠️ **フロントで並べ直さないこと**（`core/shogifont` が持っている）。
	Required string `json:"required"`
	// Face は今使うフォント。**同梱なら null**（フロントは既定に戻す）。
	Face *FontFace `json:"face"`
	// Note は選んだフォントを焼けなかった理由。**空なら問題なし。**
	//
	// ⚠️ **焼けなくても同梱で描けるので、エラーにしない**（設計原則3）。
	// フォントを消したりアンインストールしたりするのは普通に起きる。
	Note string `json:"note,omitempty"`
}

// PieceFontSettings は登録した駒フォント 1 つ（設定タブの 1 行）。
type PieceFontSettings struct {
	ID string `json:"id"`
	// Name は画面に出す名前。**空欄なら Go 側が解決した既定の名前が入る。**
	Name string `json:"name"`
	// Custom は名前を人が付けたか（false なら Name は既定の解決結果）。
	// エンジンの行と同じで、フロントは Custom のときだけ Name を欄に入れる。
	Custom bool   `json:"custom"`
	Path   string `json:"path"`
	File   string `json:"file"`
	Index  int    `json:"index"`

	// Missing は今このフォントに足りない駒の字。空なら使える。
	Missing string `json:"missing,omitempty"`
	// Note は使えない理由（消えた・字が足りない）。**表示用。**
	//
	// ⚠️ **使えない登録を黙って消さないこと。** フォントを入れ直せば戻るし、
	// 消すと「登録したはずのものが無い」になる。
	Note string `json:"note,omitempty"`
	// OK は今そのまま使えるか。
	OK bool `json:"ok"`

	// 元フォントの権利表記。**何に由来する字かを利用者が判断できるように出す。**
	// ⚠️ **空でも「制約が無い」ではない。**
	Copyright  string `json:"copyright,omitempty"`
	License    string `json:"license,omitempty"`
	LicenseURL string `json:"licenseUrl,omitempty"`
}

// FontChoice は端末に入っているフォント 1 つ（追加するときに選ぶ一覧の 1 行）。
type FontChoice struct {
	Path  string `json:"path"`
	File  string `json:"file"`
	Index int    `json:"index"`
	Name  string `json:"name"`
	// Family は英語の family 名。**日本語名と違うときだけ埋める**
	// （同じものを 2 回出さない）。
	Family string `json:"family,omitempty"`
	// Missing は足りない駒の字。**空なら使える。**
	//
	// ⚠️ **足りないものも一覧に出すこと**（選べない見た目にするだけ）。
	// 消すと、探しているのか対象外なのかが画面から分からない。
	Missing string `json:"missing,omitempty"`
	// Registered は既に登録済みか（**同じ書体を 2 つ登録しても意味が無い**）。
	Registered bool `json:"registered"`
}

// FontScan は端末のフォントを探した結果。
type FontScan struct {
	Fonts []FontChoice `json:"fonts"`
	// Usable は駒の字が揃っていた書体の数（**見出しに出す**）。
	Usable int `json:"usable"`
	// Dirs は探した場所。**表示用** —— 目当てのフォントが出てこないときに、
	// どこを見たのかが分からないと打つ手が無い。
	Dirs []string `json:"dirs"`
}

// State は今の設定を返す（設定タブを開いたとき・起動時）。
func (s *FontService) State() FontState {
	cfg := s.settings.config()
	return s.state(cfg)
}

func (s *FontService) state(cfg ikkyoku.Config) FontState {
	list := cfg.PieceFontList()
	rows := make([]PieceFontSettings, 0, len(list))
	for _, e := range list {
		rows = append(rows, s.row(e))
	}
	st := FontState{
		Fonts:       rows,
		Current:     cfg.PieceFont,
		BuiltinName: ikkyoku.BuiltinPieceFontName,
		Required:    piecefont.Required(),
	}
	entry, ok := cfg.CurrentPieceFont()
	if !ok {
		// **設定が消えた登録を指していても同梱に落ちるだけ**（`CurrentPieceFont`）。
		// ⚠️ **そのときは Current も空に直して返すこと** —— 直さないと
		// 画面が「一覧に無いものを選んでいる」状態になる。
		st.Current = ""
		return st
	}
	face, err := s.bake(entry)
	if err != nil {
		// 焼けなくても同梱で描ける（設計原則3）。理由だけ出す。
		s.logger.Warn("駒フォントを焼けませんでした", "id", entry.ID, "path", entry.Path, "error", err)
		st.Note = fmt.Sprintf("「%s」を使えないので同梱の字で描いています: %v", entry.DisplayName(), err)
		return st
	}
	st.Face = face
	return st
}

// row は登録 1 つを画面に出す形にする。**元フォントを読み直す**ので、
// アンインストールされていれば理由が出る。
func (s *FontService) row(e ikkyoku.PieceFontEntry) PieceFontSettings {
	row := PieceFontSettings{
		ID:     e.ID,
		Name:   e.DisplayName(),
		Custom: strings.TrimSpace(e.Name) != "",
		Path:   e.Path,
		File:   filepath.Base(e.Path),
		Index:  e.Index,
	}
	f, err := piecefont.Read(e.Path, e.Index)
	if err != nil {
		row.Note = "このフォントが見つかりません（消したか、別の場所へ移した可能性があります）"
		return row
	}
	row.Missing = f.Missing
	row.Copyright = f.Copyright
	row.License = f.License
	row.LicenseURL = f.LicenseURL
	if !f.Usable() {
		row.Note = "駒に要る字が足りません: " + f.Missing
		return row
	}
	row.OK = true
	return row
}

// bake は登録 1 つを焼いて data URL にする。
//
// ⚠️ **ディスクには残さない。** 残すと**元フォントを入れ替えたのに古い字で描く**
// という、画面からは気づけない事故が起きる。使い回しの鍵に更新時刻を入れて
// あるのも同じ理由で、**入れ替えたら焼き直しになる。**
func (s *FontService) bake(e ikkyoku.PieceFontEntry) (*FontFace, error) {
	family := ikkyoku.PieceFontFamily(e.ID)
	key := fmt.Sprintf("%s|%d|%s|%s", e.Path, e.Index, family, modStamp(e.Path))

	s.mu.Lock()
	if url, ok := s.baked[key]; ok {
		s.mu.Unlock()
		return &FontFace{Family: family, DataURL: url}, nil
	}
	s.mu.Unlock()

	url, err := bakeDataURL(e.Path, e.Index, family)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	// **鍵が変わったら古いものは要らない**（1 つしか使わないので溜めない）。
	s.baked = map[string]string{key: url}
	s.mu.Unlock()
	return &FontFace{Family: family, DataURL: url}, nil
}

func bakeDataURL(path string, index int, family string) (string, error) {
	ttf, err := piecefont.Build(path, index, family)
	if err != nil {
		return "", err
	}
	return "data:font/ttf;base64," + base64.StdEncoding.EncodeToString(ttf), nil
}

// modStamp は元フォントの更新時刻と大きさ。読めなければ空
// （そのときは焼く側が失敗するので、ここで理由を出す必要は無い）。
func modStamp(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d-%d", info.ModTime().UnixNano(), info.Size())
}

// Scan は端末に入っているフォントを探す。
//
// ⚠️ **押したときだけ呼ぶこと。** 実測で 190 ファイル・577MB を読んで 1 秒弱かかる
// （起動のたびに走らせる類の処理ではない）。フロントは待ちを出す。
func (s *FontService) Scan() (FontScan, error) {
	fonts, err := piecefont.List()
	if err != nil {
		return FontScan{Dirs: piecefont.Dirs()}, err
	}

	// 既に登録してあるものには印を付ける（同じ書体を 2 つ登録しても意味が無い）。
	registered := map[string]bool{}
	for _, e := range s.settings.config().PieceFontList() {
		registered[choiceKey(e.Path, e.Index)] = true
	}

	out := FontScan{Fonts: make([]FontChoice, 0, len(fonts)), Dirs: piecefont.Dirs()}
	for _, f := range fonts {
		c := FontChoice{
			Path: f.Path, File: f.File, Index: f.Index,
			Name:       f.Name,
			Missing:    f.Missing,
			Registered: registered[choiceKey(f.Path, f.Index)],
		}
		// 英語名は日本語名と違うときだけ（同じものを 2 回出さない）。
		if f.Family != "" && f.Family != f.Name {
			c.Family = f.Family
		}
		if f.Usable() {
			out.Usable++
		}
		out.Fonts = append(out.Fonts, c)
	}

	s.mu.Lock()
	s.available = fonts
	s.scanned = true
	s.mu.Unlock()
	s.logger.Info("端末のフォントを探しました", "書体", len(fonts), "焼ける", out.Usable)
	return out, nil
}

func choiceKey(path string, index int) string {
	return fmt.Sprintf("%s|%d", strings.ToLower(path), index)
}

// Preview は**登録せずに**焼いて data URL を返す（選ぶ前に字を見るため）。
//
// ⚠️ **family を毎回変えている。** 同じ名前で焼き直すと、webview 側で
// どちらが当たるかがブラウザ任せになり、**プレビューが前のフォントのまま**に
// 見えることがある。登録の family（`PieceFontFamily`）とも必ず違う名前にすること。
func (s *FontService) Preview(path string, index int) (FontFace, error) {
	f, err := piecefont.Read(path, index)
	if err != nil {
		return FontFace{}, err
	}
	if !f.Usable() {
		return FontFace{}, fmt.Errorf("駒に要る字が足りません: %s", f.Missing)
	}
	s.mu.Lock()
	s.previewSeq++
	family := fmt.Sprintf("ShogiPreview-%d", s.previewSeq)
	s.mu.Unlock()

	url, err := bakeDataURL(path, index, family)
	if err != nil {
		return FontFace{}, err
	}
	return FontFace{Family: family, DataURL: url}, nil
}

// Add は端末のフォントを 1 つ登録して、**そのまま使う状態にする。**
//
// ⚠️ **登録しただけで使わない、という状態を作らない。** 登録する操作は
// 「この字で見たい」なので、そのあともう一度選ばせるのは 1 手多い
// （気が変わったら一覧から選び直せばよい）。
func (s *FontService) Add(path string, index int) (FontState, error) {
	f, err := piecefont.Read(path, index)
	if err != nil {
		return s.State(), err
	}
	if !f.Usable() {
		return s.State(), fmt.Errorf("「%s」は駒に要る字が足りません: %s", f.Name, f.Missing)
	}

	var id string
	cfg, err := s.settings.editConfig(func(cfg *ikkyoku.Config) {
		// 同じ書体が既にあれば足さずにそれを選ぶ（**同じ字を 2 つ登録しても意味が無い**）。
		for _, e := range cfg.PieceFonts {
			if choiceKey(e.Path, e.Index) == choiceKey(f.Path, f.Index) {
				id = e.ID
				cfg.PieceFont = id
				return
			}
		}
		id = ikkyoku.NextPieceFontID(cfg.PieceFonts)
		cfg.PieceFonts = append(cfg.PieceFonts, ikkyoku.PieceFontEntry{
			ID: id, Path: f.Path, Index: f.Index, Source: f.Name,
		})
		cfg.PieceFont = id
	})
	if err != nil {
		return s.state(cfg), err
	}
	s.logger.Info("駒フォントを登録しました", "id", id, "name", f.Name, "path", f.Path, "index", f.Index)
	return s.state(cfg), nil
}

// Use は使うフォントを切り替える（**空文字なら同梱に戻す**）。
func (s *FontService) Use(id string) (FontState, error) {
	if id != "" && !s.has(id) {
		return s.State(), fmt.Errorf("その駒フォントの登録が見つかりません")
	}
	cfg, err := s.settings.editConfig(func(cfg *ikkyoku.Config) {
		cfg.PieceFont = id
	})
	return s.state(cfg), err
}

// Remove は登録を消す。
//
// ⚠️ **使っていたものを消したら同梱に戻すこと。** 戻さないと
// **一覧に無いものを選んでいる状態**になる。
func (s *FontService) Remove(id string) (FontState, error) {
	if !s.has(id) {
		return s.State(), fmt.Errorf("その駒フォントの登録が見つかりません")
	}
	cfg, err := s.settings.editConfig(func(cfg *ikkyoku.Config) {
		out := make([]ikkyoku.PieceFontEntry, 0, len(cfg.PieceFonts))
		for _, e := range cfg.PieceFonts {
			if e.ID != id {
				out = append(out, e)
			}
		}
		cfg.PieceFonts = out
		if cfg.PieceFont == id {
			cfg.PieceFont = ""
		}
	})
	return s.state(cfg), err
}

// Rename は表示名を付け替える（**空にすると元フォントの名前に戻る**）。
func (s *FontService) Rename(id, name string) (FontState, error) {
	if !s.has(id) {
		return s.State(), fmt.Errorf("その駒フォントの登録が見つかりません")
	}
	cfg, err := s.settings.editConfig(func(cfg *ikkyoku.Config) {
		for i := range cfg.PieceFonts {
			if cfg.PieceFonts[i].ID == id {
				cfg.PieceFonts[i].Name = strings.TrimSpace(name)
			}
		}
	})
	return s.state(cfg), err
}

func (s *FontService) has(id string) bool {
	for _, e := range s.settings.config().PieceFontList() {
		if e.ID == id {
			return true
		}
	}
	return false
}
