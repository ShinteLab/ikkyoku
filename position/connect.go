package position

// 「盤面の差分を、合法手の経路で手順に復元する」層（Phase 6 の芯）。
//
// 中継を撮って認識すると**盤面だけ**が分かる。そこから
// 「今の本譜の先端から何手進んだのか」を割り出すのがここ。
//
// ⚠️ **これは推測ではない。** 合法手で到達できる手順だけを返し、**一意に決まらなければ
// 何も選ばない**。4 つのことが同時に解ける:
//
//	CM 中に進んだ手   1 手で繋がらなければ深さを上げる
//	認識の誤り        合法手で到達できない盤面は落ちる（嘘の局面が棋譜に入らない）
//	手番              根で 1 回決まっていれば、以後は経路の偶奇で決まる（設計原則5）
//	駒台の先後        ApplyMove が取った駒を指した側に載せる（画像から読めなくてよい）
//
// ⚠️ **履歴に依存しない**（設計原則1）。受け取るのは「ある局面」と「ある盤面」の 2 つだけで、
// 過去のフレームも前回の結果も見ない。**ここに「前回の〜」を足さないこと。**
//
// ⚠️ **同じ局面へ戻る往復（4 手など）は差分 0 に見えるので消える。** これは仕様。
// 直そうとすると履歴に依存した推測が入る（設計原則1）。**撮る間隔を詰めて途中を
// 拾うのが手当て**であって、ここで前の盤面を覚えることではない。

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/ShinteLab/ikkyoku/legal"
)

// 既定値。⚠️ **`ConnectOptions` のゼロ値でも動くこと**の実体。
const (
	// DefaultMaxDepth は探す最大手数。**CM 1 本ぶん**を想定している。
	DefaultMaxDepth = 4
	// DefaultMaxNodes は展開する節点の上限。
	//
	// `legal.Moves` 1 回が実測 **約 7µs**（`legal.BenchmarkMoves`）なので、
	// 2 万節点でおよそ 0.14 秒。⚠️ **この見積もりは `legal.Moves` の費用に
	// 直結している** —— あちらが遅くなればここも効かなくなる。
	DefaultMaxNodes = 20000
	// DefaultMaxSolutions は集める手順の本数（画面に並べて人に選ばせるため）。
	DefaultMaxSolutions = 8
	// DefaultTolerance は**認識結果をどれだけ覆してよいか**の既定（費用の総和）。
	//
	// ⚠️ **実測で決めた数ではない**（2026-09-15 時点）。マスごとの費用を
	// 「認識器の確信度」にすると、**確信度 1.0 のマス 1 枚、または自信の無い
	// マス数枚**ぶんに当たる。**実際の中継で当たり具合を見て調整すること。**
	DefaultTolerance = 1.0
	// repairCollect は修復探索で集める解の数（**絞り込む前**）。
	//
	// 費用の一番安い組だけを残すので、`MaxSolutions` より多めに集めておかないと
	// **もっと安い解を打ち切りで取りこぼす**。
	repairCollect = 64
	// DefaultMaxWaste は**食い違いを埋めない手を何手まで見込むか**。
	//
	// ⚠️ **これが無いと「合法手で到達できない盤面は落ちる」が成り立たない**
	// （2026-09-14 にテストで踏んだ）。深さ 4 まで素直に探すと、
	// **相手が往復して戻る**手順でほとんどの盤面に辿り着けてしまう ——
	// 歩が一気に 2 マス進んだ（あり得ない）盤面が
	// `7g7f 7a6b 7f7e 6b7a` のような 4 手で「繋がった」。
	//
	// 2 にしてあるのは実際の手順との兼ね合い。**同じ駒を 2 回動かすのは普通**
	// （▲2六歩 ▲2五歩）で、それが片側 1 手ぶんの無駄に見える。両者がやれば 2。
	// 一方**往復して戻る偽の手順は、相手側だけで 2 手を無駄にした上に、
	// 盤を動かす側の無駄も乗る**ので 3 以上になる。**その間に線を引いている。**
	DefaultMaxWaste = 2
)

// CellCost は**マスごとの「認識結果を覆すのにかかる費用」**（rank, file の順）。
//
// ⚠️ **数が大きいほど覆しにくい。** 認識器の確信度をそのまま入れるのが素直で、
// **自信の無かったマスは安く覆せて、自信のあったマスは簡単には覆らない**。
// ⚠️ **人が直したマスには大きな値を入れること** —— 人が決めたものを機械が
// 覆してよい理由が無い。
//
// ⚠️ **画像の概念を持ち込んでいない**（`position` は画像を知らない層）。
// ここにあるのは 81 個の数だけで、それが何に由来するかは呼び出し側の話。
type CellCost [9][9]float64

func (c *CellCost) at(rank, file int) float64 {
	if c == nil {
		return 1.0 // 費用の表が無ければ「1 マス＝1」
	}
	return c[rank][file]
}

// CellMask は**読めていないマス**の印（rank, file の順。真 = 読めていない。2026-10-06）。
//
// 中継では、指している手や解説者の頭が盤に被った 1 枚が撮れる。そのマスの読みは
// 「空き」か「どちらかの駒」に化けていて、**どちらにしても嘘**。
// ⚠️ **読めていないマスは、どの候補の根拠にも減点にもしないこと**（食い違いに数えない）。
// 減点にすると「本当は指されていない」側に倒れ、根拠にすると「指していない手」を足す。
// ⚠️ **行き先が読めていないマスになる手は採らないこと**（`backed`）。手を足す根拠は
// 「駒が来た」ことなので、来たかどうか見えないなら待つ。**反対側で指された手は入る**。
//
// ⚠️ **画像の概念を持ち込んでいない**（`CellCost` と同じ）。ここにあるのは 81 個の真偽で、
// それが「手や頭が被った」ことに由来するのは呼び出し側の話（`app.PositionService.followFrame`）。
type CellMask [9][9]bool

func (m *CellMask) at(rank, file int) bool {
	return m != nil && m[rank][file]
}

// Count は読めていないマスの数。
func (m *CellMask) Count() int {
	n := 0
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			if m.at(r, f) {
				n++
			}
		}
	}
	return n
}

// Fix は**認識結果を覆した 1 マス**（修復探索でだけ出る）。
//
// ⚠️ **必ず画面に出すこと。** 黙って直すと、盤に出ている局面が
// 「撮ったもの」なのか「こちらが直したもの」なのか区別が付かなくなる。
type Fix struct {
	Rank, File int
	// Was は認識結果（覆される前）。
	Was Cell
	// Now は合法手から辿り着いた側（こちらが正しいと判断した）。
	Now Cell
}

// StopReason は探索が終わった理由。
//
// ⚠️ **`StopBudget` と `StopUnreachable` を混ぜないこと。** 前者は「探し切れなかった」
// なので深さを上げて再挑戦してよく、後者は「探し切って無かった」なので
// 認識の誤りとして捨てる。**次にすることが違う。**
type StopReason int

const (
	// StopFound は繋がった（手順が 1 本以上見つかった）。
	StopFound StopReason = iota
	// StopSame は盤面が同じ（0 手。何も起きていない）。
	StopSame
	// StopUnreachable は MaxDepth までに到達する手順が無かった。
	StopUnreachable
	// StopTooFar は**下界が MaxDepth を超えた**（探索すらしていない）。
	//
	// 駒が湧いた・消えたような認識の誤りはここで即死する。
	StopTooFar
	// StopBudget は節点数か ctx で打ち切った（**到達不能ではない**）。
	StopBudget
)

// String は理由の日本語表記を返す。
func (r StopReason) String() string {
	switch r {
	case StopFound:
		return "繋がりました"
	case StopSame:
		return "盤面は変わっていません"
	case StopUnreachable:
		return "この局面からは繋がりません"
	case StopTooFar:
		return "隔たりが大きすぎます"
	case StopBudget:
		return "探し切れませんでした"
	}
	return "不明"
}

// ConnectOptions は探索の上限。**ゼロ値でも動く**（既定に倒す）。
type ConnectOptions struct {
	// MaxDepth は探す最大手数（0 なら DefaultMaxDepth）。
	MaxDepth int
	// MaxNodes は展開する節点の上限（0 なら DefaultMaxNodes）。
	MaxNodes int
	// MaxSolutions は集める手順の本数（0 なら DefaultMaxSolutions）。
	MaxSolutions int
	// MaxWaste は**食い違いを埋めない手を何手まで見込むか**（0 なら DefaultMaxWaste）。
	//
	// 下界（＝どうしても要る手数）に対して、これだけ上まで探す。⚠️ **MaxDepth は
	// 別の話** —— あちらは硬い上限で、こちらは「無駄手をどこまで本当らしいと見るか」。
	// **探すのは min(MaxDepth, 下界 + MaxWaste) まで。**
	//
	// ⚠️ **「1 手も無駄を許さない」は負の数で表す。** ゼロ値が既定である以上、
	// 0 にその意味は持たせられない（0 を書いた設定が黙って既定に戻るのと同じ話）。
	MaxWaste int
	// Tolerance は**認識結果をどれだけ覆してよいか**（費用の総和の上限）。
	//
	// **0 なら厳密一致**（今までどおり）。0 より大きいと、厳密一致で 1 本も
	// 見つからなかったときにだけ**修復探索**へ進む。
	//
	// ⚠️ **厳密一致を先に試す順序を入れ替えないこと。** 認識と合法性が完全に
	// 一致したなら、それが一番強い証拠。**覆すのは最後の手段。**
	Tolerance float64
	// Cost はマスごとの覆す費用（nil なら全マス 1.0 ＝「何マスまで」と同じ意味）。
	Cost *CellCost
	// Unseen は**読めていないマス**（nil なら全部読めている。`CellMask`）。
	//
	// ⚠️ **厳密一致にも効く** —— 読めていないマスは「一致」も「食い違い」も言えないので、
	// 残りのマスがぴったり合えば厳密一致として扱う（修復の予算は使わない）。
	Unseen *CellMask
}

func (o ConnectOptions) maxDepth() int {
	if o.MaxDepth <= 0 {
		return DefaultMaxDepth
	}
	return o.MaxDepth
}

func (o ConnectOptions) maxNodes() int {
	if o.MaxNodes <= 0 {
		return DefaultMaxNodes
	}
	return o.MaxNodes
}

func (o ConnectOptions) maxSolutions() int {
	if o.MaxSolutions <= 0 {
		return DefaultMaxSolutions
	}
	return o.MaxSolutions
}

func (o ConnectOptions) maxWaste() int {
	switch {
	case o.MaxWaste == 0:
		return DefaultMaxWaste
	case o.MaxWaste < 0:
		return 0
	}
	return o.MaxWaste
}

// ConnectResult は繋がったか、繋がらなかったならなぜか。
type ConnectResult struct {
	// Moves は**一意に繋がったときだけ**入る手順（USI）。
	//
	// ⚠️ **`Solutions[0]` を勝手に使わせないための形。** 順番が決められないのに
	// 1 本選ぶと、空想の手順が「実際に現れた指し手」として棋譜に残る
	// （`TODO.md`「本譜のロック」が一番避けたい壊れ方）。**選ぶのは人。**
	Moves []string
	// Solutions は見つかった手順（MaxSolutions まで）。**全部同じ手数。**
	Solutions [][]string
	// More は MaxSolutions を超えた（**並べきれない ＝ 決めようが無い**）。
	More bool
	// Depth は見つかった手数（StopSame なら 0）。
	Depth int
	// Unique は解が 1 本だけだったか。
	Unique bool
	// Nodes は展開した節点数（ログと、上限に張り付いていないかの確認用）。
	Nodes int
	// Stop は終わった理由。
	Stop StopReason
	// Cost は採った手順が**認識結果をどれだけ覆したか**（厳密一致なら 0）。
	Cost float64
	// Fixed は覆したマス（`Unique` のときだけ埋まる）。
	//
	// ⚠️ **空でないなら、盤に出ているのは「撮ったもの」ではない。**
	// **必ず画面に出すこと。**
	Fixed []Fix
	// Near は**一番近づけた盤面と認識結果の食い違い**（繋がらなかったときの手掛かり）。
	//
	// ⚠️ **これが無いと「繋がりません」としか言えない。** 何マスがどう食い違って
	// いるのかが分からなければ、**人はどこを直せばよいのか分からない**
	// （81 マスを端から見直すことになる）。`Was` が認識結果、`Now` が
	// **本譜から辿るとそうなるはずの駒**。
	Near []Fix
	// NearCost は Near のときの費用（予算とどれくらい離れているか）。
	NearCost float64
	// NearDepth は Near がどこまで手を進めたところか。
	NearDepth int
}

// Connect は確定局面 from から、盤面 target に至る手順を探す。
//
// ⚠️ **from は確定していること**（`SFEN()` が組み上がること）。手番が未決では
// 1 手も指せないし、取った駒をどちらの駒台に載せるかも決まらない。
//
// ⚠️ **target は盤面だけ。** 手番も持ち駒も**経路から決まる**ので渡さない
// （from から辿れば付いてくる）。**ここが Phase 6 の要点** —— 追従しているあいだ、
// 画像から読めない手番と駒台の先後を人が埋める必要がない。
//
// ⚠️ **根の手番が逆でも「繋がらない」とは限らない**（2026-09-14 に実測）。
// 1 手の深さでは必ず落ちる（相手の駒は動かせない）が、深く探せば
// **「相手が指して、自分が往復して戻った」手順**が出てくる。⚠️ **だから
// 「繋がらないこと」を手番の検算にしないこと。** 言えるのは
// **一意にならない ＝ 断定して据えることはない**までで、
// ⚠️ **逆の手番で勝手に試し直さないこと**（設計原則5。言うのは「手番が逆かもしれません」まで）。
func Connect(ctx context.Context, from *Position, target *Board, opt ConnectOptions) (ConnectResult, error) {
	if from == nil || from.Board == nil {
		return ConnectResult{}, fmt.Errorf("ikkyoku/position: 繋ぐ元の局面がありません")
	}
	if target == nil {
		return ConnectResult{}, fmt.Errorf("ikkyoku/position: 繋ぐ先の盤面がありません")
	}
	if opt.Tolerance < 0 {
		return ConnectResult{}, fmt.Errorf("ikkyoku/position: 覆してよい費用に負の数は入れられません")
	}
	if from.Turn == TurnUnknown {
		return ConnectResult{}, fmt.Errorf("ikkyoku/position: 手番が決まっていないので繋げません")
	}
	if _, err := from.SFEN(); err != nil {
		return ConnectResult{}, err
	}

	root := from.Clone()

	// 下界。**ここで落ちれば `legal.Moves` を 1 回も呼ばない。**
	// 認識の誤りの大半（駒が何枚も湧いた・消えた）はここで即死する。
	if newDiff(root.Board, target, nil, 0, opt.Unseen).same() {
		return ConnectResult{Depth: 0, Stop: StopSame}, nil
	}

	// ⚠️ **厳密一致を先に試す**（`Tolerance` のコメント）。認識と合法性が完全に
	// 一致したなら、それが一番強い証拠。**覆すのは最後の手段。**
	r, err := search(ctx, root, target, opt, nil, 0, opt.maxSolutions()+1)
	if err != nil || r.Stop == StopFound || opt.Tolerance == 0 {
		return r, err
	}
	// ⚠️ **打ち切りのときは修復へ進まない。** 「探し切れなかった」のであって
	// 「厳密には無い」とは分かっていない —— そこで覆しにいくと、
	// **探せば見つかったはずの正しい手順を差し置いて直した局面を据える**。
	if r.Stop == StopBudget {
		return r, nil
	}

	// **修復探索。** 認識が外したマスを、合法手から辿り着いた側で覆す。
	// ⚠️ **集めてから一番安い組だけ残す**（`repairCollect`）。
	rep, err := search(ctx, root, target, opt, opt.Cost, opt.Tolerance, repairCollect)
	if err != nil {
		return ConnectResult{}, err
	}
	if rep.Stop != StopFound {
		rep.Nodes += r.Nodes
		// ⚠️ **手掛かりは厳密一致の側のものを使う。** あちらは費用の表を持たない
		// ので `NearCost` が**そのまま「何マス食い違っているか」**になる
		// （修復側は重み付けなので、画面に出しても人には読めない数になる）。
		rep.Near, rep.NearCost, rep.NearDepth = r.Near, r.NearCost, r.NearDepth
		// ⚠️ **手順が無く、かつ元の局面との違いが予算に収まるなら「変わっていない」。**
		// 認識が数マス外しただけで盤は 1 手も進んでいなかった、という形。
		// **空の手順を据えにいかないための逃げ道**（`dfs` が 0 手を解にしない）。
		if newDiff(root.Board, target, opt.Cost, opt.Tolerance, opt.Unseen).cost <= opt.Tolerance+costEpsilon {
			return ConnectResult{Depth: 0, Nodes: rep.Nodes, Stop: StopSame}, nil
		}
		// **厳密一致の側の理由を返す**（修復まで届かなかったことは言わない）。
		if r.Stop == StopTooFar {
			return r, nil
		}
		return rep, nil
	}
	rep.Nodes += r.Nodes
	return rep.trim(opt.maxSolutions()), nil
}

// search は 1 回ぶんの反復深化（厳密一致も修復も同じ骨格）。
//
// cost / tolerance が両方ゼロ値なら**厳密一致**。
func search(ctx context.Context, root *Position, target *Board, opt ConnectOptions,
	cost *CellCost, tolerance float64, collect int) (ConnectResult, error) {
	depth := opt.maxDepth()
	d0 := newDiff(root.Board, target, cost, tolerance, opt.Unseen)
	if h := d0.bound(root.Turn); h > depth {
		// ⚠️ **探索していなくても手掛かりは返すこと。** ここで落ちるのは
		// 「隔たりが大きすぎる」ときなので、**どこが違うのかが一番知りたい。**
		return ConnectResult{Stop: StopTooFar, Near: d0.fixes(root.Board, target, opt.Unseen),
			NearCost: d0.cost}, nil
	}
	h := d0.bound(root.Turn)
	// ⚠️ **下界から MaxWaste 手ぶんまでしか探さない。** ここを外して MaxDepth まで
	// 素直に探すと、**相手が往復して戻る手順**でほとんどの盤面が「繋がって」しまい、
	// 認識の誤りを落とす働きが消える（`DefaultMaxWaste` のコメント）。
	if top := h + opt.maxWaste(); top < depth {
		depth = top
	}
	if h < 1 {
		h = 1 // 0 手は呼ぶ前に弾いてある（`StopSame`）
	}

	s := &connect{ctx: ctx, root: root.Board, target: target, maxNodes: opt.maxNodes(),
		want: collect, cost: cost, tolerance: tolerance, unseen: opt.Unseen, bestCost: math.Inf(1)}
	for limit := h; limit <= depth; limit++ {
		s.limit = limit
		s.path = s.path[:0]
		s.seen = map[posKey]bool{keyOf(root): true}
		if over := s.dfs(root, 0); len(s.sols) > 0 {
			return s.result(limit), nil
		} else if over {
			return s.miss(StopBudget), nil
		}
	}
	return s.miss(StopUnreachable), nil
}

// miss は繋がらなかった結果に**一番近づけたところ**を載せて返す。
func (s *connect) miss(why StopReason) ConnectResult {
	r := ConnectResult{Nodes: s.nodes, Stop: why, Near: s.bestFixes, NearDepth: s.bestDepth}
	if !math.IsInf(s.bestCost, 1) {
		r.NearCost = s.bestCost
	}
	return r
}

// ConnectSFEN は盤面部分の SFEN を受ける口（`recognize.Board.SFEN` をそのまま渡せる）。
//
// ⚠️ **読めなかった盤面は断る。** `FromSFEN` は壊れた盤面でも読めた分を返すが
// （訂正 UI のための仕様）、**読めなかったマスは空マスに見える**ので差分が嘘になり、
// 「駒が消えた」経路を探しに行ってしまう。
func ConnectSFEN(ctx context.Context, from *Position, boardSFEN string, opt ConnectOptions) (ConnectResult, error) {
	b, err := FromSFEN(boardSFEN)
	if err != nil {
		return ConnectResult{}, err
	}
	return Connect(ctx, from, b, opt)
}

// connect は 1 回の探索の作業領域。
type connect struct {
	ctx context.Context
	// root は繋ぐ元の盤（**行き先の裏付け**を見るため。`backed`）。
	root     *Board
	target   *Board
	limit    int
	maxNodes int
	// want は集める解の上限（**超えたことが分かるように 1 多く**取る）。
	want int
	// cost / tolerance は修復探索のとき。**厳密一致では両方ゼロ値。**
	cost      *CellCost
	tolerance float64
	// unseen は読めていないマス（**厳密一致でも修復でも同じ表**。`ConnectOptions.Unseen`）。
	unseen *CellMask
	nodes  int
	path   []string
	seen   map[posKey]bool
	sols   []solution
	// best は**一番近づけた節点**（繋がらなかったときに「どこが説明できないか」
	// を言うため）。⚠️ **厳密一致のときも取ること** —— 手掛かりが要るのは
	// むしろそちら。
	bestCost  float64
	bestFixes []Fix
	bestDepth int
}

// solution は見つかった手順 1 本（**費用つき**）。
type solution struct {
	moves []string
	cost  float64
	fixed []Fix
}

func (s *connect) result(limit int) ConnectResult {
	// ⚠️ **一番安い組だけ残す。** 覆す量が違う手順を同列に並べると、
	// **たまたま多く覆したほうが選択肢に混じる**（選ばせる意味が薄れる）。
	best := s.sols[0].cost
	for _, sol := range s.sols[1:] {
		if sol.cost < best {
			best = sol.cost
		}
	}
	r := ConnectResult{Depth: limit, Nodes: s.nodes, Stop: StopFound, Cost: best}
	for _, sol := range s.sols {
		if sol.cost <= best+costEpsilon {
			r.Solutions = append(r.Solutions, sol.moves)
			if len(r.Solutions) == 1 {
				r.Fixed = sol.fixed
			}
		}
	}
	return r.trim(s.want - 1)
}

// trim は候補を n 本までに切り、一意かどうかを決め直す。
func (r ConnectResult) trim(n int) ConnectResult {
	if n < 1 {
		n = 1
	}
	if len(r.Solutions) > n {
		r.Solutions = r.Solutions[:n]
		r.More = true
	}
	r.Unique = len(r.Solutions) == 1 && !r.More
	r.Moves = nil
	if r.Unique {
		r.Moves = append([]string(nil), r.Solutions[0]...)
	} else {
		// ⚠️ **一意でなければ「どこを直したか」も出さない** —— どの候補の話か
		// 決まっていないのに直した跡だけ見せると、嘘の説明になる。
		r.Fixed = nil
	}
	return r
}

// costEpsilon は費用の同点判定の遊び（float の丸め対策）。
const costEpsilon = 1e-9

// dfs は深さ優先で limit 手まで辿る。戻り値は「打ち切ったか」。
//
// ⚠️ **エラーを返さない。** 伸ばせない枝（SFEN が組み上がらない・玉が欠けている）は
// **その枝を諦めるだけ**で、探索も局面も止めない（設計原則3）。
func (s *connect) dfs(pos *Position, g int) bool {
	if s.ctx != nil && s.ctx.Err() != nil {
		return true // 止めたのは呼び出し側。**到達不能ではない**ので打ち切り扱い
	}
	d := newDiff(pos.Board, s.target, s.cost, s.tolerance, s.unseen)
	if d.cost < s.bestCost {
		s.bestCost, s.bestFixes, s.bestDepth = d.cost, d.fixes(pos.Board, s.target, s.unseen), g
	}
	// ⚠️ **受理は「費用が予算に収まったか」。** 厳密一致では予算も費用も 0 なので、
	// **1 マスでも違えば通らない**（今までどおり）。
	// ⚠️ **根そのものを解にしないこと**（`g > 0`）。修復探索では「1 手も指さずに
	// 予算内」が起こりうるが、それは**何も起きていない**ということなので、
	// 空の手順を据えるのではなく `StopSame` として扱う（呼び出し側）。
	// ⚠️ **行き先に裏付けの無い手順は解にしない**（`backed`。2026-10-05）。厳密一致では
	// 必ず通る（撮った盤とぴったり同じなので）。効くのは修復のときだけ。
	// ⚠️ **読めていないマスが混じると、厳密一致でも効く**（読めていないマスは食い違いに
	// 数えないので、行き先をそこへ逃がした手順が費用 0 で通ってしまう）。
	if g > 0 && d.cost <= s.tolerance+costEpsilon && backed(s.root, pos.Board, s.target, s.unseen) {
		s.sols = append(s.sols, solution{
			moves: append([]string(nil), s.path...),
			cost:  d.cost,
			fixed: d.fixes(pos.Board, s.target, s.unseen),
		})
		return false
	}
	// f = g + h。⚠️ **下界は admissible なので、これで解を取りこぼさない。**
	if g+d.bound(pos.Turn) > s.limit || g >= s.limit {
		return false
	}
	if s.nodes >= s.maxNodes {
		return true
	}
	s.nodes++

	sfenStr, err := pos.SFEN()
	if err != nil {
		return false
	}
	moves, err := legal.Moves(sfenStr)
	if err != nil {
		return false // 玉の欠けた局面など。**panic は legal が握っている**
	}
	d.order(moves)

	for _, m := range moves {
		next := pos.Clone()
		if err := next.ApplyMove(m.USI); err != nil {
			continue
		}
		// ⚠️ **経路内の再来を刈る**（同じ局面へ戻る枝を延々と展開しないため）。
		// **千日手を禁じているのではない** —— 1 本の手順の中で同じ局面を 2 度通っても、
		// 盤面から手順を復元する話には何も足さない。
		key := keyOf(next)
		if s.seen[key] {
			continue
		}
		s.seen[key] = true
		s.path = append(s.path, m.USI)
		over := s.dfs(next, g+1)
		s.path = s.path[:len(s.path)-1]
		delete(s.seen, key)
		if over {
			return true
		}
		if len(s.sols) >= s.want {
			return false
		}
	}
	return false
}

// posKey は経路内の再来を見るための鍵（**盤面 + 手番**）。
//
// ⚠️ **文字列にしないこと**（2026-09-15 に直した）。以前は `Board.SFEN()` を組んで
// 駒台を並べ替えて連結していたが、**これを子の数（約 100）×節点ぶん**やるので
// **1 節点 300µs**まで落ちていた（実機のログで発覚。3 秒で 1 万節点しか進まない）。
// `Cell` は比較可能なので、**配列をそのまま鍵にすれば確保も並べ替えも要らない。**
//
// ⚠️ **駒台は入れない。** 盤面と手番が同じなら経路内の再来と見なして十分で、
// **入れると鍵が太るだけ**（駒台まで一致して盤面が違う経路は無い）。
// ⚠️ **1 マス 1 バイトに詰めること。** `[9][9]Cell` をそのまま鍵にすると
// **2KB 近い鍵をマスが毎回ハッシュする**ことになり、文字列より遅くなった
// （実測で深さ 4 が 0.79ms → 2.0ms）。
type posKey struct {
	cells [81]uint8
	turn  Turn
}

// cellCode は 1 マスを 1 バイトにする（0 が空マス）。
func cellCode(c Cell) uint8 {
	if !c.filled {
		return 0
	}
	v := uint8(c.piece+1) & 0x0f
	if c.black {
		v |= 0x10
	}
	if c.promoted {
		v |= 0x20
	}
	return v
}

func keyOf(p *Position) posKey {
	k := posKey{turn: p.Turn}
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			k.cells[r*9+f] = cellCode(p.Board.cells[r][f])
		}
	}
	return k
}

// diff は「今の盤」と「目指す盤」の食い違い。
type diff struct {
	// want は**駒が来る／変わる必要のあるマス**（目指す盤で駒があり、今と違う）。
	want [9][9]bool
	// free は**空になる必要のあるマス**（今は駒があり、目指す盤では空）。
	free [9][9]bool
	// arrive / vacate はそれぞれの数。
	arrive, vacate int
	// needBlack / needWhite は盤上の駒数の食い違いから出した
	// 「その側が指さねばならない手数」。
	needBlack, needWhite int
	// cost は食い違っているマスの費用の合計（**厳密一致なら「食い違った枚数」**）。
	cost float64
	// forgive は予算内で見逃せるマスの数（**下界を緩める量**）。
	forgive int
}

// unseen のマスは**食い違いにも駒数にも数えない**（`CellMask`）。
func newDiff(cur, tgt *Board, cost *CellCost, budget float64, unseen *CellMask) *diff {
	d := &diff{}
	hidden := false
	// ⚠️ **厳密一致では 1 バイトも確保しないこと。** ここは節点ごとに呼ばれるので、
	// 修復用の作業を素通しにすると**厳密一致のほうが目に見えて遅くなる**
	// （実測で深さ 4 が 0.68ms → 1.18ms に落ちた）。
	var mismatch []float64
	if budget > 0 {
		mismatch = make([]float64, 0, 8)
	}
	var curCount, tgtCount [2][16]int // [先後][ベース駒]
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			// ⚠️ **読めていないマスは丸ごと飛ばす**（一致とも食い違いとも言えない）。
			if unseen.at(r, f) {
				hidden = true
				continue
			}
			c, t := cur.cells[r][f], tgt.cells[r][f]
			if c != t {
				// **食い違ったマスは A か D のどちらか一方**（両方には入らない）。
				if !t.IsEmpty() {
					d.want[r][f] = true
					d.arrive++
				} else {
					d.free[r][f] = true
					d.vacate++
				}
				v := cost.at(r, f)
				d.cost += v
				if mismatch != nil {
					mismatch = append(mismatch, v)
				}
			}
			// ⚠️ **成りはベース駒コードを変えない**ので、この表には出ない
			// （＝成る手は「打つ」にも「取られる」にも数えられない。それで正しい）。
			if !c.IsEmpty() {
				curCount[sideIndex(c.Black())][pieceIndex(c.Piece())]++
			}
			if !t.IsEmpty() {
				tgtCount[sideIndex(t.Black())][pieceIndex(t.Piece())]++
			}
		}
	}
	// 先手が打つ数と、後手の駒が消える数（＝先手が取った）は
	// **どちらも先手が指さねばならない手**。後手側はその裏。
	var blackDrops, whiteDrops, blackLost, whiteLost int
	for p := 0; p < 16; p++ {
		if n := tgtCount[0][p] - curCount[0][p]; n > 0 {
			blackDrops += n
		} else {
			blackLost += -n
		}
		if n := tgtCount[1][p] - curCount[1][p]; n > 0 {
			whiteDrops += n
		} else {
			whiteLost += -n
		}
	}
	d.needBlack = blackDrops + whiteLost
	d.needWhite = whiteDrops + blackLost
	// ⚠️ **読めていないマスがあるときは、駒数からの下界を使わない**（admissible でなくなる）。
	// 見えている範囲だけの駒数は、**読めていないマスから出入りした駒**でも変わるので、
	// 「打った」「取った」の手数の下限にならない。マスの差分のほうは見えているマスだけでも
	// 下限のまま（1 手で埋まる「来る」「空く」は高々 1 つずつ）。
	if hidden {
		d.needBlack, d.needWhite = 0, 0
	}
	d.forgive = forgivable(mismatch, budget)
	return d
}

// forgivable は予算内で見逃せるマスの最大数（**安い順に詰める**）。
//
// ⚠️ **下界を緩めるためだけの数。** 実際にどのマスを見逃すかは探索が決めるので、
// ここは「一番都合よく見逃せたら何枚か」＝**下界として安全な側**を返す。
func forgivable(mismatch []float64, budget float64) int {
	if budget <= 0 || len(mismatch) == 0 {
		return 0
	}
	sorted := append([]float64(nil), mismatch...)
	sort.Float64s(sorted)
	n, sum := 0, 0.0
	for _, v := range sorted {
		if sum+v > budget+costEpsilon {
			break
		}
		sum += v
		n++
	}
	return n
}

// fixes は「認識結果を覆したマス」を並べる（**費用が 0 でないマスだけ**）。
//
// ⚠️ **読めていないマスは出さない**（覆したのではなく、もともと読めていない）。
func (d *diff) fixes(cur, tgt *Board, unseen *CellMask) []Fix {
	if d.arrive == 0 && d.vacate == 0 {
		return nil
	}
	out := make([]Fix, 0, d.arrive+d.vacate)
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			if unseen.at(r, f) {
				continue
			}
			if cur.cells[r][f] != tgt.cells[r][f] {
				out = append(out, Fix{Rank: r, File: f, Was: tgt.cells[r][f], Now: cur.cells[r][f]})
			}
		}
	}
	return out
}

func (d *diff) same() bool { return d.arrive == 0 && d.vacate == 0 }

// backed は from から cand へ指したとき、**駒が来たマスがすべて、撮った盤（tgt）で
// 同じ持ち主の駒として読めているか**を返す（2026-10-05）。
//
// **手を足す根拠は「駒が来た」ことでなければならない。「駒が消えた（空いた）」だけを
// 根拠にしないこと。** 実機で、指している手が 2八の飛車を隠して「空き」と読ませ、
// 手の影を 4八の「後手の香」と読ませた 1 枚から ▲4八飛を足した —— 4八はどの候補でも
// 外れるマスなので行き先にしても損が無く、**2八が空いたことだけで 1 位になっていた。**
// 空きは手や影で簡単に作られるが、**その側の駒が現れることは偶然では起きにくい。**
//
// ⚠️ **駒の種類までは見ないこと。** 種類は認識が一番外すところで（銀や飛車を香と読む
// 中継がある）、求めると正しい手まで落ちる。先後は駒の種類とは別に読んでいる。
// ⚠️ **見るのは根と最後の盤の差だけ**（途中で来て、また去ったマスは問わない）。
// 取る手は行き先に指した側の駒が来るので、そのまま同じ扱いになる。
//
// ⚠️ **行き先が読めていないマス（unseen）なら裏付けは無い**（2026-10-06）。読めていない
// マスは食い違いに数えないので、裏付けまで素通しにすると**行き先を手や頭の下へ逃がした
// 手が費用 0 で通る**（髪が被った 3一 に △3一歩打）。**見えないなら待つ**（手がどけば次の
// 1 枚で入る）。⚠️ **元のマス（空いた側）が読めていないのは構わない** —— 根拠は来た側にある。
func backed(from, cand, tgt *Board, unseen *CellMask) bool {
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			c := cand.cells[r][f]
			if c.IsEmpty() || c == from.cells[r][f] {
				continue
			}
			if unseen.at(r, f) {
				return false
			}
			t := tgt.cells[r][f]
			if t.IsEmpty() || t.Black() != c.Black() {
				return false
			}
		}
	}
	return true
}

// bound は「ここから最低何手要るか」（**admissible な下界**）。
//
// 内訳は 2 つ:
//
//	マスの差分   1 手は移動先で「来る」を高々 1、移動元で「空く」を高々 1 しか
//	             解消できない（打ちは「来る」だけ）ので、max(来る, 空く) 手は要る
//	盤上の駒数   打つ／取られるは 1 手につき 1 単位。**手番は交互**なので、
//	             先手が nb 手・後手が nw 手指せるところまで進まないと埋まらない
func (d *diff) bound(turn Turn) int {
	// ⚠️ **見逃せるぶんだけ下界を緩めること**（修復探索のとき）。緩めないと
	// **直せば届く手順を「隔たりが大きすぎる」で切り捨てる**。
	// **緩めすぎは遅くなるだけ**だが、緩め足りないと解を取りこぼす。
	k := d.forgive
	h := atLeast(d.arrive-k, d.vacate-k)
	// 駒数のほうは 1 マス見逃すと**両側の要求が 1 つずつ**消えうる
	// （相手の駒が消えた ＝ 取った、が同時に無くなる）ので 2k で引く。
	nb, nw := d.needBlack-2*k, d.needWhite-2*k
	if n := alternating(turn, nb, nw); n > h {
		h = n
	}
	return h
}

func atLeast(a, b int) int {
	h := a
	if b > h {
		h = b
	}
	if h < 0 {
		return 0
	}
	return h
}

// alternating は「先手が nb 手、後手が nw 手指すのに要る最小の手数」を返す
// （turn の側から交互に指す）。
func alternating(turn Turn, nb, nw int) int {
	if nb <= 0 && nw <= 0 {
		return 0
	}
	first, second := nb, nw
	if turn == TurnWhite {
		first, second = nw, nb
	}
	// 先に指す側は k 手のうち ceil(k/2)、後の側は floor(k/2) 指す。
	k := 0
	if first > 0 {
		k = 2*first - 1
	}
	if n := 2 * second; n > k {
		k = n
	}
	return k
}

// order は「食い違いを埋めにいく手」を先に試すよう並べ替える。
//
// **正しさには効かない**（枝刈りは `bound` が担う）。解の在りかへ早く着くほど
// 展開する節点が減るだけ。⚠️ **安定ソートにすること** —— 同点の手の順番が
// 実行のたびに変わると、**候補の並びが毎回変わって再現しなくなる**。
func (d *diff) order(moves []legal.Move) {
	score := func(m legal.Move) int {
		n := 0
		if d.want[m.ToRank][m.ToFile] {
			n += 2
		}
		if !m.Drops() && d.free[m.FromRank][m.FromFile] {
			n++
		}
		return n
	}
	sort.SliceStable(moves, func(i, j int) bool { return score(moves[i]) > score(moves[j]) })
}

func sideIndex(black bool) int {
	if black {
		return 0
	}
	return 1
}

// pieceIndex はベース駒コードを表の添字にする（範囲外は 0 に倒す）。
func pieceIndex(p int) int {
	if p < 0 || p >= 16 {
		return 0
	}
	return p
}
