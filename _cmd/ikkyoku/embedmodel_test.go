//go:build embedmodel

package main

import (
	"testing"

	"github.com/ShinteLab/ikkyoku/recognize"
)

// 焼き込んだデータが**実際に認識器として組み立てられる**ことを確かめる。
//
// **ビルドが通ることでは足りない。** go:embed はファイルがあれば通るので、
// 中身が壊れていても（gzip でない・版が違う・別のファイルを入れた）気づけない。
// **配布ビルドを作る前にここで止める**のが狙い:
//
//	go test -tags embedmodel -skip Clipboard .
//
// タグを付けないビルドでは丸ごと対象外（データが無いのが正しい状態）。
func TestEmbeddedModel(t *testing.T) {
	t.Cleanup(func() { recognize.SetEmbedded(nil) })
	recognize.SetEmbedded(embeddedModel())
	if !recognize.EmbeddedAvailable() {
		t.Fatal("embedmodel タグ付きなのに焼き込みが空です（model/ に suteme の書き出しがあるか）")
	}
	s, err := recognize.LoadEmbedded()
	if err != nil {
		t.Fatalf("焼き込んだ認識器を組み立てられません: %v", err)
	}
	if s.StripErr != nil || s.StripSamples == 0 {
		t.Fatalf("焼き込んだ帯の判定器を組み立てられません: samples=%d err=%v", s.StripSamples, s.StripErr)
	}
	if s.Date.IsZero() {
		t.Errorf("書き出しの記録（export.json）から日時が読めません（配布モデルとの新旧を比べられない）: %q", s.Source)
	}
}
