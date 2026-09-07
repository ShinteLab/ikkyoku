package app

import (
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
