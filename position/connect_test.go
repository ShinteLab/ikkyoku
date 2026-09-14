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
