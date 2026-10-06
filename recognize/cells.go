package recognize

import (
	"errors"
	"image"
	"strings"

	"github.com/ShinteLab/suteme"
)

// ReadCellMinConf は ReadCells が駒種を言い切る確信度の下限（k-NN の勝者の重み比率）。
// これを下回ったマスは「読めない」（"?"）として返す。⚠️ **当て推量**（2026-10-07）。
const ReadCellMinConf = 0.6

// CellRead は ReadCells が読んだマス 1 つ（撮った画像の向き。2026-10-07）。
type CellRead struct {
	// Piece は SFEN の駒（手前側は大文字）。空きは ""、種類が読めなければ "?"。
	Piece string
	// Side は駒の向き（`SideUp` = 手前向き / `SideDown` = 向こう向き）。空きと、マスを切り出せなかったときは ""。
	// ⚠️ **Piece が "?" でも入っていることがある**（種類は読めなくても向きは読めている）。
	Side string
}

// 駒の向き（`CellRead.Side`）。
const (
	SideUp   = "up"
	SideDown = "down"
)

// ReadCells は**マスを指定して、そのマスの駒だけを読む**（2026-10-07。追従の速い経路）。
//
// 変わったマスで手順を割り出したとき、成・不成や打った駒の種類は見分けられない。81 マスを読み直すと
// 2 秒以上かかる（CPU が混んでいると 7〜10 秒）ので、**違いの出るマスだけ**をここで読む。
// マス割りは呼び出し側が持っている（最後に 81 マスを読んだときの `Debug.Cells` / `Debug.Region`）。
//
// 返すのは**撮った画像の向き**での SFEN の駒（手前側は大文字。"P" / "+p" など）。空は ""、
// 読めない（確信度が足りない・切り出せない）は "?"。⚠️ **向きは suteme の回転照合で決める**
// （`ClassifyCellFor`。81 マスを読むときと同じ判断）。
//
// ⚠️ **駒の種類が読めなくても、向き（`CellRead.Side`）は返すこと**（2026-10-07）。速い経路は
// 行き先に「指した側の駒が来たか」を先後で確かめる（`app.FollowCells`）。成駒は学習データに入らず
// 種類が読めないのが普通なので、向きまで捨てると成る手が毎回 81 マスの読みに落ちる。
func ReadCells(img image.Image, region image.Rectangle, rects []image.Rectangle, idx []int) ([]CellRead, error) {
	useMu.Lock()
	m := current
	useMu.Unlock()
	if m == nil {
		return nil, errors.New("認識器がありません")
	}
	if img == nil || len(rects) != 81 {
		return nil, errors.New("マス割りがありません")
	}
	br := &suteme.BoardRegion{Bounds: region}
	for i, r := range rects {
		br.Cells[i/9][i%9] = r
	}
	bc := suteme.BoardColor(img, br)
	out := make([]CellRead, len(idx))
	for k, i := range idx {
		out[k] = CellRead{Piece: "?"}
		if i < 0 || i >= 81 {
			continue
		}
		cell := br.ExtractCell(img, i/9, i%9)
		if cell == nil {
			continue
		}
		cat, _ := suteme.ClassifyCellFor(cell, bc, m)
		if cat == suteme.CellEmpty {
			out[k] = CellRead{}
			continue
		}
		in := cell
		out[k].Side = SideUp
		if cat == suteme.CellPieceDown {
			in = suteme.Rotate180(cell) // 学習データは手前向きに揃えてある
			out[k].Side = SideDown
		}
		class, conf := m.Predict(in)
		if class == suteme.ClassEmpty || conf < ReadCellMinConf {
			continue // 種類は "?" のまま。向きは残す
		}
		label := suteme.ClassToBaseLabel(class)
		if cat == suteme.CellPieceDown {
			label = strings.ToLower(label)
		}
		out[k].Piece = label
	}
	return out, nil
}
