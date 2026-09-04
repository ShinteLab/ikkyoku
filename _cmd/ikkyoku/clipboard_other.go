//go:build !windows

package main

import (
	"fmt"
	"image"
)

// copyImageToClipboard は Windows 専用の実装(clipboard_windows.go)のみ提供している。
// 他 OS はビルドが壊れないようにするためのスタブで、呼ばれたらエラーを返す
// (clientrect_other.go と同じ扱い。ikkyoku/CLAUDE.md により Windows 以外は対象外)。
func copyImageToClipboard(img image.Image) error {
	return fmt.Errorf("ikkyoku: このOSでは画像のコピーは未対応です(Windows専用)")
}
