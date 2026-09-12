package app

import (
	"io"
	"log/slog"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// 二枚落ちの盤（上手の飛角が無い）。**撮った画像から来たつもり**なので盤面だけ。
const nimaiBoard = "lnsgkgsnl/9/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"

// ⚠️ **これが 2 番目の口の全部。** 落ちている駒がある局面は、駒台を逆算したままだと
// **確定できない**（飛車落ちの飛車はどちらの駒台にも無いので未決が消えない）。
// `SetHandsFixed(true)` でその逆算をやめると SFEN が組み上がり、解析タブへ渡せる。
//
// **逆算をやめる前は確定できないこと**も一緒に見ている —— ここが通ってしまうなら、
// この操作は要らないことになる。
func TestSetHandsFixedMakesHandicapPositionAdoptable(t *testing.T) {
	s := edited(t, nimaiBoard)
	if _, err := s.SetTurn(int(position.TurnWhite)); err != nil { // 駒落ちは上手から
		t.Fatalf("SetTurn: %v", err)
	}

	// 逆算したまま＝飛角が「どちらかの駒台にあるはず」の未決として残る。
	before := s.State()
	if before.SFEN != "" {
		t.Fatalf("逆算したままなのに確定しています: %q", before.SFEN)
	}
	if before.HandsFixed {
		t.Fatal("既定で HandsFixed が立っています（逆算が訂正の拠り所）")
	}

	st, err := s.SetHandsFixed(true)
	if err != nil {
		t.Fatalf("SetHandsFixed: %v", err)
	}
	if !st.HandsFixed {
		t.Fatal("HandsFixed が立っていません")
	}
	if st.SFEN == "" {
		t.Fatal("逆算をやめたのに SFEN が組み上がりません（＝解析タブへ渡せない）")
	}
	// **持ち駒は空**（落とした駒は「使わない駒」であって、誰の持ち駒でもない）。
	if want := nimaiBoard + " w - 1"; st.SFEN != want {
		t.Errorf("SFEN = %q, want %q", st.SFEN, want)
	}

	// ⚠️ **盤は 1 マスも動かないこと**（これは訂正の操作ではなく、解釈の切り替え）。
	if st.BoardSFEN != before.BoardSFEN {
		t.Errorf("盤が動きました: %q -> %q", before.BoardSFEN, st.BoardSFEN)
	}

	// 落とした飛角は「使わない駒」として**数が残る**（消すと外し忘れと区別が付かない）。
	unused := map[string]int{}
	for _, v := range st.Inventory {
		if v.Unused > 0 {
			unused[v.Name] = v.Unused
		}
		if v.Unassigned != 0 {
			t.Errorf("%s の未決が残っています: %d", v.Name, v.Unassigned)
		}
	}
	if len(unused) != 2 || unused["飛"] != 1 || unused["角"] != 1 {
		t.Errorf("使わない駒 = %v, want 飛1 角1", unused)
	}

	// ⚠️ **ここまで通って初めて意味がある。** 解析タブが根として受け取り、
	// 合法手まで出ること（＝駒を押せば手が進む）。
	study := NewStudyService(slog.New(slog.NewTextHandler(io.Discard, nil)), s)
	got, err := study.Adopt()
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got.Turn != int(position.TurnWhite) {
		t.Errorf("Turn = %d, want %d(後手番。駒落ちは上手から)", got.Turn, position.TurnWhite)
	}
	if got.LegalError != "" || len(got.Legal) == 0 {
		t.Errorf("合法手が出ません: err=%q n=%d", got.LegalError, len(got.Legal))
	}
}

// ⚠️ **戻せること。** 押し間違えても失うものが無いのが前提で入れた操作なので、
// false に戻したら逆算が復活して未決に戻る。
func TestSetHandsFixedIsReversible(t *testing.T) {
	s := edited(t, nimaiBoard)
	if _, err := s.SetHandsFixed(true); err != nil {
		t.Fatalf("SetHandsFixed(true): %v", err)
	}
	st, err := s.SetHandsFixed(false)
	if err != nil {
		t.Fatalf("SetHandsFixed(false): %v", err)
	}
	if st.HandsFixed {
		t.Fatal("HandsFixed が戻っていません")
	}
	for _, v := range st.Inventory {
		if v.Unused != 0 {
			t.Errorf("%s が使わない駒のまま残っています: %d", v.Name, v.Unused)
		}
	}
	if st.SFEN != "" {
		t.Errorf("逆算が戻ったのに確定しています: %q（未決があるはず）", st.SFEN)
	}
}

// ⚠️ **既に駒台へ割り振ったぶんは残ること。** 解釈を切り替えた拍子に、人が決めた
// 持ち駒を捨てない（詰将棋で「残りは玉方の持駒」と決めてから立てる順序もありうる）。
func TestSetHandsFixedKeepsAssignedHands(t *testing.T) {
	s := edited(t, nimaiBoard)
	if _, err := s.SetHand(0, false, 2); err != nil { // 後手の駒台に歩 2 枚（0=歩）
		t.Fatalf("SetHand: %v", err)
	}
	st, err := s.SetHandsFixed(true)
	if err != nil {
		t.Fatalf("SetHandsFixed: %v", err)
	}
	for _, v := range st.Inventory {
		if v.Name == "歩" && v.HandWhite != 2 {
			t.Errorf("後手の駒台の歩 = %d, want 2（割り振りを捨てている）", v.HandWhite)
		}
	}
}

// 局面がまだ無いなら断ること（他の操作と同じ）。
func TestSetHandsFixedWithoutPosition(t *testing.T) {
	s := NewPositionService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := s.SetHandsFixed(true); err == nil {
		t.Fatal("局面が無いのに通りました")
	}
}
