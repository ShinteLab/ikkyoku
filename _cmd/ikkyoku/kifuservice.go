package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ShinteLab/core/kifu"
	"github.com/ShinteLab/kicho"
	"github.com/ShinteLab/kicho/format"
	"github.com/ShinteLab/kicho/scrape"
	"github.com/ShinteLab/kicho/store"
)

// KifuService は棋譜データベース（棚）をフロントに公開する。
//
// **棋譜データベースの実装は `kicho` のままで、ikkyoku はそれを利用する側**
// （`kicho.Library` を開いて呼ぶだけ）。ここに書くのは DTO の変換と入力チェックだけで、
// **取得・保存・検索のロジックは kicho に置く。** kicho の UI は将来
// 「テスト用のモック」または「ikkyoku 以外の将棋ソフトからの読み込み口」になる。
//
// ⚠️ **棚は解析の前提条件ではない**（設計原則3「段階的に劣化すること」）。
// DB が開けなくてもアプリは起動し、撮った 1 局面と貼った棋譜の解析は今までどおり
// 動く。**このサービスのメソッドだけが理由を返して何もしない。**
//
// ⚠️ **棚に入るのは KIF の原本だけ。** 検討ツリー（`position.Study`）も評価値も
// 入れない（kicho の「原本を保持する。整形し直さない」原則そのもの）。
// ikkyoku で伸ばした枝を KIF に書き戻さないこと —— 棚の KIF は
// 「実際に現れた指し手」で、エンジンの読み筋や自分で指した手ではない。
//
// **取得(Fetch)と保存(Save)は別操作。** 対局中の棋譜は随時更新されるため、
// 「取得して内容を確認 → その表示内容をそのまま保存」という流れにするのが目的で、
// 保存時にサイトへ取り直しには行かない（kicho の役割分担をそのまま引き継ぐ）。
type KifuService struct {
	logger *slog.Logger

	// study は「解析する」の行き先（SendToStudy）。
	//
	// ⚠️ **`PositionService`（訂正タブ）は持たない。** 棋譜は既に確定した局面なので
	// 訂正タブを経由しない（`StudyService.LoadKifu` と同じ線引き）。撮った局面が
	// 戻り先として残る。
	study *StudyService

	mu      sync.Mutex
	path    string
	lib     *kicho.Library
	openErr error
}

// NewKifuService は棚を**まだ開かずに**作る。
//
// 開くのは open（main が起動時に、設定タブがパスを変えたときに呼ぶ）。
// **コンストラクタで開かないのは、開けなかったときにアプリを止めないため。**
func NewKifuService(logger *slog.Logger, study *StudyService) *KifuService {
	return &KifuService{logger: logger, study: study}
}

// open は指定パスの棚を開く（既に開いていれば閉じてから開き直す）。
//
// ⚠️ **エラーを返さない。** 開けなかったことは openErr に残して Status から
// 見せる —— ここで失敗を上へ投げると、呼び出し側（main の起動シーケンス・
// 設定の保存）が「棚が開けないとアプリが動かない」形になってしまう。
func (s *KifuService) open(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openLocked(path)
}

func (s *KifuService) openLocked(path string) {
	if s.lib != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.lib.Close(ctx); err != nil {
			s.logger.Warn("棋譜データベースを閉じられませんでした", "path", s.path, "error", err)
		}
		cancel()
		s.lib = nil
	}
	s.path, s.openErr = path, nil

	if strings.TrimSpace(path) == "" {
		s.openErr = fmt.Errorf("棋譜データベースの場所が決まっていません")
		s.logger.Warn("棋譜データベースの場所が決まっていません")
		return
	}
	lib, err := kicho.Open(path, s.logger)
	if err != nil {
		s.openErr = err
		s.logger.Warn("棋譜データベースを開けませんでした", "path", path, "error", err)
		return
	}
	s.lib = lib
	s.logger.Info("棋譜データベースを開きました", "path", path)
}

// close は棚を閉じる（アプリの終了時。quit から呼ぶ）。
func (s *KifuService) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lib == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.lib.Close(ctx); err != nil {
		s.logger.Warn("棋譜データベースを閉じられませんでした", "path", s.path, "error", err)
	}
	s.lib = nil
}

// library は開いている棚を返す。開けていなければ**理由の分かるエラー**を返す。
//
// ⚠️ **すべての公開メソッドがここを通ること。** nil の Library を触ると panic するし、
// 「なぜ使えないのか」を画面に出せないと設定タブへ辿り着けない。
func (s *KifuService) library() (*kicho.Library, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lib != nil {
		return s.lib, nil
	}
	if s.openErr != nil {
		return nil, fmt.Errorf("棋譜データベースを開けていません（%v）。設定タブで場所を確かめてください", s.openErr)
	}
	return nil, fmt.Errorf("棋譜データベースを開けていません。設定タブで場所を確かめてください")
}

// KifuDBStatus は棚の状態（設定タブの「棋譜データベース」の行）。
type KifuDBStatus struct {
	// Path は今開こうとしている DB ファイルの場所（既定は解決済み）。
	Path string `json:"path"`
	// Ready は開けているか。**false でもアプリは動く**（設計原則3）。
	Ready bool `json:"ready"`
	// Count は保存件数（開けていなければ 0）。
	Count int `json:"count"`
	// Error は開けなかった / 件数を数えられなかった理由。
	Error string `json:"error"`
}

// Status は棚の状態を返す（設定タブ用）。
func (s *KifuService) Status() KifuDBStatus {
	s.mu.Lock()
	st := KifuDBStatus{Path: s.path, Ready: s.lib != nil}
	lib, openErr := s.lib, s.openErr
	s.mu.Unlock()

	if openErr != nil {
		st.Error = openErr.Error()
	}
	if lib == nil {
		return st
	}
	n, err := lib.Store().Count(context.Background())
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Count = n
	return st
}

// GameSummary は一覧表示用の 1 件（KIF 本文なし）。
type GameSummary struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	SourceID string `json:"sourceId"`
	// SourceURL は取得元の URL。
	//
	// ⚠️ **解析タブの「再読み込み」の鍵になる**（SendToStudy が一緒に渡す）。
	// 連盟の中継はここが**中継ページ（HTML）の URL** なので、取り直す側は
	// HTML から .kif を辿れる必要がある。
	SourceURL string `json:"sourceUrl"`
	Event     string `json:"event"`
	Handicap  string `json:"handicap"`
	Place     string `json:"place"`
	Black     string `json:"black"`
	White     string `json:"white"`
	StartedAt string `json:"startedAt"` // RFC3339。未設定なら空文字
	// EndMark は終局の種別（例 "投了"）。対局中は空。
	EndMark string `json:"endMark"`
	// Finished は終局済みかどうか。UI で手数の横に「（終局）」を出すのに使う。
	Finished bool `json:"finished"`
	Moves    int  `json:"moves"`
}

// GameDetail は棋譜 1 件の詳細（KIF 本文つき）。
// Fetch が返したものをそのまま Save に渡せる。
type GameDetail struct {
	GameSummary
	KIF string `json:"kif"`
	// Encoding は KIF の元の文字コード（本文は UTF-8 に寄せてある）。
	// 連盟の中継は Shift_JIS なので、保存の記録として一緒に運ぶ。
	Encoding string `json:"encoding"`
}

func toSummary(r store.Record) GameSummary {
	s := GameSummary{
		ID:        r.ID,
		Source:    r.Source,
		SourceID:  r.SourceID,
		SourceURL: r.SourceURL,
		Event:     r.Event,
		Handicap:  r.Handicap,
		Place:     r.Place,
		Black:     r.Black,
		White:     r.White,
		EndMark:   r.EndMark,
		Finished:  r.Finished(),
		Moves:     r.Moves,
	}
	if !r.StartedAt.IsZero() {
		s.StartedAt = r.StartedAt.Format(time.RFC3339)
	}
	return s
}

// countMoves は KIF テキストの手数を数える。
//
// **行数を数えるのではなく core/kifu で解析する。** サイトが配信している .kif には
// コメント行（*）や `# --- Kifu for Windows ...` が混ざっており、行を数えると
// それらまで手数に入る。
func countMoves(kifText string) int {
	doc, err := kicho.ParseKIF(kifText)
	if err != nil {
		return 0
	}
	return len(doc.Moves)
}

// SearchQuery は検索条件。空の項目は「条件なし」。
type SearchQuery struct {
	// Text は棋戦名・対局者・場所への部分一致。
	// 3 文字以上なら索引（FTS5 trigram）が効く。それ未満は走査になる。
	Text string `json:"text"`
	// From / To は開始日の範囲。"YYYY-MM-DD" 形式。空なら無制限。
	From string `json:"from"`
	To   string `json:"to"`
	// FinishedOnly が true なら終局済みのみ。
	FinishedOnly bool `json:"finishedOnly"`
	// Limit は取得件数の上限（0 なら無制限）。
	Limit int `json:"limit"`
}

// MinSearchLength は索引が効く最小文字数（UI の案内表示に使う）。
//
// ⚠️ **フロントに 3 と書かないこと**（trigram の性質は store が持っている）。
const MinSearchLength = store.MinTrigramLen

// jst は日付入力の解釈に使うタイムゾーン。
var jst = time.FixedZone("JST", 9*60*60)

// List は保存済み棋譜の一覧を新しい順に返す（KIF 本文は含まない）。
func (s *KifuService) List() ([]GameSummary, error) {
	return s.Search(SearchQuery{})
}

// Search は条件に合う棋譜を新しい順に返す（KIF 本文は含まない）。
func (s *KifuService) Search(q SearchQuery) ([]GameSummary, error) {
	lib, err := s.library()
	if err != nil {
		return nil, err
	}

	sq := store.Query{
		Text:         q.Text,
		FinishedOnly: q.FinishedOnly,
		Limit:        q.Limit,
	}

	// 日付は JST の 0:00 / 23:59:59 として解釈する。
	if q.From != "" {
		t, err := time.ParseInLocation("2006-01-02", q.From, jst)
		if err != nil {
			return nil, fmt.Errorf("開始日の形式が不正です: %s", q.From)
		}
		sq.From = t
	}
	if q.To != "" {
		t, err := time.ParseInLocation("2006-01-02", q.To, jst)
		if err != nil {
			return nil, fmt.Errorf("終了日の形式が不正です: %s", q.To)
		}
		sq.To = t.Add(24*time.Hour - time.Second)
	}
	if !sq.From.IsZero() && !sq.To.IsZero() && sq.To.Before(sq.From) {
		return nil, fmt.Errorf("終了日が開始日より前になっています")
	}

	recs, err := lib.Store().Search(context.Background(), sq)
	if err != nil {
		return nil, err
	}
	out := make([]GameSummary, 0, len(recs))
	for _, r := range recs {
		out = append(out, toSummary(r))
	}
	return out, nil
}

// Count は保存件数を返す（検索結果と全体を比べて表示するため）。
func (s *KifuService) Count() (int, error) {
	lib, err := s.library()
	if err != nil {
		return 0, err
	}
	return lib.Store().Count(context.Background())
}

// Get は棋譜 1 件を KIF 本文つきで返す。
func (s *KifuService) Get(id string) (GameDetail, error) {
	lib, err := s.library()
	if err != nil {
		return GameDetail{}, err
	}
	r, err := lib.Store().Get(context.Background(), id)
	if err != nil {
		return GameDetail{}, err
	}
	return GameDetail{GameSummary: toSummary(r), KIF: r.Body, Encoding: r.Encoding}, nil
}

// Delete は棋譜を削除する。
func (s *KifuService) Delete(id string) error {
	lib, err := s.library()
	if err != nil {
		return err
	}
	return lib.Store().Delete(context.Background(), id)
}

// SendToStudy は棚の棋譜を解析タブへ送る（一覧の「解析する」）。
//
// **kicho の UI にあった「棋譜 URL をコピー」の置き換え。** あちらは ShogiHome 等の
// 外部ツールへ渡すためのものだが、ikkyoku では渡す先が自分自身なので、
// 代わりに置くのがこれ。
//
// ⚠️ **取得元の URL も一緒に渡す。** これがあると解析タブの「再読み込み」で
// 中継の最新手を追えるようになる（TODO.md「本譜は URL が更新し続ける」）。
// 貼り付け登録（paste）には取得元が無いので空のまま。
//
// **戻り値は KifuLoad を共有している** —— フロントの描き方が
// 「根を入れ替えて解析タブを開き、1 行の説明を出す」で同じだから
// （新規対局・貼り付け・URL が既にそうしている）。**別の経路を作らないこと。**
func (s *KifuService) SendToStudy(id string) (KifuLoad, error) {
	d, err := s.Get(id)
	if err != nil {
		return KifuLoad{State: s.study.State()}, err
	}
	return s.study.loadKifuFrom(d.KIF, d.SourceURL)
}

// Fetch はライブ中継から棋譜を取得する（**保存はしない**）。
//
// 取得元は入力から判別する。
//
//   - live.shogi.or.jp の URL  → 日本将棋連盟の棋譜中継
//   - それ以外（URL / 棋譜 ID）→ 読売（竜王戦）
//
// 対局中の棋譜も取得できる（その場合 Finished は false）。
func (s *KifuService) Fetch(input string) (GameDetail, error) {
	lib, err := s.library()
	if err != nil {
		return GameDetail{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if strings.TrimSpace(input) == "" {
		return GameDetail{}, fmt.Errorf("URL または棋譜 ID を入力してください")
	}
	if scrape.IsShogiLiveURL(input) {
		id, err := lib.ResolveShogiLiveInput(ctx, input)
		if err != nil {
			return GameDetail{}, err
		}
		return fetchShogiLive(ctx, lib, id)
	}

	id, err := resolveRyuohInput(ctx, lib, input)
	if err != nil {
		return GameDetail{}, err
	}
	return fetchRyuoh(ctx, lib, id)
}

// Refresh は取得済みのカードを取り直す。
//
// 入力欄からの Fetch と違って**取得元が分かっている**ので、判別も
// 中継ページ → 棋譜 ID の往復も挟まらず、棋譜 ID で直接取りに行く。
func (s *KifuService) Refresh(source, sourceID string) (GameDetail, error) {
	lib, err := s.library()
	if err != nil {
		return GameDetail{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if strings.TrimSpace(sourceID) == "" {
		return GameDetail{}, fmt.Errorf("取得元の棋譜 ID が空です")
	}
	switch source {
	case store.SourceShogiLive:
		return fetchShogiLive(ctx, lib, sourceID)
	case store.SourceYomiuri, "":
		return fetchRyuoh(ctx, lib, sourceID)
	default:
		return GameDetail{}, fmt.Errorf("取り直せない取得元です: %s", source)
	}
}

// Save は Fetch で取得して画面に表示している内容を**そのまま**保存する。
// サイトへ取り直しには行かない。
//
// 同じ棋譜を保存し直しても重複せず、既存の ID を維持したまま内容が更新される
// （(source, source_id) の upsert。対局中に保存 → 終局後に取り直して保存、で
// 同じ ID のまま最新になる）。
func (s *KifuService) Save(d GameDetail) (GameSummary, error) {
	lib, err := s.library()
	if err != nil {
		return GameSummary{}, err
	}
	if d.SourceID == "" {
		return GameSummary{}, fmt.Errorf("保存する棋譜がありません。先に取得してください")
	}
	if d.KIF == "" {
		return GameSummary{}, fmt.Errorf("棋譜が空です")
	}

	// 取得元によって諸元（どこから取ったか）の組み立てが変わる。
	source, sourceURL := d.Source, d.SourceURL
	switch source {
	case store.SourceShogiLive:
		sourceURL = lib.ShogiLiveViewerURL(d.SourceID)
	case store.SourceYomiuri, "":
		// 取得元が入っていない古い画面状態でも読売として保存できるようにしておく。
		source = store.SourceYomiuri
		sourceURL = scrape.ViewerURL(d.SourceID)
	default:
		return GameSummary{}, fmt.Errorf("保存できない取得元です: %s", source)
	}

	encoding := d.Encoding
	if encoding == "" {
		encoding = kicho.EncodingUTF8
	}

	g := store.Game{
		Source:    source,
		SourceID:  d.SourceID,
		SourceURL: sourceURL,
		Event:     d.Event,
		Handicap:  d.Handicap,
		Place:     d.Place,
		Black:     d.Black,
		White:     d.White,
		EndMark:   d.EndMark,
		Moves:     countMoves(d.KIF),

		// 画面に出している内容をそのまま書き込む（サイトへ取り直しには行かない）。
		Body:     d.KIF,
		Format:   string(format.KIF),
		Encoding: encoding,
	}
	if d.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, d.StartedAt); err == nil {
			g.StartedAt = t
		}
	}

	rec, err := lib.Store().Save(context.Background(), g)
	if err != nil {
		return GameSummary{}, err
	}
	return toSummary(rec), nil
}

// PreviewKIF は KIF テキストを解析して内容を返す（**保存はしない**）。
func (s *KifuService) PreviewKIF(text string) (GameDetail, error) {
	doc, err := kicho.ParseKIF(text)
	if err != nil {
		return GameDetail{}, err
	}
	return docToDetail(doc, store.SourcePaste, "", text, kicho.EncodingUTF8), nil
}

// ImportKIF は KIF テキストを解析して棚に入れる（入力タブの「棚に登録する」）。
//
// 取得元での一意な ID が無いため、**毎回新しい棋譜として登録される**
// （同じものを 2 回登録すれば 2 件になる。重複は棋譜タブから消す）。
func (s *KifuService) ImportKIF(text string) (GameSummary, error) {
	lib, err := s.library()
	if err != nil {
		return GameSummary{}, err
	}
	rec, err := lib.ImportKIF(context.Background(), text)
	if err != nil {
		return GameSummary{}, err
	}
	return toSummary(rec), nil
}

// PreviewURL は URL から KIF を取得して解析する（**保存はしない**）。
func (s *KifuService) PreviewURL(rawURL string) (GameDetail, error) {
	lib, err := s.library()
	if err != nil {
		return GameDetail{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	text, encoding, err := lib.FetchKIFFromURL(ctx, rawURL)
	if err != nil {
		return GameDetail{}, err
	}
	doc, err := kicho.ParseKIF(text)
	if err != nil {
		return GameDetail{}, err
	}
	return docToDetail(doc, store.SourceURL, rawURL, text, encoding), nil
}

// ImportURL は URL から KIF を取得して棚に入れる。
func (s *KifuService) ImportURL(rawURL string) (GameSummary, error) {
	lib, err := s.library()
	if err != nil {
		return GameSummary{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rec, err := lib.ImportURL(ctx, rawURL)
	if err != nil {
		return GameSummary{}, err
	}
	return toSummary(rec), nil
}

// fetchRyuoh は読売のペイロードから KIF を組み立てる（保存はしない）。
func fetchRyuoh(ctx context.Context, lib *kicho.Library, id string) (GameDetail, error) {
	g, err := lib.FetchRyuoh(ctx, id)
	if err != nil {
		return GameDetail{}, err
	}

	d := GameDetail{
		GameSummary: GameSummary{
			Source:   store.SourceYomiuri,
			SourceID: g.SourceID,
			// 諸元: どこから取ったか。読売は棋譜ビューアの URL。
			SourceURL: scrape.ViewerURL(g.SourceID),
			Event:     g.Event,
			Handicap:  g.Handicap,
			Place:     g.Place,
			Black:     g.Black,
			White:     g.White,
			EndMark:   g.EndMark,
			Finished:  g.Finished(),
			Moves:     len(g.Moves),
		},
		// スクレイピングは構造化データから組み立てるので、これ自体が原本。
		KIF:      g.KIF(),
		Encoding: kicho.EncodingUTF8,
	}
	if !g.StartedAt.IsZero() {
		d.StartedAt = g.StartedAt.Format(time.RFC3339)
	}
	return d, nil
}

// fetchShogiLive は連盟の中継から .kif を取る（保存はしない）。
//
// 本文は**サイトが配信している原本のまま**渡す（整形し直さない）。
// 画面に出すメタデータだけ解析結果から取る。
func fetchShogiLive(ctx context.Context, lib *kicho.Library, id string) (GameDetail, error) {
	g, err := lib.FetchShogiLive(ctx, id)
	if err != nil {
		return GameDetail{}, err
	}

	end := g.Doc.EndMark()
	d := GameDetail{
		GameSummary: GameSummary{
			Source:   store.SourceShogiLive,
			SourceID: g.SourceID,
			// 諸元: どこから取ったか。人が開いて確認するのは中継ページ。
			SourceURL: lib.ShogiLiveViewerURL(g.SourceID),
			Event:     g.Doc.Event,
			Handicap:  g.Doc.Handicap,
			Place:     g.Doc.Place,
			Black:     g.Doc.Black,
			White:     g.Doc.White,
			EndMark:   end,
			Finished:  end != "",
			Moves:     len(g.Doc.Moves),
		},
		KIF:      g.KIF,
		Encoding: g.Encoding,
	}
	if !g.Doc.StartedAt.IsZero() {
		d.StartedAt = g.Doc.StartedAt.Format(time.RFC3339)
	}
	return d, nil
}

// docToDetail は解析結果を表示用に変換する。
//
// 表示する KIF は解析結果を組み立て直したものではなく **原本（body）をそのまま**使う。
// 保存されるのも原本なので、画面と保存内容を食い違わせないため
// （組み立て直すと変化・コメント・不成などが落ちる）。
func docToDetail(doc kifu.Document, source, sourceURL, body, encoding string) GameDetail {
	end := doc.EndMark()
	d := GameDetail{
		GameSummary: GameSummary{
			Source:    source,
			SourceURL: sourceURL,
			Event:     doc.Event,
			Handicap:  doc.Handicap,
			Place:     doc.Place,
			Black:     doc.Black,
			White:     doc.White,
			EndMark:   end,
			Finished:  end != "",
			Moves:     len(doc.Moves),
		},
		KIF:      body,
		Encoding: encoding,
	}
	if !doc.StartedAt.IsZero() {
		d.StartedAt = doc.StartedAt.Format(time.RFC3339)
	}
	return d
}

// resolveRyuohInput は入力（対局ページ URL / 棋譜ビューア URL / 棋譜 ID）を
// 読売の棋譜 ID に解決する。
func resolveRyuohInput(ctx context.Context, lib *kicho.Library, input string) (string, error) {
	in := strings.TrimSpace(input)
	if in == "" {
		return "", fmt.Errorf("URL または棋譜 ID を入力してください")
	}
	if !strings.HasPrefix(in, "http://") && !strings.HasPrefix(in, "https://") {
		return in, nil // 棋譜 ID とみなす
	}
	// 棋譜ビューアの URL が直接貼られた場合はそこから ID を取る。
	if id, err := kicho.KifuIDFromURL(in); err == nil {
		return id, nil
	}
	// 対局ページ URL → iframe を辿って棋譜 ID を得る。
	return lib.ResolveRyuohURL(ctx, in)
}
