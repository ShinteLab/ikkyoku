package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku/analyze"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

const hirateBoard = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"

// adopted は初期局面を採った StudyService を返す。
func adopted(t *testing.T) *StudyService {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pos := NewPositionService(logger)
	if _, err := pos.Load(hirateBoard); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := pos.SetTurn(1); err != nil { // 先手番
		t.Fatalf("SetTurn: %v", err)
	}
	study := NewStudyService(logger, pos)
	if _, err := study.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	return study
}

// 採った直後は根に居て、合法手が出ていること。
// **合法手が出ないと、盤を押しても何も光らない**（画面が死んで見える）。
func TestStudyServiceAdoptHasLegalMoves(t *testing.T) {
	st := adopted(t).State()
	if !st.Loaded {
		t.Fatal("採ったのに Loaded が false です")
	}
	if st.Ply != 0 || len(st.Moves) != 0 {
		t.Errorf("採った直後に手順があります: ply=%d moves=%+v", st.Ply, st.Moves)
	}
	if len(st.Legal) != 30 {
		t.Errorf("合法手 = %d, want 30", len(st.Legal))
	}
	if st.LegalError != "" {
		t.Errorf("LegalError = %q", st.LegalError)
	}
	if st.RootSFEN != st.SFEN {
		t.Errorf("根に居るのに RootSFEN(%q) と SFEN(%q) が違います", st.RootSFEN, st.SFEN)
	}
}

// 指すと盤・手番・手順・合法手がまとめて追随すること。
func TestStudyServicePlay(t *testing.T) {
	s := adopted(t)
	st, err := s.Play("7g7f")
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if st.Ply != 1 || len(st.Moves) != 1 {
		t.Fatalf("手順が記録されていません: ply=%d moves=%+v", st.Ply, st.Moves)
	}
	if st.Moves[0].Text != "▲７六歩" {
		t.Errorf("手順の表記 = %q, want %q", st.Moves[0].Text, "▲７六歩")
	}
	if st.Turn != 2 {
		t.Errorf("手番 = %d, want 2（後手番）", st.Turn)
	}
	// **合法手は進めたあとの局面のもの。** 根のままだと嘘の移動先が光る。
	for _, m := range st.Legal {
		if m.USI == "7g7f" {
			t.Fatal("動かしたはずの手がまだ合法手に入っています")
		}
	}
	// ⚠️ **盤の SFEN も追随すること**（根を描き続けると、指したのに動かないように見える）。
	if !strings.HasPrefix(st.BoardSFEN, "lnsgkgsnl/1r5b1/ppppppppp/9/9/2P6/") {
		t.Errorf("盤が進んでいません: %q", st.BoardSFEN)
	}

	if _, err := s.Play("7f7e"); err == nil {
		t.Error("後手番なのに先手の手が指せました")
	}
}

// ⚠️ **解析に渡すのは「根 + そこまでの手順」。** 組み立て直した 1 つの SFEN を
// 渡すと、千日手と連続王手をエンジンが判定できない。
//
// **Current は別に返すこと** —— 手を進めても根は変わらないので、根で
// 「局面が変わった」を判定すると**前の手の評価値が今の盤の上に残る。**
func TestStudyServiceAnalyzeTarget(t *testing.T) {
	s := adopted(t)
	root, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	if len(root.Moves) != 0 {
		t.Errorf("根なのに手順があります: %v", root.Moves)
	}
	if root.Current != root.Root {
		t.Errorf("根なのに Current(%q) と Root(%q) が違います", root.Current, root.Root)
	}

	for _, m := range []string{"7g7f", "3c3d"} {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%s): %v", m, err)
		}
	}
	got, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	if got.Root != root.Root {
		t.Errorf("根が動いています: %q -> %q", root.Root, got.Root)
	}
	if strings.Join(got.Moves, " ") != "7g7f 3c3d" {
		t.Errorf("Moves = %v", got.Moves)
	}
	if got.Current == got.Root {
		t.Error("手を進めたのに Current が根のままです（結果が消えなくなる）")
	}
}

// 戻る操作の使い分け。**GoTo は手順を消さない / Undo は消す。**
func TestStudyServiceGoToAndUndo(t *testing.T) {
	s := adopted(t)
	for _, m := range []string{"7g7f", "3c3d"} {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%s): %v", m, err)
		}
	}
	st, err := s.GoTo(1)
	if err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if st.Ply != 1 || len(st.Moves) != 2 {
		t.Errorf("GoTo で手順が消えました: ply=%d moves=%d", st.Ply, len(st.Moves))
	}
	if strings.Join(st.Played, " ") != "7g7f" {
		t.Errorf("Played = %v（先の手を渡さないこと）", st.Played)
	}

	st, err = s.Undo()
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if st.Ply != 0 || len(st.Moves) != 0 {
		t.Errorf("Undo で消えていません: ply=%d moves=%d", st.Ply, len(st.Moves))
	}
}

// 採り直したら手順ごと入れ替わること。**前の局面の手順を引き継がない。**
func TestStudyServiceAdoptResetsMoves(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pos := NewPositionService(logger)
	if _, err := pos.Load(hirateBoard); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := pos.SetTurn(1); err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	s := NewStudyService(logger, pos)
	if _, err := s.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	st, err := s.Adopt()
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if st.Ply != 0 || len(st.Moves) != 0 {
		t.Errorf("採り直したのに手順が残っています: ply=%d moves=%+v", st.Ply, st.Moves)
	}
}

// 何も採っていなければ手は指せない（**エラーで、落ちないこと**）。
func TestStudyServicePlayWithoutPosition(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewStudyService(logger, NewPositionService(logger))
	if _, err := s.Play("7g7f"); err == nil {
		t.Error("局面が無いのに指せました")
	}
	st := s.State()
	if st.Loaded {
		t.Error("局面が無いのに Loaded です")
	}
	if st.Legal == nil || st.Moves == nil || st.Played == nil {
		t.Error("空でも配列を返すこと（フロントが null を踏む）")
	}
}

// 棋譜を貼り付けると、指し手が全て反映された状態になること。
func TestStudyServiceLoadKifu(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewStudyService(logger, NewPositionService(logger))

	load, err := s.LoadKifu("棋戦：テスト戦\n先手：先手太郎\n後手：後手花子\n" +
		"手数----指手---------消費時間--\n" +
		"   1 ７六歩(77)\n   2 ３四歩(33)\n   3 ２二角成(88)\n   4 同　銀(31)\n   5 投了\n")
	if err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	if load.Note != "" {
		t.Errorf("止まった理由が出ている: %s", load.Note)
	}
	st := load.State
	if !st.Loaded || st.Ply != 4 || len(st.Moves) != 4 {
		t.Fatalf("最終手まで反映されていません: %+v", st)
	}
	// **解析に渡せる形になっていること**（根 + 手順）。
	target, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	if len(target.Moves) != 4 || target.Root == target.Current {
		t.Errorf("解析対象が手順を持っていません: %+v", target)
	}
	// **採り直しと同じで、根ごと入れ替わること。**
	if !strings.Contains(load.Summary, "4手") {
		t.Errorf("読み込んだ手数が出ない: %q", load.Summary)
	}
	if !strings.Contains(load.Summary, "先手太郎") {
		t.Errorf("対局者が出ない: %q", load.Summary)
	}
}

// 貼り間違い（棋譜でないテキスト）は、それまでの局面を壊さずにエラーを返すこと。
func TestStudyServiceLoadKifuKeepsPositionOnError(t *testing.T) {
	s := adopted(t)
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	load, err := s.LoadKifu("これは棋譜ではありません")
	if err == nil {
		t.Fatal("棋譜でないテキストでエラーにならなかった")
	}
	if !load.State.Loaded || load.State.Ply != 1 {
		t.Errorf("失敗したのに前の局面が壊れています: %+v", load.State)
	}
}

// URL から棋譜を取って読み込めること（文字コードは Shift_JIS が多い）。
func TestStudyServiceLoadKifuURL(t *testing.T) {
	body, _, err := transform.Bytes(japanese.ShiftJIS.NewEncoder(), []byte(
		"先手：先手太郎\n後手：後手花子\n手数----指手---------消費時間--\n"+
			"   1 ７六歩(77)\n   2 ３四歩(33)\n"))
	if err != nil {
		t.Fatalf("Shift_JIS へ変換できません: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewStudyService(logger, NewPositionService(logger))
	load, err := s.LoadKifuURL(srv.URL + "/sample.kif")
	if err != nil {
		t.Fatalf("LoadKifuURL: %v", err)
	}
	if load.State.Ply != 2 {
		t.Fatalf("最終手まで反映されていません: %+v", load.State)
	}
	// 対局者が化けていないこと（＝文字コードを取り違えていないこと）。
	if !strings.Contains(load.Summary, "先手太郎") {
		t.Errorf("文字が化けています: %q", load.Summary)
	}
	// **何として読んだか**を出す（打ち間違いの切り分けに要る）。
	if !strings.Contains(load.Summary, "shift_jis") {
		t.Errorf("文字コードが出ていません: %q", load.Summary)
	}
}

// 取れなかったときは、それまでの局面を壊さずに理由を返すこと。
func TestStudyServiceLoadKifuURLKeepsPositionOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s := adopted(t)
	load, err := s.LoadKifuURL(srv.URL + "/none.kif")
	if err == nil {
		t.Fatal("404 なのにエラーになりませんでした")
	}
	if !load.State.Loaded {
		t.Errorf("失敗したのに前の局面が消えています: %+v", load.State)
	}
}

// 何もないところから対局を始められること（入力タブの「新しく対局を始める」）。
//
// ⚠️ **採ったときと同じ形（確定した局面 + 空の手順 + 合法手）**で返ること。
// ここが揃っていないと、始めた直後に駒を押しても何も光らない。
func TestStudyServiceNewGame(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewStudyService(logger, NewPositionService(logger))

	got, err := s.NewGame("平手")
	if err != nil {
		t.Fatalf("NewGame: %v", err)
	}
	if got.Note != "" {
		t.Errorf("Note = %q（新規作成で読み落とすものは無いはず）", got.Note)
	}
	if !strings.Contains(got.Summary, "平手") {
		t.Errorf("Summary = %q（手合割が出ていない）", got.Summary)
	}
	st := got.State
	if !st.Loaded || st.Ply != 0 || len(st.Moves) != 0 {
		t.Fatalf("始めた直後の状態が変です: %+v", st)
	}
	if want := hirateBoard; st.BoardSFEN != want {
		t.Errorf("BoardSFEN = %q, want %q", st.BoardSFEN, want)
	}
	if st.Turn != 1 {
		t.Errorf("Turn = %d, want 1(先手番)", st.Turn)
	}
	if len(st.Legal) != 30 {
		t.Errorf("合法手 = %d, want 30", len(st.Legal))
	}
	// **解析にそのまま渡せる形**（根 + 空の手順）であること。
	tg, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	if tg.Root != st.SFEN || len(tg.Moves) != 0 {
		t.Errorf("解析対象が根になっていません: %+v", tg)
	}
}

// ⚠️ **始め損ねても、それまでの局面を壊さないこと**（知らない手合割を打ったとき）。
// 壊すと、押し間違えただけで検討が消える（棋譜の貼り間違いと同じ話）。
func TestStudyServiceNewGameKeepsPositionOnError(t *testing.T) {
	s := adopted(t)
	before := s.State()

	if _, err := s.NewGame("そんな手合割は無い"); err == nil {
		t.Fatal("知らない手合割がエラーになりません")
	}
	if after := s.State(); after.SFEN != before.SFEN {
		t.Errorf("失敗したのに局面が変わりました: %q -> %q", before.SFEN, after.SFEN)
	}
}

// kifuServer は中身を差し替えられる .kif の配信元（再読み込みのテスト用）。
//
// **同じ URL の中身が変わる**のが中継の .kif そのものなので、そこを再現する。
func kifuServer(t *testing.T, body *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, *body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

const kifuHead = "先手：先手太郎\n後手：後手花子\n手数----指手---------消費時間--\n"

// 再読み込みで手順が伸び、**それまでの解析結果（評価値）が残る**こと。
//
// ⚠️ **ここが崩れると、1 手進むたびに折れ線が消える**（中継を追う使い方が壊れる）。
func TestStudyServiceReloadKifuKeepsEvals(t *testing.T) {
	body := kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n"
	srv := kifuServer(t, &body)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewStudyService(logger, NewPositionService(logger))
	if _, err := s.LoadKifuURL(srv.URL + "/live.kif"); err != nil {
		t.Fatalf("LoadKifuURL: %v", err)
	}
	if s.State().SourceURL == "" {
		t.Fatal("取得元が記録されていません（再読み込みのボタンが出ない）")
	}
	// 2 手目まで解析した、という状態を作る。
	target, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	for ply := 0; ply <= 2; ply++ {
		s.recordEval(target.Epoch, ply, "e1", "エンジン", analyze.Score{CP: 10 * ply}, 12)
	}

	// 中継が進んだ（頭は同じで、後ろに 2 手足された）。
	body = kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n   3 ２六歩(27)\n   4 ８四歩(83)\n"
	load, err := s.ReloadKifu()
	if err != nil {
		t.Fatalf("ReloadKifu: %v", err)
	}
	if load.Note != "" {
		t.Errorf("食い違っていないのに差し替えの断りが出ています: %s", load.Note)
	}
	if load.State.Ply != 4 || len(load.State.Moves) != 4 {
		t.Fatalf("最新の手順まで進んでいません: %+v", load.State)
	}
	// **解析結果はそのまま**（3 点とも残っていること）。
	g := s.Evals()
	if len(g.Series) != 1 || len(g.Series[0].Points) != 3 {
		t.Fatalf("評価値が残っていません: %+v", g.Series)
	}
	if s.State().SourceURL == "" {
		t.Error("取り直したあとに取得元が消えています")
	}
}

// 食い違ったら**その先だけ**捨てて URL の手順を正にすること。
//
// 一致している範囲の評価値は残す（**捨てるのは別の手順に付いた値だけ**）。
func TestStudyServiceReloadKifuReplacesDivergedMoves(t *testing.T) {
	body := kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n"
	srv := kifuServer(t, &body)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewStudyService(logger, NewPositionService(logger))
	if _, err := s.LoadKifuURL(srv.URL + "/live.kif"); err != nil {
		t.Fatalf("LoadKifuURL: %v", err)
	}
	target, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	for ply := 0; ply <= 2; ply++ {
		s.recordEval(target.Epoch, ply, "e1", "エンジン", analyze.Score{CP: 10 * ply}, 12)
	}

	// 2 手目が別の手だった（＝こちらが持っていた手順が誤り）。
	body = kifuHead + "   1 ７六歩(77)\n   2 ８四歩(83)\n"
	load, err := s.ReloadKifu()
	if err != nil {
		t.Fatalf("ReloadKifu: %v", err)
	}
	if load.Note == "" {
		t.Error("差し替えたことが出ていません（黙って手順が変わる）")
	}
	st := load.State
	if len(st.Moves) != 2 || st.Moves[1].USI != "8c8d" {
		t.Fatalf("URL の手順になっていません: %+v", st.Moves)
	}
	// 一致していた 1 手目までは残り、その先は消えること。
	g := s.Evals()
	if len(g.Series) != 1 {
		t.Fatalf("折れ線が消えました: %+v", g.Series)
	}
	if n := len(g.Series[0].Points); n != 2 {
		t.Fatalf("残す/捨てるの線引きがずれています: %d点 %+v", n, g.Series[0].Points)
	}
}

// URL から読んでいない局面では取り直せないこと（理由を返して局面は壊さない）。
func TestStudyServiceReloadKifuWithoutSource(t *testing.T) {
	s := adopted(t)
	load, err := s.ReloadKifu()
	if err == nil {
		t.Fatal("取得元が無いのにエラーになりませんでした")
	}
	if !load.State.Loaded {
		t.Errorf("失敗したのに局面が消えています: %+v", load.State)
	}
	if load.State.SourceURL != "" {
		t.Errorf("撮った局面に取得元が付いています: %q", load.State.SourceURL)
	}
}

// 取れなかったときは今の手順を壊さないこと（**中継が落ちても検討は続けられる**）。
func TestStudyServiceReloadKifuKeepsMovesOnError(t *testing.T) {
	body := kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewStudyService(logger, NewPositionService(logger))
	if _, err := s.LoadKifuURL(srv.URL + "/live.kif"); err != nil {
		t.Fatalf("LoadKifuURL: %v", err)
	}
	body = "これは棋譜ではありません"
	load, err := s.ReloadKifu()
	if err == nil {
		t.Fatal("棋譜でない中身でエラーになりませんでした")
	}
	if load.State.Ply != 2 || len(load.State.Moves) != 2 {
		t.Errorf("失敗したのに手順が壊れています: %+v", load.State)
	}
}
