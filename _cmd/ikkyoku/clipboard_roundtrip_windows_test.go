//go:build windows

package main

import (
	"image"
	"image/color"
	"testing"
	"unsafe"
)

// クリップボードに載せた CF_DIB を読み返して、ヘッダと画素の並びを検証する。
// **実行するとユーザーのクリップボードの中身が置き換わる。**
func TestCopyImageToClipboardRoundTrip(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	src.Set(0, 0, color.RGBA{R: 255, A: 255}) // 左上 = 赤
	src.Set(2, 1, color.RGBA{B: 255, A: 255}) // 右下 = 青

	if err := copyImageToClipboard(src); err != nil {
		t.Fatalf("copyImageToClipboard: %v", err)
	}

	if err := openClipboard(); err != nil {
		t.Fatalf("openClipboard: %v", err)
	}
	defer procCloseClipboard.Call()

	getData := user32.NewProc("GetClipboardData")
	h, _, err := getData.Call(cfDIB)
	if h == 0 {
		t.Fatalf("GetClipboardData(CF_DIB): %v", err)
	}
	ptr, _, err := procGlobalLock.Call(h)
	if ptr == 0 {
		t.Fatalf("GlobalLock: %v", err)
	}
	defer procGlobalUnlock.Call(h)

	stride := ((3*3 + 3) / 4) * 4
	buf := make([]byte, int(unsafe.Sizeof(bitmapInfoHeader{}))+stride*2)
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), ptr, uintptr(len(buf)))

	var got bitmapInfoHeader
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&got)), unsafe.Sizeof(got)), buf)
	if got.Width != 3 || got.Height != 2 || got.BitCount != 24 || got.Compression != 0 {
		t.Fatalf("ヘッダが違う: %+v", got)
	}

	pixels := buf[got.Size:]
	// ボトムアップなので、先頭行が画像の最下行。右下の青がそこに来る。
	if b, g, r := pixels[2*3+0], pixels[2*3+1], pixels[2*3+2]; b != 255 || g != 0 || r != 0 {
		t.Errorf("右下の画素が BGR で (255,0,0) にならない: (%d,%d,%d)", b, g, r)
	}
	// 2 行目が画像の最上行。左上の赤。
	top := pixels[stride:]
	if b, g, r := top[0], top[1], top[2]; b != 0 || g != 0 || r != 255 {
		t.Errorf("左上の画素が BGR で (0,0,255) にならない: (%d,%d,%d)", b, g, r)
	}
}
