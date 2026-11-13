package app

import (
	"slices"
	"sync"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// fakeHost は追従の口（`followHost`）の偽物。手の木は本物の `position.Study` で、版・イベントは数えるだけ。
//
// **これが follower を分けた目的**（2026-11-13）: 追従を StudyService（評価値・控え・棋譜の取得）を作らずに試せること。
type fakeHost struct {
	study     *position.Study
	rev       int
	published []StudyState
	emits     []FollowUpdate
}

func (h *fakeHost) hostTree() followTree {
	if h.study == nil {
		return nil
	}
	return h.study
}
func (h *fakeHost) hostRev() int { return h.rev }
func (h *fakeHost) hostGraft(line []string, guess int) position.GraftResult {
	if guess > 0 {
		return h.study.GraftGuess(line, guess)
	}
	return h.study.Graft(line)
}
func (h *fakeHost) hostBranch(id int) error { return h.study.Branch(id) }
func (h *fakeHost) hostGoTip() {
	if id, _, err := h.study.MainTip(); err == nil {
		_ = h.study.GoTo(id)
	}
}
func (h *fakeHost) state() StudyState { return StudyState{Loaded: h.study != nil, Rev: h.rev} }
func (h *fakeHost) changed() StudyState {
	h.rev++
	return h.state()
}
func (h *fakeHost) publish(st StudyState, err error) {
	if err == nil {
		h.published = append(h.published, st)
	}
}
func (h *fakeHost) hostEmit(u FollowUpdate)  { h.emits = append(h.emits, u) }
func (h *fakeHost) hostCamera() FollowCamera { return nil }

// newFakeFollower は平手の初期局面を手の木にした follower を作る（StudyService を作らない）。
func newFakeFollower(t *testing.T) (*follower, *fakeHost) {
	t.Helper()
	p, err := position.FromFullSFEN(hirateBoard + " b - 1")
	if err != nil {
		t.Fatalf("FromFullSFEN: %v", err)
	}
	h := &fakeHost{study: position.NewStudy(p)}
	return &follower{host: h, smu: &sync.Mutex{}, src: NewPositionService()}, h
}

// **偽の口だけで追従が手を足せること**（2026-11-13。follower を StudyService から分けた歯止め）。
// 1 手進んだ盤を 2 枚続けて見せると、本譜にその手が推測の印つきで足され、版が 1 つ進み、`study:changed` が 1 回流れ、
// 先端を見ていたので先端へ移る。⚠️ **ここが通らなくなったら、follower が口の外（StudyService）に頼り始めている。**
func TestFollowerAddsMoveThroughHost(t *testing.T) {
	f, h := newFakeFollower(t)
	board := boardAfter(t, "7g7f")
	a, err := f.FollowAuto(board, nil, nil, nil)
	if err == nil && a.Pending {
		a, err = f.FollowAuto(board, nil, nil, nil)
	}
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Applied || a.Added != 1 {
		t.Fatalf("足していません: %+v", a)
	}
	if got := h.study.MainLine(); !slices.Equal(got, []string{"7g7f"}) {
		t.Fatalf("本譜 = %v、want [7g7f]", got)
	}
	if tail := h.study.MainGuessTail(); len(tail) != 1 {
		t.Errorf("推測の印が付いていません: %v", tail)
	}
	if h.rev != 1 || len(h.published) != 1 {
		t.Errorf("版 %d・study:changed %d 回、want 1・1", h.rev, len(h.published))
	}
	if id, _, _ := h.study.MainTip(); h.study.CurrentID() != id {
		t.Errorf("先端へ移っていません: cur=%d tip=%d", h.study.CurrentID(), id)
	}
}

// ⚠️ **下見のあとに局面が変わっていたら足さないこと**（版が合鍵。`followGraft`）。足さず・版も進めず・`study:changed` も流さない。
func TestFollowerRejectsStaleRev(t *testing.T) {
	f, h := newFakeFollower(t)
	h.rev = 5
	if _, err := f.followApply([]string{"7g7f"}, 4, true); err == nil {
		t.Fatal("版が違うのに足しました")
	}
	if len(h.study.MainLine()) != 0 || h.rev != 5 || len(h.published) != 0 {
		t.Errorf("断ったのに変わっています: 本譜 %v・版 %d・study:changed %d 回", h.study.MainLine(), h.rev, len(h.published))
	}
}
