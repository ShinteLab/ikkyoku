package app

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShinteLab/kicho"
	"github.com/ShinteLab/kicho/store"
)

// newTestKifuService は一時ディレクトリに棚を作る。
//
// ⚠️ **本物の DB を触らないこと。** `Config.KifuDB()` は
// `os.UserConfigDir()/ikkyoku/kicho.db` を返すので、そちらを開くと
// **テストが手元の棚を書き換える**（settings_test.go / fontservice_test.go と同じ話）。
func newTestKifuService(t *testing.T) (*KifuService, *StudyService) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	study := NewStudyService(logger, NewPositionService(logger))
	svc := NewKifuService(logger, study)
	svc.Open(filepath.Join(t.TempDir(), "kicho.db"))
	t.Cleanup(svc.Close)
	if st := svc.Status(); !st.Ready {
		t.Fatalf("棚を開けませんでした: %+v", st)
	}
	return svc, study
}

const testKIF = "棋戦：テスト棋戦\n先手：先手太郎\n後手：後手花子\n" +
	"手数----指手---------消費時間--\n   1 ７六歩(77)\n   2 ３四歩(33)\n"

// 貼り付け登録 → 一覧に出る → 解析タブへ送れる、の一巡。
func TestKifuServiceImportListSendToStudy(t *testing.T) {
	svc, study := newTestKifuService(t)

	rec, err := svc.ImportKIF(testKIF)
	if err != nil {
		t.Fatalf("ImportKIF: %v", err)
	}
	if rec.Source != store.SourcePaste || rec.Event != "テスト棋戦" || rec.Moves != 2 {
		t.Fatalf("登録の内容が違います: %+v", rec)
	}

	list, err := svc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Games) != 1 || list.Games[0].ID != rec.ID {
		t.Fatalf("一覧に出ていません: %+v", list)
	}
	// 件数は一覧と一緒に返る（UI の「N 件中 M 件」に使う）。
	if list.Total != 1 || list.Matched != 1 || list.Truncated {
		t.Errorf("件数が合いません: %+v", list)
	}

	load, err := svc.SendToStudy(rec.ID)
	if err != nil {
		t.Fatalf("SendToStudy: %v", err)
	}
	// **指し手は全て載り、見ているのは開始局面**（貼り付け・URL と同じ約束）。
	if len(load.State.Nodes) != 2 || load.State.Ply != 0 {
		t.Fatalf("指し手が全て載っていないか、開始局面を見ていません: %+v", load.State)
	}
	// ⚠️ **貼り付け登録には取り直す先が無い**ので、再読み込みのアイコンは出さない。
	if study.State().SourceURL != "" {
		t.Errorf("取り直せない棋譜に取得元が付いています: %q", study.State().SourceURL)
	}
}

// ⚠️ **同じ棋譜を保存し直しても ID が変わらないこと**（(source, source_id) の upsert）。
//
// **`/kifu/{id}` の URL が変わらないことが重要**なので、対局中に保存 →
// 終局後に取り直して保存、で同じ ID のまま最新になる。
func TestKifuServiceSaveKeepsIDOnResave(t *testing.T) {
	svc, _ := newTestKifuService(t)

	d := GameDetail{
		GameSummary: GameSummary{
			Source: store.SourceShogiLive, SourceID: "oui/kifu/67/oui202607290101",
			Event: "テスト棋戦", Black: "先手太郎", White: "後手花子",
		},
		KIF: testKIF,
	}
	first, err := svc.Save(d)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 対局が進んだので取り直して保存し直す。
	d.KIF = testKIF + "   3 ２六歩(27)\n"
	d.EndMark = "投了"
	second, err := svc.Save(d)
	if err != nil {
		t.Fatalf("Save（2 回目）: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("保存し直したら ID が変わりました: %q → %q", first.ID, second.ID)
	}
	if second.Moves != 3 || !second.Finished {
		t.Errorf("最新化されていません: %+v", second)
	}
	if n, _ := svc.Count(); n != 1 {
		t.Errorf("棋譜が増えています: %d 件", n)
	}
}

// ⚠️ **読売の取得元 URL は解析タブへ渡さないこと**（`kicho.RefetchableURL`）。
//
// あちらは Nuxt のページで .kif を置いておらず、KIF は構造化データから
// 組み立てている。渡すと**再読み込みのアイコンが出るのに押すと必ず失敗する**
// という、画面からは理由の分からない壊れ方になる（取り直す口は取得カードの「更新」）。
//
// **判断は kicho が持つ**（取得元の性質を知っているのはあちら）。ここでは
// SendToStudyGame がその判断を通していることだけを見る。
func TestSendToStudyGameDropsUnrefetchableURL(t *testing.T) {
	page := "http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html"
	if got := kicho.RefetchableURL(store.SourceShogiLive, page); got != page {
		t.Errorf("連盟の中継ページを落としています: %q", got)
	}
	if got := kicho.RefetchableURL(store.SourceURL, "https://example.com/a.kif"); got == "" {
		t.Errorf("URL 取り込みの取得元を落としています")
	}

	// 読売の棋譜を解析タブへ送っても、取得元 URL は付かない。
	svc, study := newTestKifuService(t)
	viewer := "https://www.yomiuri.co.jp/kifu/s/66f2539c848c20bac7cb8002/"
	if _, err := svc.SendToStudyGame(GameDetail{
		GameSummary: GameSummary{Source: store.SourceYomiuri, SourceURL: viewer},
		KIF:         testKIF,
	}); err != nil {
		t.Fatalf("SendToStudyGame: %v", err)
	}
	if got := study.State().SourceURL; got != "" {
		t.Errorf("読売の URL を渡してしまっています: %q", got)
	}
}

// 索引が効く最小文字数は store が持っている（フロントの案内はこれを写す）。
func TestMinSearchLengthComesFromStore(t *testing.T) {
	if MinSearchLength != store.MinTrigramLen {
		t.Fatalf("MinSearchLength が store とずれています: %d / %d",
			MinSearchLength, store.MinTrigramLen)
	}
}

// 日付の形が違うときは、走らせる前に理由を返すこと。
func TestKifuServiceSearchRejectsBadDates(t *testing.T) {
	svc, _ := newTestKifuService(t)

	if _, err := svc.Search(SearchQuery{From: "2026/08/01"}); err == nil {
		t.Error("開始日の形式を見ていません")
	}
	if _, err := svc.Search(SearchQuery{From: "2026-08-10", To: "2026-08-01"}); err == nil {
		t.Error("終了日が開始日より前なのに通しています")
	}
}

// ⚠️ **棚が開けていなくても panic せず、理由を返すこと**（設計原則3）。
//
// **棚は解析の前提条件ではない。** 撮った 1 局面と貼った棋譜の解析は今までどおり
// 動き、棋譜タブだけが使えない。理由が読めないと設定タブへ辿り着けないので、
// **どのメソッドも同じ文言を返す**（`library()` の 1 か所）。
func TestKifuServiceWithoutDatabase(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	study := NewStudyService(logger, NewPositionService(logger))
	svc := NewKifuService(logger, study)
	// **開いていない**（main が open を呼べなかったときと同じ状態）。

	st := svc.Status()
	if st.Ready || st.Count != 0 {
		t.Fatalf("開けていないのに使える扱いです: %+v", st)
	}

	if _, err := svc.List(); err == nil {
		t.Error("List がエラーを返しません")
	}
	if _, err := svc.Count(); err == nil {
		t.Error("Count がエラーを返しません")
	}
	if _, err := svc.Get("x"); err == nil {
		t.Error("Get がエラーを返しません")
	}
	if err := svc.Delete("x"); err == nil {
		t.Error("Delete がエラーを返しません")
	}
	if _, err := svc.ImportKIF(testKIF); err == nil {
		t.Error("ImportKIF がエラーを返しません")
	}
	if _, err := svc.Fetch("abc"); err == nil {
		t.Error("Fetch がエラーを返しません")
	}
	load, err := svc.SendToStudy("x")
	if err == nil {
		t.Error("SendToStudy がエラーを返しません")
	}
	// ⚠️ **エラーでも今の解析タブの状態を返すこと**（画面を空にしない）。
	if load.State.RootSFEN != study.State().RootSFEN {
		t.Error("失敗したのに解析タブの状態が変わっています")
	}
	if !strings.Contains(err.Error(), "設定タブ") {
		t.Errorf("直す先が分かる文言になっていません: %v", err)
	}

	// ⚠️ **棚を通さない経路は棚が無くても通ること**（取得カードの「解析する」）。
	if _, err := svc.SendToStudyGame(GameDetail{KIF: testKIF}); err != nil {
		t.Errorf("棚が無いと解析タブへ送れません: %v", err)
	}
}

// 開き直すと、前に開いていた棚の中身は見えなくなること（設定でパスを変えたとき）。
func TestKifuServiceReopen(t *testing.T) {
	svc, _ := newTestKifuService(t)
	if _, err := svc.ImportKIF(testKIF); err != nil {
		t.Fatalf("ImportKIF: %v", err)
	}

	svc.Open(filepath.Join(t.TempDir(), "another.db"))
	n, err := svc.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 0 {
		t.Fatalf("前の棚の中身が見えています: %d 件", n)
	}
}

// スキーマ版が新しすぎるときは、**直し方が分かる文言**にすること。
//
// kicho と ikkyoku は別バイナリなので、同じ DB を共用していて片方だけ更新すると
// 起きる。「DB が壊れている」ではないので、そう読めては困る。
//
// ⚠️ **判定は文言比較ではなく sentinel**（`store.ErrSchemaTooNew`）。
func TestDescribeOpenErrorExplainsSchemaSkew(t *testing.T) {
	err := describeOpenError(fmt.Errorf("%w: v9 で作られています", store.ErrSchemaTooNew))
	if !errors.Is(err, store.ErrSchemaTooNew) {
		t.Fatalf("sentinel が落ちています: %v", err)
	}
	if !strings.Contains(err.Error(), "kicho アプリ") {
		t.Errorf("共用しているときの直し方が出ていません: %v", err)
	}

	// 関係ないエラーはそのまま返す（余計な案内を足さない）。
	other := errors.New("permission denied")
	if got := describeOpenError(other); got != other {
		t.Errorf("無関係なエラーを包んでいます: %v", got)
	}
}

// 一覧の上限は kicho が決めている（フロントに数値を書かないための歯止め）。
func TestMaxSearchRowsComesFromKicho(t *testing.T) {
	if MaxSearchRows != kicho.MaxSearchRows {
		t.Fatalf("MaxSearchRows が kicho とずれています: %d / %d",
			MaxSearchRows, kicho.MaxSearchRows)
	}
}

// ⚠️ **`Shown` は「実際に並んだ件数」。上限の定数を決め打ちで返さないこと。**
//
// kicho が丸めるのは「Limit が 0 か上限超え」のときだけで、**指定した Limit は
// そのまま効く**。決め打ちだと 1 件しか並べていないのに「500 件だけを表示して
// います」と出る、という**画面が嘘をつく**状態になる。
func TestKifuServiceSearchReportsShownRows(t *testing.T) {
	svc, _ := newTestKifuService(t)
	for i := 0; i < 3; i++ {
		if _, err := svc.ImportKIF(testKIF); err != nil {
			t.Fatalf("ImportKIF: %v", err)
		}
	}

	res, err := svc.Search(SearchQuery{Limit: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Games) != 1 {
		t.Fatalf("指定した Limit が効いていません: %d 件", len(res.Games))
	}
	if !res.Truncated {
		t.Fatalf("切ったことを返していません: %+v", res)
	}
	// ⚠️ **ここが本題** —— 上限の定数（500）ではなく、指定した Limit で
	// 実際に並んだ 1 件が返ること。
	if res.Shown == MaxSearchRows {
		t.Errorf("上限の定数を決め打ちで返しています: shown=%d", res.Shown)
	}
	if res.Shown != len(res.Games) {
		t.Errorf("並べた件数と食い違っています: shown=%d 行=%d", res.Shown, len(res.Games))
	}
	if res.Matched != 3 || res.Total != 3 {
		t.Errorf("件数が合いません: %+v", res)
	}
}

// 取り込みの失敗は、**kicho の sentinel で見分けて**ikkyoku 側の直し方を足すこと。
//
// ⚠️ **文言比較で分岐しない**（`describeOpenError` と同じ）。kicho は
// 「画面と文言は使う側にある」という前提で sentinel を公開している。
func TestDescribeKifuErrorAddsGuidance(t *testing.T) {
	notKifu := describeKifuError(fmt.Errorf("%w", kicho.ErrNotKifu))
	if !errors.Is(notKifu, kicho.ErrNotKifu) {
		t.Fatalf("sentinel が落ちています: %v", notKifu)
	}
	if !strings.Contains(notKifu.Error(), ".kif") {
		t.Errorf("何を指せばよいかが出ていません: %v", notKifu)
	}

	empty := describeKifuError(kicho.ErrEmptyKifu)
	if !errors.Is(empty, kicho.ErrEmptyKifu) {
		t.Fatalf("sentinel が落ちています: %v", empty)
	}

	// 関係ないエラーはそのまま返す（余計な案内を足さない）。
	other := errors.New("connection refused")
	if got := describeKifuError(other); got != other {
		t.Errorf("無関係なエラーを包んでいます: %v", got)
	}
}

// ⚠️ **kicho の文言を書き写さないこと。** 空の棋譜を断るのは kicho も同じなので、
// 手書きの文字列ではなく sentinel を返す（写すと向こうを直しても古いまま残る）。
func TestSendToStudyGameUsesKichoSentinel(t *testing.T) {
	svc, _ := newTestKifuService(t)
	_, err := svc.SendToStudyGame(GameDetail{KIF: "   \n"})
	if !errors.Is(err, kicho.ErrEmptyKifu) {
		t.Fatalf("sentinel を返していません: %v", err)
	}
}

// liveCard は仮の一覧に載せられる取得カード（連盟の中継）。
func liveCard() GameDetail {
	return GameDetail{
		GameSummary: GameSummary{
			Source: store.SourceShogiLive, SourceID: "oui/kifu/67/oui202607290101",
			Event: "テスト棋戦", Black: "先手太郎", White: "後手花子", Moves: 2,
		},
		KIF: testKIF,
	}
}

// 仮の一覧の一巡（載せる → 復元 → 外す）。
//
// ⚠️ **一番の要点は「棋譜本文を持たないこと」。** ここが KIF まで返すように
// なると、復元しただけのカードが「取れている」ように見えて、そのまま棚へ
// 保存できてしまう（中身の無い棋譜が棚に入る）。
func TestKifuServiceWatchRoundTrip(t *testing.T) {
	svc, _ := newTestKifuService(t)

	d := liveCard()
	w, err := svc.Watch(d)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	// 諸元は kicho が取得元から決め直す（画面の値を運ぶだけ）。
	if w.SourceURL == "" {
		t.Errorf("取得元の URL が決まっていません: %+v", w)
	}

	list, err := svc.Watches()
	if err != nil {
		t.Fatalf("Watches: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("仮の一覧に出ていません: %+v", list)
	}
	got := list[0]
	if got.Source != d.Source || got.SourceID != d.SourceID {
		t.Fatalf("別の棋譜が入っています: %+v", got)
	}
	// 一覧で見分けるためのメタは持つ。
	if got.Event != "テスト棋戦" || got.Black != "先手太郎" || got.Moves != 2 {
		t.Errorf("一覧で見分けるためのメタが落ちています: %+v", got)
	}

	// ⚠️ **同じ中継を取り直しても増えない**（主キーは (source, source_id)）。
	d.Moves = 3
	if _, err := svc.Watch(d); err != nil {
		t.Fatalf("Watch（2 回目）: %v", err)
	}
	list, _ = svc.Watches()
	if len(list) != 1 || list[0].Moves != 3 {
		t.Fatalf("取り直しで増えたか最新化されていません: %+v", list)
	}

	// ⚠️ **外しても棚の棋譜は消えない**（そもそも棚には入っていない）。
	if err := svc.Unwatch(d.Source, d.SourceID); err != nil {
		t.Fatalf("Unwatch: %v", err)
	}
	if list, _ = svc.Watches(); len(list) != 0 {
		t.Fatalf("外れていません: %+v", list)
	}
}

// ⚠️ **貼り付けた棋譜は仮の一覧に載せない。**
//
// 取りに行く先が無いので、復元しても「更新」が必ず失敗するカードになる。
// **判断は kicho（`ErrUnsupportedSource`）** で、ここではその種類が落ちずに、
// ikkyoku 側の直し方が付いていることだけを見る。
//
// ⚠️ **URL 取り込みを一緒に弾かないこと**（2026-09-12）—— あちらの `source_id` は
// URL そのものになったので取り直せる。「取り込み系はまとめて弾く」に戻すと、
// .kif の URL のカードが復元できなくなる。
func TestKifuServiceWatchRejectsUnrefetchable(t *testing.T) {
	svc, _ := newTestKifuService(t)

	_, err := svc.Watch(GameDetail{
		GameSummary: GameSummary{Source: store.SourcePaste, SourceID: "any"},
		KIF:         testKIF,
	})
	if !errors.Is(err, kicho.ErrUnsupportedSource) {
		t.Fatalf("sentinel が落ちています: %v", err)
	}
	if !strings.Contains(err.Error(), "貼り付けた棋譜") {
		t.Errorf("何が載せられないのかが分かる文言になっていません: %v", err)
	}

	// URL 由来は載る（取りに行く先がある）。
	w, err := svc.Watch(GameDetail{
		GameSummary: GameSummary{
			Source:   store.SourceURL,
			SourceID: "https://example.test/kifu/x.kif",
		},
		KIF: testKIF,
	})
	if err != nil {
		t.Fatalf("URL 由来を追跡できません: %v", err)
	}
	if w.SourceURL != "https://example.test/kifu/x.kif" {
		t.Errorf("SourceURL = %q", w.SourceURL)
	}
}

// ⚠️ **終局済みを保存したら仮の一覧から外れ、対局中は外れないこと。**
//
// 2 日制なら 1 日目の封じ手時点で保存しても翌日また同じカードで追うので、
// そこで一覧から消えては困る。**分岐を持っているのは kicho の `Save`** で、
// ここではそれが ikkyoku の経路でも効いていることを見る。
func TestKifuServiceSaveUnwatchesOnlyWhenFinished(t *testing.T) {
	svc, _ := newTestKifuService(t)

	d := liveCard()
	if _, err := svc.Watch(d); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// 対局中の保存では外さない。
	if _, err := svc.Save(d); err != nil {
		t.Fatalf("Save（対局中）: %v", err)
	}
	if list, _ := svc.Watches(); len(list) != 1 {
		t.Fatalf("対局中の保存で仮の一覧から外れています: %+v", list)
	}

	// 終局していれば外す（もう取り直す必要が無い）。
	d.EndMark = "投了"
	rec, err := svc.Save(d)
	if err != nil {
		t.Fatalf("Save（終局）: %v", err)
	}
	if !rec.Finished {
		t.Fatalf("終局として保存できていません: %+v", rec)
	}
	if list, _ := svc.Watches(); len(list) != 0 {
		t.Fatalf("終局しても仮の一覧に残っています: %+v", list)
	}
}

// ⚠️ **既に無いものを外すのは失敗にしないこと。**
//
// 目的は画面からカードを消すことで、終局した棋譜を保存すると kicho 側が
// 先に外している。ここでエラーにすると**カードが閉じられなくなる。**
func TestKifuServiceUnwatchMissingIsNotError(t *testing.T) {
	svc, _ := newTestKifuService(t)

	if err := svc.Unwatch(store.SourceShogiLive, "no/such/kifu"); err != nil {
		t.Fatalf("既に無い追跡を外せません: %v", err)
	}
}

// 「クリア」は仮の一覧ごと捨てる（画面から消しただけでは再起動で戻ってくる）。
func TestKifuServiceUnwatchAll(t *testing.T) {
	svc, _ := newTestKifuService(t)

	first := liveCard()
	second := liveCard()
	second.SourceID = "oui/kifu/67/oui202607290102"
	for _, d := range []GameDetail{first, second} {
		if _, err := svc.Watch(d); err != nil {
			t.Fatalf("Watch: %v", err)
		}
	}

	n, err := svc.UnwatchAll()
	if err != nil {
		t.Fatalf("UnwatchAll: %v", err)
	}
	if n != 2 {
		t.Errorf("消した件数が合いません: %d", n)
	}
	if list, _ := svc.Watches(); len(list) != 0 {
		t.Fatalf("空になっていません: %+v", list)
	}
}

// ⚠️ **棚が開けていなくても panic せず、理由を返すこと**（設計原則3）。
//
// 仮の一覧は棚（SQLite）の上にあるので、開けていなければ使えない。
// **それでも取得そのものは動く**（カードは今日のあいだ画面に残る）。
func TestKifuServiceWatchWithoutDatabase(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewKifuService(logger, NewStudyService(logger, NewPositionService(logger)))
	// **開いていない**（main が open を呼べなかったときと同じ状態）。

	if _, err := svc.Watches(); err == nil {
		t.Error("Watches がエラーを返しません")
	}
	if _, err := svc.Watch(liveCard()); err == nil {
		t.Error("Watch がエラーを返しません")
	}
	if err := svc.Unwatch(store.SourceShogiLive, "x"); err == nil {
		t.Error("Unwatch がエラーを返しません")
	}
	if _, err := svc.UnwatchAll(); err == nil {
		t.Error("UnwatchAll がエラーを返しません")
	}
}
