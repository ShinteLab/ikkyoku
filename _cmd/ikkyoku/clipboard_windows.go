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
		return fmt.Errorf("ikkyoku-app: クリップボードを空にできませんでした: %w", callErr)
	}

	// GMEM_MOVEABLE で確保して SetClipboardData に渡す。**成功したら所有権は
	// クリップボード側に移る**ので解放してはいけない(逆に失敗したときは自分で解放する)。
	hMem, _, callErr := procGlobalAlloc.Call(gmemMoveable, uintptr(len(dib)))
	if hMem == 0 {
		return fmt.Errorf("ikkyoku-app: クリップボード用のメモリを確保できませんでした: %w", callErr)
	}
	ptr, _, lockErr := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("ikkyoku-app: クリップボード用のメモリをロックできませんでした: %w", lockErr)
	}
	// GlobalLock が返すのは GC の管理外のアドレス。uintptr のまま RtlMoveMemory に
	// 渡して書き込む(unsafe.Pointer に戻して slice を作ると、値としては正しくても
	// go vet の unsafeptr が「uintptr からの復元」として警告する)。
	procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&dib[0])), uintptr(len(dib)))
	runtime.KeepAlive(dib)
	procGlobalUnlock.Call(hMem)

	if ret, _, setErr := procSetClipboard.Call(cfDIB, hMem); ret == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("ikkyoku-app: クリップボードに画像を設定できませんでした: %w", setErr)
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
	return fmt.Errorf("ikkyoku-app: クリップボードを開けませんでした(他のアプリが使用中かもしれません): %w", lastErr)
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
		return nil, fmt.Errorf("ikkyoku-app: 画像の大きさが不正です(%dx%d)", w, h)
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
