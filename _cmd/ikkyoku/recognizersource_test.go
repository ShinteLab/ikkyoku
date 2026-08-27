package main

import (
	"testing"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// resolveRecognizerSource は「設定の値」から「実際にどこから読むか」を決める。
//
// ⚠️ **焼き込みの有無で答えが変わる**ので、テストは
// `recognize.EmbeddedAvailable()` を見て期待値を切り替える(タグの有無の両方で通す)。
// ここで確かめたいのは**落ち方**:
//
//   - auto はディレクトリ優先(焼き込みへ勝手に倒れると、学習データを更新しても
//     反映されないという最も気づきにくい事故になる)
//   - "embed" を選んでも、焼き込みの無いビルドでは dir へ落ちる
//   - 読むものが何も無ければ suteme 既定の探索("")
func TestResolveRecognizerSource(t *testing.T) {
	embedded := recognize.EmbeddedAvailable()
	embedOrDir := func(dir string) string {
		if embedded {
			return ikkyoku.SutemeSourceEmbed
		}
		if dir != "" {
			return ikkyoku.SutemeSourceDir
		}
		return ""
	}

	tests := []struct {
		name string
		pref string
		dir  string
		want string
	}{
		{"auto: ディレクトリがあればそちら", ikkyoku.SutemeSourceAuto, `C:\suteme`, ikkyoku.SutemeSourceDir},
		{"auto: 無ければ焼き込み(あれば)", ikkyoku.SutemeSourceAuto, "", embedOrDir("")},
		{"空も auto", "", `C:\suteme`, ikkyoku.SutemeSourceDir},
		{"dir: 指定どおり", ikkyoku.SutemeSourceDir, `C:\suteme`, ikkyoku.SutemeSourceDir},
		{"dir: 置き場所が無ければ既定探索", ikkyoku.SutemeSourceDir, "", ""},
		{"embed: 焼き込みがあれば焼き込み", ikkyoku.SutemeSourceEmbed, "", embedOrDir("")},
		{"embed: 無ければディレクトリへ落ちる", ikkyoku.SutemeSourceEmbed, `C:\suteme`, embedOrDir(`C:\suteme`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveRecognizerSource(tt.pref, tt.dir); got != tt.want {
				t.Errorf("resolveRecognizerSource(%q, %q) = %q, want %q", tt.pref, tt.dir, got, tt.want)
			}
		})
	}
}
