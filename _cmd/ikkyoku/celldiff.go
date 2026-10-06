package main

import (
	"image"
	"strings"

	"github.com/ShinteLab/ikkyoku"
	ikkyokuapp "github.com/ShinteLab/ikkyoku/app"
	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// 追従の**速い経路**の撮る側（2026-10-07）。
//
// 81 マスの推論（2.1 秒）を回さず、**本譜の先端とぴったり合っていた 1 枚**（`cellBase`）と
// 今の 1 枚を**マスごとに**比べて（`ikkyoku.CellDiff`。数ミリ秒）、変わったマスの番号だけを返す。
// どの手かを決めるのは解析タブ側（`StudyService.FollowCells`）で、決まらなければフロントが
// 同じ 1 枚を 81 マスで読む（`RecognizeQuiet`）。理由は `_docs/design-follow.md` の「速い経路」。
//
// ⚠️ **使ってよいのは、ふるいが「変化して止まった」と言った 1 枚だけ**（`gateReadSettled`）。
// ⚠️ **比べる相手は解析タブが「先端と合っている」と言った 1 枚だけ**（`SetCellBase`）。
// 前に読んだ 1 枚と比べると、それが手の被った 1 枚だったとき残像が混ざる（2026-10-06 に実測）。
// ⚠️ **変わったマスが多ければ 81 マスを読む**（`cellFastMax`。手や頭が被った 1 枚）。
const (
	// cellChanged は「変わったマス」と見なす、マスの内側で変わった画素の割合。
	// 録画の実測（2026-10-06）で、指した手のマスは最小 0.72、ほかのマスは最大 0.077 だった。
	cellChanged = 0.3
	// cellFastMax は速い経路で扱う変わったマスの数の上限（2 手で 4 マス + 前の手の色付け 2 マス + 余裕）。
	cellFastMax = 8

	// cellSkipSame は**比べる相手から、どのマスも変わっていない**（`CaptureResult.Skipped`）。
	// ⚠️ **`unchanged`（前の 1 枚と同じ）とは別物。** フロントは `unchanged` で控えた手を確かめる
	// （`FollowConfirm`）ので、混ぜると「先端に戻った」1 枚で控えた手を足してしまう。
	cellSkipSame = "same"
)

// SetCellBase は**直前に撮った 1 枚を、速い経路の比べる相手にする**（2026-10-07）。
//
// 呼ぶのはフロントで、解析タブ側が `FollowAuto.AtFrame`（本譜の先端がこの 1 枚とぴったり合う）を
// 返したときだけ。⚠️ **ここで「合っているか」を判断しないこと**（局面を知らない層なので）。
func (s *CaptureService) SetCellBase() {
	s.mu.Lock()
	s.cellBase = s.lastQuiet
	s.mu.Unlock()
}

// ReadCells は**直前に撮った 1 枚の、指定したマスの駒だけを読む**（2026-10-07）。
//
// 速い経路で成・不成や打った駒の種類が分かれたとき、フロントが `FollowAuto.ReadCells` を渡して呼ぶ。
// 81 マスを読み直す（2 秒以上。CPU が混んでいると 7〜10 秒）代わりに、違いの出るマスだけを読む。
// ⚠️ **撮り直さない**（変わったマスを測った 1 枚と同じ 1 枚を読む）。
func (s *CaptureService) ReadCells(cells []int) ([]ikkyokuapp.CellPiece, error) {
	s.mu.Lock()
	img, rects, region := s.lastQuiet, s.cellRects, s.cellRegion
	s.mu.Unlock()
	pieces, err := recognize.ReadCells(img, region, rects, cells)
	if err != nil {
		return nil, err
	}
	out := make([]ikkyokuapp.CellPiece, len(cells))
	names := make([]string, len(cells))
	for i, c := range cells {
		out[i] = ikkyokuapp.CellPiece{Cell: c, Piece: pieces[i]}
		names[i] = cellName(c) + "=" + pieces[i]
	}
	log.Info("違いの出るマスを読みました（追跡）", "マス", strings.Join(names, ","))
	return out, nil
}

// noteCellRects は 81 マスを読んだときのマス割りを控える（速い経路はこれでマスを切る）。
func (s *CaptureService) noteCellRects(img image.Image, d *recognize.Debug) {
	if d == nil || len(d.Cells) != 81 {
		return
	}
	rects := make([]image.Rectangle, 81)
	for _, c := range d.Cells {
		if c.Row < 0 || c.Row > 8 || c.Col < 0 || c.Col > 8 {
			return
		}
		rects[c.Row*9+c.Col] = c.Rect
	}
	s.mu.Lock()
	s.cellRects, s.cellRegion, s.cellBounds = rects, d.Region, img.Bounds()
	s.mu.Unlock()
}

// fastCells は比べる相手から変わったマスの番号を返す（2026-10-07）。
//
// comparable は比べられたか（比べる相手とマス割りがあり、画像の大きさが同じ）。fast は**速い経路で扱ってよいか**
// —— ふるいが「変化して止まった」と言った 1 枚で、変わったマスが `cellFastMax` 以下のときだけ真。
// 偽なら 81 マスを読む（そのときも cells は `FrameFits` のために返す）。
func (s *CaptureService) fastCells(img image.Image) (cells []int, comparable, fast bool) {
	s.mu.Lock()
	base, rects, region, bounds, why := s.cellBase, s.cellRects, s.cellRegion, s.cellBounds, s.gateRead
	s.mu.Unlock()
	if base == nil || len(rects) != 81 || !bounds.Eq(img.Bounds()) {
		return nil, false, false
	}
	ratios, ok := ikkyoku.CellDiff(base, img, rects, region)
	if !ok {
		return nil, false, false
	}
	cells = []int{}
	var names []string
	for i, v := range ratios {
		if v >= cellChanged {
			cells = append(cells, i)
			names = append(names, cellName(i))
		}
	}
	if why != gateReadSettled {
		return cells, true, false
	}
	if len(cells) > cellFastMax {
		log.Info("変わったマスが多いので 81 マスを読みます（追跡）", "変わったマス", len(cells))
		return cells, true, false
	}
	if len(cells) > 0 {
		log.Info("変わったマス（先端と合っていた 1 枚と）", "マス", strings.Join(names, ","))
	}
	return cells, true, true
}

// cellName は 81 マスの番号を、撮った画像の向きの USI のマス（"7g"）にする（ログ用）。
func cellName(i int) string {
	return string([]byte{byte('9' - i%9), byte('a' + i/9)})
}
