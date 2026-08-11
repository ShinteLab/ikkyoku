package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

const hirateBoard = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"

// adopted は初期局面を採った StudyService を返す。
func adopted(t *testing.T) *StudyService {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pos := NewPositionService(logger)
	if _, err := pos.Load(hirateBoard); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := pos.SetTurn(1); err != nil { // 先手番
		t.Fatalf("SetTurn: %v", err)
	}
	study := NewStudyService(logger, pos)
	if _, err := study.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	return study
}

// 採った直後は根に居て、合法手が出ていること。
// **合法手が出ないと、盤を押しても何も光らない**（画面が死んで見える）。
func TestStudyServiceAdoptHasLegalMoves(t *testing.T) {
	st := adopted(t).State()
	if !st.Loaded {
		t.Fatal("採ったのに Loaded が false です")
	}
	if st.Ply != 0 || len(st.Moves) != 0 {
		t.Errorf("採った直後に手順があります: ply=%d moves=%+v", st.Ply, st.Moves)
	}
	if len(st.Legal) != 30 {
		t.Errorf("合法手 = %d, want 30", len(st.Legal))
	}
	if st.LegalError != "" {
		t.Errorf("LegalError = %q", st.LegalError)
	}
	if st.RootSFEN != st.SFEN {
		t.Errorf("根に居るのに RootSFEN(%q) と SFEN(%q) が違います", st.RootSFEN, st.SFEN)
	}
}

// 指すと盤・手番・手順・合法手がまとめて追随すること。
func TestStudyServicePlay(t *testing.T) {
	s := adopted(t)
	st, err := s.Play("7g7f")
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if st.Ply != 1 || len(st.Moves) != 1 {
		t.Fatalf("手順が記録されていません: ply=%d moves=%+v", st.Ply, st.Moves)
	}
	if st.Moves[0].Text != "▲７六歩" {
		t.Errorf("手順の表記 = %q, want %q", st.Moves[0].Text, "▲７六歩")
	}
	if st.Turn != 2 {
		t.Errorf("手番 = %d, want 2（後手番）", st.Turn)
	}
	// **合法手は進めたあとの局面のもの。** 根のままだと嘘の移動先が光る。
	for _, m := range st.Legal {
		if m.USI == "7g7f" {
			t.Fatal("動かしたはずの手がまだ合法手に入っています")
		}
	}
	// ⚠️ **盤の SFEN も追随すること**（根を描き続けると、指したのに動かないように見える）。
	if !strings.HasPrefix(st.BoardSFEN, "lnsgkgsnl/1r5b1/ppppppppp/9/9/2P6/") {
		t.Errorf("盤が進んでいません: %q", st.BoardSFEN)
	}

	if _, err := s.Play("7f7e"); err == nil {
		t.Error("後手番なのに先手の手が指せました")
	}
}

// ⚠️ **解析に渡すのは「根 + そこまでの手順」。** 組み立て直した 1 つの SFEN を
// 渡すと、千日手と連続王手をエンジンが判定できない。
//
// **Current は別に返すこと** —— 手を進めても根は変わらないので、根で
// 「局面が変わった」を判定すると**前の手の評価値が今の盤の上に残る。**
func TestStudyServiceAnalyzeTarget(t *testing.T) {
	s := adopted(t)
	root, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	if len(root.Moves) != 0 {
		t.Errorf("根なのに手順があります: %v", root.Moves)
	}
	if root.Current != root.Root {
		t.Errorf("根なのに Current(%q) と Root(%q) が違います", root.Current, root.Root)
	}

	for _, m := range []string{"7g7f", "3c3d"} {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%s): %v", m, err)
		}
	}
	got, err := s.analyzeTarget()
	if err != nil {
		t.Fatalf("analyzeTarget: %v", err)
	}
	if got.Root != root.Root {
		t.Errorf("根が動いています: %q -> %q", root.Root, got.Root)
	}
	if strings.Join(got.Moves, " ") != "7g7f 3c3d" {
		t.Errorf("Moves = %v", got.Moves)
	}
	if got.Current == got.Root {
		t.Error("手を進めたのに Current が根のままです（結果が消えなくなる）")
	}
}

// 戻る操作の使い分け。**GoTo は手順を消さない / Undo は消す。**
func TestStudyServiceGoToAndUndo(t *testing.T) {
	s := adopted(t)
	for _, m := range []string{"7g7f", "3c3d"} {
		if _, err := s.Play(m); err != nil {
			t.Fatalf("Play(%s): %v", m, err)
		}
	}
	st, err := s.GoTo(1)
	if err != nil {
		t.Fatalf("GoTo: %v", err)
	}
	if st.Ply != 1 || len(st.Moves) != 2 {
		t.Errorf("GoTo で手順が消えました: ply=%d moves=%d", st.Ply, len(st.Moves))
	}
	if strings.Join(st.Played, " ") != "7g7f" {
		t.Errorf("Played = %v（先の手を渡さないこと）", st.Played)
	}

	st, err = s.Undo()
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if st.Ply != 0 || len(st.Moves) != 0 {
		t.Errorf("Undo で消えていません: ply=%d moves=%d", st.Ply, len(st.Moves))
	}
}

// 採り直したら手順ごと入れ替わること。**前の局面の手順を引き継がない。**
func TestStudyServiceAdoptResetsMoves(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pos := NewPositionService(logger)
	if _, err := pos.Load(hirateBoard); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := pos.SetTurn(1); err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	s := NewStudyService(logger, pos)
	if _, err := s.Adopt(); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if _, err := s.Play("7g7f"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	st, err := s.Adopt()
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if st.Ply != 0 || len(st.Moves) != 0 {
		t.Errorf("採り直したのに手順が残っています: ply=%d moves=%+v", st.Ply, st.Moves)
	}
}

// 何も採っていなければ手は指せない（**エラーで、落ちないこと**）。
func TestStudyServicePlayWithoutPosition(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewStudyService(logger, NewPositionService(logger))
	if _, err := s.Play("7g7f"); err == nil {
		t.Error("局面が無いのに指せました")
	}
	st := s.State()
	if st.Loaded {
		t.Error("局面が無いのに Loaded です")
	}
	if st.Legal == nil || st.Moves == nil || st.Played == nil {
		t.Error("空でも配列を返すこと（フロントが null を踏む）")
	}
}
