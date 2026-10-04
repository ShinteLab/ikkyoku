//go:build !windows

package main

import (
	"fmt"
	"image"
	"unsafe"
)

// Windows 以外のスタブ。**ビルドが壊れないようにするためだけ**にある。
//
// 実装は `native_windows.go`（Win32 の直呼び）。⚠️ **ここに実装を足さないこと** ——
// このアプリは Windows 専用（ikkyoku/AGENTS.md）で、他 OS では
// **呼ばれたらエラーを返すだけ**という約束にしてある。設定は保存できるが何も起きない。

// physicalRect はスクリーン座標系・物理ピクセルでの矩形。
//
// ⚠️ **型の定義はスタブ側にもある**（`native_windows.go` と同じ形）。
// 片方だけ直すとビルドタグの反対側で壊れる。
type physicalRect struct {
	X, Y, Width, Height int
}

func clientRectPhysical(hwnd unsafe.Pointer) (physicalRect, float64, error) {
	return physicalRect{}, 0, fmt.Errorf("ikkyoku: このOSでは未対応です(Windows専用)")
}

func windowRectPhysical(hwnd unsafe.Pointer) (physicalRect, error) {
	return physicalRect{}, fmt.Errorf("ikkyoku: このOSでは未対応です(Windows専用)")
}

func setMouseTransparent(hwnd unsafe.Pointer, on bool) error {
	return fmt.Errorf("ikkyoku: このOSでは未対応です(Windows専用)")
}

func cursorPos() (x, y int, ok bool) { return 0, 0, false }

func mouseButtonDown() bool { return false }

func copyImageToClipboard(img image.Image) error {
	return fmt.Errorf("ikkyoku: このOSでは画像のコピーは未対応です(Windows専用)")
}

func messageBox(title, text string) error {
	return fmt.Errorf("ikkyoku: このOSでは未対応です(Windows専用)")
}
