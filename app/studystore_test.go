package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// empty はまだ何も採っていない解析タブを返す（復元の受け皿）。
func empty(t *testing.T) *StudyService {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewStudyService(logger, NewPositionService(logger))
}

// analyzed は本譜 2 手＋枝 1 本に評価値を付けた検討を返す。
func analyzed(t *testing.T) *StudyService {
	t.Helper()
	s := adopted(t)
	record(t, s, "e1", score(0))
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "e1", score(40))
	if _, err := s.Play("3c3d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "e1", score(30))
	// 枝（**ここにも評価値を付ける** —— 控えは木ごと残るのが要点）。
	if _, err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("8c8d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "e1", score(-120))
	return s
}

// 控えと復元で**評価値グラフがそのまま戻ること**（2026-09-16。Step 1 の目的）。
//
// ⚠️ **ここが崩れると、再起動のたびに解析が失われる**（開発中はそれが毎日起きていた）。
func TestStudySessionRoundTrip(t *testing.T) {
	s := analyzed(t)
	before := s.Evals()
	rec, ok := s.sessionRecord()
	if !ok {
		t.Fatal("控えが作れません")
	}
	back := empty(t)
	if err := back.restoreSession(rec); err != nil {
		t.Fatalf("restoreSession: %v", err)
	}
	after := back.Evals()
	if len(after.Series) != len(before.Series) {
		t.Fatalf("折れ線の本数 = %d, want %d", len(after.Series), len(before.Series))
	}
	if len(after.Series[0].Points) != len(before.Series[0].Points) {
		t.Fatalf("点の数 = %d, want %d", len(after.Series[0].Points), len(before.Series[0].Points))
	}
	for i, p := range after.Series[0].Points {
		if p != before.Series[0].Points[i] {
			t.Errorf("点 %d が違います: %+v, want %+v", i, p, before.Series[0].Points[i])
		}
	}
	// 見ていた場所（枝の中）まで戻ること。
	if back.State().Ply != s.State().Ply {
		t.Errorf("見ていた手数 = %d, want %d", back.State().Ply, s.State().Ply)
	}
}

// ⚠️ **枝の点も残ること。** `series` は今の経路だけを返すので、**それを控えると
// 開き直したときに枝の評価値だけ消えている**（`evalStore.all` が要る理由）。
func TestStudySessionKeepsBranchPoints(t *testing.T) {
	s := analyzed(t)
	rec, ok := s.sessionRecord()
	if !ok {
		t.Fatal("控えが作れません")
	}
	// 本譜 3 点（根・1 手目・2 手目）＋ 枝 1 点 = 4 点。
	if n := len(rec.Evals[0].Points); n != 4 {
		t.Fatalf("控えた点 = %d, want 4（枝の点が落ちています）", n)
	}
	back := empty(t)
	if err := back.restoreSession(rec); err != nil {
		t.Fatalf("restoreSession: %v", err)
	}
	// 本譜へ戻ると、本譜側の点が出ること（枝の点が本譜を潰していない）。
	if _, err := back.GoTo(2); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	g := back.Evals()
	if len(g.Series[0].Points) != 3 {
		t.Errorf("本譜の点 = %d, want 3", len(g.Series[0].Points))
	}
}

// ⚠️ **どのエンジンが出した値かが残ること**（2026-09-16）。
//
// ⚠️ **残すのは ID と凡例の名前だけ。** exe のパスも option も設定
// （`Config.Engines`）にあるので、**`EngineID` で引けば済む** —— 写すと
// 同じことが 2 か所に載って片方だけ古くなる。**それが成り立つ条件が
// 「ID を使い回さない」**で、`ikkyoku.NextEngineID` がそうしてある。
func TestStudySessionKeepsEngineID(t *testing.T) {
	s := analyzed(t)
	rec, ok := s.sessionRecord()
	if !ok {
		t.Fatal("控えが作れません")
	}
	if len(rec.Evals) != 1 || rec.Evals[0].EngineID != "e1" {
		t.Fatalf("エンジンの id が残っていません: %+v", rec.Evals)
	}
	// ⚠️ **凡例の名前だけは写す** —— 登録を消したあとでも折れ線に名前が要る
	// （生の id が並ぶと、どの線が何なのか読めない）。
	if rec.Evals[0].Label == "" {
		t.Error("凡例の名前が落ちています")
	}
	back := empty(t)
	if err := back.restoreSession(rec); err != nil {
		t.Fatalf("restoreSession: %v", err)
	}
	g := back.Evals()
	if len(g.Series) != 1 || g.Series[0].EngineID != "e1" || g.Series[0].Label == "" {
		t.Errorf("復元で折れ線の素性が落ちています: %+v", g.Series)
	}
}

// ⚠️ **根を入れ替えたら控えの id を振り直すこと。** 同じ id のまま書くと、
// **前の対局の控えが上書きされる。**
func TestStudySessionNewIDOnNewRoot(t *testing.T) {
	s := analyzed(t)
	first, _ := s.sessionRecord()
	if _, err := s.NewGame(""); err != nil {
		t.Fatalf("NewGame: %v", err)
	}
	second, ok := s.sessionRecord()
	if !ok {
		t.Fatal("控えが作れません")
	}
	if second.ID == first.ID {
		t.Errorf("根を入れ替えたのに同じ id です: %s", second.ID)
	}
	if len(second.Evals) != 0 {
		t.Errorf("前の対局の評価値が残っています: %+v", second.Evals)
	}
}

// ⚠️ **読めない版は復元しないこと**（起動は通る）。控えは「あると嬉しいもの」で、
// **無いと動かないものにしない**（設計原則3）。
func TestStudySessionRejectsOtherVersion(t *testing.T) {
	s := analyzed(t)
	rec, _ := s.sessionRecord()
	rec.Version = studyRecordVersion + 1
	if err := empty(t).restoreSession(rec); err == nil {
		t.Error("知らない版を復元しています")
	}
}

// ⚠️ **人が始めた検討を控えで上書きしないこと**（黙って作業を捨てるのと同じ）。
func TestStudySessionDoesNotOverwriteLiveStudy(t *testing.T) {
	rec, _ := analyzed(t).sessionRecord()
	live := adopted(t)
	if err := live.restoreSession(rec); err == nil {
		t.Error("始まっている検討を上書きしています")
	}
}

// ディスクへ書いて読み直せること（`StudyStore`）。
func TestStudyStoreWritesAndRestores(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := analyzed(t)
	store := NewStudyStore(logger, dir, s)
	store.flush()

	names, err := store.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("控えのファイル = %v", names)
	}
	// ⚠️ **差し替えで書くこと**（途中で落ちても壊れた JSON が残らない）。
	if _, err := os.Stat(filepath.Join(dir, names[0]+".tmp")); err == nil {
		t.Error("途中のファイルが残っています")
	}

	back := empty(t)
	ok, err := NewStudyStore(logger, dir, back).Restore()
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !ok {
		t.Fatal("戻せていません")
	}
	if len(back.Evals().Series) != 1 {
		t.Errorf("折れ線が戻っていません: %+v", back.Evals().Series)
	}
}

// ⚠️ **変わっていなければ書き直さないこと。** 控えた時刻を含めて比べると
// **毎回「変わった」ことになり、解析のたびにディスクを叩く。**
func TestStudyStoreSkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := analyzed(t)
	store := NewStudyStore(logger, dir, s)
	store.flush()
	names, _ := store.list()
	before, err := os.Stat(filepath.Join(dir, names[0]))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	store.flush()
	after, err := os.Stat(filepath.Join(dir, names[0]))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("変わっていないのに書き直しています")
	}
}

// ⚠️ **控えが 1 つも無くても起動が通ること**（設計原則3）。
func TestStudyStoreRestoreWithoutFiles(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ok, err := NewStudyStore(logger, filepath.Join(t.TempDir(), "まだ無い"), empty(t)).Restore()
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if ok {
		t.Error("何も無いのに戻したことになっています")
	}
}

// ⚠️ **控えに手数も指し手も入れないこと**（2026-09-16）。**どれも同じ控えの中の
// 木から引ける**ので、写すと同じことが 2 か所に載って食い違いうる。
// **復元で木から引き直して、元と 1 つも違わないこと**まで見る。
func TestStudySessionPointsCarryNoTreeData(t *testing.T) {
	s := analyzed(t)
	rec, ok := s.sessionRecord()
	if !ok {
		t.Fatal("控えが作れません")
	}
	body, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// 点の側に木の話（手数・棋譜手数・指し手）が出てこないこと。
	// ⚠️ 木の節点は `usi` / `text` を持つので、**点の鍵の名前で見る。**
	for _, key := range []string{`"ply"`, `"number"`, `"move"`} {
		if bytes.Contains(body, []byte(key)) {
			t.Errorf("控えに %s が入っています（木から引けるもの）", key)
		}
	}

	// 復元すると、手数も棋譜手数も指し手も元どおりであること。
	before := s.Evals().Series[0].Points
	back := empty(t)
	if err := back.restoreSession(rec); err != nil {
		t.Fatalf("restoreSession: %v", err)
	}
	after := back.Evals().Series[0].Points
	if len(after) != len(before) {
		t.Fatalf("点の数 = %d, want %d", len(after), len(before))
	}
	for i := range after {
		if after[i] != before[i] {
			t.Errorf("点 %d が復元で変わりました: %+v, want %+v", i, after[i], before[i])
		}
	}
}

// ⚠️ **木に無い節点の点は捨てること。** 描く先が無いので持っていても意味が無く、
// 残すと**手数も指し手も分からない点**が折れ線に混ざる。
func TestStudySessionDropsPointsWithoutNode(t *testing.T) {
	s := analyzed(t)
	rec, _ := s.sessionRecord()
	n := len(rec.Evals[0].Points)
	rec.Evals[0].Points = append(rec.Evals[0].Points, StudyEvalPoint{ID: 9999, CP: 500})
	back := empty(t)
	if err := back.restoreSession(rec); err != nil {
		t.Fatalf("restoreSession: %v", err)
	}
	again, _ := back.sessionRecord()
	if len(again.Evals[0].Points) != n {
		t.Errorf("木に無い点が残っています: %d, want %d", len(again.Evals[0].Points), n)
	}
}

// ---- 棚の棋譜と結ぶ（Step 2）---------------------------------------------

// loaded は棚から開いたことにして棋譜を読み込む（`gameID` が付く）。
func loaded(t *testing.T, s *StudyService, gameID string) {
	t.Helper()
	const kif = "手合割：平手\n手数----指手---------消費時間--\n   1 ７六歩(77)\n   2 ３四歩(33)\n"
	if _, err := s.loadKifuFrom(kif, "", gameID); err != nil {
		t.Fatalf("loadKifuFrom: %v", err)
	}
}

// 棚から開いた検討の控えに**棚の棋譜 id が付くこと**（2026-09-16。Step 2）。
//
// ⚠️ **これが無いと「次に同じ棋譜を開いたら解析が戻る」が成立しない。**
func TestStudySessionKeepsGameID(t *testing.T) {
	s := adopted(t)
	loaded(t, s, "game-1")
	record(t, s, "e1", score(70))
	rec, ok := s.sessionRecord()
	if !ok {
		t.Fatal("控えが作れません")
	}
	if rec.GameID != "game-1" {
		t.Fatalf("棚の棋譜 id = %q, want game-1", rec.GameID)
	}
	// ⚠️ **根を入れ替えたら捨てること**（撮った局面は棚のどの棋譜でもない）。
	if _, err := s.NewGame(""); err != nil {
		t.Fatalf("NewGame: %v", err)
	}
	if again, _ := s.sessionRecord(); again.GameID != "" {
		t.Errorf("別の対局に前の棚の id が残っています: %q", again.GameID)
	}
}

// 棚の棋譜を開き直すと**前の検討がそのまま戻ること**（Step 2 の目的）。
func TestStudyStoreRestoreGame(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := adopted(t)
	loaded(t, s, "game-1")
	record(t, s, "e1", score(70))
	// 枝を 1 本掘る（**これが戻ることが値打ち**）。
	if _, err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("8c8d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	record(t, s, "e1", score(-40))
	store := NewStudyStore(logger, dir, s)
	store.flush()

	// 別のアプリを立ち上げた、のつもり。
	back := empty(t)
	other := NewStudyStore(logger, dir, back)
	if !other.HasGame("game-1") {
		t.Fatal("「解析あり」の印が出ません")
	}
	if other.HasGame("game-2") {
		t.Error("解析していない棋譜に印が出ています")
	}
	if _, ok := other.RestoreGame("game-1"); !ok {
		t.Fatal("前の検討を開けません")
	}
	if len(back.State().Nodes) != len(s.State().Nodes) {
		t.Errorf("手順が戻っていません: %d 節点, want %d",
			len(back.State().Nodes), len(s.State().Nodes))
	}
	if len(back.Evals().Series) != 1 {
		t.Errorf("折れ線が戻っていません: %+v", back.Evals().Series)
	}
}

// ⚠️ **棚の棋譜 id が無い検討は索引に載らないこと**（撮った 1 局面・貼り付け）。
// **それが普通**で、棚に入っているほうが特別。
func TestStudyStoreIgnoresSessionsWithoutGame(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := NewStudyStore(logger, dir, analyzed(t))
	store.flush()
	other := NewStudyStore(logger, dir, empty(t))
	if other.HasGame("") {
		t.Error("空の id に印が出ています")
	}
	if _, ok := other.RestoreGame(""); ok {
		t.Error("空の id で開けてしまいます")
	}
}

// ⚠️ **控えが無い棋譜では黙って false を返すこと**（設計原則3）。
// **「解析する」が押せなくなってはいけない。**
func TestStudyStoreRestoreGameMissing(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := NewStudyStore(logger, t.TempDir(), empty(t))
	if _, ok := store.RestoreGame("game-9"); ok {
		t.Error("無い控えを開いたことになっています")
	}
}

// ⚠️ **開き直すときは今の検討と入れ替えること**（`restoreSession` の門番は通らない）。
// 人が「この棋譜を解析する」と言っているのだから、入れ替えるのが正しい。
func TestStudyStoreRestoreGameReplacesLiveStudy(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := adopted(t)
	loaded(t, s, "game-1")
	NewStudyStore(logger, dir, s).flush()

	// 別の検討をしている最中に開く。
	live := adopted(t)
	if _, err := live.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if _, ok := NewStudyStore(logger, dir, live).RestoreGame("game-1"); !ok {
		t.Fatal("始まっている検討の上から開けません")
	}
	rec, _ := live.sessionRecord()
	if rec.GameID != "game-1" {
		t.Errorf("入れ替わっていません: %q", rec.GameID)
	}
}

// ⚠️ **セッションを入れ替える前に控えを書かせること。** 間引きの幅（3 秒）の
// あいだに入れ替えると、**直前までの手と評価値が前のセッションから落ちる。**
func TestStudyStoreSavesBeforeSwitching(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := adopted(t)
	loaded(t, s, "game-1")
	store := NewStudyStore(logger, dir, s)
	store.Start()
	defer store.Close()

	// **書かせずに**手を足してから、別の棋譜へ入れ替える。
	if _, err := s.Play("2g2f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	loaded(t, s, "game-2")

	// 前の棋譜の控えに、入れ替え直前の手が入っていること。
	if _, ok := store.RestoreGame("game-1"); !ok {
		t.Fatal("前の棋譜の控えがありません")
	}
	found := false
	for _, n := range s.State().Nodes {
		if n.USI == "2g2f" {
			found = true
		}
	}
	if !found {
		t.Error("入れ替え直前の手が控えから落ちています")
	}
}
