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
	wsExLayered     = 0x00080000   // WS_EX_LAYERED
	lwaAlpha        = 0x00000002   // LWA_ALPHA

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

	procSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")

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

// setMouseTransparent は HWND を「マウスに対して透明」にする／戻す。
//
// **これが「後ろを操作できる」の実体。**
//
// ⚠️ **`WS_EX_TRANSPARENT` だけでは効かない。3 つ揃って初めて素通しになる**
// （2026-08-19 に実機で確かめた。**一度 TRANSPARENT だけで実装して全く効かなかった**）:
//
//  1. `WS_EX_TRANSPARENT`
//  2. `WS_EX_LAYERED`
//  3. `SetLayeredWindowAttributes`（ヒットテスト領域の更新。**呼ばないと反映されない**）
//
// TRANSPARENT だけだと、拡張スタイルは確かに変わるのに**クリックは枠が取る**。
// 枠の中身を描いている **WebView2 は別プロセスの子ウィンドウ**
// （`Chrome_WidgetWin_1` など）で、そちらがマウスを受け取ってしまうため。
// ⚠️ **子ウィンドウ側に TRANSPARENT を付けて回っても直らない**（これも試した）。
// Wails 自身の `setIgnoreMouseEvents` も LAYERED を一緒に付けている。
//
// ⚠️ **ChildWindowFromPointEx(CWP_SKIPTRANSPARENT) で確かめないこと。**
// あれは「TRANSPARENT だけで素通しになる」と答えるが、**実際のクリックの
// 行き先とは食い違う**（実測）。確かめるなら実際にクリックして
// `GetForegroundWindow` を見ること。
//
// ⚠️ **付けっぱなしにしない。** 付いているあいだはツールバーも押せなくなるので、
// 呼ぶ側（captureservice.go の watchCursor）がカーソルの位置で付け外しする。
// 戻すときは **LAYERED も一緒に外して元の姿に戻す** —— 枠は WebView2 の合成で
// 透過している（`BackgroundTypeTransparent` = `WS_EX_NOREDIRECTIONBITMAP`）ので、
// 素通しでないあいだまで layered を残す理由が無い。
func setMouseTransparent(hwnd unsafe.Pointer, on bool) error {
	if hwnd == nil {
		return fmt.Errorf("ikkyoku-app: ネイティブウィンドウハンドルがありません")
	}
	h := windows.HWND(uintptr(hwnd))

	cur := windowLong(h, gwlExStyle)
	next := cur
	if on {
		next |= wsExTransparent | wsExLayered
	} else {
		next &^= wsExTransparent | wsExLayered
	}
	if next == cur {
		return nil
	}

	setWindowLong(h, gwlExStyle, next)

	// SetWindowLongPtrW は「元の値」を返すので、0 が成功なのか失敗なのかを
	// 戻り値だけでは決められない（GetLastError を消してから読む作法が要る）。
	// **読み直して確かめるほうが確実。**
	if got := windowLong(h, gwlExStyle); got&(wsExTransparent|wsExLayered) != next&(wsExTransparent|wsExLayered) {
		return fmt.Errorf("ikkyoku-app: 拡張スタイルを変更できませんでした(WS_EX_TRANSPARENT|WS_EX_LAYERED)")
	}

	if on {
		// ⚠️ **これを落とすと素通しにならない。** layered ウィンドウのヒットテスト
		// 領域はスタイルを変えただけでは更新されない（Wails 自身も同じ理由で
		// この呼び出しを持っている）。alpha 255 = 見た目は変えない。
		if ret, _, callErr := procSetLayeredWindowAttributes.Call(uintptr(h), 0, 255, lwaAlpha); ret == 0 {
			return fmt.Errorf("ikkyoku-app: SetLayeredWindowAttributes に失敗しました: %w", callErr)
		}
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
