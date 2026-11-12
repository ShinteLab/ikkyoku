package recognize_test

import (
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku/config"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// 段の落ち方の歯止め（2026-11-13 に `_cmd/ikkyoku/recognizersource_test.go` から移した）。
//
//   - **置いてあるのに読めない段は Skipped に残して下へ落ちる**（⚠ に出る）
//   - **置かれていない段（既定の置き場所が空）は Skipped に入れない**（普通の状態）
//   - **場所を指定したのに置かれていないときは Skipped に入れる**（指定は意思表示）
//   - **どれも読めなければ認識器を外す**（suteme 既定の探索に落とさない）
//
// 学習ディレクトリと配布セットの作り方は predictor_test.go の writeTrainingDir / writePack。
func TestLoadSourcesFallsThrough(t *testing.T) {
	t.Cleanup(recognize.Clear)
	if recognize.EmbeddedAvailable() {
		t.Skip("焼き込みのあるビルドでは最後の段が埋まるので、落ち方を確かめられない")
	}
	broken := t.TempDir() // 学習データとして指定したが、中身が無い
	pack := writePack(t, writeTrainingDir(t, false), "2026-10-01", false)

	st := recognize.LoadSources(recognize.Sources{DataDir: broken, ModelDir: pack, Source: config.SutemeSourceAuto})
	if !st.Ready || st.Mode != config.SutemeSourceModel || st.Source != pack {
		t.Fatalf("配布モデルへ落ちていない: %+v", st)
	}
	if len(st.Skipped) != 1 || !strings.Contains(st.Skipped[0], "学習データ") {
		t.Fatalf("Skipped = %v（読めなかった学習データが残ること）", st.Skipped)
	}

	// 学習データを指定せず、配布モデルの場所も既定（ここでは空のディレクトリを指定して
	// 置かれていない状態を作る）。指定した場所に無いので Skipped に入り、認識器は外れる。
	st = recognize.LoadSources(recognize.Sources{ModelDir: t.TempDir(), Source: config.SutemeSourceAuto})
	if st.Ready || st.Error == "" || recognize.Ready() {
		t.Fatalf("どれも無いのに読めたことになっている: %+v ready=%v", st, recognize.Ready())
	}
	if len(st.Skipped) != 1 || !strings.Contains(st.Skipped[0], "置かれていません") {
		t.Fatalf("指定した場所に無いことが Skipped に無い: %v", st.Skipped)
	}

	// 学習データを指定すれば、それが使われる（配布モデルより上の段）。
	st = recognize.LoadSources(recognize.Sources{DataDir: writeTrainingDir(t, false), ModelDir: pack, Source: config.SutemeSourceAuto})
	if st.Mode != config.SutemeSourceDir || len(st.Skipped) != 0 {
		t.Fatalf("学習データが使われていない: %+v", st)
	}

	// 「配布モデルから」にすれば、学習データがあっても配布モデル。
	st = recognize.LoadSources(recognize.Sources{DataDir: writeTrainingDir(t, false), ModelDir: pack, Source: config.SutemeSourceModel})
	if st.Mode != config.SutemeSourceModel {
		t.Fatalf("配布モデルから見始めていない: %+v", st)
	}
}
