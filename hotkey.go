package ikkyoku

import (
	"fmt"
	"strings"

	"golang.design/x/hotkey"
)

// DefaultHotkey は常駐モードの既定のグローバルホットキー。
const DefaultHotkey = "alt+s"

// ParseHotkey は "alt+s" のような "修飾キー+キー" 形式の文字列を
// golang.design/x/hotkey の修飾子・キーコードに変換する。
// 修飾キーは alt / ctrl / shift / win（大文字小文字を区別しない）、
// キーは A-Z・0-9 の 1 文字のみサポートする（必要になったら拡張する）。
func ParseHotkey(s string) ([]hotkey.Modifier, hotkey.Key, error) {
	parts := strings.Split(s, "+")
	if len(parts) < 2 {
		return nil, 0, fmt.Errorf("ikkyoku: ホットキーの指定が不正です（例: alt+s）: %q", s)
	}

	var mods []hotkey.Modifier
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "alt":
			mods = append(mods, hotkey.ModAlt)
		case "ctrl", "control":
			mods = append(mods, hotkey.ModCtrl)
		case "shift":
			mods = append(mods, hotkey.ModShift)
		case "win", "super", "cmd":
			mods = append(mods, hotkey.ModWin)
		default:
			return nil, 0, fmt.Errorf("ikkyoku: 未知の修飾キーです: %q", p)
		}
	}

	key, err := parseKey(parts[len(parts)-1])
	if err != nil {
		return nil, 0, err
	}
	return mods, key, nil
}

func parseKey(s string) (hotkey.Key, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) != 1 {
		return 0, fmt.Errorf("ikkyoku: キーは英数字 1 文字のみサポートしています: %q", s)
	}
	c := s[0]
	switch {
	case c >= 'A' && c <= 'Z':
		// golang.design/x/hotkey の Key 定数は仮想キーコード基準で
		// 'A'-'Z' と ASCII 値が一致する（hotkey.KeyA == 0x41 == 'A'）。
		return hotkey.Key(c), nil
	case c >= '0' && c <= '9':
		return hotkey.Key(c), nil
	default:
		return 0, fmt.Errorf("ikkyoku: 未知のキーです: %q", s)
	}
}
