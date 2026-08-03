package main

import (
	"strings"

	"github.com/ShinteLab/ikkyoku"
)

// resolveHotkey は GUI 版の既定ホットキーを返す。
// 今のところ設定 UI は無く常に既定値(alt+s)を使う。将来 UI から変更できるようにする場合、
// ikkyoku.Config を読み書きする形にすれば CLI 版(_cmd/ikkyoku)と設定ファイルを共有できる。
func resolveHotkey() string {
	return ikkyoku.DefaultHotkey
}

// hotkeyAccelerator は "alt+s" 形式の文字列を、Wails の GlobalShortcut.Register が
// 期待するアクセラレータ表記に変換する。
//
// 実際のホットキー登録は golang.design/x/hotkey ではなく Wails 標準の GlobalShortcut を使う。
// 理由: GlobalShortcut はウィンドウが非フォーカスでも確実に発火する経路として wails3 skill
// (tray-hotkey.md)で保証されており、二重にホットキー実装を持ち込むと同じキーの奪い合いや
// メッセージループの競合を招くリスクがある。一方で「alt+s」のような文字列の妥当性検証・
// 既定値の一元化は ikkyoku.ParseHotkey / ikkyoku.DefaultHotkey を単一のソースとして再利用し、
// CLI 版と GUI 版で「有効なホットキー文字列」の定義がずれないようにする。
//
// ikkyoku.ParseHotkey が受け付ける修飾キーのエイリアス(control/win/cmd 等)のうち、
// Wails の accelerator パーサ(pkg/application/keys.go の modifierMap)が直接は
// 受け付けない表記だけを変換する。大文字小文字は Wails 側が lower 化して吸収するので
// ここでは揃えない。
func hotkeyAccelerator(s string) (string, error) {
	if _, _, err := ikkyoku.ParseHotkey(s); err != nil {
		return "", err
	}

	parts := strings.Split(s, "+")
	for i, p := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "control":
			parts[i] = "ctrl"
		case "win":
			parts[i] = "super"
		}
	}
	return strings.Join(parts, "+"), nil
}
