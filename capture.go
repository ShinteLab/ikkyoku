// Package ikkyoku は画面の指定領域を PNG として保存するだけのライブラリ。
//
// 名前は「それもまた一局」から。将棋の盤面認識・SFEN 変換・棋譜組み立てのロジックは
// 一切持たない。仕様は github.com/ShinteLab/core、認識は github.com/ShinteLab/suteme の
// 担当であり、このパッケージはその手前で「画面の指定領域を画像にする」だけに徹する。
//
// 状態を持たない。1 回のキャプチャは他のキャプチャと完全に独立しており、
// 履歴に依存する処理はここには書かないこと（親ディレクトリの TODO.md の設計原則）。
package ikkyoku

import (
	"fmt"
	"image"

	"github.com/kbinani/screenshot"
)

// Region はキャプチャする画面上の矩形領域（グローバル座標系）。
// マルチモニタ環境では負の座標や複数ディスプレイに跨る領域も取り得る。
type Region struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Rect は image.Rectangle に変換する。
func (r Region) Rect() image.Rectangle {
	return image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height)
}

// String は "x,y,width,height" 形式で表示する（ParseRegion の逆変換）。
func (r Region) String() string {
	return fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height)
}

// Valid は幅・高さが正の値であるかを確認する。
func (r Region) Valid() bool {
	return r.Width > 0 && r.Height > 0
}

// DisplayInfo は 1 台のディスプレイの情報。
type DisplayInfo struct {
	Index  int
	Bounds image.Rectangle
}

// Region はディスプレイ全体を Region に変換する。
func (d DisplayInfo) Region() Region {
	b := d.Bounds
	return Region{X: b.Min.X, Y: b.Min.Y, Width: b.Dx(), Height: b.Dy()}
}

// ListDisplays は接続されている全ディスプレイの情報を、OS が割り振った番号順に返す。
func ListDisplays() []DisplayInfo {
	n := screenshot.NumActiveDisplays()
	displays := make([]DisplayInfo, 0, n)
	for i := range n {
		displays = append(displays, DisplayInfo{
			Index:  i,
			Bounds: screenshot.GetDisplayBounds(i),
		})
	}
	return displays
}

// DisplayRegion は index 番目のディスプレイ全体の領域を返す。
func DisplayRegion(index int) (Region, error) {
	displays := ListDisplays()
	if index < 0 || index >= len(displays) {
		return Region{}, fmt.Errorf("ikkyoku: ディスプレイ番号が範囲外です: %d（検出数 %d）", index, len(displays))
	}
	return displays[index].Region(), nil
}

// PrimaryRegion はプライマリディスプレイ（0 番）全体の領域を返す。
// キャプチャ領域が未指定のときの既定値として使う。
func PrimaryRegion() (Region, error) {
	return DisplayRegion(0)
}

// Capture は指定領域を画面から取り込む。
func Capture(r Region) (image.Image, error) {
	if !r.Valid() {
		return nil, fmt.Errorf("ikkyoku: 不正な領域です: %s", r)
	}
	img, err := screenshot.CaptureRect(r.Rect())
	if err != nil {
		return nil, fmt.Errorf("ikkyoku: キャプチャに失敗しました: %w", err)
	}
	return img, nil
}
