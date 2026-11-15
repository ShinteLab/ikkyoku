package main

import (
	"errors"
	"testing"
)

// TestDialogPath は「取り消しはエラーではない」の歯止め（2026-11-15）。
// Wails v3.0.0 は Windows だけ取り消しを "cancelled by user" のエラーで返す。
// ⚠️ 取り消しは空文字・nil に揃えること・**ほかのエラーは握りつぶさないこと**。
func TestDialogPath(t *testing.T) {
	if p, err := dialogPath("", errors.New(dialogCancelledMsg)); p != "" || err != nil {
		t.Fatalf("取り消し: got (%q, %v), want (\"\", nil)", p, err)
	}
	if p, err := dialogPath(`C:\a.png`, nil); p != `C:\a.png` || err != nil {
		t.Fatalf("選んだ: got (%q, %v)", p, err)
	}
	other := errors.New("unable to open dialog")
	if _, err := dialogPath("", other); !errors.Is(err, other) {
		t.Fatalf("ほかのエラーは通すこと: got %v", err)
	}
}
