package app

import (
	"io"
	"log/slog"
	"testing"
)

// 対局者名（勝率バーの左右に出すもの）。
//
// ⚠️ **棋譜を読んだときだけ埋まり、根を入れ替えたら消えること。** 残っていると
// **別の対局の名前を今の盤に出す**（撮った局面にも新規対局にも対局者は居ない）。
//
// ⚠️ **空を「先手」「後手」で埋めないこと** —— 名前が分かっているのか、既定を
// 出しているだけなのかが区別できなくなる（既定の文言は表示側が持つ）。
func TestStudyServicePlayers(t *testing.T) {
	const kif = `先手：先手太郎
後手：後手花子
手数----指手---------消費時間--
   1 ７六歩(77)   ( 0:16/00:00:16)
`
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pos := NewPositionService(logger)
	s := NewStudyService(logger, pos)

	load, err := s.LoadKifu(kif)
	if err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	if load.State.Black != "先手太郎" || load.State.White != "後手花子" {
		t.Errorf("対局者が載っていません: black=%q white=%q", load.State.Black, load.State.White)
	}

	// 新しく対局を始めたら消える（新規対局に対局者は居ない）。
	got, err := s.NewGame("平手")
	if err != nil {
		t.Fatalf("NewGame: %v", err)
	}
	if got.State.Black != "" || got.State.White != "" {
		t.Errorf("新規対局に前の対局者が残っています: black=%q white=%q",
			got.State.Black, got.State.White)
	}

	// 撮った局面を採ったときも同じ（画像に対局者は付いていない）。
	if _, err := s.LoadKifu(kif); err != nil {
		t.Fatalf("LoadKifu: %v", err)
	}
	if _, err := pos.Load(hirateBoard, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := pos.SetTurn(1); err != nil { // 先手番
		t.Fatalf("SetTurn: %v", err)
	}
	st, err := s.Adopt()
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if st.Black != "" || st.White != "" {
		t.Errorf("採った局面に前の対局者が残っています: black=%q white=%q", st.Black, st.White)
	}
}
