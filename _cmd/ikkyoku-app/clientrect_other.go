//go:build !windows

package main

import (
	"fmt"
	"unsafe"
)

// physicalRect はスクリーン座標系・物理ピクセルでの矩形。
type physicalRect struct {
	X, Y, Width, Height int
}

// clientRectPhysical は Windows 専用の実装(clientrect_windows.go)のみ提供している。
// 他 OS はビルドが壊れないようにするためのスタブで、呼ばれたらエラーを返す
// (ikkyoku/CLAUDE.md・依頼の指示により Windows 以外の実装は対象外)。
func clientRectPhysical(hwnd unsafe.Pointer) (physicalRect, float64, error) {
	return physicalRect{}, 0, fmt.Errorf("ikkyoku-app: このOSでは未対応です(Windows専用)")
}
