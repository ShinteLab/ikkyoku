//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// マウスを素通しさせるための Win32 の定数。
//
// gwlExStyle は GetWindowLongPtrW / SetWindowLongPtrW の nIndex（GWL_EXSTYLE = -20）。
// uintptr に負の定数はそのまま書けないので 2 の補数で作る。
const (
	gwlExStyle      = ^uintptr(19) // GWL_EXSTYLE (-20)
	wsExTransparent = 0x00000020   // WS_EX_TRANSPARENT

	vkLButton = 0x01
	vkRButton = 0x02
	vkMButton = 0x04
)

var (
	// 64bit では GetWindowLongPtrW、32bit では GetWindowLongW（Ptr 版が無い）。
	// **どちらかが必ず居る**ので、Find() で選ぶ。
	procGetWindowLongPtr = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLong    = user32.NewProc("GetWindowLongW")
	procSetWindowLong    = user32.NewProc("SetWindowLongW")

	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
)

func windowLong(h windows.HWND, index uintptr) uintptr {
	if procGetWindowLongPtr.Find() == nil {
		v, _, _ := procGetWindowLongPtr.Call(uintptr(h), index)
		return v
	}
	v, _, _ := procGetWindowLong.Call(uintptr(h), index)
	return v
}

func setWindowLong(h windows.HWND, index, value uintptr) {
	if procSetWindowLongPtr.Find() == nil {
		procSetWindowLongPtr.Call(uintptr(h), index, value)
		return
	}
	procSetWindowLong.Call(uintptr(h), index, value)
}

// setMouseTransparent は HWND の WS_EX_TRANSPARENT を付け外しする。
//
// **これが「後ろを操作できる」の実体。** WS_EX_TRANSPARENT が付いたウィンドウは
// WindowFromPoint に拾われなくなるので、その上のクリックもホイールも
// **そのまま後ろのウィンドウへ届く**。子ウィンドウ（WebView2 が持っている）も
// 親を経由してしか探されないので、まとめて素通しになる。
//
// ⚠️ **WS_EX_LAYERED は足さないこと。** 枠は WebView2 の合成で透過しており
// （BackgroundTypeTransparent）、layered を後付けすると描画経路が変わる。
// 素通しに要るのは TRANSPARENT のほうだけで、WindowFromPoint はこれだけを見る。
//
// ⚠️ **付けっぱなしにしない。** 付いているあいだはツールバーも押せなくなるので、
// 呼ぶ側（captureservice.go の watchCursor）がカーソルの位置で付け外しする。
func setMouseTransparent(hwnd unsafe.Pointer, on bool) error {
	if hwnd == nil {
		return fmt.Errorf("ikkyoku-app: ネイティブウィンドウハンドルがありません")
	}
	h := windows.HWND(uintptr(hwnd))

	cur := windowLong(h, gwlExStyle)
	next := cur
	if on {
		next |= wsExTransparent
	} else {
		next &^= wsExTransparent
	}
	if next == cur {
		return nil
	}

	setWindowLong(h, gwlExStyle, next)

	// SetWindowLongPtrW は「元の値」を返すので、0 が成功なのか失敗なのかを
	// 戻り値だけでは決められない（GetLastError を消してから読む作法が要る）。
	// **読み直して確かめるほうが確実。**
	if got := windowLong(h, gwlExStyle); got&wsExTransparent != next&wsExTransparent {
		return fmt.Errorf("ikkyoku-app: 拡張スタイルを変更できませんでした(WS_EX_TRANSPARENT)")
	}
	return nil
}

// cursorPos はマウスカーソルの位置を物理ピクセルのスクリーン座標で返す。
//
// per-monitor DPI aware なプロセスなので、GetCursorPos がそのまま物理ピクセルを返す
// （clientRectPhysical と同じ座標系。**混ぜても換算が要らない**）。
func cursorPos() (x, y int, ok bool) {
	var p win32Point
	ret, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	if ret == 0 {
		return 0, 0, false
	}
	return int(p.X), int(p.Y), true
}

// mouseButtonDown はマウスのボタンが押されているか。
//
// **押している最中に素通しを切り替えないため**に見る。枠のドラッグ移動も
// リサイズもボタンを押したまま動かす操作なので、途中で切り替わると
// **掴んだままカーソルだけが枠から抜けて、動かなくなる**。
func mouseButtonDown() bool {
	for _, vk := range []uintptr{vkLButton, vkRButton, vkMButton} {
		st, _, _ := procGetAsyncKeyState.Call(vk)
		if st&0x8000 != 0 {
			return true
		}
	}
	return false
}
