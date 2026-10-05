package main

import "testing"

// 開発ビルドは patch を上げて `+dev`、配るビルドはそのまま名乗ること。
// ⚠️ **配るビルドに `+dev` が付くと、配った exe のログが開発中のものに見える**。
func TestDisplayVersion(t *testing.T) {
	cases := []struct {
		v    string
		dev  bool
		want string
	}{
		{"0.2.3", false, "0.2.3"},
		{"0.2.3", true, "0.2.4+dev"},
		{"1.9.9", true, "1.9.10+dev"},
		{"0.2", true, "0.2+dev"},     // 読めない版は上げない
		{"0.2.x", true, "0.2.x+dev"}, // 同上
	}
	for _, c := range cases {
		if got := displayVersion(c.v, c.dev); got != c.want {
			t.Errorf("displayVersion(%q, %v) = %q, want %q", c.v, c.dev, got, c.want)
		}
	}
}
