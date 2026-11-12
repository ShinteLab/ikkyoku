package recognize

import (
	"slices"
	"testing"

	"github.com/ShinteLab/ikkyoku/config"
)

// 認識器の 3 段（学習データ → 配布モデル → 焼き込み。2026-10-04）の見る順。
//
//   - **auto は学習データから**（焼き込みへ勝手に倒れると、学習データを更新しても
//     反映されないという最も気づきにくい事故になる）
//   - 見始めた段から下へ、**そのあと上の段へも回る**（焼き込みの無いビルドで
//     「焼き込み」にしていても動くように）
func TestRecognizerOrder(t *testing.T) {
	d, m, e := config.SutemeSourceDir, config.SutemeSourceModel, config.SutemeSourceEmbed
	tests := []struct {
		pref string
		want []string
	}{
		{config.SutemeSourceAuto, []string{d, m, e}},
		{d, []string{d, m, e}},
		{m, []string{m, e, d}},
		{e, []string{e, d, m}},
	}
	for _, tt := range tests {
		if got := recognizerOrder(tt.pref); !slices.Equal(got, tt.want) {
			t.Errorf("recognizerOrder(%q) = %v, want %v", tt.pref, got, tt.want)
		}
	}
}
