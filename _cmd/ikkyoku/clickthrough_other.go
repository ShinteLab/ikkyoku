//go:build !windows

package main

import (
	"fmt"
	"unsafe"
)

// 枠の素通し（clickthrough_windows.go）は Windows 専用。
// 他 OS はビルドが壊れないようにするためのスタブで、**設定は保存できるが何も起きない**
// （clientrect_other.go と同じ扱い）。
func setMouseTransparent(hwnd unsafe.Pointer, on bool) error {
	return fmt.Errorf("ikkyoku: このOSでは未対応です(Windows専用)")
}

func cursorPos() (x, y int, ok bool) { return 0, 0, false }

func mouseButtonDown() bool { return false }
