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
	"sort"
	"strconv"

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
	// Tolerance は**盤面の食い違いを何マスまで許すか**。
	//
	// ⚠️ **まだ 0 しか受け付けない**（非 0 はエラー）。認識の誤りを合法性で
	// 訂正できるようになる代わりに、**手順を捏造する側に倒れうる**ので、
	// 厳密一致のテストが固まるまで入れない。**口だけ開けてある。**
	Tolerance int
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
	if opt.Tolerance != 0 {
		return ConnectResult{}, fmt.Errorf("ikkyoku/position: 盤面の食い違いを許す探索はまだありません")
	}
	if from.Turn == TurnUnknown {
		return ConnectResult{}, fmt.Errorf("ikkyoku/position: 手番が決まっていないので繋げません")
	}
	if _, err := from.SFEN(); err != nil {
		return ConnectResult{}, err
	}

	depth := opt.maxDepth()
	root := from.Clone()

	// 下界。**ここで落ちれば `legal.Moves` を 1 回も呼ばない。**
	// 認識の誤りの大半（駒が 1 枚湧いた・消えた）はここで即死する。
	h := newDiff(root.Board, target).bound(root.Turn)
	if h == 0 {
		return ConnectResult{Depth: 0, Stop: StopSame}, nil
	}
	if h > depth {
		return ConnectResult{Stop: StopTooFar}, nil
	}

	// ⚠️ **下界から MaxWaste 手ぶんまでしか探さない。** ここを外して MaxDepth まで
	// 素直に探すと、**相手が往復して戻る手順**でほとんどの盤面が「繋がって」しまい、
	// 認識の誤りを落とす働きが消える（`DefaultMaxWaste` のコメント）。
	if top := h + opt.maxWaste(); top < depth {
		depth = top
	}

	s := &connect{ctx: ctx, target: target, maxNodes: opt.maxNodes(), want: opt.maxSolutions() + 1}
	for limit := h; limit <= depth; limit++ {
		s.limit = limit
		s.path = s.path[:0]
		s.seen = map[string]bool{positionKey(root): true}
		if over := s.dfs(root, 0); len(s.sols) > 0 {
			return s.result(limit), nil
		} else if over {
			return ConnectResult{Nodes: s.nodes, Stop: StopBudget}, nil
		}
	}
	return ConnectResult{Nodes: s.nodes, Stop: StopUnreachable}, nil
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
	ctx      context.Context
	target   *Board
	limit    int
	maxNodes int
	// want は集める解の上限（MaxSolutions + 1。**超えたことが分かるように 1 多い**）。
	want  int
	nodes int
	path  []string
	seen  map[string]bool
	sols  [][]string
}

func (s *connect) result(limit int) ConnectResult {
	r := ConnectResult{Solutions: s.sols, Depth: limit, Nodes: s.nodes, Stop: StopFound}
	if len(r.Solutions) >= s.want {
		r.Solutions = r.Solutions[:s.want-1]
		r.More = true
	}
	r.Unique = len(r.Solutions) == 1 && !r.More
	if r.Unique {
		r.Moves = append([]string(nil), r.Solutions[0]...)
	}
	return r
}

// dfs は深さ優先で limit 手まで辿る。戻り値は「打ち切ったか」。
//
// ⚠️ **エラーを返さない。** 伸ばせない枝（SFEN が組み上がらない・玉が欠けている）は
// **その枝を諦めるだけ**で、探索も局面も止めない（設計原則3）。
func (s *connect) dfs(pos *Position, g int) bool {
	if s.ctx != nil && s.ctx.Err() != nil {
		return true // 止めたのは呼び出し側。**到達不能ではない**ので打ち切り扱い
	}
	d := newDiff(pos.Board, s.target)
	if d.same() {
		s.sols = append(s.sols, append([]string(nil), s.path...))
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
		key := positionKey(next)
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

// positionKey は経路内の再来を見るための鍵（盤面 + 手番 + 駒台）。
// ⚠️ **手数は入れない**（手数だけ違う同じ局面を別物にしない）。
func positionKey(p *Position) string {
	black, white := p.Hands()
	return p.Board.SFEN() + "|" + p.Turn.mark() + "|" + handKey(black) + "|" + handKey(white)
}

func handKey(h map[int]int) string {
	keys := make([]int, 0, len(h))
	for k, v := range h {
		if v > 0 {
			keys = append(keys, k)
		}
	}
	sort.Ints(keys)
	s := ""
	for _, k := range keys {
		s += strconv.Itoa(k) + ":" + strconv.Itoa(h[k]) + ","
	}
	return s
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
}

func newDiff(cur, tgt *Board) *diff {
	d := &diff{}
	var curCount, tgtCount [2][16]int // [先後][ベース駒]
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			c, t := cur.cells[r][f], tgt.cells[r][f]
			if !t.IsEmpty() && c != t {
				d.want[r][f] = true
				d.arrive++
			}
			if !c.IsEmpty() && t.IsEmpty() {
				d.free[r][f] = true
				d.vacate++
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
	return d
}

func (d *diff) same() bool { return d.arrive == 0 && d.vacate == 0 }

// bound は「ここから最低何手要るか」（**admissible な下界**）。
//
// 内訳は 2 つ:
//
//	マスの差分   1 手は移動先で「来る」を高々 1、移動元で「空く」を高々 1 しか
//	             解消できない（打ちは「来る」だけ）ので、max(来る, 空く) 手は要る
//	盤上の駒数   打つ／取られるは 1 手につき 1 単位。**手番は交互**なので、
//	             先手が nb 手・後手が nw 手指せるところまで進まないと埋まらない
func (d *diff) bound(turn Turn) int {
	h := d.arrive
	if d.vacate > h {
		h = d.vacate
	}
	if k := alternating(turn, d.needBlack, d.needWhite); k > h {
		h = k
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
