package position_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

const connectHirate = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL b - 1"

// mustPos は完全形 SFEN から確定局面を作る。
func mustPos(t *testing.T, s string) *position.Position {
	t.Helper()
	p, err := position.FromFullSFEN(s)
	if err != nil {
		t.Fatalf("FromFullSFEN(%q): %v", s, err)
	}
	return p
}

// advance は from から手を進めた**盤面だけ**を返す（認識器が返すものの模擬）。
func advance(t *testing.T, from *position.Position, moves ...string) string {
	t.Helper()
	p := from.Clone()
	for _, m := range moves {
		if err := p.ApplyMove(m); err != nil {
			t.Fatalf("ApplyMove(%q): %v", m, err)
		}
	}
	return p.Board.SFEN()
}

// after は SFEN から手を進めた**局面**を返す（繋ぐ元を作るのに使う）。
func after(t *testing.T, s string, moves ...string) *position.Position {
	t.Helper()
	p := mustPos(t, s)
	for _, m := range moves {
		if err := p.ApplyMove(m); err != nil {
			t.Fatalf("ApplyMove(%q): %v", m, err)
		}
	}
	return p
}

func connect(t *testing.T, from *position.Position, board string, opt position.ConnectOptions) position.ConnectResult {
	t.Helper()
	r, err := position.ConnectSFEN(context.Background(), from, board, opt)
	if err != nil {
		t.Fatalf("ConnectSFEN: %v", err)
	}
	return r
}

// 1 手で繋がる 4 通り（移動・取り・成り・打ち）。
//
// **ここが Phase 6 の土台。** 撮った盤面が 1 手ぶん進んでいたら、その 1 手が出ること。
func TestConnectOneMove(t *testing.T) {
	cases := []struct {
		name string
		from *position.Position
		move string
	}{
		{"移動", mustPos(t, connectHirate), "7g7f"},
		// 角交換（**取りと成りが同時に起きる手**）。
		{"取り", after(t, connectHirate, "7g7f", "3c3d"), "8h2b+"},
		// 先手の歩が 3 段目に居る（取らずに成れる）。
		{"成り", mustPos(t, "lnsgkgsnl/1r5b1/pppPppppp/9/9/9/1PPPPPPPP/1B5R1/LNSGKGSNL b - 1"), "6c6b+"},
		// ⚠️ **打ち先は二歩にならない筋**（9 筋の歩だけ駒台に載せてある）。
		{"打ち", mustPos(t, "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/1PPPPPPPP/1B5R1/LNSGKGSNL b P 1"), "P*9e"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := connect(t, c.from, advance(t, c.from, c.move), position.ConnectOptions{})
			if r.Stop != position.StopFound {
				t.Fatalf("Stop = %v, want StopFound", r.Stop)
			}
			if !r.Unique {
				t.Fatalf("一意になりませんでした: %v", r.Solutions)
			}
			if len(r.Moves) != 1 || r.Moves[0] != c.move {
				t.Fatalf("Moves = %v, want [%s]", r.Moves, c.move)
			}
			if r.Depth != 1 {
				t.Errorf("Depth = %d, want 1", r.Depth)
			}
		})
	}
}

// ⚠️ **盤面が同じなら 0 手（`StopSame`）。** 何も起きていないので木を触る理由が無い。
// **`StopFound` にしないこと** —— 空の手順を据えに行ってしまう。
func TestConnectSame(t *testing.T) {
	from := mustPos(t, connectHirate)
	r := connect(t, from, from.Board.SFEN(), position.ConnectOptions{})
	if r.Stop != position.StopSame {
		t.Fatalf("Stop = %v, want StopSame", r.Stop)
	}
	if r.Depth != 0 || len(r.Moves) != 0 || len(r.Solutions) != 0 {
		t.Fatalf("0 手のはずが中身があります: %+v", r)
	}
}

// 2 手・3 手が**正しい順**で返ること（CM の間に進んだぶんを埋める側）。
func TestConnectMultipleMoves(t *testing.T) {
	cases := [][]string{
		{"7g7f", "3c3d"},
		{"7g7f", "3c3d", "8h2b+"},
	}
	for _, moves := range cases {
		t.Run(strings.Join(moves, " "), func(t *testing.T) {
			from := mustPos(t, connectHirate)
			r := connect(t, from, advance(t, from, moves...), position.ConnectOptions{})
			if r.Stop != position.StopFound {
				t.Fatalf("Stop = %v, want StopFound", r.Stop)
			}
			if r.Depth != len(moves) {
				t.Fatalf("Depth = %d, want %d", r.Depth, len(moves))
			}
			// 順番が違えば同じ盤面に至る別解になるので、**一意なら順番も正しい**。
			if r.Unique && strings.Join(r.Moves, " ") != strings.Join(moves, " ") {
				t.Fatalf("Moves = %v, want %v", r.Moves, moves)
			}
			if !r.Unique {
				// 一意でなくても、正解が候補に入っていること。
				want := strings.Join(moves, " ")
				for _, s := range r.Solutions {
					if strings.Join(s, " ") == want {
						return
					}
				}
				t.Fatalf("正解 %v が候補に入っていません: %v", moves, r.Solutions)
			}
		})
	}
}

// ⚠️ **1 マスだけ違う盤面（＝認識の誤りの模擬）で繋がらないこと。**
//
// **これが「合法手で到達できない盤面は落ちる」という副作用フィルタの歯止め。**
// ここが緩むと、認識が外した局面がそのまま棋譜に入る。
func TestConnectRejectsMisrecognition(t *testing.T) {
	from := mustPos(t, connectHirate)
	// 7 筋の歩が**一気に 2 マス進んだ**盤面（歩は 1 マスずつしか進めない）。
	bad := "lnsgkgsnl/1r5b1/ppppppppp/9/2P6/9/PP1PPPPPP/1B5R1/LNSGKGSNL"
	r := connect(t, from, bad, position.ConnectOptions{})
	if r.Stop == position.StopFound {
		t.Fatalf("到達できない盤面が繋がってしまいました: %v", r.Solutions)
	}
}

// ⚠️ **駒が何枚も湧いた盤面は探索せずに `StopTooFar`。**
//
// 認識器は**余計な駒を作る**（実測で「L: 11枚（上限4）」）ので、ここが一番よくある
// 外し方。`MaxNodes` を 1 にしてあるのが肝で、**もし探索に入っていたら即座に上限へ
// 当たって `StopBudget` になる**。`StopTooFar` が返ること自体が
// 「1 節点も展開していない」ことの証明になる。
//
// ⚠️ **1 枚だけ湧いたときはここでは落ちない**（下界の上では「1 回打てば届く」ので）。
// あちらは `legal.Moves` が「その駒は持っていない」と言って落とす。
// **安い足切りであって、正しさの砦ではない。**
func TestConnectTooFarWithoutSearching(t *testing.T) {
	from := mustPos(t, connectHirate)
	// 5 段目に先手の香が 5 枚湧いた。
	bad := "lnsgkgsnl/1r5b1/ppppppppp/9/LLLLL4/9/PPPPPPPPP/1B5R1/LNSGKGSNL"
	r := connect(t, from, bad, position.ConnectOptions{MaxNodes: 1})
	if r.Stop != position.StopTooFar {
		t.Fatalf("Stop = %v, want StopTooFar（1 節点も展開しないはず）", r.Stop)
	}
	if r.Nodes != 0 {
		t.Errorf("Nodes = %d, want 0", r.Nodes)
	}
}

// ⚠️ **手番が逆だと 1 手では繋がらず、深く探しても一意にならないこと。**
//
// 相手の駒は動かせないので、**1 手の深さでは必ず落ちる**。ただし深く探せば
// 「相手が指して、自分が往復した」ような手順は出る —— ⚠️ **だから
// 「繋がらないこと」を手番の検算にしてはいけない。** 言えるのは
// **「一意にならない ＝ 断定して据えることはない」**までで、
// ⚠️ **逆の手番で勝手に試し直さないこと**（設計原則5）。
func TestConnectWrongTurnIsNotConfident(t *testing.T) {
	black := mustPos(t, connectHirate)
	target := advance(t, black, "7g7f") // 先手が指した盤面

	white := mustPos(t, strings.Replace(connectHirate, " b ", " w ", 1))
	if r := connect(t, white, target, position.ConnectOptions{MaxDepth: 1}); r.Stop == position.StopFound {
		t.Fatalf("手番が逆なのに 1 手で繋がりました: %v", r.Solutions)
	}
	r := connect(t, white, target, position.ConnectOptions{})
	if r.Unique {
		t.Fatalf("手番が逆なのに手順が 1 本に決まりました: %v", r.Moves)
	}
}

// ⚠️ **順番が決められないとき（transposition）は `Moves` を空にすること。**
//
// **一番大事なテスト。** ここが崩れると、空想の手順が「実際に現れた指し手」として
// 棋譜に残る（`TODO.md`「本譜のロック」が一番避けたい壊れ方）。
func TestConnectAmbiguousLeavesNoChoice(t *testing.T) {
	from := mustPos(t, connectHirate)
	// ▲7六歩 △3四歩 ▲2六歩 △8四歩 と ▲2六歩 △8四歩 ▲7六歩 △3四歩 は同じ盤面。
	board := advance(t, from, "7g7f", "3c3d", "2g2f", "8c8d")
	r := connect(t, from, board, position.ConnectOptions{})
	if r.Stop != position.StopFound {
		t.Fatalf("Stop = %v, want StopFound", r.Stop)
	}
	if r.Unique {
		t.Fatalf("順番が 1 通りに決まってしまいました: %v", r.Moves)
	}
	if len(r.Moves) != 0 {
		t.Fatalf("一意でないのに Moves が入っています: %v", r.Moves)
	}
	if len(r.Solutions) < 2 {
		t.Fatalf("候補が %d 本しかありません", len(r.Solutions))
	}
	// 候補はどれも同じ手数で、どれも本当に到達すること。
	for _, s := range r.Solutions {
		if len(s) != r.Depth {
			t.Fatalf("候補の手数がばらばらです: %v (Depth=%d)", s, r.Depth)
		}
		if got := advance(t, from, s...); got != board {
			t.Fatalf("候補 %v が別の盤面に至ります", s)
		}
	}
}

// ⚠️ **無駄手の上限（`MaxWaste`）が、認識の誤りを落とす働きそのもの。**
//
// **同じ駒を 2 回動かすのは普通の手順**（▲2六歩 ▲2五歩）なので見込む必要があるが、
// そこを青天井にすると**往復して戻る偽の手順**まで通ってしまう
// （`TestConnectRejectsMisrecognition` が落ちる）。**間に線が引けていることを見る。**
func TestConnectMaxWaste(t *testing.T) {
	from := mustPos(t, connectHirate)
	// ▲2六歩 △8四歩 ▲2五歩 △8五歩 —— 両者が同じ歩を 2 回突く（無駄 2）。
	board := advance(t, from, "2g2f", "8c8d", "2f2e", "8d8e")

	if r := connect(t, from, board, position.ConnectOptions{}); r.Stop != position.StopFound {
		t.Fatalf("同じ駒を 2 回動かす手順が繋がりません: %v", r.Stop)
	}
	// ⚠️ **「1 手も無駄を許さない」は負の数**（0 は既定に倒れる）。
	if r := connect(t, from, board, position.ConnectOptions{MaxWaste: -1}); r.Stop == position.StopFound {
		t.Fatalf("無駄手を許さないのに繋がりました: %v", r.Solutions)
	}
	// 0 は既定（2）と同じ扱いであること。
	if r := connect(t, from, board, position.ConnectOptions{MaxWaste: 0}); r.Stop != position.StopFound {
		t.Fatalf("MaxWaste=0 が既定に倒れていません: %v", r.Stop)
	}
}

// ⚠️ **並べきれないほど候補が出たら `More`。** 画面に出して選ばせられないので、
// **「決められません」に落とすための印**（`Unique` が立たないだけでは足りない）。
func TestConnectMoreThanWeCanShow(t *testing.T) {
	from := mustPos(t, connectHirate)
	board := advance(t, from, "7g7f", "3c3d", "2g2f", "8c8d")
	r := connect(t, from, board, position.ConnectOptions{MaxSolutions: 1})
	if !r.More {
		t.Fatalf("More が立ちませんでした: %+v", r)
	}
	if len(r.Solutions) != 1 {
		t.Fatalf("Solutions = %d 本, want 1", len(r.Solutions))
	}
	if r.Unique {
		t.Fatal("並べきれていないのに一意になっています")
	}
}

// ⚠️ **「探し切れなかった」と「探し切って無かった」を取り違えないこと。**
//
// 前者は深さを上げて再挑戦してよく、後者は認識の誤りとして捨てる。**次にすることが違う。**
func TestConnectBudgetIsNotUnreachable(t *testing.T) {
	from := mustPos(t, connectHirate)
	board := advance(t, from, "7g7f", "3c3d")
	r := connect(t, from, board, position.ConnectOptions{MaxNodes: 1})
	if r.Stop != position.StopBudget {
		t.Fatalf("Stop = %v, want StopBudget", r.Stop)
	}
	// 上限を外せば同じ盤面が繋がること（＝到達不能ではない）。
	if r2 := connect(t, from, board, position.ConnectOptions{}); r2.Stop != position.StopFound {
		t.Fatalf("上限を外しても繋がりません: %v", r2.Stop)
	}
}

// 打ち切られたら止まること（ctx）。**エラーにはしない**（止めたのは呼び出し側。設計原則3）。
func TestConnectCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	from := mustPos(t, connectHirate)
	board := advance(t, from, "7g7f", "3c3d")
	r, err := position.ConnectSFEN(ctx, from, board, position.ConnectOptions{})
	if err != nil {
		t.Fatalf("ConnectSFEN: %v", err)
	}
	if r.Stop != position.StopBudget {
		t.Fatalf("Stop = %v, want StopBudget", r.Stop)
	}
}

// ⚠️ **玉の欠けた局面でも panic しないこと**（`legal.Moves` の panic 握りに乗る）。
// 訂正 UI は詰将棋のような局面も確定できるので、ここに来ること自体はある。
func TestConnectWithoutKingDoesNotPanic(t *testing.T) {
	from := mustPos(t, "9/9/9/9/4P4/9/9/9/9 b - 1")
	// 返り値は問わない。**アプリごと道連れにしないこと**が要件（設計原則3）。
	_, _ = position.ConnectSFEN(context.Background(), from, "9/9/9/4P4/9/9/9/9/9", position.ConnectOptions{})
}

// ⚠️ **駒落ち（`HandsFixed`）の局面からも繋がること。**
//
// `Clone` が `HandsFixed` を落とすと逆算が復活して SFEN が組み上がらず、
// **1 手も進められなくなる**（既知の壊れ方）。ここはその歯止め。
// ⚠️ **上手（後手）が初手**なので、手番も後手から始まる。
func TestConnectHandicap(t *testing.T) {
	from, err := position.NewPosition("二枚落ち")
	if err != nil {
		t.Fatalf("NewPosition: %v", err)
	}
	if !from.HandsFixed {
		t.Fatal("駒落ちなのに HandsFixed が立っていません")
	}
	r := connect(t, from, advance(t, from, "3c3d"), position.ConnectOptions{})
	if r.Stop != position.StopFound || !r.Unique {
		t.Fatalf("駒落ちから繋がりませんでした: %+v", r)
	}
	if r.Moves[0] != "3c3d" {
		t.Fatalf("Moves = %v, want [3c3d]", r.Moves)
	}
}

// 手番が未決なら断ること（`ApplyMove` が指せないので、探索に入る前に言う）。
func TestConnectRefusesUnknownTurn(t *testing.T) {
	p, err := position.FromBoardSFEN("lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL")
	if err != nil {
		t.Fatalf("FromBoardSFEN: %v", err)
	}
	if _, err := position.ConnectSFEN(context.Background(), p, p.Board.SFEN(), position.ConnectOptions{}); err == nil {
		t.Fatal("手番が未決なのに通りました")
	}
}

// ⚠️ **覆してよい費用に負の数は入れられない**（意味が無いので黙って 0 に倒さない）。
func TestConnectNegativeTolerance(t *testing.T) {
	from := mustPos(t, connectHirate)
	if _, err := position.ConnectSFEN(context.Background(), from, from.Board.SFEN(),
		position.ConnectOptions{Tolerance: -1}); err == nil {
		t.Fatal("負の Tolerance が通ってしまいました")
	}
}

// 「認識が 1 マス外した盤面」の模擬 —— 7六歩まで進んでいて、9三の歩が抜けている。
func repairTarget(t *testing.T) string {
	t.Helper()
	return "lnsgkgsnl/1r5b1/1pppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL"
}

// ⚠️ **認識が外したマスを覆して繋ぐ**（修復探索。2026-09-15）。
//
// **これが入ると人は 1 マスも直さなくてよくなる。** 合法手から辿り着いた盤面が
// 正しいので、認識の誤りはそちらで上書きされる。
// ⚠️ **どこを覆したかを必ず返すこと**（`Fixed`）—— 黙って直すと、盤に出ている
// 局面が「撮ったもの」なのか「直したもの」なのか区別が付かなくなる。
func TestConnectRepairsOneCell(t *testing.T) {
	from := mustPos(t, connectHirate)
	r := connect(t, from, repairTarget(t), position.ConnectOptions{Tolerance: 1})
	if r.Stop != position.StopFound || !r.Unique {
		t.Fatalf("修復して繋がりませんでした: %+v", r)
	}
	if len(r.Moves) != 1 || r.Moves[0] != "7g7f" {
		t.Fatalf("Moves = %v, want [7g7f]", r.Moves)
	}
	if r.Cost <= 0 {
		t.Errorf("覆したのに Cost が 0 です: %v", r.Cost)
	}
	if len(r.Fixed) != 1 {
		t.Fatalf("Fixed = %+v, want 1 マス", r.Fixed)
	}
	// 9三（rank=2, file=0）。認識では空、実際は後手の歩。
	fx := r.Fixed[0]
	if fx.Rank != 2 || fx.File != 0 {
		t.Errorf("直したマス = (%d,%d), want (2,0)", fx.Rank, fx.File)
	}
	if !fx.Was.IsEmpty() || fx.Now.IsEmpty() || fx.Now.Black() {
		t.Errorf("直した中身が違います: was=%s now=%s", fx.Was.Name(), fx.Now.Name())
	}
}

// ⚠️ **修復でも、行き先に裏付けの無い手順を繋がないこと**（`backed`。2026-10-05）。
//
// 2八が「空き」・4八が「後手の香」と読まれた 1 枚（指している手が飛車を隠した）は、
// 4八を 1 マス覆せば ▲4八飛 で説明が付いてしまい、**「一意に繋がった」と返っていた。**
// 根拠は 2八が空いたことだけで、4八には先手の駒が 1 つも読めていない。
//
// ⚠️ **種類だけが違うなら繋ぐこと**（4八を「先手の香」と読んでいれば ▲4八飛）。
func TestConnectRepairNeedsArrivalEvidence(t *testing.T) {
	from := mustPos(t, connectHirate)
	covered := "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B3l3/LNSGKGSNL"
	if r := connect(t, from, covered, position.ConnectOptions{Tolerance: 1}); r.Stop == position.StopFound {
		t.Fatalf("行き先に先手の駒が無いのに繋がりました: %v（fixed=%+v）", r.Solutions, r.Fixed)
	}

	kind := "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B3L3/LNSGKGSNL"
	r := connect(t, from, kind, position.ConnectOptions{Tolerance: 1})
	if r.Stop != position.StopFound || !r.Unique || len(r.Moves) != 1 || r.Moves[0] != "2h4h" {
		t.Fatalf("種類だけ違うなら ▲4八飛 で繋がるはず: %+v", r)
	}
}

// relayConf は録画から読み直した 1 枚の確信度（行優先 81 個。空きは下限の 0.15 にしてある）。
func relayConf(v [81]float64) *position.CellCost {
	c := &position.CellCost{}
	for i, x := range v {
		c[i/9][i%9] = x
	}
	return c
}

// relayShot / relayCost は録画 `follow/20261007-152341/002-2e2d-guess.png` を読み直した盤と確信度。
const relayShot = "ln1g3nl/1gs3p2/1pp1s2p1/p4b1Pp/3p1p3/P1P1p4/1P1P1PSLP/2KGGG3/L7+b"

var relayCost = [81]float64{
	1.00, 1.00, 0.15, 1.00, 0.15, 0.15, 0.15, 1.00, 1.00,
	0.15, 0.80, 1.00, 0.15, 0.15, 0.15, 1.00, 0.15, 0.15,
	0.15, 1.00, 1.00, 0.15, 1.00, 0.15, 0.15, 1.00, 0.15,
	1.00, 0.15, 0.15, 0.15, 0.15, 1.00, 0.15, 1.00, 1.00,
	0.15, 0.15, 0.15, 1.00, 0.15, 1.00, 0.15, 0.15, 0.15,
	1.00, 0.15, 1.00, 0.15, 1.00, 0.15, 0.15, 0.15, 0.15,
	0.15, 1.00, 0.15, 1.00, 0.15, 1.00, 1.00, 0.56, 1.00,
	0.15, 0.15, 0.80, 0.69, 0.68, 0.39, 0.15, 0.15, 0.15,
	0.85, 0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 0.98,
}

// ⚠️ **種類だけの読み違いが何マスかあっても、修復で繋がること**（`kindOnlyWeight`。2026-10-07 に実機で踏んだ）。
//
// **実機の症状**（録画 `follow/20261007-152341/002-2e2d-guess.png`）: △6五桂 のあと ▲同桂 △同歩 ▲2四歩 と
// 進んだ 1 枚で、8二 の後手玉を「金」（確信度 0.80。この中継では毎回）、2七 の飛を「香」（0.56）、
// 4八 の銀を「金」（0.39）と読んだ。種類の違いを満額で数えると、厳密一致は駒が消えて湧いたことになって
// 「隔たりが大きすぎます」、修復も 3 マスで予算 1.0 を超えて落ちた。そこへ `Rank` が ▲2四歩 だけを
// 1 手で採り（7七・6四 が空いたのは空きの読みなので安い）、局面がずれて △1八馬 ▲2六飛 も繋がらなくなった。
func TestConnectRepairForgivesKindMisreads(t *testing.T) {
	from := after(t, "ln1g3nl/1ks3p2/1pp1s2p1/p2p1b1lp/5p1P1/P1P1p4/1PNP1PSRP/2KGGS3/L7+b w RGPnp 1", "N*6e")
	r := connect(t, from, relayShot, position.ConnectOptions{Tolerance: position.DefaultTolerance, Cost: relayConf(relayCost)})
	if r.Stop != position.StopFound || !r.Unique || strings.Join(r.Moves, " ") != "7g6e 6d6e 2e2d" {
		t.Fatalf("▲同桂 △同歩 ▲2四歩 で繋がるはず: stop=%v moves=%v solutions=%v near=%d", r.Stop, r.Moves, r.Solutions, len(r.Near))
	}
	if len(r.Fixed) != 3 {
		t.Errorf("覆したのは 8二・2七・4八 の 3 マスのはず: %+v", r.Fixed)
	}
}

// gameShot / gameCost は録画 `follow/20261008-040431/021-8g8f_8e8f_4h3g.png` を読み直した盤と確信度
// （空きは 0.15）。1四 の後手の歩を香と読んでいる（0.65）ほかは正しい。
const gameShot = "ln1g2snl/3s1kg2/p1pppp1p1/6p1l/4b4/Pr4PRP/2PPPPS2/1BG6/LNS1KG1NL"

var gameCost = [81]float64{
	1.00, 1.00, 0.15, 1.00, 0.15, 0.15, 1.00, 1.00, 0.80,
	0.15, 0.15, 0.15, 1.00, 0.15, 1.00, 1.00, 0.15, 0.15,
	1.00, 0.15, 1.00, 1.00, 1.00, 1.00, 0.15, 1.00, 0.15,
	0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 1.00, 0.15, 0.65,
	0.15, 0.15, 0.15, 0.15, 1.00, 0.15, 0.15, 0.15, 0.15,
	1.00, 1.00, 0.15, 0.15, 0.15, 0.15, 1.00, 1.00, 1.00,
	0.15, 0.15, 1.00, 1.00, 1.00, 1.00, 1.00, 0.15, 0.15,
	0.15, 1.00, 1.00, 0.15, 0.15, 0.15, 0.15, 0.15, 0.15,
	1.00, 1.00, 1.00, 0.15, 1.00, 1.00, 0.15, 1.00, 1.00,
}

// ⚠️ **修復で浅い答えが見つかっても、1 手深い手順がもっと少なく覆して合うなら、そちらを採ること**（2026-10-08。`cheaperDeeper`）。
//
// **実機の症状**（ゲーム画面）: ▲3七銀 △8六歩 ▲同歩 △同飛 の 4 手が 1 枚に写った。1四 の歩を香と読んでいたので
// 厳密一致は無く、修復が 3 手の [8g8f 8e8f 4h3g] で止まった —— 8六 の飛を「歩の種類の読み違い」、空いた 8二 を
// 「飛が読めていない」として覆した（空きの読みは安い）。正しい 4 手は 1四 を覆すだけで合うのに、深さの上限で探されて
// いなかった。本譜と持ち駒が狂い、そこから見失った。
func TestConnectRepairPrefersCheaperDeeper(t *testing.T) {
	from := after(t, connectHirate,
		"2g2f", "5a4b", "2f2e", "4a3b", "2e2d", "2c2d", "2h2d", "P*2c", "2d2f", "8c8d",
		"6i7h", "8d8e", "9g9f", "1c1d", "1g1f", "7a6b", "3i4h", "3c3d", "3g3f", "2b5e")
	r := connect(t, from, gameShot, position.ConnectOptions{Tolerance: position.DefaultTolerance, Cost: relayConf(gameCost)})
	if r.Stop != position.StopFound || !r.Unique || strings.Join(r.Moves, " ") != "4h3g 8e8f 8g8f 8b8f" {
		t.Fatalf("▲3七銀 △8六歩 ▲同歩 △同飛 で繋がるはず: stop=%v moves=%v solutions=%v cost=%v", r.Stop, r.Moves, r.Solutions, r.Cost)
	}
	if len(r.Fixed) != 1 {
		t.Errorf("覆したのは 1四 の 1 マスのはず: %+v", r.Fixed)
	}
}

// anagumaAfter8d は中継（2026-10-08。録画 `follow/20261008-101441/`）の ▲8四歩 まで進めた局面（後手は居飛車穴熊）。
const anagumaAfter8d = "kn2g2nl/lsg2srb1/pppppp1pp/1P4p2/3P5/2P6/P1BSPPPPP/1R4K2/LN1G1GSNL w - 1"

// anagumaShot3 / anagumaCost3 は録画 `x005-same-3.png` を読み直した盤と確信度（△同歩 ▲同飛 △8三歩 のあと）。
// 動いていない 6 マスで種類を読み違えている: 5一 の金と 2一 の桂を「歩」、3八 の玉を「金」、4九 の金・3九 の銀・
// 1九 の香を「歩」。
const anagumaShot3 = "kn2p2pl/lsg2srb1/pppppp1pp/1R4p2/3P5/2P6/P1BSPPPPP/6G2/LN1G1PPNP"

var anagumaCost3 = [81]float64{
	1.00, 1.00, 0.15, 0.15, 0.60, 0.15, 0.15, 0.58, 1.00,
	1.00, 0.86, 1.00, 0.15, 0.15, 0.94, 0.88, 0.92, 0.15,
	1.00, 1.00, 1.00, 1.00, 1.00, 1.00, 0.15, 1.00, 1.00,
	0.15, 0.81, 0.15, 0.15, 0.15, 0.15, 1.00, 0.15, 0.15,
	0.15, 0.15, 0.15, 1.00, 0.15, 0.15, 0.15, 0.15, 0.15,
	0.15, 0.15, 0.81, 0.15, 0.15, 0.15, 0.15, 0.15, 0.15,
	1.00, 0.15, 0.53, 1.00, 1.00, 1.00, 1.00, 1.00, 1.00,
	0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 0.51, 0.15, 0.15,
	1.00, 1.00, 0.15, 0.82, 0.15, 0.34, 0.77, 1.00, 0.51,
}

// anagumaShot2 / anagumaCost2 は録画 `x005-same-1.png`（△同歩 ▲同飛 のあと）。2一 の桂・3二 の飛・2九 の桂を「歩」、
// 2二 の角と 3八 の玉を「銀」と読み、8四 に来た飛も「歩」と読んでいる。
const anagumaShot2 = "kn2g2pl/lsg2sps1/p1pppp1pp/1P4p2/3P5/2P6/P1BSPPPPP/6S2/LN1G1GSPL"

var anagumaCost2 = [81]float64{
	1.00, 1.00, 0.15, 0.15, 0.45, 0.15, 0.15, 0.79, 0.64,
	1.00, 0.82, 0.88, 0.15, 0.15, 1.00, 1.00, 0.40, 0.15,
	1.00, 0.15, 1.00, 1.00, 1.00, 1.00, 0.15, 1.00, 1.00,
	0.15, 1.00, 0.15, 0.15, 0.15, 0.15, 1.00, 0.15, 0.15,
	0.15, 0.15, 0.15, 1.00, 0.15, 0.15, 0.15, 0.15, 0.15,
	0.15, 0.15, 1.00, 0.15, 0.15, 0.15, 0.15, 0.15, 0.15,
	1.00, 0.15, 0.64, 1.00, 1.00, 1.00, 1.00, 1.00, 1.00,
	0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 0.46, 0.15, 0.15,
	1.00, 1.00, 0.15, 0.82, 0.15, 0.66, 0.43, 0.81, 1.00,
}

// furibishaShot / furibishaCost は同じ日の別の中継（録画 `follow/20261008-103837/013-4d3e_2g2f-guess.png`）を読み直した盤と確信度。
// ▲3五同歩 のあと △2六歩 ▲同歩 △同銀 と進んだ 1 枚で、8六 の角・7六 の飛・7七 の桂を「歩」、4八 の金を「角」、
// 3八 の玉を「金」、2二 の飛を「香」、1九 の香を「歩」と読んでいる。
const furibishaShot = "kn5nl/lsgg3l1/ppppbp2p/4p4/P1PP2s2/1PP4P1/2PSPP2P/4BGGS1/L6NP"

var furibishaCost = [81]float64{
	1.00, 0.65, 0.15, 0.15, 0.15, 0.15, 0.15, 0.42, 1.00,
	1.00, 0.81, 0.89, 1.00, 0.15, 0.15, 0.15, 0.63, 0.15,
	1.00, 1.00, 1.00, 1.00, 0.75, 1.00, 0.15, 0.15, 1.00,
	0.15, 0.15, 0.15, 0.15, 1.00, 0.15, 0.15, 0.15, 0.15,
	1.00, 0.15, 1.00, 1.00, 0.15, 0.15, 0.82, 0.15, 0.15,
	0.15, 0.41, 1.00, 0.15, 0.15, 0.15, 0.15, 1.00, 0.15,
	0.15, 0.15, 0.60, 0.85, 1.00, 1.00, 0.15, 0.15, 1.00,
	0.15, 0.15, 0.15, 0.15, 0.39, 0.62, 0.60, 1.00, 0.15,
	1.00, 0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 1.00, 0.79,
}

// ⚠️ **手順が触れていないマスの、種類だけの読み違いは修復の予算から払わないこと**（`staleKinds`。2026-10-08 に実機で踏んだ）。
//
// **実機の症状**: ▲8四歩 のあと △同歩 ▲同飛 △8三歩 ▲8六飛 と進んだが、どの 1 枚も動いていない駒を 4〜6 マス
// 種類違いに読んでいた（半分に数えても 1.1〜1.7 で、予算 1.0 を超える）。正しい手順が修復に収まらず、
// 「隔たりが大きすぎます」が続いて 1 手も進まなかった。動いていない駒の種類には、何手指されたかの情報が無い。
// 同じ日の別の中継では、△2六歩 ▲同歩 △同銀 の 1 枚で深く探せず、`Rank` の 1 手（△同銀）と次の ▲2六歩（同歩ではない）を
// 足して手番が 1 つずれた。
func TestConnectRepairIgnoresStaleKindMisreads(t *testing.T) {
	// furibishaFrom は ▲3五同歩 まで進めた局面。
	const furibishaFrom = "kn5nl/lsgg3r1/ppppbp2p/4ps3/P1PP2pp1/1BR3P2/2NSPP1PP/4GGKS1/L6NL b P 1"
	cases := []struct {
		name  string
		from  string
		after []string
		shot  string
		cost  [81]float64
		moves string
	}{
		{"△同歩 ▲同飛", anagumaAfter8d, nil, anagumaShot2, anagumaCost2, "8c8d 8h8d"},
		// ⚠️ 2 手の [8c8d 8h8d] も 8三 の歩（確信度 1.00）を「無い」と覆せば予算に収まる。打ち直した △8三歩 まで採ること
		// （`cheaperDeeper`）。
		{"△同歩 ▲同飛 △8三歩", anagumaAfter8d, nil, anagumaShot3, anagumaCost3, "8c8d 8h8d P*8c"},
		{"△2六歩 ▲同歩 △同銀", furibishaFrom, []string{"3f3e"}, furibishaShot, furibishaCost, "2e2f 2g2f 4d3e"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			from := after(t, c.from, c.after...)
			r := connect(t, from, c.shot, position.ConnectOptions{Tolerance: position.DefaultTolerance, Cost: relayConf(c.cost)})
			if r.Stop != position.StopFound || !r.Unique || strings.Join(r.Moves, " ") != c.moves {
				t.Fatalf("%s で繋がるはず: stop=%v moves=%v solutions=%v cost=%v", c.moves, r.Stop, r.Moves, r.Solutions, r.Cost)
			}
			// ⚠️ **覆したことは隠さない**（読み違えたマスは Fixed に出る）。
			for _, f := range r.Fixed {
				if f.Was.IsEmpty() || f.Now.IsEmpty() || f.Was.Black() != f.Now.Black() {
					t.Errorf("種類だけの読み違いのほかに覆したマスがあります: %+v", f)
				}
			}
		})
	}
}

// ⚠️ **見なかった種類の読み違いも、覆したマスとしては出すこと**（`staleKinds`。読みを覆したのは本当なので）。
//
// 1四 の後手の歩を香と読んだ 1 枚から △3四歩 を繋ぐ。1四 は手順が触れていないので費用には数えないが、
// 画面に出す `Fixed` からは消さない（黙って直さない）。
func TestConnectRepairShowsStaleKindAsFixed(t *testing.T) {
	from := mustPos(t, "lnsgkgsnl/1r5b1/pppppppp1/8p/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL w - 1")
	shot := "lnsgkgsnl/1r5b1/pppppp1p1/6p1l/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL" // △3四歩 + 1四 の歩を香と読んだ
	r := connect(t, from, shot, position.ConnectOptions{Tolerance: position.DefaultTolerance, Cost: uniformCost(1)})
	if r.Stop != position.StopFound || !r.Unique || strings.Join(r.Moves, " ") != "3c3d" {
		t.Fatalf("△3四歩 で繋がるはず: %+v", r)
	}
	if r.Cost != 0 {
		t.Errorf("動いていない 1四 の読み違いを費用に数えています: cost=%v", r.Cost)
	}
	if len(r.Fixed) != 1 || r.Fixed[0].Rank != 3 || r.Fixed[0].File != 8 {
		t.Errorf("1四 を覆したことが Fixed に出ていません: %+v", r.Fixed)
	}
}

// uniformCost はどのマスも v の費用表。
func uniformCost(v float64) *position.CellCost {
	c := &position.CellCost{}
	for r := range c {
		for f := range c[r] {
			c[r][f] = v
		}
	}
	return c
}

// ⚠️ **厳密一致を先に試すこと。** 認識と合法性が完全に一致したなら、それが
// 一番強い証拠。**覆す余地があっても覆さない。**
func TestConnectPrefersExact(t *testing.T) {
	from := mustPos(t, connectHirate)
	r := connect(t, from, advance(t, from, "7g7f"), position.ConnectOptions{Tolerance: 2})
	if r.Stop != position.StopFound || !r.Unique {
		t.Fatalf("繋がりませんでした: %+v", r)
	}
	if r.Cost != 0 || len(r.Fixed) != 0 {
		t.Fatalf("厳密に一致しているのに覆しています: cost=%v fixed=%+v", r.Cost, r.Fixed)
	}
}

// ⚠️ **マスごとの費用で覆しやすさが変わること**（ここが修復の肝）。
//
// 認識器が**自信の無かった**マスは安く覆せて、**自信のあった**マス（や
// **人が直した**マス）は同じ予算では覆らない。**盤も予算も同じで、
// 費用の表だけを替えて結果が変わること**を見る。
//
// ⚠️ **他のマスも高くしてあるのは、比べたいものを 1 つに絞るため。**
// 安いままにすると「9三の歩が 9四へ動いた」という**合法な説明**が同じ費用で
// 成立してしまい（実際にそうなった）、何を測っているのか分からなくなる。
func TestConnectCellCost(t *testing.T) {
	from := mustPos(t, connectHirate)
	target := repairTarget(t)

	table := func(at float64) *position.CellCost {
		c := &position.CellCost{}
		for r := 0; r < 9; r++ {
			for f := 0; f < 9; f++ {
				c[r][f] = 50 // ここ以外は覆せない
			}
		}
		c[2][0] = at
		return c
	}

	// 認識器が自信の無かったマス → 覆せる。
	got := connect(t, from, target, position.ConnectOptions{Tolerance: 1, Cost: table(0.2)})
	if got.Stop != position.StopFound || !got.Unique {
		t.Fatalf("安いマスなら覆せるはず: %+v", got)
	}
	if len(got.Moves) != 1 || got.Moves[0] != "7g7f" {
		t.Fatalf("Moves = %v, want [7g7f]", got.Moves)
	}
	if got.Cost > 0.5 {
		t.Errorf("費用が重み付けされていません: %v", got.Cost)
	}
	if len(got.Fixed) != 1 || got.Fixed[0].Rank != 2 || got.Fixed[0].File != 0 {
		t.Errorf("直したマスが違います: %+v", got.Fixed)
	}

	// 人が直したマス → 同じ予算では覆らない。
	if got := connect(t, from, target, position.ConnectOptions{Tolerance: 1, Cost: table(50)}); got.Stop == position.StopFound {
		t.Fatalf("覆してはいけないマスを覆しました: %v", got.Moves)
	}
}

// ⚠️ **予算を超えたら繋がないこと。** 緩めすぎると嘘の手順を作る側に倒れるので、
// 「たくさん外している ＝ 別の局面か、認識が崩れている」として断る。
func TestConnectRepairRespectsBudget(t *testing.T) {
	from := mustPos(t, connectHirate)
	// 9三・8三・7三の歩が 3 枚とも抜けている（1 手では説明が付かない）。
	bad := "lnsgkgsnl/1r5b1/3pppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL"
	if r := connect(t, from, bad, position.ConnectOptions{Tolerance: 1}); r.Stop == position.StopFound {
		t.Fatalf("予算を超えているのに繋がりました: %v（cost=%v）", r.Moves, r.Cost)
	}
}

// ⚠️ **1 手も指さずに予算内なら「変わっていない」。** 空の手順を据えにいかないこと
// （認識が数マス外しただけで、盤は 1 手も進んでいない形）。
func TestConnectRepairSameBoard(t *testing.T) {
	from := mustPos(t, connectHirate)
	// 初期局面のまま、9三の歩だけ認識が落とした。
	glitch := "lnsgkgsnl/1r5b1/1pppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"
	r := connect(t, from, glitch, position.ConnectOptions{Tolerance: 1})
	if r.Stop != position.StopSame {
		t.Fatalf("Stop = %v, want StopSame", r.Stop)
	}
	if len(r.Moves) != 0 || len(r.Solutions) != 0 {
		t.Fatalf("0 手のはずが手順が入っています: %+v", r)
	}
}

// ⚠️ **打ち切ったときは修復へ進まないこと。**
//
// 「探し切れなかった」のであって「厳密には無い」とは分かっていない。そこで
// 覆しにいくと、**探せば見つかったはずの正しい手順を差し置いて直した局面を据える。**
func TestConnectBudgetDoesNotFallIntoRepair(t *testing.T) {
	from := mustPos(t, connectHirate)
	board := advance(t, from, "7g7f", "3c3d")
	r := connect(t, from, board, position.ConnectOptions{MaxNodes: 1, Tolerance: 2})
	if r.Stop != position.StopBudget {
		t.Fatalf("Stop = %v, want StopBudget", r.Stop)
	}
}

// BenchmarkConnect は**時間の歯止め**。無いと後から重くなっても誰も気づかない。
// ⚠️ **深さ 4 が実用の上限**（CM 1 本ぶん）なので、そこまで測る。
func BenchmarkConnect(b *testing.B) {
	p, err := position.FromFullSFEN(connectHirate)
	if err != nil {
		b.Fatal(err)
	}
	lines := map[string][]string{
		"depth1": {"7g7f"},
		"depth2": {"7g7f", "3c3d"},
		"depth4": {"7g7f", "3c3d", "2g2f", "8c8d"},
	}
	for name, moves := range lines {
		cur := p.Clone()
		for _, m := range moves {
			if err := cur.ApplyMove(m); err != nil {
				b.Fatal(err)
			}
		}
		board := cur.Board
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := position.Connect(context.Background(), p, board, position.ConnectOptions{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkConnectRepair は**修復探索の時間の歯止め**。
//
// ⚠️ **厳密一致が空振りしてから走る**ので、素直に 2 回ぶん掛かる。
// **ここが重くなると、撮るたびに数秒待つアプリになる。**
func BenchmarkConnectRepair(b *testing.B) {
	p, err := position.FromFullSFEN(connectHirate)
	if err != nil {
		b.Fatal(err)
	}
	board, err := position.FromSFEN("lnsgkgsnl/1r5b1/1pppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL")
	if err != nil {
		b.Fatal(err)
	}
	opt := position.ConnectOptions{Tolerance: 1}
	for i := 0; i < b.N; i++ {
		r, err := position.Connect(context.Background(), p, board, opt)
		if err != nil {
			b.Fatal(err)
		}
		if r.Stop != position.StopFound {
			b.Fatalf("繋がりませんでした: %v", r.Stop)
		}
	}
}

// BenchmarkConnectRelay は**種類の読み違いが混じった中継の 1 枚から 3 手を修復で繋ぐ**時間の歯止め（2026-10-07）。
//
// ⚠️ **追従の打ち切り（`app.followTimeout` 1.5 秒）に届くと、繋がるはずの 3 手を落として 1 手の答えに戻る。**
func BenchmarkConnectRelay(b *testing.B) {
	from, err := position.FromFullSFEN("ln1g3nl/1ks3p2/1pp1s2p1/p2p1b1lp/5p1P1/P1P1p4/1PNP1PSRP/2KGGS3/L7+b w RGPnp 1")
	if err != nil {
		b.Fatal(err)
	}
	if err := from.ApplyMove("N*6e"); err != nil {
		b.Fatal(err)
	}
	board, err := position.FromSFEN(relayShot)
	if err != nil {
		b.Fatal(err)
	}
	opt := position.ConnectOptions{Tolerance: position.DefaultTolerance, Cost: relayConf(relayCost)}
	for i := 0; i < b.N; i++ {
		r, err := position.Connect(context.Background(), from, board, opt)
		if err != nil {
			b.Fatal(err)
		}
		if r.Stop != position.StopFound {
			b.Fatalf("繋がりませんでした: %v", r.Stop)
		}
	}
}

// ⚠️ **読みの弱い駒を、手を足して説明しないこと**（`cheaperDeeper` は確かな読みのマスを覆したときだけ。2026-10-08）。
//
// 光の差し込んだ 1五 を「先手の香」（0.40）と読んだ 1 枚（録画 `follow/20261008-140929/`）。▲2五歩 で 1五 を覆す（0.40）
// のが正しいのに、1 手深く探して ▲1五角打 △2五角打 ▲同歩（覆さずに済む）を採った。
func TestConnectRepairDoesNotExplainWeakPiece(t *testing.T) {
	from := mustPos(t, "ln1g3nl/1ks3g2/1ppppp1pp/p4sr2/6p2/PPP2P1P1/1SNPPSP1P/1KG1G2R1/L6NL b Bb 1")
	shot := "ln1g3pp/1ks3g2/1ppppp1pl/p4sr2/6pPL/PPP2P3/1SPPPSP1P/1KG1P2R1/L6PL"
	cost := [81]float64{
		0.86, 1.00, 0.15, 0.83, 0.15, 0.15, 0.15, 0.62, 0.60,
		0.15, 1.00, 0.61, 0.15, 0.15, 0.15, 0.80, 0.15, 0.15,
		0.15, 1.00, 1.00, 1.00, 1.00, 1.00, 0.15, 1.00, 0.42,
		1.00, 0.15, 0.15, 0.15, 0.15, 0.61, 1.00, 0.15, 0.15,
		0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 0.81, 0.82, 0.40,
		1.00, 0.80, 1.00, 0.15, 0.15, 1.00, 0.15, 0.15, 0.15,
		0.15, 1.00, 0.41, 1.00, 1.00, 1.00, 1.00, 0.15, 0.81,
		0.15, 0.63, 1.00, 0.15, 0.40, 0.15, 0.15, 0.82, 0.15,
		1.00, 0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 0.68, 0.79,
	}
	r := connect(t, from, shot, position.ConnectOptions{Tolerance: position.DefaultTolerance, Cost: relayConf(cost)})
	if r.Stop != position.StopFound || !r.Unique || strings.Join(r.Moves, " ") != "2f2e" {
		t.Fatalf("▲2五歩 だけで繋がるはず: stop=%v moves=%v solutions=%v cost=%v", r.Stop, r.Moves, r.Solutions, r.Cost)
	}
}

// ⚠️ **何も指していないほうが安く説明できるなら、修復で手を足さないこと**（2026-10-08 に実機で踏んだ）。
//
// 録画 `follow/20261008-142803/020-2c1c_1a1c.png` を読み直した盤と確信度。盤の右側に光が当たり、2三 のと金を「空き」（0.15）、
// 1三 の歩を「香」と読んでいる（1〜2 筋は実際には動いていない）。`staleKinds` で予算が空いたぶん、▲1三と △同桂（費用 0.90）が
// 予算に収まって入った。何も指していなければ 0.15 で済む。
func TestConnectRepairNotWorseThanNothing(t *testing.T) {
	from := mustPos(t, "ln1g3nl/1kss1g3/1pp2p1+Pp/p2pSb3/3N5/PPPPPP3/1S1G2P1P/1KG5B/L2r3NL b R3P 1")
	shot := "ln1g3pr/1gss1g3/1pp2p2p/p2pGb3/3N5/PPPPPP3/1S1P2P1P/1KG5L/L2p3NL"
	cost := [81]float64{
		0.70, 1.00, 0.15, 1.00, 0.15, 0.15, 0.15, 0.40, 0.42,
		0.15, 0.55, 1.00, 0.42, 0.15, 1.00, 0.15, 0.15, 0.15,
		0.15, 1.00, 1.00, 0.15, 0.15, 0.43, 0.15, 0.15, 1.00,
		0.58, 0.15, 0.15, 1.00, 0.63, 0.83, 0.15, 0.15, 0.15,
		0.15, 0.15, 0.15, 0.42, 0.15, 0.15, 0.15, 0.15, 0.15,
		1.00, 1.00, 0.84, 1.00, 1.00, 1.00, 0.15, 0.15, 0.15,
		0.15, 1.00, 0.15, 0.59, 0.15, 0.15, 1.00, 0.15, 1.00,
		0.15, 0.80, 0.42, 0.15, 0.15, 0.15, 0.15, 0.15, 0.78,
		1.00, 0.15, 0.15, 0.81, 0.15, 0.15, 0.15, 0.74, 0.85,
	}
	r := connect(t, from, shot, position.ConnectOptions{Tolerance: position.DefaultTolerance, Cost: relayConf(cost)})
	if r.Stop != position.StopSame || len(r.Moves) != 0 {
		t.Fatalf("何も指していないはず: stop=%v moves=%v solutions=%v cost=%v", r.Stop, r.Moves, r.Solutions, r.Cost)
	}
}

// hakureiRoot / hakureiLine / hakureiShot / hakureiCost は録画 `follow/20261010-122957/009-…-guess.png`（中継）の根・本譜と、
// その 1 枚を読み直した盤と確信度。大盤と解説の画面のあいだに ▲2五桂 △4五桂 ▲同歩 △2五歩 ▲4四歩 △2三飛 の 6 手
// （△2五歩 と △2三飛 は入れ替わりうる）が進んだ。
const hakureiRoot = "l6rl/1kg2b3/1png1snpp/p1psppp2/7P1/P1PSSPP2/1P1P2N1P/1BK1G4/LN1G3RL b 2P 1"

var hakureiLine = []string{"6i6h", "4c5b", "2i4i", "2a4a", "2e2d", "2c2d", "P*2c", "4a2a"}

const hakureiShot = "l7l/1kg1sb3/1png3rp/p1pspPp2/7p1/P1PSS1P2/1P1P4P/1BKGG4/LN3R2L"

var hakureiCost = [81]float64{
	1.00, 0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 1.00,
	0.15, 1.00, 1.00, 0.15, 1.00, 1.00, 0.15, 0.15, 0.15,
	0.15, 1.00, 1.00, 1.00, 0.15, 0.15, 0.15, 1.00, 1.00,
	1.00, 0.15, 1.00, 1.00, 1.00, 1.00, 1.00, 0.15, 0.15,
	0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 0.15, 1.00, 0.15,
	1.00, 0.15, 1.00, 1.00, 1.00, 0.15, 1.00, 0.15, 0.15,
	0.15, 1.00, 0.15, 1.00, 0.15, 0.15, 0.15, 0.15, 1.00,
	0.15, 1.00, 1.00, 1.00, 1.00, 0.15, 0.15, 0.15, 0.15,
	1.00, 1.00, 0.15, 0.15, 0.15, 1.00, 0.15, 0.15, 1.00,
}

// ⚠️ **MaxDepth の外にある、もっとよく合う手順を探すこと**（`DeepDepth`。2026-10-10 に中継で踏んだ）。
//
// **実機の症状**: 上の 6 手が写った 1 枚で、深さ 4 の修復 ▲4五歩 △2五歩 ▲4四歩 △2三飛（空きと読めた 3七・3三 を
// 「読めていない」として覆した。0.15 × 2）が通り、続けて ▲2五桂 △同桂（2五 の歩を桂に覆した）が継ぎ足されて、
// 2五 の駒と先手の持ち駒が狂った。ぴったり合う 6 手は深さ 6 で 72 節点しか要らない。
// ⚠️ **1 マスでも種類を読み違えた 1 枚（実際に 1一 の香を飛と読んだ）でも同じ答えになること**（そちらはぴったりの手順が
// 無いので、修復の答えより安い深い手順を探す `cheaperDeeper` の側で届く）。
// ⚠️ **順番は決まらないが、行き着く局面（持ち駒まで）は全部同じであること**（追従が推測の印を付けて進んでよい形）。
func TestConnectDeepDepthFindsBetterFit(t *testing.T) {
	from := after(t, hakureiRoot, hakureiLine...)
	opt := position.ConnectOptions{Tolerance: position.DefaultTolerance, Cost: relayConf(hakureiCost)}

	// 前提: 深さを渡さなければ、今までどおり 4 手の修復が通る（ここが取り違えの入口だった）。
	if r := connect(t, from, hakureiShot, opt); r.Stop != position.StopFound || r.Depth != 4 {
		t.Fatalf("前提が崩れています: stop=%v depth=%d sols=%v", r.Stop, r.Depth, r.Solutions)
	}

	misread := "l7r" + hakureiShot[3:] // 1一 の香を後手の飛と読んだ 1 枚
	for _, shot := range []string{hakureiShot, misread} {
		deep := opt
		deep.DeepDepth = 8
		r := connect(t, from, shot, deep)
		if r.Stop != position.StopFound || r.Depth != 6 || r.Cost != 0 {
			t.Fatalf("%s: 6 手に届いていません: stop=%v depth=%d cost=%v sols=%v", shot[:3], r.Stop, r.Depth, r.Cost, r.Solutions)
		}
		if r.More || len(r.Solutions) < 2 {
			t.Fatalf("%s: 順番違いが並んでいません: more=%v sols=%v", shot[:3], r.More, r.Solutions)
		}
		end := ""
		for i, sol := range r.Solutions {
			p := from.Clone()
			for _, m := range sol {
				if err := p.ApplyMove(m); err != nil {
					t.Fatalf("ApplyMove(%q): %v", m, err)
				}
			}
			got, err := p.SFEN()
			if err != nil {
				t.Fatalf("SFEN: %v", err)
			}
			if i == 0 {
				end = got
			} else if got != end {
				t.Fatalf("%s: 行き着く局面が揃っていません: %v → %s / %s", shot[:3], sol, got, end)
			}
		}
		if !strings.HasPrefix(end, "l7l/1kg1sb3/1png3rp/p1pspPp2/7p1/P1PSS1P2/1P1P4P/1BKGG4/LN3R2L b N2Pn2p ") {
			t.Errorf("%s: 行き着いた局面 = %s", shot[:3], end)
		}
	}
}

// BenchmarkConnectDeep は**深く探してやっと繋がる 1 枚**の時間の歯止め（`DeepDepth`。2026-10-10）。
// 1一 を読み違えた 1 枚はぴったりの手順が無く、厳密一致を深さ 8 まで空振りしてから修復と `cheaperDeeper` に進む。
//
// ⚠️ **追従の打ち切り（`app.followTimeout` 1.5 秒）に届くと、繋がるはずの 6 手を落として 4 手の修復に戻る。**
func BenchmarkConnectDeep(b *testing.B) {
	from, err := position.FromFullSFEN(hakureiRoot)
	if err != nil {
		b.Fatal(err)
	}
	for _, m := range hakureiLine {
		if err := from.ApplyMove(m); err != nil {
			b.Fatal(err)
		}
	}
	board, err := position.FromSFEN("l7r" + hakureiShot[3:])
	if err != nil {
		b.Fatal(err)
	}
	opt := position.ConnectOptions{Tolerance: position.DefaultTolerance, Cost: relayConf(hakureiCost), DeepDepth: 8}
	for i := 0; i < b.N; i++ {
		r, err := position.Connect(context.Background(), from, board, opt)
		if err != nil {
			b.Fatal(err)
		}
		if r.Stop != position.StopFound || r.Depth != 6 {
			b.Fatalf("6 手に届いていません: %v depth=%d", r.Stop, r.Depth)
		}
	}
}

// ⚠️ **根から動いていない成駒のマスは、厳密一致でも見ないこと**（`staleKinds.promo`。2026-10-10 に中継で踏んだ）。
//
// **実機の症状**（録画 `follow/20261010-153156/011-…-guess.png`）: 実際は △5八歩成（5八 の金を取る）▲同金 の 2 手。
// 認識器が、動いていない 1一 の先手の馬を「後手の金」と読み、深く探すと △5八歩成 ▲5七金 △1二金打 ▲5八金 △1一金
// （後手が馬を取る）の 5 手がぴったり合って通った。成駒は学習データにほぼ入らず、先後まで外す。
// ⚠️ **見えないマスがある 1 枚では今までどおり見る**（`TestConnectUnseenDropsCountBound`）。
func TestConnectIgnoresUnmovedPromotedPiece(t *testing.T) {
	from := after(t, "l6+R+B/1kg2b3/1pngss2p/pnp2pp2/4p2p1/P1P3P2/1P1PSS2P/2KGG4/LN6L w NL2Pr2p 1",
		"5c5d", "N*6f", "5e5f", "4g5f", "5d6e", "5f6e", "7c6e", "5g5f", "P*5g", "5f6e")
	const shot = "l6+Rg/1kg2b3/1p1g1s2p/pnp2pp2/3S3p1/P1PN2P2/1P1P4P/2K1G4/LN6L" // 1一 の馬を後手の金と読んだ
	r := connect(t, from, shot, position.ConnectOptions{Tolerance: position.DefaultTolerance, Cost: uniformCost(1), DeepDepth: 8})
	if r.Stop != position.StopFound || r.Depth != 2 {
		t.Fatalf("△5八歩成 ▲同金 の 2 手になっていません: stop=%v depth=%d sols=%v", r.Stop, r.Depth, r.Solutions)
	}
	for _, sol := range r.Solutions {
		if sol[1] != "6h5h" {
			t.Fatalf("▲同金 ではない手順が並んでいます: %v", r.Solutions)
		}
	}
}
