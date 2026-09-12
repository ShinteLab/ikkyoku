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

// 手合割の初期局面から訂正を始められること（入力タブ →「駒を落として並べる」）。
//
// ⚠️ **最初から手合割として始まること**が要点。逆算から始めると、落とした駒が
// 未決として出てきて**そのままでは確定できない**（＝独自ハンデを作る入口として
// 使えない）。手番が上手（後手）であることも一緒に見ている。
func TestLoadHandicapStartsAsHandicap(t *testing.T) {
	s := NewPositionService(slog.New(slog.NewTextHandler(io.Discard, nil)))

	st, err := s.LoadHandicap("二枚落ち")
	if err != nil {
		t.Fatalf("LoadHandicap: %v", err)
	}
	if !st.HandsFixed {
		t.Fatal("手合割として始まっていません（逆算したままでは確定できない）")
	}
	if st.BoardSFEN != nimaiBoard {
		t.Errorf("BoardSFEN = %q, want %q", st.BoardSFEN, nimaiBoard)
	}
	if st.Turn != int(position.TurnWhite) {
		t.Errorf("Turn = %d, want %d(上手＝後手から)", st.Turn, position.TurnWhite)
	}
	// **画像が無いので目線は先手目線**（残っていると解析へ渡すときに勝手に回る）。
	if st.NearWhite {
		t.Error("目線が後手のままです")
	}
	// **そのまま確定できること**（訂正するものが無ければ、すぐ解析へ渡せる）。
	if st.SFEN == "" {
		t.Error("手合割の初期局面が確定できません")
	}
}

// ⚠️ **撮った局面の目線を、手合割から並べ直しても引きずらないこと。**
func TestLoadHandicapClearsViewpoint(t *testing.T) {
	s := edited(t, nimaiBoard)
	if _, err := s.SetViewpoint(true); err != nil {
		t.Fatalf("SetViewpoint: %v", err)
	}
	st, err := s.LoadHandicap("平手")
	if err != nil {
		t.Fatalf("LoadHandicap: %v", err)
	}
	if st.NearWhite {
		t.Error("前の目線が残っています")
	}
}

// ⚠️ **「訂正を捨てて戻す」も手合割のまま戻すこと**（何度でも）。
// 盤面 SFEN から作り直すと駒台の逆算が復活し、**捨てた拍子に確定できなくなる。**
func TestResetKeepsHandicap(t *testing.T) {
	s := NewPositionService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := s.LoadHandicap("二枚落ち"); err != nil {
		t.Fatalf("LoadHandicap: %v", err)
	}
	// 独自ハンデのつもりで 1 枚外す（9 筋の香）。
	if _, err := s.Remove(0, 0); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for i := 1; i <= 2; i++ { // 2 回目でも外れないこと
		st, err := s.Reset()
		if err != nil {
			t.Fatalf("Reset(%d 回目): %v", i, err)
		}
		if !st.HandsFixed {
			t.Fatalf("%d 回目の Reset で手合割が外れました", i)
		}
		if st.BoardSFEN != nimaiBoard {
			t.Errorf("%d 回目の Reset: BoardSFEN = %q, want %q", i, st.BoardSFEN, nimaiBoard)
		}
	}
}

// 詰将棋を並べ始められること（入力タブの「詰将棋を並べる」）。
//
// ⚠️ **手番は攻方＝先手**（盤が空でも手番は決まっている）。⚠️ **駒台は逆算する**
// （詰将棋の「残り全部は玉方の持駒」がまさに逆算。手合割とは逆なので混同しないこと）。
func TestLoadEmptyStartsProblem(t *testing.T) {
	s := NewPositionService(slog.New(slog.NewTextHandler(io.Discard, nil)))

	st, err := s.LoadEmpty()
	if err != nil {
		t.Fatalf("LoadEmpty: %v", err)
	}
	if st.BoardSFEN != "9/9/9/9/9/9/9/9/9" {
		t.Errorf("BoardSFEN = %q, want 空の盤", st.BoardSFEN)
	}
	if st.Turn != int(position.TurnBlack) {
		t.Errorf("Turn = %d, want %d(攻方＝先手)", st.Turn, position.TurnBlack)
	}
	if st.HandsFixed {
		t.Error("駒台を逆算しない状態で始まっています（詰将棋は逆算する）")
	}
}

// 「残り全部を片側の駒台へ」がまとめて効くこと（詰将棋の慣習）。
//
// ⚠️ **玉は載せないこと**（駒台に乗らない）。⚠️ **寄せたら確定できること**
// が目的そのもの —— 歩 17 枚を 1 枚ずつドラッグさせないために入れた操作なので、
// 押したあとに未決が残っていては意味が無い。
func TestFillHandsSweepsRest(t *testing.T) {
	// 詰将棋のつもりの盤（玉方の玉が 1 枚と、攻方の金が 1 枚だけ）。
	s := edited(t, "4k4/9/4G4/9/9/9/9/9/9")
	if _, err := s.SetTurn(int(position.TurnBlack)); err != nil { // 攻方から
		t.Fatalf("SetTurn: %v", err)
	}
	// **手番を決めても確定しない**（駒台の未決が残っているため）。ここが
	// 「1 枚ずつドラッグさせない」操作の要る理由そのもの。
	if before := s.State(); before.SFEN != "" {
		t.Fatalf("駒台が未決なのに確定しています: %q", before.SFEN)
	}

	st, err := s.FillHands(false) // 玉方（奥＝後手）へ寄せる
	if err != nil {
		t.Fatalf("FillHands: %v", err)
	}
	if st.SFEN == "" {
		t.Fatal("寄せたのに確定しません（未決が残っている）")
	}
	for _, v := range st.Inventory {
		if v.Name == "玉" {
			if v.HandBlack != 0 || v.HandWhite != 0 {
				t.Errorf("玉が駒台に載りました: 先手%d 後手%d", v.HandBlack, v.HandWhite)
			}
			continue
		}
		if v.Unassigned != 0 {
			t.Errorf("%s の未決が残っています: %d", v.Name, v.Unassigned)
		}
	}
	// 歩は 18 枚全部が玉方の持駒になる。
	for _, v := range st.Inventory {
		if v.Name == "歩" && v.HandWhite != 18 {
			t.Errorf("後手の駒台の歩 = %d, want 18", v.HandWhite)
		}
	}

	// 2 回目は寄せるものが無い（押しても何も起きないことを理由付きで返す）。
	if _, err := s.FillHands(false); err == nil {
		t.Error("寄せるものが無いのにエラーになりません")
	}
}

// ⚠️ **詰将棋も解析タブへ渡せて、手も進められること**（設計原則3）。
//
// **玉が 1 枚でも合法手は出る**（実測。攻方の玉が無くても、攻方の駒の動きは
// 決まる）。断られるのは**エンジンにかけるところだけ**で（`analyze.ensurePlayable`。
// 玉の揃った局面しか読めない）、**盤を見ることも手を進めることも止めない。**
// ⚠️ **ここが通らなくなったら、詰将棋を並べる意味がほぼ無くなる**ので、
// 「玉が 1 枚なら断る」を手前の層に足さないこと。
func TestAdoptMateProblemKeepsBoardWithoutMoves(t *testing.T) {
	s := edited(t, "4k4/9/4G4/9/9/9/9/9/9")
	if _, err := s.SetTurn(int(position.TurnBlack)); err != nil {
		t.Fatalf("SetTurn: %v", err)
	}
	if _, err := s.FillHands(false); err != nil {
		t.Fatalf("FillHands: %v", err)
	}

	study := NewStudyService(slog.New(slog.NewTextHandler(io.Discard, nil)), s)
	st, err := study.Adopt()
	if err != nil {
		t.Fatalf("Adopt: %v（詰将棋を断ってはいけない）", err)
	}
	if !st.Loaded || st.BoardSFEN == "" {
		t.Fatal("盤が出ていません")
	}
	if st.LegalError != "" {
		t.Errorf("合法手が出せませんでした: %s", st.LegalError)
	}
	if len(st.Legal) == 0 {
		t.Error("手が 1 つも出ていません（詰将棋を並べても動かせない）")
	}
	// 手番は攻方。**寄せた持ち駒は玉方（後手）のもの**なので、攻方の打つ手は出ない。
	if st.Turn != int(position.TurnBlack) {
		t.Errorf("Turn = %d, want %d(攻方)", st.Turn, position.TurnBlack)
	}
}
