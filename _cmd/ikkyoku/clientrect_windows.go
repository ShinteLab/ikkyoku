//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// physicalRect はスクリーン座標系・物理ピクセルでの矩形。
type physicalRect struct {
	X, Y, Width, Height int
}

var (
	user32 = windows.NewLazySystemDLL("user32.dll")

	procGetClientRect   = user32.NewProc("GetClientRect")
	procGetWindowRect   = user32.NewProc("GetWindowRect")
	procClientToScreen  = user32.NewProc("ClientToScreen")
	procGetDpiForWindow = user32.NewProc("GetDpiForWindow") // Windows 10 1607(RS1)以降
)

// windowRectPhysical は HWND のウィンドウ全体(タイトルバー・枠を含む)を
// 物理ピクセルのスクリーン座標で返す。
//
// 用途は**キャプチャした画面から自分のウィンドウを消すこと**。ガイド枠の自動フィットで
// 画面全体から盤を探すとき、メイン画面が写っていると `<shogi-board>` が描いている
// 「本物の将棋盤」を掴んでしまう(中継の盤より綺麗なので、むしろそちらが勝つ)。
// クライアント領域ではなくウィンドウ全体を使うのは、消し残しを作らないため。
func windowRectPhysical(hwnd unsafe.Pointer) (physicalRect, error) {
	var rect win32Rect
	ret, _, callErr := procGetWindowRect.Call(uintptr(windows.HWND(uintptr(hwnd))), uintptr(unsafe.Pointer(&rect)))
	if ret == 0 {
		return physicalRect{}, fmt.Errorf("ikkyoku-app: GetWindowRect に失敗しました: %w", callErr)
	}
	return physicalRect{
		X:      int(rect.Left),
		Y:      int(rect.Top),
		Width:  int(rect.Right - rect.Left),
		Height: int(rect.Bottom - rect.Top),
	}, nil
}

// win32Rect は Win32 の RECT に対応するレイアウト(LONG × 4)。
type win32Rect struct {
	Left, Top, Right, Bottom int32
}

// win32Point は Win32 の POINT に対応するレイアウト(LONG × 2)。
type win32Point struct {
	X, Y int32
}

// clientRectPhysical は HWND のクライアント領域を、物理ピクセルのスクリーン座標で返す。
//
// GetClientRect でクライアント領域のローカル矩形(常に Left=Top=0)を取り、
// ClientToScreen でその原点をスクリーン座標に変換する。この組み合わせだけで、
// タイトルバー・枠の厚みの計算も DPI 換算も自前でやらずに済む
// (per-monitor DPI aware なプロセスでは GetClientRect 自体が物理ピクセルを返す)。
//
// scale は CSS px → 物理 px の換算係数(GetDpiForWindow(hwnd)/96)。
// フロントで描くガイド枠の太さ(CSS px 指定)を物理ピクセルのオフセットに変換するために使う。
// GetDpiForWindow が無い環境(Windows 10 未満)では 1.0 にフォールバックする
// (cgo を使わず golang.org/x/sys/windows 経由の LazyProc で解決するため、
// 存在しない場合は Find() がエラーを返す)。
func clientRectPhysical(hwnd unsafe.Pointer) (physicalRect, float64, error) {
	h := windows.HWND(uintptr(hwnd))

	var rect win32Rect
	ret, _, callErr := procGetClientRect.Call(uintptr(h), uintptr(unsafe.Pointer(&rect)))
	if ret == 0 {
		return physicalRect{}, 0, fmt.Errorf("ikkyoku-app: GetClientRect に失敗しました: %w", callErr)
	}

	origin := win32Point{X: rect.Left, Y: rect.Top}
	ret2, _, callErr2 := procClientToScreen.Call(uintptr(h), uintptr(unsafe.Pointer(&origin)))
	if ret2 == 0 {
		return physicalRect{}, 0, fmt.Errorf("ikkyoku-app: ClientToScreen に失敗しました: %w", callErr2)
	}

	scale := 1.0
	if err := procGetDpiForWindow.Find(); err == nil {
		dpi, _, _ := procGetDpiForWindow.Call(uintptr(h))
		if dpi > 0 {
			scale = float64(dpi) / 96.0
		}
	}

	return physicalRect{
		X:      int(origin.X),
		Y:      int(origin.Y),
		Width:  int(rect.Right - rect.Left),
		Height: int(rect.Bottom - rect.Top),
	}, scale, nil
}
