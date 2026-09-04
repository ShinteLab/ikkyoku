package app

import (
	"io"
	"log/slog"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// 撮った盤が後手目線だったときの局面（▲７六歩まで進んだ形を後手側から撮ったつもり）。
// **対称でない盤**でないと、回っているかどうかが分からない。
const (
	gotePovBoard = "lnsgkgsnl/1r5b1/ppppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL"
	rotatedBoard = "lnsgkgsnl/1r5b1/pppppp1pp/6p2/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"
)

// edited は盤を読み込んだ PositionService を返す。
func edited(t *testing.T, board string) *PositionService {
	t.Helper()
	s := NewPositionService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := s.Load(board); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

// ⚠️ **これが一番大事。** 目線を後手にしても、訂正タブの盤も、suteme へ送る
// 画像ラベルも 1 文字も変わらないこと（**ラベルは画素と一致していなければならない**）。
// 変わるのは「解析へ渡す SFEN」だけ。
func TestSetViewpointDoesNotTouchBoard(t *testing.T) {
	s := edited(t, gotePovBoard)
	before := s.State()

	st, err := s.SetViewpoint(true)
	if err != nil {
		t.Fatalf("SetViewpoint: %v", err)
	}
	if st.BoardSFEN != before.BoardSFEN {
		t.Fatalf("訂正タブの盤が回っている\n got=%s\nwant=%s", st.BoardSFEN, before.BoardSFEN)
	}
	if st.LabelSFEN != before.LabelSFEN {
		t.Fatalf("学習ラベルが回っている\n got=%s\nwant=%s", st.LabelSFEN, before.LabelSFEN)
	}
	if !st.NearWhite {
		t.Fatal("目線が保存されていない")
	}
}

// 目線と手番は独立した 2 つの事実。**目線を切り替えても手番は動かない。**
// 画面に出るのは「対局としての先後」なので、内部の見た目の手番のほうが入れ替わる。
func TestSetViewpointKeepsGameTurn(t *testing.T) {
	s := edited(t, gotePovBoard)
	st, err := s.SetTurn(int(position.TurnBlack)) // 先手番
	if err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	if st.SeenTurn != int(position.TurnBlack) {
		t.Fatal("先手目線・先手番なら上向きの側が指すはず")
	}

	st, err = s.SetViewpoint(true)
	if err != nil {
		t.Fatalf("SetViewpoint: %v", err)
	}
	if st.Turn != int(position.TurnBlack) {
		t.Fatalf("目線を変えたら手番まで変わった: %s", st.TurnLabel)
	}
	// 後手目線で先手番なら、指すのは**奥**（手前に写っているのは後手）。
	if st.SeenTurn != int(position.TurnWhite) {
		t.Fatal("後手目線・先手番なら奥から指すはず")
	}

	// 後手番に変えれば手前から指す。
	st, err = s.SetTurn(int(position.TurnWhite))
	if err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	if st.SeenTurn != int(position.TurnBlack) {
		t.Fatal("後手目線・後手番なら手前から指すはず")
	}

	// 先手目線へ戻しても手番はそのまま。
	st, err = s.SetViewpoint(false)
	if err != nil {
		t.Fatalf("SetViewpoint: %v", err)
	}
	if st.Turn != int(position.TurnWhite) {
		t.Fatalf("目線を戻したら手番まで変わった: %s", st.TurnLabel)
	}
	if st.SeenTurn != int(position.TurnWhite) {
		t.Fatal("先手目線・後手番なら奥から指すはず")
	}
}

// 手番が未決なら、目線を切り替えても未決のまま（決めていないことを決めない）。
func TestSetViewpointKeepsUnknownTurn(t *testing.T) {
	s := edited(t, gotePovBoard)
	st, err := s.SetViewpoint(true)
	if err != nil {
		t.Fatalf("SetViewpoint: %v", err)
	}
	if st.Turn != int(position.TurnUnknown) {
		t.Fatalf("未決の手番が %s になった", st.TurnLabel)
	}
	if st.SeenTurn != int(position.TurnUnknown) {
		t.Fatal("手番が未決なのに見た目の手番が決まっている")
	}
}

// 解析へ渡す SFEN は**後手目線のときだけ**画面の SFEN と違う。
// ⚠️ **同じにしてしまうと、上下逆の局面をそのままエンジンに渡すことになる。**
func TestAnalyzeSFENRotatesOnlyForGotePOV(t *testing.T) {
	s := edited(t, gotePovBoard)
	if _, err := s.SetTurn(int(position.TurnBlack)); err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	st := s.State()
	if st.AnalyzeSFEN != st.SFEN {
		t.Fatalf("先手目線で回っている\n got=%s\nwant=%s", st.AnalyzeSFEN, st.SFEN)
	}

	st, err := s.SetViewpoint(true)
	if err != nil {
		t.Fatalf("SetViewpoint: %v", err)
	}
	if want := gotePovBoard + " w - 1"; st.SFEN != want {
		t.Fatalf("画面の SFEN は撮った向きのまま\n got=%s\nwant=%s", st.SFEN, want)
	}
	if want := rotatedBoard + " b - 1"; st.AnalyzeSFEN != want {
		t.Fatalf("解析へ渡す SFEN が違う\n got=%s\nwant=%s", st.AnalyzeSFEN, want)
	}
}

// 採ると解析タブには**回した局面**が入る（訂正タブは撮った向きのまま）。
func TestAdoptRotatesForGotePOV(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pos := edited(t, gotePovBoard)
	if _, err := pos.SetTurn(int(position.TurnBlack)); err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	if _, err := pos.SetViewpoint(true); err != nil {
		t.Fatalf("SetViewpoint: %v", err)
	}

	study := NewStudyService(logger, pos)
	st, err := study.Adopt()
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if st.BoardSFEN != rotatedBoard {
		t.Fatalf("解析タブの盤が回っていない\n got=%s\nwant=%s", st.BoardSFEN, rotatedBoard)
	}
	if st.Turn != int(position.TurnBlack) {
		t.Fatalf("解析タブの手番が違う: %s", st.TurnLabel)
	}
	// 訂正タブは撮ったとおりのまま（採っても動かない）。
	if got := pos.State().BoardSFEN; got != gotePovBoard {
		t.Fatalf("採ったら訂正タブの盤まで回った: %s", got)
	}
}
