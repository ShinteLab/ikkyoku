package app

import "github.com/ShinteLab/ikkyoku/position"

// テストが StudyService から直に呼んでいる追従の非公開メソッドの取り次ぎ（2026-11-13 に follower へ移したもの）。
// ⚠️ **本体のコードには置かない**（StudyService が追従の中身を呼ぶ口を増やさない）。

func (s *StudyService) followApply(moves []string, rev int, goTo bool) (a FollowApplied, err error) {
	return s.follow.followApply(moves, rev, goTo)
}

func (s *StudyService) followBaseFrom() (base *position.Position, since, line []string, ok bool) {
	return s.follow.followBaseFrom()
}

func (s *StudyService) followProbe(board *position.Board, cost *position.CellCost, unseen *position.CellMask, fromImage, rotated bool) (FollowProbe, error) {
	return s.follow.followProbe(board, cost, unseen, fromImage, rotated)
}

func (s *StudyService) followRewindProbe(board *position.Board, cost *position.CellCost, p FollowProbe) *followRewind {
	return s.follow.followRewindProbe(board, cost, p)
}

func (s *StudyService) mainTip() int { return s.follow.mainTip() }

func (s *StudyService) markBase() { s.follow.markBase() }

func (s *StudyService) pendingMoves() []string { return s.follow.pendingMoves() }

func (s *StudyService) setPending(p *followPending) { s.follow.setPending(p) }
