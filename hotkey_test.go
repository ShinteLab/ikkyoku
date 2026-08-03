package ikkyoku

import (
	"testing"

	"golang.design/x/hotkey"
)

func TestParseHotkey(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantMods []hotkey.Modifier
		wantKey  hotkey.Key
		wantErr  bool
	}{
		{"default", DefaultHotkey, []hotkey.Modifier{hotkey.ModAlt}, hotkey.KeyS, false},
		{"ctrl+shift", "ctrl+shift+A", []hotkey.Modifier{hotkey.ModCtrl, hotkey.ModShift}, hotkey.KeyA, false},
		{"case insensitive mod", "ALT+s", []hotkey.Modifier{hotkey.ModAlt}, hotkey.KeyS, false},
		{"digit key", "alt+1", []hotkey.Modifier{hotkey.ModAlt}, hotkey.Key('1'), false},
		{"no modifier", "s", nil, 0, true},
		{"unknown modifier", "foo+s", nil, 0, true},
		{"multi-char key", "alt+ss", nil, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mods, key, err := ParseHotkey(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseHotkey(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if key != tt.wantKey {
				t.Errorf("key = %v, want %v", key, tt.wantKey)
			}
			if len(mods) != len(tt.wantMods) {
				t.Fatalf("mods = %v, want %v", mods, tt.wantMods)
			}
			for i := range mods {
				if mods[i] != tt.wantMods[i] {
					t.Errorf("mods[%d] = %v, want %v", i, mods[i], tt.wantMods[i])
				}
			}
		})
	}
}
