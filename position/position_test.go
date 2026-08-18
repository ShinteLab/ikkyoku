package position

import (
	"strings"
	"testing"

	"github.com/ShinteLab/core/sfen"
)

// 初期局面は駒台が空。手番を決めれば SFEN が組み上がる。
func TestSFEN(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}

	// 手番未決では組み立てない（先手に倒さない）。
	if _, err := p.SFEN(); err == nil {
		t.Fatal("手番が未決なのに SFEN が返りました")
	}
	// 盤面だけなら手番が未決でも取れる。
	if got := p.BoardSFEN(); got != initialBoard {
		t.Errorf("BoardSFEN() = %q, want %q", got, initialBoard)
	}

	p.Turn = TurnBlack
	got, err := p.SFEN()
	if err != nil {
		t.Fatal(err)
	}
	if want := initialBoard + " b - 1"; got != want {
		t.Errorf("SFEN() = %q, want %q", got, want)
	}

	// 手数は指定があればそれを書く。
	p.Turn = TurnWhite
	p.MoveNumber = 42
	got, err = p.SFEN()
	if err != nil {
		t.Fatal(err)
	}
	if want := initialBoard + " w - 42"; got != want {
		t.Errorf("SFEN() = %q, want %q", got, want)
	}
}

// 盤から駒を抜くと、その分が駒台の逆算に出ること（core/sfen がやっている）。
func TestHandTotalFollowsBoard(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	if n := p.HandTotal()[sfen.Pawn]; n != 0 {
		t.Fatalf("初期局面の駒台に歩が %d 枚あります", n)
	}

	// 先手の歩を 1 枚消す（訂正で消したのと同じ）。
	if err := p.Board.Set(6, 0, Cell{}); err != nil {
		t.Fatal(err)
	}
	if n := p.HandTotal()[sfen.Pawn]; n != 1 {
		t.Errorf("HandTotal[歩] = %d, want 1", n)
	}
}

// 駒台の割り振り。**合計は盤から逆算されるので、割り振りで壊れないこと**が肝。
func TestHandsSplit(t *testing.T) {
	// 歩を 2 枚、盤から抜いた局面。
	p, err := FromBoardSFEN("lnsgkgsnl/1r5b1/1ppppppp1/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL")
	if err != nil {
		t.Fatal(err)
	}
	if n := p.HandTotal()[sfen.Pawn]; n != 2 {
		t.Fatalf("HandTotal[歩] = %d, want 2", n)
	}

	// 既定はどちらにも入らない。**先後不明を片側に寄せない**（設計原則5）。
	black, white := p.Hands()
	if black[sfen.Pawn] != 0 || white[sfen.Pawn] != 0 {
		t.Errorf("既定の割り振り = 先手%d/後手%d, want 0/0", black[sfen.Pawn], white[sfen.Pawn])
	}
	if n := p.Unassigned()[sfen.Pawn]; n != 2 {
		t.Errorf("未割り当て = %d, want 2", n)
	}

	// 割り振りが残っているあいだは SFEN を組み立てない。
	p.Turn = TurnBlack
	if _, err := p.SFEN(); err == nil {
		t.Fatal("駒台の先後が未決なのに SFEN が返りました")
	}

	if err := p.SetHand(sfen.Pawn, true, 2); err != nil {
		t.Fatal(err)
	}
	black, white = p.Hands()
	if black[sfen.Pawn] != 2 || white[sfen.Pawn] != 0 {
		t.Errorf("割り振り = 先手%d/後手%d, want 2/0", black[sfen.Pawn], white[sfen.Pawn])
	}
	if len(p.Unassigned()) != 0 {
		t.Errorf("未割り当てが残っています: %v", p.Unassigned())
	}

	got, err := p.SFEN()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, " 2P ") {
		t.Errorf("SFEN() = %q に先手の歩 2 枚が出ていません", got)
	}

	if err := p.SetHand(sfen.King, true, 1); err == nil {
		t.Error("玉が駒台に入りました")
	}

	// **足りている駒でも駒台に載せられること。** ここで止めると、余計な駒を盤から
	// 外す前に正しい持ち駒を載せられず、駒台から先後を決める操作が詰む。
	if err := p.SetHand(sfen.Pawn, false, 1); err != nil {
		t.Fatalf("足りている駒を駒台に載せられませんでした: %v", err)
	}
	black, white = p.Hands()
	if black[sfen.Pawn] != 2 || white[sfen.Pawn] != 1 {
		t.Errorf("割り振り = 先手%d/後手%d, want 2/1（丸めてはいけない）",
			black[sfen.Pawn], white[sfen.Pawn])
	}
	// 多すぎることは警告に出す（止めない。設計原則3・4）。
	if !hasWarning(p.Warnings(), "歩が 19枚あります") {
		t.Errorf("駒台まで数えた超過が警告に出ていません: %v", p.Warnings())
	}

	// 訂正で盤の枚数が変わっても、割り振りは人が決めたまま残る。
	if err := p.Board.Set(6, 0, Cell{}); err != nil { // 歩をもう 1 枚消す → 逆算 3
		t.Fatal(err)
	}
	black, white = p.Hands()
	if black[sfen.Pawn]+white[sfen.Pawn] != 3 {
		t.Errorf("割り振りの合計 = %d, want 3", black[sfen.Pawn]+white[sfen.Pawn])
	}
	if len(p.Warnings()) != 0 {
		t.Errorf("盤上 15 + 駒台 3 = 上限内なのに警告が出ました: %v", p.Warnings())
	}
}

func hasWarning(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

// LabelSFEN は suteme への登録用。**局面が確定していなくても持ち駒を落とさない。**
//
// 以前これを `SFEN()` で作っていて、手番か駒台の先後が未決だと空になり、
// 呼び出し側が盤面部分だけにフォールバックしていた（＝**持ち駒が黙って落ちていた**）。
// 学習に使われるのは盤面部分だけだが、送った持ち駒は suteme 側に保持されるので
// 落としてよい情報ではない。
func TestLabelSFENKeepsHands(t *testing.T) {
	// 歩を 2 枚、盤から抜いた局面。
	p, err := FromBoardSFEN("lnsgkgsnl/1r5b1/1ppppppp1/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetHand(sfen.Pawn, true, 1); err != nil {
		t.Fatal(err)
	}

	// 手番も残り 1 枚の持ち主も未決。**それでも組み上がること**が要点。
	got, notes := p.LabelSFEN()
	if _, err := p.SFEN(); err == nil {
		t.Fatal("この時点では SFEN() は組み上がらないはず（前提が変わっている）")
	}
	if !strings.Contains(got, " P ") {
		t.Errorf("LabelSFEN() = %q に先手の歩 1 枚が出ていません", got)
	}
	// 手番が未決なら b と書く（SFEN は位置で意味が決まるので、持ち駒を書くには要る）。
	if f := strings.Fields(got); len(f) != 4 || f[1] != "b" || f[3] != "1" {
		t.Errorf("LabelSFEN() = %q（4 フィールド・手番 b・手数 1 を期待）", got)
	}
	// **妥協した点は必ず出す**（黙って捨てない）。手番と未決の持ち駒で 2 件。
	if len(notes) != 2 {
		t.Errorf("notes = %v, want 2 件（手番未決 / 先後未決の持ち駒）", notes)
	}

	// 全部決めれば SFEN() と一致する。**2 つの組み立てが食い違わないこと。**
	p.Turn = TurnWhite
	p.MoveNumber = 42
	if err := p.SetHand(sfen.Pawn, false, 1); err != nil {
		t.Fatal(err)
	}
	full, err := p.SFEN()
	if err != nil {
		t.Fatal(err)
	}
	got, notes = p.LabelSFEN()
	if got != full {
		t.Errorf("LabelSFEN() = %q, SFEN() = %q（確定後は一致すること）", got, full)
	}
	if len(notes) != 0 {
		t.Errorf("確定しているのに妥協点が出ています: %v", notes)
	}
}

// 直している最中の盤は壊れて当たり前。**エラーではなく警告として出す**（設計原則3）。
func TestWarnings(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings()) != 0 {
		t.Errorf("初期局面に警告が出ました: %v", p.Warnings())
	}

	// 二歩にする。
	pawn, err := NewCell(sfen.Pawn, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Board.Set(5, 0, pawn); err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings()) == 0 {
		t.Fatal("二歩なのに警告が出ません")
	}
	found := false
	for _, v := range p.Violations() {
		if v.Check == sfen.CheckDoubledPawn {
			found = true
		}
	}
	if !found {
		t.Errorf("二歩の違反が入っていません: %v", p.Warnings())
	}
}

// 「足りない駒」も警告に出す（2026-08-18）。
//
// **過剰だけを言って不足を黙っていると、何をすれば確定するのかが警告から読めない。**
// 盤にも駒台にも無い駒は、駒数保存則からするとどちらかの駒台にあるはずで、
// **そのあいだ局面は確定しない**（SFEN が組み上がらない）。
func TestMissingWarnings(t *testing.T) {
	p, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	// 先手の歩を 1 枚外す → 盤上 17 枚。駒台に載せるまでは「足りない」。
	if err := p.Board.Set(6, 0, Cell{}); err != nil {
		t.Fatal(err)
	}
	if !hasWarning(p.Warnings(), "歩が 1枚足りません") {
		t.Errorf("足りない駒が警告に出ていません: %v", p.Warnings())
	}
	// 駒台に割り振れば足りている（＝置き場所が決まった）。
	if err := p.SetHand(sfen.Pawn, true, 1); err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings()) != 0 {
		t.Errorf("駒台に割り振ったのに警告が残りました: %v", p.Warnings())
	}

	// ⚠️ **過剰と二重に言わない。** 盤上が上限を超えているぶんは core/sfen の担当で、
	// こちらは「足りない」だけを言う。
	over, err := FromBoardSFEN(initialBoard)
	if err != nil {
		t.Fatal(err)
	}
	lance, err := NewCell(sfen.Lance, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := over.Board.Set(4, 4, lance); err != nil {
		t.Fatal(err)
	}
	if hasWarning(over.Warnings(), "香が") && hasWarning(over.Warnings(), "足りません") {
		t.Errorf("過剰な駒を「足りない」と言っています: %v", over.Warnings())
	}

	// ⚠️ **駒台が書いてある局面（駒落ち）では言わない。** 盤にも駒台にも無い駒が
	// あって正常なので、言うと毎回警告が出る。
	// 二枚落ち（上手の飛車・角が盤にも駒台にも無い）。
	fixed, err := FromFullSFEN("lnsgkgsnl/9/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL w - 1")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range fixed.Warnings() {
		if strings.Contains(w, "足りません") {
			t.Errorf("駒台が書いてある局面で不足を言っています: %v", fixed.Warnings())
			break
		}
	}
}

func TestClone(t *testing.T) {
	p, err := FromBoardSFEN("lnsgkgsnl/1r5b1/1ppppppp1/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL")
	if err != nil {
		t.Fatal(err)
	}
	p.Turn = TurnBlack
	if err := p.SetHand(sfen.Pawn, true, 2); err != nil {
		t.Fatal(err)
	}

	c := p.Clone()
	if err := c.SetHand(sfen.Pawn, true, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Board.Set(0, 0, Cell{}); err != nil {
		t.Fatal(err)
	}

	black, _ := p.Hands()
	if black[sfen.Pawn] != 2 {
		t.Error("複製の割り振りを変えたら元まで変わりました")
	}
	if p.BoardSFEN() != "lnsgkgsnl/1r5b1/1ppppppp1/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL" {
		t.Error("複製の盤を直したら元まで変わりました")
	}
}

func TestTurnString(t *testing.T) {
	for turn, want := range map[Turn]string{
		TurnUnknown: "手番不明",
		TurnBlack:   "先手番",
		TurnWhite:   "後手番",
	} {
		if got := turn.String(); got != want {
			t.Errorf("Turn(%d).String() = %q, want %q", turn, got, want)
		}
	}
}
