package app

// 中継の追従を `StudyService` から分けた型（`follower`）と、追従が手の木に触る口（`followHost`）。2026-11-13。
//
// **追従の段取りと判断は `follower` のメソッド**で、`StudyService` は `follower` を 1 つ持ち（`StudyService.follow`）、
// 公開メソッドは同じ名前のまま薄い取り次ぎとして残す。分けた理由は 2 つ —— 追従を `StudyService` を丸ごと作らずに
// 試せること（偽の `followHost` で足りる）と、追従と手の木の線をコードに引くこと（`TODO.md` 4 の ①）。
// 理由の全文は `_docs/design-follow.md` の「追従を別の型に分ける」。
//
// ⚠️ **口を広げないこと**（今は 10）。追従が手の木を書き換えるのは「足す」「分岐にする」「先端へ移る」の 3 つだけで、
// 読むのも読み取り専用の面（`followTree`）だけ。足したくなったら、それが追従の仕事かを先に疑う。
// ⚠️ **ロックは今までどおり 2 つ**（ループの状態は `follower.mu`、答えの控えと手の木は `StudyService.mu` ＝ `follower.smu`）。

import (
	"github.com/ShinteLab/ikkyoku/position"
)

// followTree は追従が**読む**手の木（読み取り専用の面）。実体は `*position.Study`。
type followTree interface {
	Root() *position.Position
	MainLine() []string
	MainGuessTail() []int
	MainTip() (int, *position.Position, error)
	CurrentID() int
}

// followHost は追従が解析タブ（手の木・版・イベント・撮る側）に触る口。実体は `StudyService`。
//
// ⚠️ **`publish` 以外は `StudyService.mu`（`follower.smu`）を持って呼ぶこと。** `publish` はロックの外
// （`study:changed` の約束。`app/AGENTS.md`）。
type followHost interface {
	// hostTree は手の木（まだ局面が無ければ nil）。
	hostTree() followTree
	// hostRev は局面の版（下見と据えるのあいだに局面が変わっていないかの合鍵）。
	hostRev() int
	// hostGraft は本譜を line に据え直す。guess は後ろの何手に推測の印を付けるか（0 なら印を付けない）。
	hostGraft(line []string, guess int) position.GraftResult
	// hostBranch は節点 id から先を枝にする（推測で足した末尾を取り消す）。
	hostBranch(id int) error
	// hostGoTip は本譜の先端へ移る（組み立てられなければ何もしない）。
	hostGoTip()
	// state は今の状態（版は進めない）。changed は版を進めて今の状態を返す。publish は `study:changed` を流す。
	state() StudyState
	changed() StudyState
	publish(st StudyState, err error)
	// hostEmit は追従の様子（`follow:update`）を流す（流す先が無ければ何もしない）。
	hostEmit(u FollowUpdate)
	// hostCamera は撮る側（無ければ nil）。
	hostCamera() FollowCamera
}

// tree は手の木（まだ局面が無ければ nil）。`smu` を持って呼ぶこと。
func (f *follower) tree() followTree { return f.host.hostTree() }

// State は今の状態（`StudyService.State` と同じ。`smu` を持たずに呼ぶ）。
func (f *follower) State() StudyState {
	f.smu.Lock()
	defer f.smu.Unlock()
	return f.host.state()
}

// initFollower は追従の持ち物を StudyService に繋ぐ（`NewStudyService` から 1 度だけ）。
func (s *StudyService) initFollower() {
	s.follow.host = s
	s.follow.smu = &s.mu
	s.follow.src = s.src
}

var _ followHost = (*StudyService)(nil)

func (s *StudyService) hostTree() followTree {
	// ⚠️ **nil のポインタを interface に入れないこと**（`tree() == nil` が偽になる）。
	if s.study == nil {
		return nil
	}
	return s.study
}

func (s *StudyService) hostRev() int { return s.rev }

func (s *StudyService) hostGraft(line []string, guess int) position.GraftResult {
	if guess > 0 {
		return s.study.GraftGuess(line, guess)
	}
	return s.study.Graft(line)
}

func (s *StudyService) hostBranch(id int) error { return s.study.Branch(id) }

func (s *StudyService) hostGoTip() {
	if id, _, err := s.study.MainTip(); err == nil {
		_ = s.study.GoTo(id)
	}
}

func (s *StudyService) hostEmit(u FollowUpdate) {
	if s.Emit != nil {
		s.Emit("follow:update", u)
	}
}

func (s *StudyService) hostCamera() FollowCamera { return s.Camera }
