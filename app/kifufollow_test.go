package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ShinteLab/ikkyoku/analyze"
)

// followService は URL から 2 手の棋譜を読み、自動更新を入れた StudyService を返す。
func followService(t *testing.T, body *string) *StudyService {
	t.Helper()
	srv := kifuServer(t, body)
	s := NewStudyService(NewPositionService())
	if _, err := s.LoadKifuURL(srv.URL + "/live.kif"); err != nil {
		t.Fatalf("LoadKifuURL: %v", err)
	}
	// ⚠️ **読み込んだだけでは追わないこと**（自動更新は人が入れるもの）。
	if s.State().KifuFollow {
		t.Fatal("読み込んだだけで自動更新が入っています")
	}
	st, err := s.SetKifuFollow(true)
	if err != nil {
		t.Fatalf("SetKifuFollow: %v", err)
	}
	if !st.KifuFollow {
		t.Fatal("自動更新が入っていません")
	}
	return s
}

// 本譜の先端を見ていたら、**伸びた先の先端へ付いていく**こと（2026-09-26）。
//
// ⚠️ **評価値は残ること**（`ReloadKifu` と同じ据え直しを通っている）。
func TestStudyServiceFollowKifuAdvancesAtTip(t *testing.T) {
	body := kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n"
	s := followService(t, &body)
	line := s.State().Line
	if _, err := s.GoTo(line[len(line)-1]); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	target, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	for ply := 0; ply <= 2; ply++ {
		s.recordEval(target.Epoch, ply, "e1", "エンジン", analyze.Score{CP: 10 * ply}, 12)
	}

	body = kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n   3 ２六歩(27)\n   4 ８四歩(83)\n"
	load, err := s.FollowKifu(true)
	if err != nil {
		t.Fatalf("FollowKifu: %v", err)
	}
	if load.Added != 2 || !load.Followed {
		t.Errorf("増えた手と付いていったかが返っていません: added=%d followed=%v",
			load.Added, load.Followed)
	}
	if load.State.CurrentID != load.State.MainTip || load.State.Ply != 4 {
		t.Errorf("先端へ付いていっていません: cur=%d tip=%d ply=%d",
			load.State.CurrentID, load.State.MainTip, load.State.Ply)
	}
	if !load.State.KifuFollow {
		t.Error("終局していないのに自動更新が切れました")
	}
	// **解析結果はそのまま**（今の経路に 3 点が残っていること）。
	if g := s.Evals(); len(g.Series) != 1 || len(g.Series[0].Points) != 3 {
		t.Fatalf("評価値が残っていません: %+v", g.Series)
	}

	// 手が来ていなければ何も起きない（**毎回ここを通るので黙ること**）。
	load, err = s.FollowKifu(true)
	if err != nil {
		t.Fatalf("FollowKifu: %v", err)
	}
	if load.Added != 0 || load.Followed || load.Note != "" {
		t.Errorf("手が来ていないのに何か起きています: %+v", load)
	}
}

// 先端を見ていないなら**動かさない**こと（戻って検討している最中に飛ばされない）。
func TestStudyServiceFollowKifuKeepsPlaceWhenNotAtTip(t *testing.T) {
	body := kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n"
	s := followService(t, &body)
	// 読み込んだ直後は開始局面を見ている（＝先端ではない）。
	at := s.State().CurrentID

	body = kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n   3 ２六歩(27)\n"
	load, err := s.FollowKifu(true)
	if err != nil {
		t.Fatalf("FollowKifu: %v", err)
	}
	if load.Followed || load.State.CurrentID != at {
		t.Errorf("先端を見ていないのに動きました: cur=%d (want %d)", load.State.CurrentID, at)
	}
	if len(load.State.Nodes) != 3 {
		t.Errorf("手順は伸びていること: %+v", load.State.Nodes)
	}
}

// `advance` が偽なら先端に居ても動かさないこと（**連続解析が局面を握っているとき**）。
func TestStudyServiceFollowKifuWithoutAdvance(t *testing.T) {
	body := kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n"
	s := followService(t, &body)
	line := s.State().Line
	last := line[len(line)-1]
	if _, err := s.GoTo(last); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	body = kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n   3 ２六歩(27)\n"
	load, err := s.FollowKifu(false)
	if err != nil {
		t.Fatalf("FollowKifu: %v", err)
	}
	if load.Followed || load.State.CurrentID != last {
		t.Errorf("連続解析中の取り直しで局面が動きました: cur=%d (want %d)",
			load.State.CurrentID, last)
	}
}

// 終局まで載ったら**自動更新を切る**こと（投了のあとは伸びない）。
func TestStudyServiceFollowKifuStopsAtEnd(t *testing.T) {
	body := kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n"
	s := followService(t, &body)

	body = kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n   3 投了\n"
	load, err := s.FollowKifu(true)
	if err != nil {
		t.Fatalf("FollowKifu: %v", err)
	}
	if load.State.KifuFollow {
		t.Error("終局したのに自動更新が切れていません")
	}
	if load.Note == "" {
		t.Error("自動更新を止めたことが出ていません")
	}
	// **取り直した側の終局の種別も写すこと**（2026-10-09。手順リストの「3 投了」）。
	// ⚠️ 据え直しは手しか受け取らないので、ここで写さないと行が出ない。
	ends := map[int]string{}
	for _, n := range load.State.Nodes {
		if n.End != "" {
			ends[n.Number] = n.End
		}
	}
	if len(ends) != 1 || ends[2] != "投了" {
		t.Errorf("終局の印 = %v, want 2 手目に「投了」だけ", ends)
	}
}

// URL から読んでいない局面では入れられないこと。**根を入れ替えたら切れる**こと。
func TestStudyServiceSetKifuFollow(t *testing.T) {
	s := adopted(t)
	if _, err := s.SetKifuFollow(true); err == nil {
		t.Error("取り直す先が無いのに自動更新が入りました")
	}

	body := kifuHead + "   1 ７六歩(77)\n"
	s = followService(t, &body)
	if _, err := s.NewGame("平手"); err != nil {
		t.Fatalf("NewGame: %v", err)
	}
	if s.State().KifuFollow {
		t.Error("根を入れ替えたのに自動更新が残っています（取り直す先が無い）")
	}
}

// 取りに行っているあいだに別の局面へ入れ替わったら、**取った棋譜を据えない**こと。
//
// ⚠️ **自動更新は人が見ていないところで走る**ので、ここが崩れると
// 採ったばかりの局面が中継の棋譜で黙って上書きされる（根が違うので木ごと入れ替わる）。
func TestStudyServiceFollowKifuDropsStaleFetch(t *testing.T) {
	body := kifuHead + "   1 ７六歩(77)\n"
	var s *StudyService
	swap := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if swap {
			// 取っている最中に、人が新しい対局を始めた。
			if _, err := s.NewGame("二枚落ち"); err != nil {
				t.Errorf("NewGame: %v", err)
			}
		}
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	s = NewStudyService(NewPositionService())
	if _, err := s.LoadKifuURL(srv.URL + "/live.kif"); err != nil {
		t.Fatalf("LoadKifuURL: %v", err)
	}
	if _, err := s.SetKifuFollow(true); err != nil {
		t.Fatalf("SetKifuFollow: %v", err)
	}
	swap = true
	if _, err := s.FollowKifu(true); err == nil {
		t.Error("入れ替わったのに取り直しが通りました")
	}
	st := s.State()
	if st.Handicap == "" || st.SourceURL != "" {
		t.Errorf("始めた対局が棋譜で上書きされました: handicap=%q url=%q", st.Handicap, st.SourceURL)
	}
}

// **解析タブが取り直して本譜が変わったら、入力タブのカードに知らせる**こと（2026-11-18。`kifu:reloaded`）。
// ⚠️ 歯止めは —— **手が来ていないときは知らせないこと**（自動更新は何度も回るので、毎回カードが取り直すと
// サイトを叩き続ける）・**カードの鍵（取得元と棋譜 ID）が、カードから送ったときと同じに戻ること**
// （棋譜 ID が URL でも切れない）・**取得元の鍵が無い局面では知らせないこと**。
func TestStudyServiceReloadEmitsKifuReloaded(t *testing.T) {
	body := kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n"
	srv := kifuServer(t, &body)
	url := srv.URL + "/live.kif"
	s := NewStudyService(NewPositionService())
	var got []KifuReloaded
	s.Emit = func(name string, data any) {
		if name == KifuReloadedEvent {
			got = append(got, data.(KifuReloaded))
		}
	}
	// カードの「解析する」と同じ鍵で読む（`SendToStudyGame`）。
	if _, err := s.loadKifuFrom(body, url, "", sourceKeyOf("url", url)); err != nil {
		t.Fatalf("loadKifuFrom: %v", err)
	}

	if _, err := s.ReloadKifu(); err != nil {
		t.Fatalf("ReloadKifu: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("手が来ていないのに知らせました: %+v", got)
	}

	body = kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n   3 ２六歩(27)\n"
	if _, err := s.ReloadKifu(); err != nil {
		t.Fatalf("ReloadKifu: %v", err)
	}
	if len(got) != 1 || got[0].Source != "url" || got[0].SourceID != url || got[0].Moves != 3 {
		t.Fatalf("kifu:reloaded = %+v, want 1 件（url / %s / 3 手）", got, url)
	}

	// 取得元の鍵が無い（URL から読んだがカードではない）局面では知らせない。
	got = nil
	body = kifuHead + "   1 ７六歩(77)\n"
	if _, err := s.LoadKifuURL(url); err != nil {
		t.Fatalf("LoadKifuURL: %v", err)
	}
	body = kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n"
	if _, err := s.ReloadKifu(); err != nil {
		t.Fatalf("ReloadKifu: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("取得元の鍵が無いのに知らせました: %+v", got)
	}
}

func TestSplitSourceKey(t *testing.T) {
	src, id, ok := splitSourceKey(sourceKeyOf("url", "https://example.com/a:b.kif"))
	if !ok || src != "url" || id != "https://example.com/a:b.kif" {
		t.Errorf("splitSourceKey = %q %q %v", src, id, ok)
	}
	for _, k := range []string{"", "kif:abcd", "src:", "src:url", "src::x", "src:url:"} {
		if _, _, ok := splitSourceKey(k); ok {
			t.Errorf("splitSourceKey(%q) が ok を返しました", k)
		}
	}
}
