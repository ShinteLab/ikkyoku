package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku/analyze"
	"github.com/ShinteLab/ikkyoku/position"
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
	if st.Ply != 0 || len(st.Nodes) != 0 {
		t.Errorf("採った直後に手順があります: ply=%d moves=%+v", st.Ply, st.Nodes)
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
	if st.Ply != 1 || len(st.Nodes) != 1 {
		t.Fatalf("手順が記録されていません: ply=%d moves=%+v", st.Ply, st.Nodes)
	}
	if st.Nodes[0].Text != "▲７六歩" {
		t.Errorf("手順の表記 = %q, want %q", st.Nodes[0].Text, "▲７六歩")
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

// 戻る操作の使い分け。**GoTo は手順を消さない / DropFrom は消す。**
func TestStudyServiceGoToAndDropFrom(t *testing.T) {
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
	if st.Ply != 1 || len(st.Nodes) != 2 {
		t.Errorf("GoTo で手順が消えました: ply=%d moves=%d", st.Ply, len(st.Nodes))
	}
	if strings.Join(st.Played, " ") != "7g7f" {
		t.Errorf("Played = %v（先の手を渡さないこと）", st.Played)
	}

	// **1 手目から下を消す。** 戻って見ている最中でも、消える範囲は押した手で決まる。
	st, err = s.DropFrom(1)
	if err != nil {
		t.Fatalf("DropFrom: %v", err)
	}
	if st.Ply != 0 || len(st.Nodes) != 0 {
		t.Errorf("DropFrom で消えていません: ply=%d moves=%d", st.Ply, len(st.Nodes))
	}
	if _, err := s.DropFrom(1); err == nil {
		t.Error("無い手を消せました")
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
	if st.Ply != 0 || len(st.Nodes) != 0 {
		t.Errorf("採り直したのに手順が残っています: ply=%d moves=%+v", st.Ply, st.Nodes)
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
	if st.Legal == nil || st.Nodes == nil || st.Played == nil {
		t.Error("空でも配列を返すこと（フロントが null を踏む）")
	}
}

// 棋譜を貼り付けると指し手が全て載り、**見ているのは開始局面**であること。
//
// ⚠️ **最終手に置かないこと**（2026-08-18 に変えた）。棋譜を読むのは
// 「この対局を初手から解析する」ためで、連続解析の始点は**今見ている手**なので、
// 最終手に置くと押す前に必ず戻る操作が要る。
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
	if !st.Loaded || len(st.Nodes) != 4 || len(st.Line) != 5 {
		t.Fatalf("指し手が全て載っていません: %+v", st)
	}
	// ⚠️ **見ているのは開始局面**（手順は 1 手も消えていない）。
	if st.Ply != 0 || st.CurrentID != 0 {
		t.Fatalf("開始局面を見ていません: ply=%d current=%d", st.Ply, st.CurrentID)
	}
	// **解析に渡すのも開始局面**（根そのもの。手順は付かない）。
	target, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	if len(target.Moves) != 0 || target.Root != target.Current {
		t.Errorf("解析対象が開始局面になっていません: %+v", target)
	}
	// **最終手まで辿れること**（載っているものは全部指せる形で残っている）。
	last := st.Line[len(st.Line)-1]
	if _, err := s.GoTo(last); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	target, err = s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	if len(target.Moves) != 4 {
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
	// **指し手は全て載り、見ているのは開始局面**（貼り付けと同じ）。
	if len(load.State.Nodes) != 2 || load.State.Ply != 0 {
		t.Fatalf("指し手が全て載っていないか、開始局面を見ていません: %+v", load.State)
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
	if !st.Loaded || st.Ply != 0 || len(st.Nodes) != 0 {
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
	// ⚠️ **最終手まで進めておく**（読み込んだ直後は開始局面を見ている）。
	// 中継を追っている状態をここで作る。
	line := s.State().Line
	last := line[len(line)-1]
	if _, err := s.GoTo(last); err != nil {
		t.Fatalf("GoTo: %v", err)
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
	// ⚠️ **伸びただけなら断りを出さないこと**（中継は 1 手ごとに伸びる）。
	if load.Note != "" {
		t.Errorf("食い違っていないのに差し替えの断りが出ています: %s", load.Note)
	}
	if len(load.State.Nodes) != 4 {
		t.Fatalf("最新の手順が載っていません: %+v", load.State.Nodes)
	}
	// ⚠️ **見ている位置は動かさないこと**（2026-08-19）。**最後の手を見ていても
	// 伸びた先へは進まない** —— どこを見ているかはユーザーが選んだ状態で、
	// 取り直しは「URL の側を正にする」操作でしかない。
	if load.State.CurrentID != last || load.State.Ply != 2 {
		t.Errorf("取り直しで選択が動いています: cur=%d ply=%d (want cur=%d ply=2)",
			load.State.CurrentID, load.State.Ply, last)
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

// 食い違ったら**URL の手順を本譜にし、それまでの手順は枝として残す**こと。
//
// ⚠️ **消さないのが要点**（2026-08-13。枝が入るまでは捨てていた）。
// 評価値も節点に紐づいているので、**枝へ戻ればそのまま出る**。
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
		t.Error("差し替えたことが出ていません（黙って本譜が変わる）")
	}
	st := load.State
	// **本譜は URL のもの**（今見ている経路もそちら）。
	if len(st.Line) != 3 {
		t.Fatalf("本譜が 2 手になっていません: %+v", st.Line)
	}
	var main, branch *position.Node
	for i := range st.Nodes {
		n := &st.Nodes[i]
		if n.USI == "8c8d" {
			main = n
		}
		if n.USI == "3c3d" {
			branch = n
		}
	}
	if main == nil || !main.Main {
		t.Fatalf("URL の手が本譜になっていません: %+v", st.Nodes)
	}
	// ⚠️ **前の手順が消えていないこと**（枝として残る）。
	if branch == nil || branch.Main {
		t.Fatalf("前の手順が消えました: %+v", st.Nodes)
	}
	// 一致していた 1 手目までの評価値は今の経路に出る（**枝の点は混ぜない**）。
	g := s.Evals()
	if len(g.Series) != 1 {
		t.Fatalf("折れ線が消えました: %+v", g.Series)
	}
	if n := len(g.Series[0].Points); n != 2 {
		t.Fatalf("今の経路の点だけになっていません: %d点 %+v", n, g.Series[0].Points)
	}
	// ⚠️ **枝へ戻せば、そちらに付けた評価値がそのまま出ること**（消していない）。
	if _, err := s.GoTo(branch.ID); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if n := len(s.Evals().Series[0].Points); n != 3 {
		t.Errorf("枝の評価値が消えました: %d点", n)
	}
}

// 枝を見ている最中に取り直しても、**選択がそこから動かない**こと（2026-08-19）。
//
// ⚠️ **これが壊れていた。** 「最後の手を見ていたか」を深さ
// （`Ply() >= len(MainLine())`）だけで見ていたので、**枝は本譜より深くなり得る**
// ぶん「最後の手」と誤判定され、**本譜の終わりへ飛ばされていた**。
func TestStudyServiceReloadKifuKeepsBranchSelection(t *testing.T) {
	body := kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n   3 ２六歩(27)\n"
	srv := kifuServer(t, &body)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewStudyService(logger, NewPositionService(logger))
	if _, err := s.LoadKifuURL(srv.URL + "/live.kif"); err != nil {
		t.Fatalf("LoadKifuURL: %v", err)
	}
	// 1 手目まで戻って別の手を指す（＝**本譜より深い枝**に居る状態）。
	if _, err := s.GoTo(s.State().Line[1]); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	for _, mv := range []string{"8c8d", "2g2f"} {
		if _, err := s.Play(mv); err != nil {
			t.Fatalf("Play(%s): %v", mv, err)
		}
	}
	at := s.State().CurrentID

	// 中継が 1 手進んだ。
	body = kifuHead + "   1 ７六歩(77)\n   2 ３四歩(33)\n   3 ２六歩(27)\n   4 ８四歩(83)\n"
	load, err := s.ReloadKifu()
	if err != nil {
		t.Fatalf("ReloadKifu: %v", err)
	}
	if load.State.CurrentID != at {
		t.Errorf("枝を見ていたのに選択が動きました: cur=%d (want %d)", load.State.CurrentID, at)
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
	if len(load.State.Nodes) != 2 || len(load.State.Line) != 3 {
		t.Errorf("失敗したのに手順が壊れています: %+v", load.State)
	}
}

// 候補手の読み筋を枝として足せること（解析タブの候補手の右クリック）。
//
// ⚠️ **足しただけで今見ている局面が動かないこと。** 動くと走っている解析が
// 別の局面のものになり、候補を続けて足せない。
func TestStudyServiceAddLine(t *testing.T) {
	s := adopted(t)
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	at := s.State().CurrentID

	got, err := s.AddLine("e1", []string{"3c3d", "2g2f"})
	if err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	if got.Added != 2 || got.FirstID == 0 {
		t.Fatalf("枝が生えていません: %+v", got)
	}
	if got.State.CurrentID != at {
		t.Errorf("足しただけで局面が動きました: %d -> %d", at, got.State.CurrentID)
	}
	// **見ている局面は動かないが、経路（＝この先どう続くか）はそこへ伸びる。**
	// ⚠️ 続きが他に無いのだから、それが今の経路になるのが正しい
	// （連続解析もそこを辿る）。**カーソルが動いていないことと混同しないこと。**
	if got.State.Ply != 1 || len(got.State.Line) != 4 {
		t.Errorf("経路がおかしい: ply=%d line=%+v", got.State.Ply, got.State.Line)
	}
}

// ⚠️ **候補が本譜と同じ手なら枝を増やさないこと**（食い違うところまで辿る）。
func TestStudyServiceAddLineFollowsMainLine(t *testing.T) {
	s := adopted(t)
	for _, mv := range []string{"7g7f", "3c3d"} {
		if _, err := s.Play(mv); err != nil {
			t.Fatalf("Play %s: %v", mv, err)
		}
	}
	if _, err := s.GoTo(0); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	got, err := s.AddLine("e1", []string{"7g7f", "3c3d"})
	if err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	if got.Added != 0 || got.FirstID != 0 {
		t.Errorf("同じ手順で枝が増えました: %+v", got)
	}
	if len(got.State.Nodes) != 2 {
		t.Errorf("節点が増えました: %+v", got.State.Nodes)
	}
}

// ⚠️ **枝を消しても、他の枝と本譜は残ること**（消えるのは子孫だけ）。
func TestStudyServiceDropFromKeepsSiblings(t *testing.T) {
	s := adopted(t)
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	got, err := s.AddLine("e1", []string{"3c3d"})
	if err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	branch := got.FirstID
	other, err := s.AddLine("e1", []string{"8c8d"})
	if err != nil {
		t.Fatalf("AddLine: %v", err)
	}

	st, err := s.DropFrom(branch)
	if err != nil {
		t.Fatalf("DropFrom: %v", err)
	}
	if len(st.Nodes) != 2 {
		t.Fatalf("消しすぎ/消し足りません: %+v", st.Nodes)
	}
	for _, n := range st.Nodes {
		if n.ID == branch {
			t.Errorf("消えていません: %+v", n)
		}
	}
	if _, err := s.GoTo(other.FirstID); err != nil {
		t.Errorf("兄弟の枝まで消えました: %v", err)
	}
}

// TestStudyServiceAddLineSource は**誰が言った手か**が手順に残ることを固定する
// （2026-08-14）。枝は「エンジンがそう読んだ」だけの手なので、**本譜と同じ
// 見た目で並ぶとどれが誰の読み筋か分からない**（手順リストで色の丸になる）。
func TestStudyServiceAddLineSource(t *testing.T) {
	s := adopted(t)
	got, err := s.AddLine("engine-1", []string{"7g7f", "3c3d"})
	if err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	found := false
	for _, n := range got.State.Nodes {
		if n.ID == got.FirstID {
			found = true
			if len(n.Sources) != 1 || n.Sources[0] != "engine-1" {
				t.Errorf("Sources = %v, want [engine-1]", n.Sources)
			}
		}
	}
	if !found {
		t.Fatalf("足した節点が見つかりません: %+v", got)
	}
}

// ---- study:changed（別ウィンドウとの連動の土台。2026-09-08）----------------

// studyEvents は流れたイベントを控える（`Emit` の差し込み先）。
type studyEvents struct {
	names []string
	revs  []int
	last  StudyState
}

func (e *studyEvents) emit(name string, data any) {
	e.names = append(e.names, name)
	st, _ := data.(StudyState)
	e.revs = append(e.revs, st.Rev)
	e.last = st
}

// watched は Emit を差し込んだ StudyService を返す。
func watched(t *testing.T) (*StudyService, *studyEvents) {
	t.Helper()
	s := adopted(t)
	ev := &studyEvents{}
	s.Emit = ev.emit
	return s, ev
}

// ⚠️ **変えたら必ず知らせること。** これが無いと、別ウィンドウは
// 「自分が呼んでいない変更」に気づけない（連動の土台そのもの）。
// ⚠️ **rev が進むこと**も見ている —— 受け取る側は「既に描いた版より新しいときだけ
// 描く」で古いイベントを弾くので、進まないと**2 回目以降が全部捨てられる**。
func TestStudyServicePublishesOnChange(t *testing.T) {
	s, ev := watched(t)
	st, err := s.Play("7g7f")
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if len(ev.names) != 1 || ev.names[0] != "study:changed" {
		t.Fatalf("流れたイベント = %v, want [study:changed]", ev.names)
	}
	// ⚠️ **戻り値とイベントは同じ版であること。** 食い違うと、呼んだ窓と
	// 別の窓が違う局面を描く。
	if ev.last.Rev != st.Rev {
		t.Errorf("イベントの rev = %d, 戻り値の rev = %d（同じであること）", ev.last.Rev, st.Rev)
	}
	if ev.last.SFEN != st.SFEN {
		t.Errorf("イベントの SFEN = %q, 戻り値 = %q", ev.last.SFEN, st.SFEN)
	}

	before := st.Rev
	next, err := s.Play("3c3d")
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if next.Rev <= before {
		t.Errorf("rev が進んでいません: %d → %d", before, next.Rev)
	}
	if len(ev.names) != 2 {
		t.Errorf("イベントの数 = %d, want 2", len(ev.names))
	}
}

// ⚠️ **失敗したら出さないこと。** 状態は変わっていないので、出すと
// 全部の窓が同じ絵を描き直すだけになる。
func TestStudyServiceNoPublishOnError(t *testing.T) {
	s, ev := watched(t)
	if _, err := s.Play("5e5d"); err == nil { // 5e には駒が居ない
		t.Fatal("指せない手が通りました")
	}
	if len(ev.names) != 0 {
		t.Errorf("失敗したのにイベントが流れました: %v", ev.names)
	}
	if _, err := s.GoTo(999); err == nil { // 無い節点
		t.Fatal("無い節点へ行けました")
	}
	if len(ev.names) != 0 {
		t.Errorf("失敗したのにイベントが流れました: %v", ev.names)
	}
}

// ⚠️ **読むだけでは rev を進めないこと**（進めると全部の窓が無駄に描き直す）。
func TestStudyServiceStateDoesNotBumpRev(t *testing.T) {
	s, ev := watched(t)
	first := s.State().Rev
	for i := 0; i < 3; i++ {
		if got := s.State().Rev; got != first {
			t.Fatalf("State を呼んだだけで rev が動きました: %d → %d", first, got)
		}
	}
	if len(ev.names) != 0 {
		t.Errorf("読んだだけでイベントが流れました: %v", ev.names)
	}
}

// ⚠️ **棋譜の読み込みは「取得元まで入った 1 回」で知らせること**（2026-09-08）。
// 2 回に分けると、**取得元が空の状態が 1 回ぶん外へ漏れる** ——
// 別の窓で再読み込みのアイコンが出たり消えたりする。
func TestStudyServiceLoadKifuURLPublishesOnce(t *testing.T) {
	kif := "手合割：平手\n手数----指手---------消費時間--\n   1 ７六歩(77)   ( 0:01/00:00:01)\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, kif)
	}))
	defer srv.Close()

	s, ev := watched(t)
	if _, err := s.LoadKifuURL(srv.URL + "/x.kif"); err != nil {
		t.Fatalf("LoadKifuURL: %v", err)
	}
	if len(ev.names) != 1 {
		t.Fatalf("イベントの数 = %d, want 1（%v）", len(ev.names), ev.names)
	}
	if ev.last.SourceURL == "" {
		t.Error("イベントに取得元が入っていません（別の窓で再読み込みが出せない）")
	}
}

// ⚠️ **Emit が nil でも動くこと**（設計原則3）。イベントを捨てても、
// 呼んだ窓は戻り値で描けるので今までどおり動く。
func TestStudyServiceWithoutEmitter(t *testing.T) {
	s := adopted(t) // Emit は nil のまま
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if s.State().Ply != 1 {
		t.Error("Emit が無いと手が進みません")
	}
}
