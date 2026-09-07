package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ShinteLab/kicho"
	"github.com/ShinteLab/kicho/store"
)

// KifuService は棋譜データベース（棚）をフロントに公開する。
//
// **棋譜データベースの実装は `kicho` のままで、ikkyoku はそれを利用する側**
// （`kicho.Library` を開いて呼ぶだけ）。ここに書くのは DTO の変換と入力チェックだけで、
// **取得・保存・検索のロジックは kicho に置く。** kicho の UI は将来
// 「テスト用のモック」または「ikkyoku 以外の将棋ソフトからの読み込み口」になる。
//
// ⚠️ **取得元の知識をここに書かないこと。** 取得元ごとの `source_url` の決め方、
// 文字コードの既定、手数の数え方、「再読み込みで取り直せる URL か」の判断は
// **すべて kicho 側にある**（`kicho.Fetched` / `Library.Save` / `RefetchableURL`）。
// 以前はここと `kicho/_cmd/kicho/kifuservice.go` が同じ変換をそれぞれ持っていて、
// 片方だけ直せば黙って挙動が割れる状態だった。
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

// 操作ごとの制限時間。
//
// ⚠️ **`context.Background()` をそのまま渡さないこと。** kicho の store は
// 接続を1本に絞っているので、止まらないクエリが1つあると以後の棚の操作が
// 全部待たされる（画面からは「棚が反応しない」に見える）。
const (
	kifuDBTimeout    = 10 * time.Second
	kifuNetTimeout   = 60 * time.Second
	kifuCloseTimeout = 5 * time.Second
)

func kifuDBContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), kifuDBTimeout)
}

func kifuNetContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), kifuNetTimeout)
}

// Open は指定パスの棚を開く（既に開いていれば閉じてから開き直す）。
//
// ⚠️ **エラーを返さない。** 開けなかったことは openErr に残して Status から
// 見せる —— ここで失敗を上へ投げると、呼び出し側（main の起動シーケンス・
// 設定の保存）が「棚が開けないとアプリが動かない」形になってしまう。
//
// ⚠️ **`//wails:ignore` を外さないこと。** これは `_cmd/ikkyoku` が起動・終了で
// 呼ぶための口で、**フロントの API ではない**（外すと bindings に出てしまう）。
//
//wails:ignore
func (s *KifuService) Open(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openLocked(path)
}

func (s *KifuService) openLocked(path string) {
	if s.lib != nil {
		ctx, cancel := context.WithTimeout(context.Background(), kifuCloseTimeout)
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

// Close は棚を閉じる（アプリの終了時。quit から呼ぶ）。
//
// ⚠️ **`//wails:ignore` を外さないこと。** これは `_cmd/ikkyoku` が起動・終了で
// 呼ぶための口で、**フロントの API ではない**（外すと bindings に出てしまう）。
//
//wails:ignore
func (s *KifuService) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lib == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), kifuCloseTimeout)
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

	ctx, cancel := kifuDBContext()
	defer cancel()

	n, err := lib.Count(ctx)
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

// fetchedToDetail は kicho.Fetched を画面用に変換する（表示のための整形だけ）。
func fetchedToDetail(f kicho.Fetched) GameDetail {
	d := GameDetail{
		GameSummary: GameSummary{
			Source:    f.Source,
			SourceID:  f.SourceID,
			SourceURL: f.SourceURL,
			Event:     f.Event,
			Handicap:  f.Handicap,
			Place:     f.Place,
			Black:     f.Black,
			White:     f.White,
			EndMark:   f.EndMark,
			Finished:  f.Finished(),
			Moves:     f.Moves,
		},
		KIF:      f.KIF,
		Encoding: f.Encoding,
	}
	if !f.StartedAt.IsZero() {
		d.StartedAt = f.StartedAt.Format(time.RFC3339)
	}
	return d
}

// detailToFetched は画面の内容を保存できる形に戻す。
//
// **諸元（source_url）と手数は `Library.Save` が決め直す**ので、ここでは運ぶだけ。
func detailToFetched(d GameDetail) kicho.Fetched {
	f := kicho.Fetched{
		Source:    d.Source,
		SourceID:  d.SourceID,
		SourceURL: d.SourceURL,
		Event:     d.Event,
		Handicap:  d.Handicap,
		Place:     d.Place,
		Black:     d.Black,
		White:     d.White,
		EndMark:   d.EndMark,
		Moves:     d.Moves,
		KIF:       d.KIF,
		Encoding:  d.Encoding,
	}
	if d.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, d.StartedAt); err == nil {
			f.StartedAt = t
		}
	}
	return f
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
	// Limit は取得件数の上限（0 なら MaxSearchRows）。
	Limit int `json:"limit"`
	// Offset は読み飛ばす件数。
	Offset int `json:"offset"`
}

// SearchResult は検索結果と件数。
//
// **件数を 2 つ返すのは UI が「N 件中 M 件」を出すため。** 以前は Search と
// Count を別々に呼んでいたが、条件付きの該当件数が取れなかった。
type SearchResult struct {
	Games []GameSummary `json:"games"`
	// Matched は条件に合う件数（上限で切る前）。
	Matched int `json:"matched"`
	// Total は棚全体の件数。
	Total int `json:"total"`
	// Truncated は上限で切ったかどうか。
	Truncated bool `json:"truncated"`
	// Limit は実際に適用した上限。UI が「先頭 N 件」と出すのに使う。
	Limit int `json:"limit"`
}

// MinSearchLength は索引が効く最小文字数（UI の案内表示に使う）。
//
// ⚠️ **フロントに 3 と書かないこと**（trigram の性質は store が持っている）。
const MinSearchLength = store.MinTrigramLen

// MaxSearchRows は一覧が 1 回に返す上限（UI の案内表示に使う）。
//
// ⚠️ **フロントに数値を書かないこと**（上限は kicho が決めている）。
const MaxSearchRows = kicho.MaxSearchRows

// jst は日付入力の解釈に使うタイムゾーン。
var jst = time.FixedZone("JST", 9*60*60)

// List は保存済み棋譜の一覧を新しい順に返す（KIF 本文は含まない）。
func (s *KifuService) List() (SearchResult, error) {
	return s.Search(SearchQuery{})
}

// Search は条件に合う棋譜を新しい順に返す（KIF 本文は含まない）。
//
// **件数も一緒に返る。** 条件付きの該当件数は検索と同じ条件で数える必要があり、
// kicho 側で同じ WHERE を共有している。
func (s *KifuService) Search(q SearchQuery) (SearchResult, error) {
	lib, err := s.library()
	if err != nil {
		return SearchResult{}, err
	}

	sq := store.Query{
		Text:         q.Text,
		FinishedOnly: q.FinishedOnly,
		Limit:        q.Limit,
		Offset:       q.Offset,
	}

	// 日付は JST の 0:00 / 23:59:59 として解釈する。
	if q.From != "" {
		t, err := time.ParseInLocation("2006-01-02", q.From, jst)
		if err != nil {
			return SearchResult{}, fmt.Errorf("開始日の形式が不正です: %s", q.From)
		}
		sq.From = t
	}
	if q.To != "" {
		t, err := time.ParseInLocation("2006-01-02", q.To, jst)
		if err != nil {
			return SearchResult{}, fmt.Errorf("終了日の形式が不正です: %s", q.To)
		}
		sq.To = t.Add(24*time.Hour - time.Second)
	}
	if !sq.From.IsZero() && !sq.To.IsZero() && sq.To.Before(sq.From) {
		return SearchResult{}, fmt.Errorf("終了日が開始日より前になっています")
	}

	ctx, cancel := kifuDBContext()
	defer cancel()

	res, err := lib.Search(ctx, sq)
	if err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{
		Games:     make([]GameSummary, 0, len(res.Games)),
		Matched:   res.Matched,
		Total:     res.Total,
		Truncated: res.Truncated,
		Limit:     MaxSearchRows,
	}
	for _, r := range res.Games {
		out.Games = append(out.Games, toSummary(r))
	}
	return out, nil
}

// Count は保存件数を返す。
//
// 一覧の件数は Search が一緒に返すので、こちらは棚の状態を単独で見たいとき用。
func (s *KifuService) Count() (int, error) {
	lib, err := s.library()
	if err != nil {
		return 0, err
	}
	ctx, cancel := kifuDBContext()
	defer cancel()

	return lib.Count(ctx)
}

// Get は棋譜 1 件を KIF 本文つきで返す。
func (s *KifuService) Get(id string) (GameDetail, error) {
	lib, err := s.library()
	if err != nil {
		return GameDetail{}, err
	}
	ctx, cancel := kifuDBContext()
	defer cancel()

	r, err := lib.Get(ctx, id)
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
	ctx, cancel := kifuDBContext()
	defer cancel()

	return lib.Delete(ctx, id)
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
	return s.SendToStudyGame(d)
}

// SendToStudyGame は手元にある棋譜（棚に入れていないものも）を解析タブへ送る。
//
// **取得カードの「解析する」がここを通る。** ⚠️ **棚を経由しない** ——
// 取得しただけの棋譜も解析できる（「棚は解析の前提条件ではない」の一部）。
//
// 「再読み込みで取り直せる URL か」の判断は **kicho の `RefetchableURL`**。
// ⚠️ **ここで取得元ごとに分岐しないこと** —— 取り直せない URL を渡すと
// 解析タブに再読み込みのアイコンが出るのに押すと必ず失敗する、という
// 画面からは理由の分からない壊れ方になる。
func (s *KifuService) SendToStudyGame(d GameDetail) (KifuLoad, error) {
	if strings.TrimSpace(d.KIF) == "" {
		return KifuLoad{State: s.study.State()}, fmt.Errorf("棋譜が空です")
	}
	return s.study.loadKifuFrom(d.KIF, kicho.RefetchableURL(d.Source, d.SourceURL))
}

// Fetch はライブ中継から棋譜を取得する（**保存はしない**）。
//
// 取得元は入力から判別する（判別も取得も kicho 側）。
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
	ctx, cancel := kifuNetContext()
	defer cancel()

	f, err := lib.Fetch(ctx, input)
	if err != nil {
		return GameDetail{}, err
	}
	return fetchedToDetail(f), nil
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
	ctx, cancel := kifuNetContext()
	defer cancel()

	f, err := lib.Refresh(ctx, source, sourceID)
	if err != nil {
		return GameDetail{}, err
	}
	return fetchedToDetail(f), nil
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
	ctx, cancel := kifuDBContext()
	defer cancel()

	rec, err := lib.Save(ctx, detailToFetched(d))
	if err != nil {
		return GameSummary{}, err
	}
	return toSummary(rec), nil
}

// PreviewKIF は KIF テキストを解析して内容を返す（**保存はしない**）。
func (s *KifuService) PreviewKIF(text string) (GameDetail, error) {
	lib, err := s.library()
	if err != nil {
		return GameDetail{}, err
	}
	f, err := lib.PreviewKIF(text)
	if err != nil {
		return GameDetail{}, err
	}
	return fetchedToDetail(f), nil
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
	ctx, cancel := kifuDBContext()
	defer cancel()

	rec, err := lib.ImportKIF(ctx, text)
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
	ctx, cancel := kifuNetContext()
	defer cancel()

	f, err := lib.PreviewURL(ctx, rawURL)
	if err != nil {
		return GameDetail{}, err
	}
	return fetchedToDetail(f), nil
}

// ImportURL は URL から KIF を取得して棚に入れる。
func (s *KifuService) ImportURL(rawURL string) (GameSummary, error) {
	lib, err := s.library()
	if err != nil {
		return GameSummary{}, err
	}
	ctx, cancel := kifuNetContext()
	defer cancel()

	rec, err := lib.ImportURL(ctx, rawURL)
	if err != nil {
		return GameSummary{}, err
	}
	return toSummary(rec), nil
}
