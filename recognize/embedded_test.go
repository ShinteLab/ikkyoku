//go:build embedmodel

package recognize

import (
	"strings"
	"testing"
)

// 焼き込んだデータが**実際に認識器として組み立てられる**ことを確かめる。
//
// **ビルドが通ることでは足りない。** go:embed はファイルがあれば通るので、
// 中身が壊れていても(gzip でない・空・別のファイルを入れた)気づけない。
// **配布ビルドを作る前にここで止める**のが狙い:
//
//	task model:copy
//	go test -tags embedmodel ./recognize/
//
// タグを付けないビルドでは丸ごと対象外(データが無いのが正しい状態)。
func TestEmbeddedPredictor(t *testing.T) {
	if !EmbeddedAvailable() {
		t.Fatal("embedmodel タグ付きなのに焼き込みが空です")
	}
	if err := UsePredictorEmbedded(); err != nil {
		t.Fatalf("焼き込んだ駒種推論器を組み立てられません: %v", err)
	}
	t.Cleanup(UseDefaultPredictor)
}

func TestEmbeddedStripJudge(t *testing.T) {
	n, err := UseStripJudgeEmbedded()
	if err != nil {
		t.Fatalf("焼き込んだ帯の判定器を組み立てられません: %v", err)
	}
	if n == 0 {
		t.Fatal("帯の教師データが空です")
	}
	t.Cleanup(UseDefaultStripJudge)
}

// 出所のラベルは画面とログに出る。**BOM や改行が混ざらないこと**
// (書き出しは Windows PowerShell なので BOM が付く)。
func TestEmbeddedSource(t *testing.T) {
	src := EmbeddedSource()
	if src == "" {
		t.Fatal("source.txt が空です(copy-model.ps1 が書くはず)")
	}
	if strings.ContainsAny(src, "\ufeff\r\n") {
		t.Errorf("出所のラベルに BOM か改行が残っています: %q", src)
	}
}
