package app

import (
	"strings"
	"testing"
	"time"

	"github.com/ShinteLab/ikkyoku/analyze"
	"github.com/ShinteLab/ikkyoku/position"
)

// following は「初期局面を採った解析タブ」と「撮った局面を置く訂正タブ」を返す。
func following(t *testing.T) (*StudyService, *PositionService) {
	t.Helper()
	pos := NewPositionService()
	if _, err := pos.Load(hirateBoard, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := pos.SetTurn(1); err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	study := NewStudyService(pos)
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

// boardAfter は「初期局面から n 手進んだ盤面」の SFEN を作る（＝撮った 1 枚）。
//
// ⚠️ **手番も駒台も付かない** —— 撮った直後はどちらも未決なのが普通で、
// **そのままで繋がること自体が要件**（`position.Connect` が経路から決める）。
func boardAfter(t *testing.T, moves ...string) string {
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
	return p.BoardSFEN()
}

// shot は撮った盤面を**訂正タブ**に置く（手で繋ぐ側＝`FollowProbe` の入口）。
//
// ⚠️ **追従（`FollowAuto`）はここを通らない** —— あちらは撮った 1 枚を直に受け取る。
// **訂正タブは人の作業場**なので、1 秒ごとに上書きしてはいけない（実機で踏んだ）。
func shot(t *testing.T, pos *PositionService, moves ...string) {
	t.Helper()
	if _, err := pos.Load(boardAfter(t, moves...), nil); err != nil {
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
	pos := NewPositionService()
	if _, err := pos.Load(hirateBoard, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := NewStudyService(pos)
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

// followAuto は追従に**同じ 1 枚を 2 回**渡す（2026-10-05 から、2 枚続けて同じ答えで初めて足す）。
// 1 回目で控えなかった（足せる手が無かった）ならそれを返す。
func followAuto(t *testing.T, s *StudyService, board string, conf []float64) (FollowAuto, error) {
	t.Helper()
	first, err := s.FollowAuto(board, conf, nil)
	if err != nil || !first.Pending {
		return first, err
	}
	return s.FollowAuto(board, conf, nil)
}

// ⚠️ **追従は人に判断させないこと**（2026-09-15）。
//
// **「訂正を挟むかどうか」をこちらが決めない**のが要点。繋がるなら聞かずに繋ぎ、
// 足せる手が無いときだけ黙って見送る。**判断するのは人で、そのために戻れる。**
func TestFollowAutoAdvances(t *testing.T) {
	s, _ := following(t)
	// ⚠️ **訂正タブに置かないこと** —— 追従は撮った 1 枚を直に受け取る。
	a, err := followAuto(t, s, "lnsgkgsnl/1r5b1/2ppppppp/9/4p4/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL",
		evenConf(0.5))
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Applied || a.Added != 1 {
		t.Fatalf("進んでいません: %+v", a)
	}
	if !a.Guess {
		t.Error("推測で選んだので Guess が立つはず")
	}
	// ⚠️ **手順にも印が付くこと**（どこまで戻ればよいかの材料）。
	nodes := a.State.Nodes
	if len(nodes) != 1 || !nodes[0].Guess {
		t.Fatalf("推測の印が付いていません: %+v", nodes)
	}
	// **先端を見ていたので付いていく。**
	if a.State.CurrentID != a.State.MainTip {
		t.Errorf("先端に付いていっていません: cur=%d tip=%d", a.State.CurrentID, a.State.MainTip)
	}
}

// ⚠️ **戻って読んでいる最中は、見ている場所を動かさないこと。**
//
// 中継が進むたびに引き剥がされると、**検討そのものができない**。
func TestFollowAutoKeepsPositionWhenBrowsing(t *testing.T) {
	s, _ := following(t)
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if _, err := s.Play("3c3d"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	// **1 手目まで戻って読んでいる。**
	if _, err := s.GoTo(1); err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	a, err := followAuto(t, s, boardAfter(t, "7g7f", "3c3d", "2g2f"), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Applied {
		t.Fatalf("進んでいません: %+v", a)
	}
	if a.State.CurrentID != 1 {
		t.Errorf("読んでいる場所から引き剥がされました: %d", a.State.CurrentID)
	}
}

// ⚠️ **足せる手が無いときは黙って見送ること**（失敗にしない）。
//
// 盤が映っていない・変わっていない・決められない —— どれも**正しさの判断ではなく、
// 足せる手が無いという事実**。
func TestFollowAutoSkipsQuietly(t *testing.T) {
	cases := []struct {
		name  string
		board string
		conf  []float64
	}{
		{"盤が映っていない", "9/9/9/9/9/9/9/9/9", evenConf(0.5)},
		{"変わっていない", hirateBoard, evenConf(0.5)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := following(t)
			before := s.State().Rev
			a, err := s.FollowAuto(c.board, c.conf, nil)
			if err != nil {
				t.Fatalf("FollowAuto: %v（見送りは失敗にしない）", err)
			}
			if a.Applied {
				t.Fatalf("足せないはずが進みました: %+v", a)
			}
			if s.State().Rev != before {
				t.Error("見送ったのに木が動きました")
			}
		})
	}
}

// ⚠️ **大きく進んだ盤面でも無反応にならないこと**（2026-09-15 に実機で踏んだ）。
//
// 大盤解説のあいだに何手も進むと、深く探しても届かない。**そのときに
// 「探し切れませんでした」で行き止まりにすると、追跡が黙って止まる**
// （実機のログ: `stop=探し切れませんでした nodes=10021`。以降ずっと無反応）。
//
// ⚠️ **見るのは「次にできることが返ること」。** どれを返すかは固定しない ——
// 1 手ずつ追いつく（`unique`）でも、候補を並べる（`choices`）でもよい。
func TestFollowProbeDoesNotDeadEnd(t *testing.T) {
	s, pos := following(t)
	// 初期局面から 6 手進んだ盤面（`MaxDepth` を超える）。
	p, err := position.FromFullSFEN(hirateBoard + " b - 1")
	if err != nil {
		t.Fatalf("FromFullSFEN: %v", err)
	}
	for _, m := range []string{"7g7f", "3c3d", "2g2f", "8c8d", "2f2e", "8d8e"} {
		if err := p.ApplyMove(m); err != nil {
			t.Fatalf("ApplyMove(%q): %v", m, err)
		}
	}
	shotWith(t, pos, p.BoardSFEN(), evenConf(0.5))

	got, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	switch got.Kind {
	case FollowUnique, FollowChoices:
		if len(got.Candidates) == 0 {
			t.Fatalf("%s なのに候補がありません", got.Kind)
		}
	default:
		t.Fatalf("次にできることが返りません: kind=%q reason=%q", got.Kind, got.Reason)
	}
}

// ⚠️ **1 手進んだだけなら深い探索に入らないこと**（2026-09-15）。
//
// 中継は 0 手か 1 手しか進まないのが普通で、そこは候補を並べるだけで片が付く。
// **深い探索を先に回していたせいで、中盤の局面では 3 秒のタイムアウトに
// 当たっていた**（実機）。⚠️ **速さの歯止め** —— 遅くなったらここが落ちる。
func TestFollowProbeIsFastForOneMove(t *testing.T) {
	s, pos := following(t)
	shot(t, pos, "7g7f")

	start := time.Now()
	got, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Errorf("1 手の下見に %v かかりました（深い探索に入っている）", elapsed)
	}
	if got.Kind != FollowUnique {
		t.Fatalf("Kind = %q, want %q", got.Kind, FollowUnique)
	}
}

// ⚠️ **長考中（盤が動いていない）は、認識がぶれていても手を足さないこと。**
//
// 実機のログで見つけた形（`moves=[] fit=0.945 margin=0.15`）——
// **「何も動いていない」が 1 位なら、差が小さくても足す手は無い。**
// ⚠️ **「決められない」に落とさないこと** —— 落とすと深い探索へ進み、
// **1 tick まるごと無駄になる**（進んでも「隔たりが大きすぎます」が返るだけ）。
func TestFollowProbeSameDespiteNoise(t *testing.T) {
	s, pos := following(t)
	// 盤は初期局面のまま。**認識が 3 マス外している**（9三・8三が抜け、5五に湧いた）。
	shotWith(t, pos, "lnsgkgsnl/1r5b1/2ppppppp/9/4p4/9/PPPPPPPPP/1B5R1/LNSGKGSNL",
		evenConf(0.5))

	got, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if got.Kind != FollowSame {
		t.Fatalf("Kind = %q, want %q（%s）", got.Kind, FollowSame, got.Reason)
	}
	// 追従でも手が増えないこと。
	a, err := s.FollowAuto("lnsgkgsnl/1r5b1/2ppppppp/9/4p4/9/PPPPPPPPP/1B5R1/LNSGKGSNL",
		evenConf(0.5), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Applied {
		t.Fatalf("動いていないのに手を足しました: %+v", a)
	}
}

// ⚠️ **追従が訂正タブを触らないこと**（2026-09-15 に実機で踏んだ）。
//
// **実機の症状**: 中継を追っているあいだに、その日の盤面を `suteme` に登録しようと
// 撮って訂正していたら、**1 秒ごとに中継の盤で上書きされて作業にならなかった。**
//
// 原因は**追従が訂正タブを一時バッファとして使っていた**こと（1 周ごとに
// `PositionService.Load` を呼んでいた）。⚠️ **訂正タブは人の作業場**であって、
// 機械が書き込んでよい場所ではない（⚠️ **`_docs/design-capture.md` の「訂正タブと解析タブは
// 別の局面を持っている」を、追従が破っていた**）。
//
// ⚠️ **禁止事項で塞がないこと** —— 中継を観ながら学習データを作るのは
// **正当な使い方**（訂正結果を学習に回すのは 2026-08-07 の決定）。
func TestFollowAutoDoesNotTouchEditor(t *testing.T) {
	s, pos := following(t)

	// **人が訂正タブで別の盤を直している最中。**
	mine := "9/9/9/9/9/9/9/9/4K4"
	if _, err := pos.Load(mine, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	before := pos.State()

	a, err := followAuto(t, s, boardAfter(t, "7g7f"), evenConf(0.9))
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	// 追従そのものは働いていること（触らないだけで、動かないのでは意味が無い）。
	if !a.Applied || a.Added != 1 {
		t.Fatalf("追従が進んでいません: %+v", a)
	}

	after := pos.State()
	if after.BoardSFEN != before.BoardSFEN {
		t.Errorf("訂正タブの盤が書き換わりました: %q → %q", before.BoardSFEN, after.BoardSFEN)
	}
	// ⚠️ **学習へ送るラベルも変わっていないこと** —— ここが書き換わると、
	// **画素と一致しないラベルを suteme へ送る**ことになる（一番たちが悪い）。
	if after.LabelSFEN != before.LabelSFEN {
		t.Errorf("学習ラベルが書き換わりました: %q → %q", before.LabelSFEN, after.LabelSFEN)
	}
}

// ⚠️ **手で繋ぐ側（`FollowProbe`）は今までどおり訂正タブを読むこと。**
//
// **2 つは別の口。** 追従は「撮った 1 枚を直に受け取る」、
// 訂正タブの「本譜に繋ぐ」は「**人が直した盤**を繋ぐ」——
// だからあちらだけが `humanEditCost`（人が直したマスは覆さない）を持つ。
func TestFollowProbeStillReadsEditor(t *testing.T) {
	s, pos := following(t)
	shot(t, pos, "7g7f")

	got, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if got.Kind != FollowUnique || len(got.Candidates) != 1 ||
		len(got.Candidates[0].Moves) != 1 || got.Candidates[0].Moves[0] != "7g7f" {
		t.Fatalf("訂正タブの盤から繋がっていません: %+v", got)
	}
}

// ⚠️ **1 枚の画像から取れるだけ取ること**（2026-09-15 に実機で踏んだ）。
//
// **実機の症状**: ゲーム画面で試したら 4 手目までしか追えず、そこから 90 手まで
// 無反応になった。**認識 1 枚に 2.1 秒**かかるのに**1 周 1 手**しか足していな
// かったので、**3 秒に 1 手**しか進めず、遅れが一方的に開いて
// **`Connect` の探索範囲（4 手）を超えた時点で永久に復帰できなくなった。**
//
// **撮った 1 枚には何手ぶんも先が写っている。** 1 手だけ取って残りを捨て、
// また 2 秒かけて撮り直すのは無駄でしかない。
func TestFollowAutoCatchesUpFromOneFrame(t *testing.T) {
	s, _ := following(t)

	// **2 手進んだ盤面が 1 枚に写っている**（実機で起きていたのがこれ）。
	//
	// ⚠️ **手順が入れ替えられない組み合わせにすること** —— 7g7f と 2g2f のように
	// **互いに独立な 2 手は順番が決まらない**（`▲7六 △3四 ▲2六` と
	// `▲2六 △3四 ▲7六` が同じ盤面になる）ので、**繋がらないのが正しい。**
	want := []string{"7g7f", "3c3d"}
	a, err := followAuto(t, s, boardAfter(t, want...), evenConf(0.9))
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Applied {
		t.Fatalf("進んでいません: %+v", a)
	}
	if a.Added != 2 {
		t.Fatalf("足したのは %d 手（2 手のはず）: %v", a.Added, a.Moves)
	}
	for i, mv := range want {
		if a.Moves[i] != mv {
			t.Fatalf("手順が違います: %v, want %v", a.Moves, want)
		}
	}
	// ⚠️ **日本語表記も手数ぶん揃っていること**（画面に出すのはこちら）。
	if len(a.Text) != 2 {
		t.Errorf("表記が %d 個（2 個のはず）: %v", len(a.Text), a.Text)
	}
	// **先端を見ていたので付いていく。**
	if a.State.CurrentID != a.State.MainTip {
		t.Errorf("先端に付いていっていません: cur=%d tip=%d", a.State.CurrentID, a.State.MainTip)
	}
}

// ⚠️ **指している手が盤を覆った 1 枚から、空想の手を足さないこと**（2026-10-05）。
//
// **実機の症状**: 手が 2八の飛車を隠して「空き」、手の影が 4八の「後手の香」に読まれた
// 1 枚から ▲4八飛を足し、次の 1 枚でそれを打ち消す ▲2八飛を足して、本当に指された
// ▲4五同銀を落とした。そこから先は 1 手も繋がらなくなった。
//
// **止めているのは `position` の `backed`**（行き先に、指した側の駒が読めていない手は
// 採らない）。ここでは追従の入口から見て、**1 手も足さず「変わっていない」で見送る**ことを見る。
func TestFollowAutoIgnoresCoveredBoard(t *testing.T) {
	s, _ := following(t)
	// 飛車が手で隠れて、まわりに香や玉の誤読が散った 1 枚（実機の 002 を写した形）。
	const covered = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPLPP/1B3l3/LNSGKGSkP"
	a, err := s.FollowAuto(covered, evenConf(0.5), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Applied || a.Added != 0 {
		t.Fatalf("手で隠れた 1 枚から手を足しました: %v", a.Moves)
	}
	if a.Kind != FollowSame {
		t.Errorf("Kind = %q, want %q（%s）", a.Kind, FollowSame, a.Reason)
	}
}

// ⚠️ **行き先の違う候補（`Unsettled`）から、追従が 1 本選んで足さないこと**（2026-10-05）。
//
// `Rank` で差が付かず `Connect` も繋がらないと、`fallbackFollow` が 1 手の候補を
// `FollowChoices` で並べる。**候補ごとに行き着く局面が違う**ので、「順番が決まらない
// だけ（行き先は同じ）」として進んでよい `Connect` の解とは別物。
// 実機ではこれを取り違えて、上の ▲4八飛を採っていた。
//
// ⚠️ **手で繋ぐ側（`FollowProbe`）が候補を出すことは変えない**（黙って無反応にしない）。
func TestFollowAutoDoesNotPickUnsettled(t *testing.T) {
	s, pos := following(t)
	// 先手の歩が 3 本進んでいる（後手は 1 筋の香で往復しただけ）。1 手では説明が付かず、
	// 深さ 4 でも繋がらない。
	board := boardAfter(t, "7g7f", "1a1b", "2g2f", "1b1a", "8g8f")

	// 前提: 手で繋ぐ側には「行き先の違う候補」として並ぶこと。
	shotWith(t, pos, board, evenConf(0.5))
	p, err := s.FollowProbe()
	if err != nil {
		t.Fatalf("FollowProbe: %v", err)
	}
	if p.Kind != FollowChoices || !p.Unsettled || len(p.Candidates) < 2 {
		t.Fatalf("前提が崩れています: kind=%q unsettled=%v 候補=%v", p.Kind, p.Unsettled, p.Candidates)
	}

	a, err := s.FollowAuto(board, evenConf(0.5), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Applied || a.Added != 0 {
		t.Fatalf("行き先が決まっていないのに足しました: %v", a.Moves)
	}
	if n := len(s.State().Nodes); n != 0 {
		t.Fatalf("手順 = %d, want 0", n)
	}
	// **見送った理由は返すこと**（フロントが「繋がらなかった周」として数えて画像を残す）。
	if a.Kind != FollowChoices {
		t.Errorf("Kind = %q, want %q", a.Kind, FollowChoices)
	}
}

// ⚠️ **追いつき切ったら止まること**（無限に手を生やさない）。
//
// **盤面が本譜と同じなら 1 手も足さない** —— 繰り返しの終わり方がこれ。
func TestFollowAutoStopsWhenCaughtUp(t *testing.T) {
	s, _ := following(t)

	if _, err := followAuto(t, s, boardAfter(t, "7g7f", "3c3d"), evenConf(0.9)); err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	// **同じ盤面をもう一度渡す。** 追いつき済みなので 1 手も増えないこと。
	a, err := followAuto(t, s, boardAfter(t, "7g7f", "3c3d"), evenConf(0.9))
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Applied {
		t.Fatalf("追いつき済みなのに手を足しました: %+v", a.Moves)
	}
	if a.Kind != FollowSame {
		t.Errorf("Kind = %q, want %q", a.Kind, FollowSame)
	}
}

// ⚠️ **盤が上下逆に映っていることに気づくこと**（2026-09-15 に実機で踏んだ）。
//
// **実機の症状**: 後手で対局していたら 1 手目から 1 手も進まなかった。
// ゲーム画面は**自分が手前**に出るので、後手なら**盤が上下逆に映る** ——
// 相手（先手）の手は、アプリから見ると「上側＝後手が動いた」ことになるので、
// **先手のどの手でも説明できず「変わっていません」で終わる。**
//
// ⚠️ **平手の初期局面は上下対称なので、盤を見ても目線が逆だと分からない。**
// **最初の 1 手が指されて初めて分かる**ので、ここが唯一の検出の機会。
func TestFollowAutoNoticesFlippedBoard(t *testing.T) {
	s, _ := following(t)

	// **上下逆に映った「1 手進んだ盤面」。** そのままでは先手の手で説明できない。
	flipped, err := position.FromSFEN(boardAfter(t, "7g7f"))
	if err != nil {
		t.Fatalf("FromSFEN: %v", err)
	}
	a, err := s.FollowAuto(flipped.Rotate180().SFEN(), evenConf(0.9), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Applied {
		t.Fatalf("上下逆なのに手を足しました: %+v", a.Moves)
	}
	if !a.Flipped {
		t.Fatalf("目線が逆だと気づいていません: kind=%q reason=%q", a.Kind, a.Reason)
	}
	// ⚠️ **根拠を出すこと**（「逆かも」だけでは確かめようが無い）。
	if a.FlipMove == "" {
		t.Error("根拠の手が空です")
	}
}

// ⚠️ **実機に近い確信度（空きは 0）でも、上下逆に気づくこと**（2026-10-06 に見直した）。
//
// 「逆」を疑うのは「駒が来たのに今の向きでは説明が付かない」1 枚だけになったので、
// 逆に映った相手の手がそこへ入ることを見る。今の向きのまま先を探すと、先手が行って
// 戻る手を挟んで「何手か進んだが順番が多すぎる」になるが、それも説明が付かない側に数える。
func TestFollowAutoNoticesFlippedBoardWithRealConfidence(t *testing.T) {
	s, _ := following(t)
	flipped, err := position.FromSFEN(boardAfter(t, "7g7f"))
	if err != nil {
		t.Fatalf("FromSFEN: %v", err)
	}
	board := flipped.Rotate180().SFEN()
	a, err := s.FollowAuto(board, realConf(board, 0.9), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Applied {
		t.Fatalf("上下逆なのに手を足しました: %+v", a.Moves)
	}
	if !a.Flipped || a.FlipMove == "" {
		t.Fatalf("目線が逆だと気づいていません: kind=%q reason=%q flipMove=%q", a.Kind, a.Reason, a.FlipMove)
	}
}

// ⚠️ **繋がっているときに「逆かも」と言わないこと。**
//
// 正しく追えているのに疑いを出すと、**本当に逆のときに信じてもらえない。**
func TestFollowAutoDoesNotCryFlipWhenFine(t *testing.T) {
	s, _ := following(t)

	a, err := followAuto(t, s, boardAfter(t, "7g7f"), evenConf(0.9))
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Applied {
		t.Fatalf("進んでいません: %+v", a)
	}
	if a.Flipped {
		t.Error("正しく繋がっているのに「目線が逆」と言いました")
	}
}

// realConf は実機に近い確信度の表を作る（駒のマスは v、空きは 0 —— suteme は空きを確信度 0 で返す）。
func realConf(board string, v float64) []float64 {
	out := make([]float64, 0, 81)
	for _, row := range strings.Split(board, "/") {
		for _, ch := range row {
			switch {
			case ch >= '1' && ch <= '9':
				for i := 0; i < int(ch-'0'); i++ {
					out = append(out, 0)
				}
			case ch == '+':
			default:
				out = append(out, v)
			}
		}
	}
	return out
}

// ⚠️ **指して 1 秒で取り返された 1 枚を「変わっていない」で見送らないこと**（2026-10-06）。
//
// **実機の症状**: ゲーム画面で ▲2四歩 △同歩 が 1 秒のあいだに指され、撮った 1 枚は 2 手先だった。
// 歩が 1 マスずれただけに見えるので、1 手の候補より「何も指していない」のほうがよく合い
// （食い違いは 2五・2三 が空いたのと 2四 に後手の歩が来たこと）、`Rank` の 1 位が
// 「何も指していない」になって、2 手先を探しに行かずに見送り続けた。
// **駒が来たマスがあるなら、何かは指されている。**
func TestFollowAutoCatchesExchangeInOneFrame(t *testing.T) {
	s, _ := following(t)
	opening := []string{"2g2f", "8c8d", "2f2e", "8d8e"}
	if _, err := s.FollowApply(opening, s.State().Rev, false); err != nil {
		t.Fatalf("FollowApply: %v", err)
	}
	board := boardAfter(t, append(opening, "2e2d", "2c2d")...)

	a, err := followAuto(t, s, board, realConf(board, 0.9))
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Applied || len(a.Moves) != 2 || a.Moves[0] != "2e2d" || a.Moves[1] != "2c2d" {
		t.Fatalf("▲2四歩 △同歩 を足していません: kind=%q reason=%q moves=%v", a.Kind, a.Reason, a.Moves)
	}
}

// ⚠️ **取り合いで同じ側の駒に入れ替わった 1 枚も見送らないこと**（2026-10-06）。
//
// **実機の症状**: ▲2四歩 のあと △同歩 ▲同飛 が続けて指され、撮った 1 枚では 2四 が
// 先手の歩から先手の飛車に変わっただけ（先後は同じ）。残りは 2三・2八 が空いたことだけなので
// 「駒が来た」に当たらず、「変わっていない」で見送り続けた。
// **駒の種類が確かに変わり、しかも空いたマスがある**なら、何かは指されている。
func TestFollowAutoCatchesRecaptureBySameSide(t *testing.T) {
	s, _ := following(t)
	opening := []string{"2g2f", "8c8d", "2f2e", "8d8e", "2e2d"}
	if _, err := s.FollowApply(opening, s.State().Rev, false); err != nil {
		t.Fatalf("FollowApply: %v", err)
	}
	board := boardAfter(t, append(opening, "2c2d", "2h2d")...)

	a, err := followAuto(t, s, board, realConf(board, 0.9))
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Applied || len(a.Moves) != 2 || a.Moves[0] != "2c2d" || a.Moves[1] != "2h2d" {
		t.Fatalf("△同歩 ▲同飛 を足していません: kind=%q reason=%q moves=%v", a.Kind, a.Reason, a.Moves)
	}
}

// ⚠️ **駒の種類を読み違えただけの 1 枚では、先を探しに行かないこと**（2026-10-06）。
//
// 中継は種類を外しやすい（銀を香と読む）。空いたマスが無いなら指された手は無いので、
// 毎周深い探索を回して遅くしない（足さないのは今までどおり）。
func TestFollowAutoKindMisreadStaysSame(t *testing.T) {
	s, _ := following(t)
	// 3九 の銀を香と読んだ 1 枚。
	const misread = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGLNL"
	a, err := s.FollowAuto(misread, realConf(misread, 0.9), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Applied || a.Kind != FollowSame {
		t.Fatalf("種類の読み違いだけで手を足したか、見送りの形が変わりました: %+v", a)
	}
}

// ⚠️ **今の向きで「変わっていない」とぴったり合っているなら、「逆かも」と言わないこと**（2026-10-06）。
//
// **実機の症状**: ゲーム画面で後手を持ち、相手の ▲6八玉 のあとで目線を後手にして採った。
// 正しく追えていて「変わっていない」（一致度 1）なのに、盤を 180 度回すと玉の 2 マスを
// 直せば △4二玉 で説明が付く（一致度 0.975）ので、「目線を後手が手前にして」と言われて止まった。
// 逆向きの説明は、**今の向きより良く合うときだけ**採る。
func TestFollowAutoDoesNotCryFlipWhenSameFits(t *testing.T) {
	pos := NewPositionService()
	board := boardAfter(t, "5i6h")
	if _, err := pos.Load(board, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := pos.SetTurn(2); err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	s := NewStudyService(pos)
	if _, err := s.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	a, err := s.FollowAuto(board, evenConf(0.9), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Applied || a.Kind != FollowSame {
		t.Fatalf("前提: 何も指していない 1 枚は「変わっていない」のはず: %+v", a)
	}
	if a.Flipped {
		t.Fatalf("今の向きでぴったり合っているのに「目線が逆」と言いました（%s）", a.FlipMove)
	}
}

// ⚠️ **1 枚だけでは足さず、2 枚続けて同じ答えで足すこと**（2026-10-05。`followPending`）。
//
// **実機の症状**: 指している手や頭が盤に映った 1 枚から、指していない手を足した
// （▲4八飛・△6一玉・△4一歩打）。どれもその 1 枚だけの読み違いで、次の 1 枚では消えていた。
func TestFollowAutoWaitsForSecondFrame(t *testing.T) {
	s, _ := following(t)
	board := boardAfter(t, "7g7f")

	a, err := s.FollowAuto(board, evenConf(0.9), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Applied || !a.Pending {
		t.Fatalf("1 枚目で足しました（あるいは控えていません）: %+v", a)
	}
	if n := len(s.State().Nodes); n != 0 {
		t.Fatalf("手順 = %d, want 0（まだ足さない）", n)
	}

	a, err = s.FollowAuto(board, evenConf(0.9), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Applied || a.Pending || len(a.Moves) != 1 || a.Moves[0] != "7g7f" {
		t.Fatalf("2 枚続けて同じ答えなのに足していません: %+v", a)
	}
}

// ⚠️ **2 枚目が先へ進んでいたら、2 枚で揃った手だけ足すこと**（2026-10-06）。
//
// **実機の症状**: 5 秒おきの指し手が 2〜3 手続くと見失った。完全に同じ答えを 2 枚に
// 求めていたので、読んでいるあいだに次の手が来るたびに控え直しになり、1 手も足せないまま
// 遅れが `Connect` の 4 手を超えた。⚠️ **2 枚目にしか無い手はまだ足さない**（次の 1 枚で確かめる）。
func TestFollowAutoConfirmsPrefixWhileMovesKeepComing(t *testing.T) {
	s, _ := following(t)
	line := []string{"7g7f", "3c3d", "6g6f"}
	// ⚠️ **確信度を渡さないこと**（厳密一致にする）。渡すと 2 枚目で `Rank` が 1 手目だけを
	// 修復つきで決めてしまい、2 枚目の答えが「先へ進んだ手順」にならない（見たい形にならない）。

	if a, _ := s.FollowAuto(boardAfter(t, line[:1]...), nil, nil); !a.Pending || a.Applied {
		t.Fatalf("前提: 1 枚目は控えるはず: %+v", a)
	}
	for i := 2; i <= len(line); i++ {
		a, err := s.FollowAuto(boardAfter(t, line[:i]...), nil, nil)
		if err != nil {
			t.Fatalf("%d 枚目: %v", i, err)
		}
		if !a.Applied || !a.Pending || len(a.Moves) != 1 || a.Moves[0] != line[i-2] {
			t.Fatalf("%d 枚目: 揃った %s だけ足して残りを控えるはず: %+v", i, line[i-2], a)
		}
		if n := len(s.State().Nodes); n != i-1 {
			t.Fatalf("%d 枚目: 手順 = %d, want %d（2 枚目にしか無い手を足した）", i, n, i-1)
		}
	}
	// 盤が止まれば、控えた最後の手も入る。
	a, err := s.FollowConfirm()
	if err != nil {
		t.Fatalf("FollowConfirm: %v", err)
	}
	if !a.Applied || len(a.Moves) != 1 || a.Moves[0] != line[2] {
		t.Fatalf("止まった 1 枚で控えた手を足していません: %+v", a)
	}
}

// ⚠️ **間に「足せる手が無い」1 枚が挟まったら、控えは捨てること。**
//
// 手が映った 1 枚の答えは、次の 1 枚で消えるのが普通（それがこの形）。
// 捨てないと、離れた 2 枚の読み違いが「続けて同じ答え」に数えられる。
func TestFollowAutoDropsTransientFrame(t *testing.T) {
	s, _ := following(t)
	moved := boardAfter(t, "7g7f")

	if a, _ := s.FollowAuto(moved, evenConf(0.9), nil); !a.Pending {
		t.Fatalf("前提: 1 枚目は控えるはず: %+v", a)
	}
	// 手がどいて、何も指していない盤に戻った。
	if a, err := s.FollowAuto(hirateBoard, evenConf(0.9), nil); err != nil || a.Applied || a.Pending {
		t.Fatalf("何も指していない盤で足すか控えました: %+v（%v）", a, err)
	}
	// もう一度同じ盤が来ても、**控えは捨ててあるので 1 枚目の扱い**になること。
	a, err := s.FollowAuto(moved, evenConf(0.9), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Applied || !a.Pending {
		t.Fatalf("捨てたはずの控えで足しました: %+v", a)
	}
}

// ⚠️ **行き先の駒が「読める・読めない」を繰り返しても、控えを捨てないこと**（2026-10-06）。
//
// **実機の症状**: ゲーム画面で △1五歩 の歩が、明るさの揺れで読める 1 枚と読めない 1 枚が交互に来て、
// 読めない 1 枚のたびに控えを捨て、2 回続けて読めるまで 90 秒入らなかった。
// 読めない 1 枚は「元のマスが空いて、行き先も空き」なので、何も指していないとも、控えた手とも
// 同じだけ食い違う —— 控えを否定していない。
func TestFollowAutoKeepsPendingThroughUnclearFrame(t *testing.T) {
	s, _ := following(t)
	moved := boardAfter(t, "7g7f")
	// 7七 は空いたが、7六 の歩が読めなかった 1 枚。
	const unclear = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PP1PPPPPP/1B5R1/LNSGKGSNL"

	if a, _ := s.FollowAuto(moved, realConf(moved, 0.9), nil); !a.Pending {
		t.Fatalf("前提: 1 枚目は控えるはず: %+v", a)
	}
	a, err := s.FollowAuto(unclear, realConf(unclear, 0.9), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Applied || !a.Pending {
		t.Fatalf("矛盾しない 1 枚で控えを捨てたか、足しました: %+v", a)
	}
	a, err = s.FollowAuto(moved, realConf(moved, 0.9), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Applied || len(a.Moves) != 1 || a.Moves[0] != "7g7f" {
		t.Fatalf("控えた手と同じ答えがもう一度来たのに足していません: %+v", a)
	}
}

// ⚠️ **撮り直した 1 枚が変わっていなければ、それで確かめたことにする**（`FollowConfirm`）。
//
// 変わっていない画像を読み直しても同じ答えが返るだけなので、読み直さずに足す
// （確かめが 1 周で済む）。⚠️ **控えが無いとき・本譜の先端が動いたときは何もしない。**
func TestFollowConfirm(t *testing.T) {
	s, _ := following(t)

	if a, err := s.FollowConfirm(); err != nil || a.Applied || a.Kind != FollowSame {
		t.Fatalf("控えが無いのに足しました: %+v（%v）", a, err)
	}

	if a, _ := s.FollowAuto(boardAfter(t, "7g7f"), evenConf(0.9), nil); !a.Pending {
		t.Fatalf("前提: 1 枚目は控えるはず: %+v", a)
	}
	a, err := s.FollowConfirm()
	if err != nil {
		t.Fatalf("FollowConfirm: %v", err)
	}
	if !a.Applied || len(a.Moves) != 1 || a.Moves[0] != "7g7f" {
		t.Fatalf("変わっていない 1 枚で確かめたのに足していません: %+v", a)
	}
	// 控えは使い切ること（もう一度呼んでも足さない）。
	if a, _ := s.FollowConfirm(); a.Applied {
		t.Fatalf("同じ控えで 2 回足しました: %+v", a)
	}

	// 本譜の先端が動いたら、控えは捨てる（違う局面の先に繋がないため）。
	if a, _ := s.FollowAuto(boardAfter(t, "7g7f", "3c3d"), evenConf(0.9), nil); !a.Pending {
		t.Fatalf("前提: 1 枚目は控えるはず: %+v", a)
	}
	if _, err := s.FollowApply([]string{"8c8d"}, s.State().Rev, false); err != nil {
		t.Fatalf("FollowApply: %v", err)
	}
	if a, _ := s.FollowConfirm(); a.Applied {
		t.Fatalf("先端が動いたのに控えで足しました: %+v", a)
	}
}

// cellIdx は USI のマス（"7g" など）を、撮った画像の向きの 81 マスの番号（行優先）にする。
func cellIdx(sq ...string) []int {
	out := make([]int, 0, len(sq))
	for _, s := range sq {
		out = append(out, int(s[1]-'a')*9+(9-int(s[0]-'0')))
	}
	return out
}

// ⚠️ **速い経路（変わったマスだけで割り出す）も、1 枚だけでは足さないこと**（2026-10-07）。
// 変わったマスが 1 通りの手に決まったら控え、次の 1 枚が変わっていなければ（`FollowConfirm`）足す。
// 足したら**この 1 枚は先端そのもの**なので `AtFrame` を立てる（次の比べる相手になる）。
func TestFollowCellsPendsThenConfirms(t *testing.T) {
	s, _ := following(t)
	a, err := s.FollowCells(cellIdx("7g", "7f"))
	if err != nil {
		t.Fatalf("FollowCells: %v", err)
	}
	if a.Applied || !a.Pending {
		t.Fatalf("1 枚目で足したか、控えていません: %+v", a)
	}
	a, err = s.FollowConfirm()
	if err != nil {
		t.Fatalf("FollowConfirm: %v", err)
	}
	if !a.Applied || len(a.Moves) != 1 || a.Moves[0] != "7g7f" || !a.AtFrame {
		t.Fatalf("変わっていない 1 枚で確かめたのに足していないか、AtFrame が立っていません: %+v", a)
	}
}

// ⚠️ **決まらなければ何もしないこと**（フロントが 81 マスを読み直す合図）。
func TestFollowCellsFallsBackWhenUnclear(t *testing.T) {
	s, _ := following(t)
	a, err := s.FollowCells(cellIdx("5e", "4e", "5f", "4f")) // 手が被ったような 1 枚
	if err != nil {
		t.Fatalf("FollowCells: %v", err)
	}
	if a.Applied || a.Pending {
		t.Fatalf("合う手が無いのに足すか控えました: %+v", a)
	}
}

// ⚠️ **後手目線で採った局面では、撮った向きのマスを回してから割り出すこと。**
func TestFollowCellsRotatesForGoteView(t *testing.T) {
	pos := NewPositionService()
	if _, err := pos.Load(hirateBoard, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := pos.SetViewpoint(true); err != nil {
		t.Fatalf("SetViewpoint: %v", err)
	}
	if _, err := pos.SetTurn(1); err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	s := NewStudyService(pos)
	if _, err := s.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	// 画面では上下逆なので、▲7六歩 は 3三→3四 が変わったように見える。
	if a, err := s.FollowCells(cellIdx("3c", "3d")); err != nil || !a.Pending {
		t.Fatalf("FollowCells: %+v（%v）", a, err)
	}
	a, err := s.FollowConfirm()
	if err != nil || !a.Applied || a.Moves[0] != "7g7f" {
		t.Fatalf("▲7六歩 になっていません: %+v（%v）", a, err)
	}
}

// ⚠️ **ぴったり「変わっていない」1 枚だけ AtFrame を立てること**（読めていないマスがある 1 枚は立てない）。
func TestFollowAutoAtFrameOnlyWhenExact(t *testing.T) {
	s, _ := following(t)
	a, err := s.FollowAuto(hirateBoard, realConf(hirateBoard, 0.9), nil)
	if err != nil || a.Kind != FollowSame || !a.AtFrame {
		t.Fatalf("ぴったり合う 1 枚で AtFrame が立っていません: %+v（%v）", a, err)
	}
	// 7七 の歩が読めなかった 1 枚（行き先の読めない手の名残など）。
	const missing = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PP1PPPPPP/1B5R1/LNSGKGSNL"
	a, err = s.FollowAuto(missing, realConf(missing, 0.9), nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.AtFrame {
		t.Fatalf("読めていないマスがあるのに AtFrame が立ちました: %+v", a)
	}
}

// ⚠️ **駒の種類の読み違いだけなら AtFrame を立てること**（2026-10-07。成った駒を少し読み違える
// ゲーム画面で、比べる相手になれず速い経路が止まった）。
func TestFollowAutoAtFrameDespiteKindMisread(t *testing.T) {
	s, _ := following(t)
	// 3九 の銀を香と読んだ 1 枚（駒の有無と先後は合っている）。
	const misread = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGLNL"
	a, err := s.FollowAuto(misread, realConf(misread, 0.9), nil)
	if err != nil || a.Kind != FollowSame || !a.AtFrame {
		t.Fatalf("種類の読み違いだけなのに AtFrame が立っていません: %+v（%v）", a, err)
	}
}

// ⚠️ **何かが被って見えないマスがある 1 枚は、ぴったり合っても AtFrame を立てないこと**（2026-10-07。
// ShogiHome のダイアログが被った 1 枚が比べる相手になり、閉じたあと速い経路が二度と使えなくなった）。
func TestFollowAutoNoAtFrameWhenCovered(t *testing.T) {
	s, _ := following(t)
	hidden := make([]bool, 81)
	for i := 0; i < 81; i += 4 {
		hidden[i] = true
	}
	a, err := s.FollowAuto(hirateBoard, realConf(hirateBoard, 0.9), hidden)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.AtFrame {
		t.Fatalf("見えないマスがあるのに AtFrame が立ちました: %+v", a)
	}
}

// ⚠️ **本譜の先端が比べる相手より先へ進んでいても、速い経路で残りの手を割り出すこと**（2026-10-07）。
//
// **実機の症状**: 81 マスの読みで手を足すと先端だけが進み、比べる相手は古いまま。変わったマスには
// 足し済みの手のマスも入るので、先端から突き合わせて「合う手順 0」が続き、速い経路がほとんど効かなかった。
func TestFollowCellsFromBaseWhenTipIsAhead(t *testing.T) {
	s, _ := following(t)
	// 平手の 1 枚が先端とぴったり合った ＝ 比べる相手になる。
	if a, err := s.FollowAuto(hirateBoard, realConf(hirateBoard, 0.9), nil); err != nil || !a.AtFrame {
		t.Fatalf("前提: 平手の 1 枚で AtFrame が立つはず: %+v（%v）", a, err)
	}
	// 81 マスの読みで ▲7六歩 を足した（比べる相手は平手の 1 枚のまま）。
	if _, err := s.FollowApply([]string{"7g7f"}, s.State().Rev, false); err != nil {
		t.Fatalf("FollowApply: %v", err)
	}
	// 平手の 1 枚から見ると、▲7六歩 △3四歩 の 4 マスが変わっている。
	if a, err := s.FollowCells(cellIdx("7g", "7f", "3c", "3d")); err != nil || !a.Pending {
		t.Fatalf("残りの △3四歩 を控えていません: %+v（%v）", a, err)
	}
	a, err := s.FollowConfirm()
	if err != nil || !a.Applied || len(a.Moves) != 1 || a.Moves[0] != "3c3d" {
		t.Fatalf("△3四歩 を足していません: %+v（%v）", a, err)
	}
}

// ⚠️ **足した手で変わったマスの説明が付くなら、比べる相手を今の 1 枚に替えること**（`FrameFits`）。
// ⚠️ **説明が付かない変化があれば替えないこと**（読めていない手が残っている 1 枚を相手にしない）。
func TestFrameFits(t *testing.T) {
	s, _ := following(t)
	if a, _ := s.FollowAuto(hirateBoard, realConf(hirateBoard, 0.9), nil); !a.AtFrame {
		t.Fatalf("前提: 平手の 1 枚で AtFrame が立つはず: %+v", a)
	}
	if _, err := s.FollowApply([]string{"7g7f"}, s.State().Rev, false); err != nil {
		t.Fatalf("FollowApply: %v", err)
	}
	if s.FrameFits(cellIdx("7g", "7f", "3c")) {
		t.Fatal("足していない変化（3三）があるのに、比べる相手を替えました")
	}
	if !s.FrameFits(cellIdx("7g", "7f")) {
		t.Fatal("▲7六歩 で説明が付くのに、比べる相手を替えませんでした")
	}
	// 替えたあとは、▲7六歩 のあとの局面から割り出す。
	if a, err := s.FollowCells(cellIdx("3c", "3d")); err != nil || !a.Pending {
		t.Fatalf("新しい比べる相手から △3四歩 を控えていません: %+v（%v）", a, err)
	}
}
