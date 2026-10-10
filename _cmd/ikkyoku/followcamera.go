package main

import (
	ikkyokuapp "github.com/ShinteLab/ikkyoku/app"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// followCamera は `CaptureService` を**中継セッションの撮る側**（`app.FollowCamera`）として包む（2026-10-11）。
//
// 中継を追う 1 周の段取りは `app` の `StudyService`（`followsession.go`）が回し、撮る・ふるう・読む・録画は
// ここを通して `CaptureService` に頼む。⚠️ **ここに判断を書かないこと**（`CaptureResult` を写すだけ）。
type followCamera struct{ s *CaptureService }

func (c followCamera) AnchorBoard() (ikkyokuapp.FollowShot, error) {
	r, err := c.s.AnchorBoard()
	return followShotOf(r), err
}

func (c followCamera) CaptureQuiet() (ikkyokuapp.FollowShot, error) {
	r, err := c.s.CaptureQuiet()
	return followShotOf(r), err
}

func (c followCamera) RecognizeQuiet() (ikkyokuapp.FollowShot, error) {
	r, err := c.s.RecognizeQuiet()
	return followShotOf(r), err
}

func (c followCamera) ReadCells(cells []int) ([]ikkyokuapp.CellPiece, error) {
	return c.s.ReadCells(cells)
}
func (c followCamera) ComparePieces(pairs []ikkyokuapp.CellPair) []float64 {
	return c.s.ComparePieces(pairs)
}
func (c followCamera) CellsUnmoved()  { c.s.CellsUnmoved() }
func (c followCamera) CellsUnclear()  { c.s.CellsUnclear() }
func (c followCamera) ForgetUnmoved() { c.s.ForgetUnmoved() }
func (c followCamera) SetCellBase()   { c.s.SetCellBase() }
func (c followCamera) SaveFollowFrame(number int, moves []string, guess bool) {
	c.s.SaveFollowFrame(number, moves, guess)
}
func (c followCamera) SaveFollowMiss(number int, kind string) { c.s.SaveFollowMiss(number, kind) }
func (c followCamera) SetFollowing(on bool)                   { c.s.SetFollowing(on) }
func (c followCamera) ClearBoardAnchor()                      { c.s.ClearBoardAnchor() }

// followShotOf は撮った結果のうち、中継の追従が使うものを写す。
// ⚠️ **`Cells` の nil（測っていない）と空（何も変わっていない）を潰さないこと**（`FrameFits` は測った 1 枚なら空でも呼ぶ）。
func followShotOf(r CaptureResult) ikkyokuapp.FollowShot {
	return ikkyokuapp.FollowShot{
		Skipped: r.Skipped, Fast: r.Fast, Cells: r.Cells, SFEN: r.SFEN,
		OffBoard: r.OffBoard, OffBoardReason: r.OffBoardReason,
		CellConfidence: r.CellConfidence, CellHidden: recognize.CellHidden(r.Debug),
	}
}
