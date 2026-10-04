//go:build windows

package main

import (
	"fmt"
	"image"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 の直呼び。**このアプリで cgo を使わないための層**（`golang.org/x/sys/windows`
// の LazyProc 経由。ikkyoku は PureGo が方針）。
//
// ⚠️ **ここに判断を書かないこと。** 素通しを付け外しするかの判断は
// `captureservice.go` の `watchCursor`、寸法の計算は `ikkyoku/guide` にある。
// ここにあるのは **OS を叩くところだけ**。
//
// ⚠️ **Windows 以外は `native_other.go`**（スタブ）。関数を足したら**両方に足す**。
//
// 中身は 3 つ:
//
//	① ウィンドウの矩形   … HWND からクライアント/ウィンドウ矩形を物理ピクセルで取る
//	② 枠の素通し         … WS_EX_TRANSPARENT の付け外しとカーソルの位置
//	③ クリップボード     … 画像を CF_DIB で載せる
//	④ メッセージボックス … 画面を出す前に終わるときの最後の口（fatal.go）

// ---- ① ウィンドウの矩形 ---------------------------------------------------

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
		return physicalRect{}, fmt.Errorf("ikkyoku: GetWindowRect に失敗しました: %w", callErr)
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
		return physicalRect{}, 0, fmt.Errorf("ikkyoku: GetClientRect に失敗しました: %w", callErr)
	}

	origin := win32Point{X: rect.Left, Y: rect.Top}
	ret2, _, callErr2 := procClientToScreen.Call(uintptr(h), uintptr(unsafe.Pointer(&origin)))
	if ret2 == 0 {
		return physicalRect{}, 0, fmt.Errorf("ikkyoku: ClientToScreen に失敗しました: %w", callErr2)
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

// ---- ② 枠の素通し ---------------------------------------------------------

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
		return fmt.Errorf("ikkyoku: ネイティブウィンドウハンドルがありません")
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
		return fmt.Errorf("ikkyoku: 拡張スタイルを変更できませんでした(WS_EX_TRANSPARENT|WS_EX_LAYERED)")
	}

	if on {
		// ⚠️ **これを落とすと素通しにならない。** layered ウィンドウのヒットテスト
		// 領域はスタイルを変えただけでは更新されない（Wails 自身も同じ理由で
		// この呼び出しを持っている）。alpha 255 = 見た目は変えない。
		if ret, _, callErr := procSetLayeredWindowAttributes.Call(uintptr(h), 0, 255, lwaAlpha); ret == 0 {
			return fmt.Errorf("ikkyoku: SetLayeredWindowAttributes に失敗しました: %w", callErr)
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

// ---- ③ クリップボード -----------------------------------------------------

// 撮った画像そのものをクリップボードへ入れる(Windows)。
//
// Wails ランタイムのクリップボードはテキストだけ(Clipboard.SetText / Text)なので、
// 画像はここで Win32 の API を直接叩く。**cgo は使わない**(clientrect_windows.go と同じく
// LazyProc 経由。ikkyoku は PureGo 方針)。
//
// 形式は **CF_DIB(24bpp・無圧縮・ボトムアップ)** の 1 本だけにしてある。
// 貼り付け先が Windows 側で CF_BITMAP に変換してくれるため、対応範囲がいちばん広い。
// PNG 形式(RegisterClipboardFormat("PNG"))を併せて載せる手もあるが、撮った画像は
// 中継画面の不透明な矩形で透過を持たないので、増やしても得るものが無い。
var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGlobalAlloc   = kernel32.NewProc("GlobalAlloc")
	procGlobalLock    = kernel32.NewProc("GlobalLock")
	procGlobalUnlock  = kernel32.NewProc("GlobalUnlock")
	procGlobalFree    = kernel32.NewProc("GlobalFree")
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")

	// user32 は clientrect_windows.go で定義済み(同じパッケージ)。
	procOpenClipboard  = user32.NewProc("OpenClipboard")
	procCloseClipboard = user32.NewProc("CloseClipboard")
	procEmptyClipboard = user32.NewProc("EmptyClipboard")
	procSetClipboard   = user32.NewProc("SetClipboardData")
)

const (
	gmemMoveable = 0x0002
	cfDIB        = 8
)

// bitmapInfoHeader は Win32 の BITMAPINFOHEADER に対応するレイアウト(40 バイト)。
// CF_DIB のデータは「このヘッダ + 画素」を連続で並べたもの。
type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32 // 正 = ボトムアップ(最下行が先頭)
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// copyImageToClipboard は画像を CF_DIB としてクリップボードへ入れる。
func copyImageToClipboard(img image.Image) error {
	dib, err := dibFromImage(img)
	if err != nil {
		return err
	}

	// クリップボードは一度に 1 プロセスしか開けない。他アプリが開いている最中は
	// 普通に失敗するので、短く数回だけ待って諦める(押し直せば済むため)。
	if err := openClipboard(); err != nil {
		return err
	}
	defer procCloseClipboard.Call()

	if ret, _, callErr := procEmptyClipboard.Call(); ret == 0 {
		return fmt.Errorf("ikkyoku: クリップボードを空にできませんでした: %w", callErr)
	}

	// GMEM_MOVEABLE で確保して SetClipboardData に渡す。**成功したら所有権は
	// クリップボード側に移る**ので解放してはいけない(逆に失敗したときは自分で解放する)。
	hMem, _, callErr := procGlobalAlloc.Call(gmemMoveable, uintptr(len(dib)))
	if hMem == 0 {
		return fmt.Errorf("ikkyoku: クリップボード用のメモリを確保できませんでした: %w", callErr)
	}
	ptr, _, lockErr := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("ikkyoku: クリップボード用のメモリをロックできませんでした: %w", lockErr)
	}
	// GlobalLock が返すのは GC の管理外のアドレス。uintptr のまま RtlMoveMemory に
	// 渡して書き込む(unsafe.Pointer に戻して slice を作ると、値としては正しくても
	// go vet の unsafeptr が「uintptr からの復元」として警告する)。
	procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&dib[0])), uintptr(len(dib)))
	runtime.KeepAlive(dib)
	procGlobalUnlock.Call(hMem)

	if ret, _, setErr := procSetClipboard.Call(cfDIB, hMem); ret == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("ikkyoku: クリップボードに画像を設定できませんでした: %w", setErr)
	}
	return nil
}

func openClipboard() error {
	var lastErr error
	for i := 0; i < 5; i++ {
		ret, _, callErr := procOpenClipboard.Call(0)
		if ret != 0 {
			return nil
		}
		lastErr = callErr
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("ikkyoku: クリップボードを開けませんでした(他のアプリが使用中かもしれません): %w", lastErr)
}

// dibFromImage は image.Image を CF_DIB のバイト列(ヘッダ + 画素)にする。
//
// 24bpp・BGR・**ボトムアップ**(最下行が先頭)・各行 4 バイト境界にパディング、という
// DIB の素の並び。トップダウン(Height を負にする形)も規格上は有効だが、受け取り側が
// 対応していないことがあるため素直なほうにしてある。
func dibFromImage(img image.Image) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("ikkyoku: 画像の大きさが不正です(%dx%d)", w, h)
	}

	stride := ((w*3 + 3) / 4) * 4 // 各行を 4 バイト境界に揃える
	header := bitmapInfoHeader{
		Size:      uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:     int32(w),
		Height:    int32(h),
		Planes:    1,
		BitCount:  24,
		SizeImage: uint32(stride * h),
	}

	out := make([]byte, int(header.Size)+stride*h)
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(&header)), header.Size))

	pixels := out[header.Size:]
	for y := 0; y < h; y++ {
		// ボトムアップなので、画像の下の行から順に詰める。
		row := pixels[(h-1-y)*stride:]
		for x := 0; x < w; x++ {
			// RGBA() は 16bit のアルファ乗算済み。撮った画像は不透明なので
			// 上位 8bit を取るだけでよい。
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			row[x*3+0] = byte(bl >> 8) // DIB は BGR 順
			row[x*3+1] = byte(g >> 8)
			row[x*3+2] = byte(r >> 8)
		}
	}
	return out, nil
}

// ---- ④ メッセージボックス -------------------------------------------------

// messageBox は OS のメッセージボックスでエラーを出し、閉じられるまで待つ。
//
// **WebView2 にも Wails にも頼らない**（それが動かないときに出すものなので）。
// 最前面に出す —— 起動直後で自分の窓が 1 枚も無いと、後ろに隠れて気づかれない。
func messageBox(title, text string) error {
	_, err := windows.MessageBox(0,
		windows.StringToUTF16Ptr(text), windows.StringToUTF16Ptr(title),
		windows.MB_OK|windows.MB_ICONERROR|windows.MB_SETFOREGROUND|windows.MB_TOPMOST)
	return err
}
