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
	if !st.MateProblem {
		t.Error("詰将棋として始まっていません")
	}
	if st.HandsFixed {
		t.Error("手合割になっています（詰将棋とは排他）")
	}
}

// 「詰将棋」は操作ではなく**属性**であること（2026-09-12）。
//
// ⚠️ **立てた時点で、余った駒が全部 玉方（後手）の持駒になる**（駒台を 1 枚ずつ
// 埋める作業が要らない）。⚠️ **盤を直すたびに追随すること**が属性である意味で、
// **1 枚外したらその駒がそのまま玉方の持駒に増える。**
// ⚠️ **手番は攻方＝先手**・⚠️ **目線は先手固定**も一緒に決まる。
func TestSetMateProblemGivesRestToWhite(t *testing.T) {
	// 撮った詰将棋のつもり（玉方の玉 1 枚と攻方の金 1 枚）。
	s := edited(t, "4k4/9/4G4/9/9/9/9/9/9")
	if _, err := s.SetViewpoint(true); err != nil { // 後手目線で撮ったつもり
		t.Fatalf("SetViewpoint: %v", err)
	}
	if before := s.State(); before.SFEN != "" {
		t.Fatalf("並べただけで確定しています: %q", before.SFEN)
	}

	st, err := s.SetMateProblem(true)
	if err != nil {
		t.Fatalf("SetMateProblem: %v", err)
	}
	if !st.MateProblem {
		t.Fatal("詰将棋になっていません")
	}
	if st.Turn != int(position.TurnBlack) {
		t.Errorf("Turn = %d, want %d(攻方＝先手)", st.Turn, position.TurnBlack)
	}
	if st.NearWhite {
		t.Error("目線が先手固定になっていません")
	}
	if st.SFEN == "" {
		t.Fatal("詰将棋にしたのに確定しません（余りが玉方へ回っていない）")
	}
	handWhite := func(st EditState, name string) int {
		for _, v := range st.Inventory {
			if v.Name == name {
				return v.HandWhite
			}
		}
		return -1
	}
	if got := handWhite(st, "歩"); got != 18 {
		t.Errorf("玉方の歩 = %d, want 18", got)
	}
	for _, v := range st.Inventory {
		// ⚠️ **玉だけは未決が残って正しい** —— 詰将棋に攻方の玉は無く、
		// 玉は駒台に載らないので、**「足りない駒」に玉が出るのが正しい姿**
		// （置きたければそこから置ける）。**確定は止めない。**
		if v.Name == "玉" {
			continue
		}
		if v.Unassigned != 0 || v.Unused != 0 {
			t.Errorf("%s: 未決=%d 使わない=%d, want 0, 0", v.Name, v.Unassigned, v.Unused)
		}
	}

	// ⚠️ **盤を直すと追随すること**（属性である意味そのもの）。金を外せば
	// その金が玉方の持駒になる。
	after, err := s.Remove(2, 4)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := handWhite(after, "金"); got != 4 {
		t.Errorf("外した金が玉方に回っていません: %d, want 4", got)
	}
}

// ⚠️ **手合割と詰将棋は排他**（同じ問いへの別の答え）。
func TestMateProblemAndHandicapAreExclusive(t *testing.T) {
	s := edited(t, nimaiBoard)
	if _, err := s.SetHandsFixed(true); err != nil {
		t.Fatalf("SetHandsFixed: %v", err)
	}
	st, err := s.SetMateProblem(true)
	if err != nil {
		t.Fatalf("SetMateProblem: %v", err)
	}
	if st.HandsFixed {
		t.Error("詰将棋にしたのに手合割が残っています")
	}
	st, err = s.SetHandsFixed(true)
	if err != nil {
		t.Fatalf("SetHandsFixed: %v", err)
	}
	if st.MateProblem {
		t.Error("手合割にしたのに詰将棋が残っています")
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
	if _, err := s.SetMateProblem(true); err != nil {
		t.Fatalf("SetMateProblem: %v", err)
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

// ⚠️ **詰将棋でも駒台の駒を掴めること**（2026-09-12 に実機で踏んだ回帰）。
//
// 余りを「計算で見せる」だけにしていたときは、**駒台に並んで見えるのに Go 側の
// 割り振りは 0 枚**だったので、`FromHand`（駒台 → 盤）が毎回「駒台にありません」で
// 落ち、**駒台から 1 枚も動かせなかった。** 実体として載せること。
//
// ⚠️ **「攻方に持ち駒が無い」わけではない**ことも一緒に見ている。攻方の駒台に
// 載せれば、そのぶん玉方の持駒が減るだけ（玉方 = 合計 − 攻方）。
func TestMateProblemHandsAreReal(t *testing.T) {
	const pawn = 0 // 歩（position/sfen の駒コード）

	s := NewPositionService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	st, err := s.LoadEmpty()
	if err != nil {
		t.Fatalf("LoadEmpty: %v", err)
	}
	handOf := func(st EditState, name string) (black, white int) {
		for _, v := range st.Inventory {
			if v.Name == name {
				return v.HandBlack, v.HandWhite
			}
		}
		return -1, -1
	}
	if _, w := handOf(st, "歩"); w != 18 {
		t.Fatalf("玉方の歩 = %d, want 18", w)
	}

	// **駒台から盤へ打てること**（ここが落ちていた）。
	st, err = s.FromHand(4, 4, pawn, false)
	if err != nil {
		t.Fatalf("FromHand: %v（駒台から掴めない）", err)
	}
	if _, w := handOf(st, "歩"); w != 17 {
		t.Errorf("打ったあとの玉方の歩 = %d, want 17", w)
	}

	// **攻方の駒を盤に置くと、そのぶん玉方の持駒が減ること**
	// （足すだけにすると「玉方に多すぎる」警告が出続ける）。
	st, err = s.Place(8, 4, pawn, true, false)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if _, w := handOf(st, "歩"); w != 16 {
		t.Errorf("攻方の駒を置いたあとの玉方の歩 = %d, want 16", w)
	}

	// **攻方にも持ち駒を持たせられること**（詰将棋の攻方の持駒はここで決まる）。
	st, err = s.SetHand(pawn, true, 2)
	if err != nil {
		t.Fatalf("SetHand: %v", err)
	}
	if b, w := handOf(st, "歩"); b != 2 || w != 14 {
		t.Errorf("攻方の歩 = %d（want 2）/ 玉方の歩 = %d（want 14）", b, w)
	}
	// **その状態でも確定できること。**
	if st.SFEN == "" {
		t.Error("確定できません")
	}
}
