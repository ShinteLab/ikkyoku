package app

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku/analyze"
	"github.com/ShinteLab/ikkyoku/position"
)

// following は「初期局面を採った解析タブ」と「撮った局面を置く訂正タブ」を返す。
func following(t *testing.T) (*StudyService, *PositionService) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pos := NewPositionService(logger)
	if _, err := pos.Load(hirateBoard, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := pos.SetTurn(1); err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	study := NewStudyService(logger, pos)
	if _, err := study.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	return study, pos
}

// shotWith は「撮った盤面 + マスごとの確信度」を訂正タブに置く。
//
// ⚠️ **確信度が無いと修復は働かない**（人が並べた盤を機械が覆さないため）。
func shotWith(t *testing.T, pos *PositionService, boardSFEN string, conf []float64) {
	t.Helper()
	if _, err := pos.Load(boardSFEN, conf); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// evenConf は全マス同じ確信度の表を作る。
func evenConf(v float64) []float64 {
	out := make([]float64, 81)
	for i := range out {
		out[i] = v
	}
	return out
}

// shot は「初期局面から n 手進んだ盤面を撮った」状態を訂正タブに作る。
//
// ⚠️ **手番も駒台も設定しない** —— 撮った直後はどちらも未決なのが普通で、
// **そのままで繋がること自体が要件**（`position.Connect` が経路から決める）。
func shot(t *testing.T, pos *PositionService, moves ...string) {
	t.Helper()
	p, err := position.FromFullSFEN(hirateBoard + " b - 1")
	if err != nil {
		t.Fatalf("FromFullSFEN: %v", err)
	}
	for _, m := range moves {
		if err := p.ApplyMove(m); err != nil {
			t.Fatalf("ApplyMove(%q): %v", m, err)
		}
	}
	if _, err := pos.Load(p.BoardSFEN(), nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// 撮った盤面が 1 手進んでいたら、その 1 手で繋がって本譜が伸びること。
//
// ⚠️ **根に手が 1 つも無い状態から繋ぐのが「普通」。** 画像から追うときは
// **最初の 1 回が必ずこれ**（`MainTip` は 0 のまま正しい）。
// **「手順があること」を繋ぐ条件にしないこと** —— 画面側でそうして
// **本来の使い方でボタンが出なかった**（2026-09-15 に直した）。
//
// ⚠️ **手番も駒台の先後も未決のまま通ること**が肝（`Adopt` との一番の違い）。
// 追従しながら人が駒台を埋めるのは不可能なので、ここで確定を求めると
// Phase 6 そのものが成り立たない。
func TestFollowProbeUnique(t *testing.T) {
	s, pos := following(t)
	shot(t, pos, "7g7f")

	// ⚠️ **同じ局面で `Adopt` は通らない**（未決なので）。**そこが違う。**
	if _, err := s.Adopt(); err == nil {
		t.Fatal("未決の局面が Adopt で通ってしまいました（前提が崩れています）")
	}

	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if p.Kind != FollowUnique {
		t.Fatalf("Kind = %q, want %q（%s）", p.Kind, FollowUnique, p.Reason)
	}
	if len(p.Candidates) != 1 || len(p.Candidates[0].Moves) != 1 || p.Candidates[0].Moves[0] != "7g7f" {
		t.Fatalf("候補 = %+v, want [7g7f]", p.Candidates)
	}
	// 日本語表記が付いていること（**画面に並べるのはこちら**）。
	if txt := p.Candidates[0].Text; len(txt) != 1 || !strings.Contains(txt[0], "歩") {
		t.Errorf("日本語表記が付いていません: %v", txt)
	}

	a, err := s.FollowApply(p.Candidates[0].Moves, p.Rev, true)
	if err != nil {
		t.Fatalf("FollowApply: %v", err)
	}
	if a.Added != 1 || len(a.State.Nodes) != 1 {
		t.Fatalf("本譜が伸びていません: added=%d nodes=%+v", a.Added, a.State.Nodes)
	}
	// **手で押した操作なので、繋いだ先を見せる。**
	if a.State.CurrentID != a.State.MainTip || a.State.Ply != 1 {
		t.Errorf("繋いだ先に移っていません: cur=%d tip=%d ply=%d",
			a.State.CurrentID, a.State.MainTip, a.State.Ply)
	}
}

// ⚠️ **繋ぎ先は本譜の先端で、「今見ている場所」ではない。**
//
// ユーザーが枝の途中を読んでいるのは普通にある。そこへ繋ぐと**検討の枝に
// 中継の手が生える**（本譜が枝の下にぶら下がる）。
// ⚠️ **下見だけで今見ている場所が動かないこと**も一緒に見ている。
func TestFollowProbeAnchorsAtMainTip(t *testing.T) {
	s, pos := following(t)
	for _, m := range []string{"7g7f", "3c3d"} {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%q): %v", m, err)
		}
	}
	tip := s.State().MainTip
	// 1 手目へ戻って**自分の検討の枝**を生やし、そこを見たままにする。
	if _, err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if _, err := s.Play("8c8d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	branch := s.State().CurrentID
	if branch == tip {
		t.Fatal("枝が生えていません（前提が崩れています）")
	}

	// 中継は本譜の続きへ進んだ。
	shot(t, pos, "7g7f", "3c3d", "2g2f")
	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if p.Kind != FollowUnique || len(p.Candidates[0].Moves) != 1 {
		t.Fatalf("本譜の先端から 1 手で繋がるはず: %+v（%s）", p.Candidates, p.Reason)
	}
	// ⚠️ **下見は木も選択も動かさない。**
	if st := s.State(); st.CurrentID != branch {
		t.Errorf("下見で見ている場所が動きました: %d (want %d)", st.CurrentID, branch)
	}

	// 据えたあとも**枝は残る**（`Graft` は消さずに据える）。
	if _, err := s.FollowApply(p.Candidates[0].Moves, p.Rev, false); err != nil {
		t.Fatalf("FollowApply: %v", err)
	}
	st := s.State()
	// ⚠️ **goTo が偽なら見ている場所は動かない**（自動追従はこちらで呼ぶ）。
	if st.CurrentID != branch {
		t.Errorf("goTo=false なのに見ている場所が動きました: %d (want %d)", st.CurrentID, branch)
	}
	found := false
	for _, n := range st.Nodes {
		if n.ID == branch {
			found = true
		}
	}
	if !found {
		t.Error("繋いだら自分の検討の枝が消えました")
	}
}

// ⚠️ **繋がらないとき・順番が決まらないときは木を 1 手も触らないこと。**
//
// `Rev` が進まない ＝ `study:changed` も出ない。**撮った 1 枚は訂正タブに残る**ので、
// 「この局面を解析する」で新しく始められる（設計原則3）。
func TestFollowProbeLeavesTreeAlone(t *testing.T) {
	s, pos := following(t)
	before := s.State()
	// 歩が一気に 2 マス進んだ盤面（＝認識の誤りの模擬。合法手では到達できない）。
	if _, err := pos.Load("lnsgkgsnl/1r5b1/ppppppppp/9/2P6/9/PP1PPPPPP/1B5R1/LNSGKGSNL", nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if p.Kind != FollowUnreachable {
		t.Fatalf("Kind = %q, want %q", p.Kind, FollowUnreachable)
	}
	if p.Reason == "" {
		t.Error("理由が空です（画面に何も出せない）")
	}
	after := s.State()
	if after.Rev != before.Rev || len(after.Nodes) != len(before.Nodes) {
		t.Errorf("下見で木が動きました: rev %d→%d, nodes %d→%d",
			before.Rev, after.Rev, len(before.Nodes), len(after.Nodes))
	}
}

// ⚠️ **順番が決められないときは候補を並べるだけで、1 本選ばないこと。**
//
// **一番大事。** ここが崩れると、空想の手順が「実際に現れた指し手」として棋譜に残る
// （`TODO.md`「本譜のロック」）。
func TestFollowProbeAmbiguous(t *testing.T) {
	s, pos := following(t)
	shot(t, pos, "7g7f", "3c3d", "2g2f", "8c8d")
	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if p.Kind != FollowChoices {
		t.Fatalf("Kind = %q, want %q（%s）", p.Kind, FollowChoices, p.Reason)
	}
	if len(p.Candidates) < 2 {
		t.Fatalf("候補が %d 本しかありません", len(p.Candidates))
	}
	if p.Reason == "" {
		t.Error("理由が空です（なぜ選ばされているのか分からない）")
	}
	for _, c := range p.Candidates {
		if len(c.Text) != len(c.Moves) {
			t.Fatalf("日本語表記の数が合いません: %+v", c)
		}
	}
	// 選べば据わること。
	if _, err := s.FollowApply(p.Candidates[0].Moves, p.Rev, false); err != nil {
		t.Fatalf("FollowApply: %v", err)
	}
	if n := len(s.State().Nodes); n != 4 {
		t.Fatalf("手順 = %d, want 4", n)
	}
}

// 盤面が変わっていなければ `same`（何もすることが無い）。
func TestFollowProbeSame(t *testing.T) {
	s, pos := following(t)
	shot(t, pos)
	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if p.Kind != FollowSame || p.Depth != 0 {
		t.Fatalf("Kind = %q depth = %d, want %q/0", p.Kind, p.Depth, FollowSame)
	}
}

// ⚠️ **下見と据えるのは別の呼び出し。** その間に局面が変わっていたら断ること
// （別の窓が手を指しうる。**違う局面の先に繋がない**）。
func TestFollowApplyRejectsStaleRev(t *testing.T) {
	s, pos := following(t)
	shot(t, pos, "7g7f")
	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	// 下見のあとで誰かが手を指した。
	if _, err := s.Play("2g2f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if _, err := s.FollowApply(p.Candidates[0].Moves, p.Rev, false); err == nil {
		t.Fatal("古い版のまま据えられてしまいました")
	}
}

// ⚠️ **評価値を捨てないこと。** `evals.reset` も `dropAfter` も世代を進めるので、
// **走っている解析の途中経過が消える** —— 中継を追うたびにそれが起きる。
func TestFollowApplyKeepsEvals(t *testing.T) {
	s, pos := following(t)
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	target, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	for ply := 0; ply <= 1; ply++ {
		s.recordEval(target.Epoch, ply, "e1", "エンジン", analyze.Score{CP: 10 * ply}, 12)
	}

	shot(t, pos, "7g7f", "3c3d")
	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if _, err := s.FollowApply(p.Candidates[0].Moves, p.Rev, false); err != nil {
		t.Fatalf("FollowApply: %v", err)
	}
	g := s.Evals()
	if len(g.Series) != 1 || len(g.Series[0].Points) != 2 {
		t.Fatalf("評価値が消えました: %+v", g.Series)
	}
}

// 解析タブに何も無ければ断ること（繋ぐ先が無い）。**理由を出す。**
func TestFollowProbeWithoutStudy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pos := NewPositionService(logger)
	if _, err := pos.Load(hirateBoard, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := NewStudyService(logger, pos)
	if _, err := s.FollowProbe(); err == nil {
		t.Fatal("繋ぐ先が無いのに通りました")
	}
}

// ⚠️ **認識が外したマスを覆して繋ぐ**（修復。2026-09-15）。
//
// **これが入ると人は 1 マスも直さなくてよくなる** —— 撮って押すだけで追える。
// ⚠️ **直したマスを必ず返すこと**（`Fixed`）。黙って直すと、盤に出ている局面が
// 「撮ったもの」なのか「こちらが直したもの」なのか区別が付かなくなる。
func TestFollowProbeRepairs(t *testing.T) {
	s, pos := following(t)
	// ▲7六歩まで進んでいて、認識が 9三の歩を落とした盤面。
	shotWith(t, pos, "lnsgkgsnl/1r5b1/1pppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL", evenConf(0.3))

	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if p.Kind != FollowUnique {
		t.Fatalf("Kind = %q, want %q（%s）", p.Kind, FollowUnique, p.Reason)
	}
	if len(p.Candidates) != 1 || len(p.Candidates[0].Moves) != 1 || p.Candidates[0].Moves[0] != "7g7f" {
		t.Fatalf("候補 = %+v, want [7g7f]", p.Candidates)
	}
	if len(p.Fixed) != 1 {
		t.Fatalf("直したマスが返っていません: %+v", p.Fixed)
	}
	if p.Fixed[0].Square != "９三" {
		t.Errorf("直したマス = %q, want ９三", p.Fixed[0].Square)
	}
	if p.Fixed[0].Was != "空" || p.Fixed[0].Now != "後手の歩" {
		t.Errorf("直した中身 = %q → %q", p.Fixed[0].Was, p.Fixed[0].Now)
	}
}

// ⚠️ **確信度が無い盤（手合割・詰将棋）では修復しないこと。**
//
// 人が並べたものを機械が覆す理由は無い。**厳密一致のままにする。**
func TestFollowProbeNoRepairWithoutConfidence(t *testing.T) {
	s, pos := following(t)
	// 同じ盤面を、確信度を付けずに置く。
	shotWith(t, pos, "lnsgkgsnl/1r5b1/1pppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL", nil)
	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if p.Kind == FollowUnique {
		t.Fatalf("確信度が無いのに修復しました: %+v", p.Candidates)
	}
}

// ⚠️ **人が直したマスは覆さないこと。**
//
// 1 マス直したのに修復で戻されると、**直しても直しても戻る**という一番たちの
// 悪い壊れ方になる。
func TestFollowProbeKeepsHumanEdits(t *testing.T) {
	s, pos := following(t)
	// 認識は 9三を「先手の歩」と誤り、人が盤から外した（＝空にした）。
	shotWith(t, pos, "lnsgkgsnl/1r5b1/Ppppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL", evenConf(0.3))
	if _, err := pos.Remove(2, 0); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	// 人が空にしたマスを「後手の歩」に戻すのが修復の唯一の道なので、**繋がらない**。
	for _, fx := range p.Fixed {
		if fx.Square == "９三" {
			t.Fatalf("人が直したマスを覆しました: %+v", fx)
		}
	}
}

// ⚠️ **認識が大きく外していても、候補の中から選んで繋げること**（2026-09-15）。
//
// **これが「認識器が 100% でないと動かない」への答え。** 厳密一致も修復も
// 空振りしたら、**問いを変えて「どの候補が一番よく合うか」を聞く。**
func TestFollowProbeGuesses(t *testing.T) {
	s, pos := following(t)
	// ▲7六歩まで進み、**さらに認識が 3 マス外している**盤面。
	shotWith(t, pos, "lnsgkgsnl/1r5b1/2ppppppp/9/4p4/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL",
		evenConf(0.5))

	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if p.Kind != FollowUnique {
		t.Fatalf("Kind = %q, want %q（%s）", p.Kind, FollowUnique, p.Reason)
	}
	if !p.Guess {
		t.Error("ぴったり一致ではないので Guess が立つはず")
	}
	if len(p.Candidates) != 1 || p.Candidates[0].Moves[0] != "7g7f" {
		t.Fatalf("候補 = %+v, want [7g7f]", p.Candidates)
	}
	// ⚠️ **どこを覆すのかは必ず返すこと**（黙って直さない）。
	if len(p.Fixed) == 0 {
		t.Error("外していたマスが返っていません")
	}
	if p.Fit < 0.9 {
		t.Errorf("Fit = %v, want 0.9 以上", p.Fit)
	}
	// 据えられること。
	if _, err := s.FollowApply(p.Candidates[0].Moves, p.Rev, false); err != nil {
		t.Fatalf("FollowApply: %v", err)
	}
	if n := len(s.State().Nodes); n != 1 {
		t.Fatalf("手順 = %d, want 1", n)
	}
}

// ⚠️ **盤が映っていない画面は候補を出さないこと**（CM・解説）。
//
// ここで落とせないと、**映っていない盤から適当な手を選んで棋譜に足す**ことになる。
// ⚠️ **「繋がらない」とは別の種類**にすること —— 次にすることが違う（撮り直す）。
func TestFollowProbeUnreadableFrame(t *testing.T) {
	s, pos := following(t)
	shotWith(t, pos, "9/9/9/9/9/9/9/9/9", evenConf(0.5))
	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if p.Kind != FollowUnreadable {
		t.Fatalf("Kind = %q, want %q（%s）", p.Kind, FollowUnreadable, p.Reason)
	}
	if len(p.Candidates) != 0 {
		t.Fatalf("読めていないのに候補を出しました: %+v", p.Candidates)
	}
}

// ⚠️ **1 手では説明が付かない盤面を、推測で 1 手に決め打たないこと。**
//
// **これが推測を入れたことで増えた一番の危険。** 「一番よく合う候補」は必ず 1 つ
// 出てしまうので、**差が付いていないのに採る**と指していない手が棋譜に残る。
//
// ⚠️ **どの `Kind` に落ちるかは固定しない** —— 厳密一致が深いところで拾えば
// それでよいし（そちらのほうが確か）、拾えなければ候補を並べる。
// **見るのは「推測で 1 本に決めていないこと」だけ。**
func TestFollowProbeDoesNotGuessWhenUnclear(t *testing.T) {
	s, pos := following(t)
	// ▲7六歩 と ▲8六歩 の両方が指されている（1 手では説明が付かない）。
	shotWith(t, pos, "lnsgkgsnl/1r5b1/ppppppppp/9/9/1PP6/P2PPPPPP/1B5R1/LNSGKGSNL",
		evenConf(0.5))
	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if p.Kind == FollowUnique && p.Guess {
		t.Fatalf("1 手では説明が付かないのに推測で決め打ちました: %+v", p.Candidates)
	}
}
