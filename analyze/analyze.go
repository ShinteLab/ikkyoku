// Package analyze は確定した局面を engine に渡して評価値を得る層（Phase 4）。
//
// **画像を知らない**（position と同じ側）。受け取るのは局面の SFEN 文字列だけで、
// 返すのは評価値と最善手だけ。撮った画像も、その座標系も、ここには入れないこと。
//
// ⚠️ **正式な SFEN を要求するのはここだけ。** 手番と駒台の先後が決まっていないと
// 評価値は出せない（`position.Position.SFEN` がエラーを返す状態では呼べない）。
// 盤を見るだけ・訂正するだけなら未決のままでよい、という線引きがこの境界にある。
//
// **視点（手前が後手の画面）の反転もこの境界で行う。** 盤を 180 度回して先後と
// 手番を入れ替えたものを渡す。**表示は絶対に反転しない**（CLAUDE.md の「視点」）。
// 反転そのものはまだ未実装で、入口は呼び出し側が渡す SFEN。
//
// **状態を持たない。** 1 回の解析は他の解析と独立している（設計原則1）。
// 「前回の評価値」を参照する関数をここに足さないこと。
package analyze

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ShinteLab/core/sfen"
	shogi "github.com/ShinteLab/engine"
	"github.com/ShinteLab/engine/search"
)

const (
	// DefaultDepth は既定の探索深さ。
	//
	// engine の既定（3）は浅すぎて手の善し悪しが出ないが、深くすると待ち時間が伸びる。
	// **打ち切りは Movetime が担う**ので、ここは「時間内に届けば嬉しい深さ」でよい。
	DefaultDepth = 8
	// DefaultMovetime は既定の打ち切り時間。
	//
	// 反復深化なので途中で切っても「最後に完走した深さ」の結果が返る（設計原則3）。
	DefaultMovetime = 5 * time.Second
)

// mateMargin は「詰みスコア」と判定する余裕。
//
// engine は詰みを `MateScore - ply` で表す。ply は探索深さ止まりなので、
// この余裕を超えて MateScore に近い値は詰みとみなしてよい。
const mateMargin = 4096

// Options は解析の指定。ゼロ値でも動く（既定値に倒す）。
type Options struct {
	// Depth は探索深さ。0 以下なら DefaultDepth。
	Depth int
	// Movetime は打ち切り時間。0 以下なら DefaultMovetime。
	Movetime time.Duration
	// Parallel は Lazy SMP を使うか。
	//
	// ⚠️ **既定で使わない。** engine が局面を扱えなかったときの panic は
	// worker の goroutine で起きると拾えず、アプリごと落ちる。速さより
	// 「撮った 1 枚で落ちないこと」を優先する（設計原則3）。
	Parallel bool
	// Workers は Parallel のときの worker 数（0 以下なら engine の既定）。
	Workers int
}

// Score は評価値。**先手視点に直してある。**
//
// engine が返すのは negamax の値（手番側から見た評価）なので、そのまま出すと
// 後手番のときだけ符号が反転して見える。**どちらの視点かを決めるのはここの責務**で、
// 表示側で符号をいじらないこと。
type Score struct {
	// CP はセンチポーン相当の評価値（先手が良ければ正）。Mate が 0 のときだけ意味を持つ。
	CP int `json:"cp"`
	// Mate は詰みまでの手数。0 なら詰みなし。**正なら先手が詰ます**、負なら後手。
	Mate int `json:"mate"`
	// Label は表示用の文字列（"+230" / "先手の詰み 5手"）。
	//
	// **書式を 1 か所にまとめるためにここに入れてある。** フロントで組み立て直さないこと。
	Label string `json:"label"`
}

// newScore は engine の手番視点のスコアを先手視点の Score にする。
func newScore(cp int, black bool) Score {
	v := cp
	if !black {
		v = -v
	}
	if abs(cp) > search.MateScore-mateMargin {
		plies := search.MateScore - abs(cp)
		if plies < 1 {
			plies = 1
		}
		if v < 0 {
			plies = -plies
		}
		return Score{Mate: plies, Label: mateLabel(plies)}
	}
	return Score{CP: v, Label: cpLabel(v)}
}

func cpLabel(v int) string {
	if v > 0 {
		return fmt.Sprintf("+%d", v)
	}
	return fmt.Sprintf("%d", v)
}

func mateLabel(plies int) string {
	if plies > 0 {
		return fmt.Sprintf("先手の詰み %d手", plies)
	}
	return fmt.Sprintf("後手の詰み %d手", -plies)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Progress は反復深化の 1 段ぶんの途中経過。**深さが 1 つ終わるたびに届く。**
//
// 探索は数秒かかるので、終わるまで何も出さないと固まったように見える。
// 途中経過をそのまま出せば「深さ 4 で +230」と育っていく様子が見える。
type Progress struct {
	// Depth は完走した探索深さ。
	Depth int `json:"depth"`
	// Score は先手視点の評価値。
	Score Score `json:"score"`
	// Nodes は訪問ノード数。
	Nodes int64 `json:"nodes"`
	// Best は最善手（USI 表記。"7g7f" / "P*5e"）。
	//
	// ⚠️ **読み筋（PV）ではない。** engine の Info が返す PV は今のところ 1 手だけで、
	// 置換表から読み筋を復元する経路が無い。**深い読み筋があるかのように出さないこと。**
	Best string `json:"best"`
	// ElapsedMS は解析を始めてからの経過ミリ秒。
	ElapsedMS int64 `json:"elapsedMs"`
}

// Result は解析の結果。最後に完走した深さの Progress そのもの。
type Result struct {
	Progress
	// Turn は解析した局面の手番（"b" / "w"）。評価値の符号を読むときの手掛かり。
	Turn string `json:"turn"`
}

// Analyze は局面を解析して評価値と最善手を返す。
//
// positionSFEN は**局面全体の SFEN**（盤面・手番・持ち駒・手数）。
// `position.Position.SFEN()` が返すものをそのまま渡す。
//
// info は深さが 1 つ完走するたびに呼ばれる（nil 可）。**探索の goroutine から
// 呼ばれる**ので、UI へ流すならイベント経由にすること。
//
// ctx をキャンセルすると打ち切って、**それまでに完走した深さの結果**を返す
// （設計原則3。途中で止めても評価値は出る）。
func Analyze(ctx context.Context, positionSFEN string, opt Options, info func(Progress)) (res Result, err error) {
	// engine は「対局として成立する局面」を前提にしていて、玉が無いと合法手生成が
	// 落ちる。事前に弾いてはいるが、ここで想定していない壊れ方をしても
	// **アプリごと落とさない**（撮った 1 枚と訂正した局面は残る。設計原則3）。
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("エンジンがこの局面を扱えませんでした: %v", r)
		}
	}()

	if strings.TrimSpace(positionSFEN) == "" {
		return Result{}, fmt.Errorf("局面がありません")
	}
	fields := strings.Fields(positionSFEN)
	if len(fields) < 4 {
		return Result{}, fmt.Errorf("局面が確定していません（手番・持ち駒・手数まで揃った SFEN が要ります）: %s", positionSFEN)
	}
	if err := ensurePlayable(fields[0]); err != nil {
		return Result{}, err
	}

	// engine の NewBoard は USI の position コマンドと同じ書式を読む。
	b, err := shogi.NewBoard("sfen " + strings.Join(fields, " "))
	if err != nil {
		return Result{}, fmt.Errorf("局面を読み込めませんでした: %w", err)
	}
	black := b.Turn() == shogi.TurnBlack

	started := time.Now()
	var last Progress
	sopt := search.Options{
		Depth:    orDefault(opt.Depth, DefaultDepth),
		Movetime: opt.Movetime,
		Parallel: opt.Parallel,
		Workers:  opt.Workers,
	}
	if sopt.Movetime <= 0 {
		sopt.Movetime = DefaultMovetime
	}
	sopt.Info = func(i search.Info) {
		p := Progress{
			Depth:     i.Depth,
			Score:     newScore(i.ScoreCP, black),
			Nodes:     i.Nodes,
			Best:      firstMove(i.PV),
			ElapsedMS: time.Since(started).Milliseconds(),
		}
		last = p
		if info != nil {
			info(p)
		}
	}

	r, err := search.BestContext(ctx, b, sopt)
	if err != nil {
		// 合法手が 1 つも無い = 詰んでいる。**エラーとして投げない**
		// （局面の状態であって、解析の失敗ではない）。
		if err == search.ErrNoMoves {
			mate := 1
			if black {
				mate = -1
			}
			return Result{
				Progress: Progress{
					Score:     Score{Mate: mate, Label: mateLabel(mate)},
					ElapsedMS: time.Since(started).Milliseconds(),
				},
				Turn: string(b.Turn()),
			}, nil
		}
		return Result{}, fmt.Errorf("解析できませんでした: %w", err)
	}

	// 深さ 1 すら完走しないまま打ち切られた場合、Info は 1 度も来ていないが
	// engine は必ず有効な手を返す。その手だけでも出す（設計原則3）。
	final := last
	if final.Depth == 0 {
		final.Score = newScore(r.Score, black)
		final.Nodes = r.Nodes
	}
	if r.Action != nil {
		final.Best = r.Action.String()
	}
	final.ElapsedMS = time.Since(started).Milliseconds()
	return Result{Progress: final, Turn: string(b.Turn())}, nil
}

// ensurePlayable は engine に渡せる局面かを確かめる。
//
// ⚠️ **玉が欠けた局面は渡せない。** engine の合法手生成は玉の位置を前提にしている
// （王手・ピンの判定がそこから始まる）ので、渡すと落ちる。
//
// **訂正 UI 側でこれを禁止しないこと。** 詰将棋のように玉が 1 枚しかない局面も
// 「正しい局面」として確定できるのが訂正 UI の要件（CLAUDE.md）。ここで止めるのは
// **エンジンに渡す瞬間だけ**で、盤を見ることも訂正することも学習に送ることもできる。
func ensurePlayable(boardSFEN string) error {
	vs := sfen.Inspect(boardSFEN, sfen.CheckKing).Filter(sfen.CheckKing)
	if len(vs) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(vs))
	for _, v := range vs {
		msgs = append(msgs, v.Detail)
	}
	return fmt.Errorf("エンジンは玉の揃った局面しか扱えません: %s", strings.Join(msgs, " / "))
}

func firstMove(pv []*shogi.Action) string {
	if len(pv) == 0 || pv[0] == nil {
		return ""
	}
	return pv[0].String()
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
