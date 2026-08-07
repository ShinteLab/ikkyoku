// Package analyze は確定した局面をエンジンに解析させる層（Phase 4）。
//
// **画像を知らない**（position と同じ側）。受け取るのは局面の SFEN 文字列だけで、
// 返すのは評価値と読み筋だけ。撮った画像も、その座標系も、ここには入れないこと。
//
// **エンジンは USI を話す相手**（`core/usi/client`）。Go の関数として import しない。
// 繋ぎ先はやねうら王でも `prokishi.exe` でも自作 `engine` でもよく、
// **ここから上のコードは違いを知らない**（`_docs/phase4-engine-usi.md` の案 B）。
//
// ⚠️ **正式な SFEN を要求するのはここだけ。** 手番と駒台の先後が決まっていないと
// 評価値は出せない（`position.Position.SFEN` がエラーを返す状態では呼べない）。
// 盤を見るだけ・訂正するだけなら未決のままでよい、という線引きがこの境界にある。
//
// **視点（手前が後手の画面）の反転もこの境界で行う。** 盤を 180 度回して先後と
// 手番を入れ替えたものを渡す。**表示は絶対に反転しない**（CLAUDE.md の「視点」）。
// 反転そのものはまだ未実装で、入口は呼び出し側が渡す SFEN。
package analyze

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ShinteLab/core/sfen"
	coreusi "github.com/ShinteLab/core/usi"
	"github.com/ShinteLab/core/usi/client"
	localusi "github.com/ShinteLab/ikkyoku/usi"
)

// DefaultMovetime は既定の打ち切り時間。
//
// 反復深化なので途中で切っても「最後に完走した深さ」の結果が届いている（設計原則3）。
const DefaultMovetime = 5 * time.Second

// mateMargin は「詰みスコア」と判定する余裕（`score cp` しか返さないエンジン向け）。
//
// ⚠️ **自作 `engine` は詰みを `score mate` で返さない**（`MateScore - ply` の生値を
// `score cp` として出す。engine/TODO.md の 2）。向こうが直るまではこの推定が要る。
// **`score mate` を返すエンジンではそちらを優先する**ので、この推定は使われない。
const mateMargin = 4096

// engineMateScore は自作 `engine` の詰みスコアの基準値（`search.MateScore`）。
//
// **engine を import しないので定数を写している。** ここが食い違うと詰みの検出が
// ずれるが、向こうが `score mate` を返すようになればこの定数ごと消える。
const engineMateScore = 1 << 20

// Options は解析の指定。ゼロ値でも動く。
type Options struct {
	// Movetime は考える時間。0 以下なら DefaultMovetime。
	Movetime time.Duration
	// MultiPV は候補手をいくつ出させるか。0/1 なら最善手だけ。
	//
	// ⚠️ **対応していないエンジンでは無視される**（自作 `engine` が今それ。
	// engine/TODO.md の 1）。**候補が 1 本しか返らないことを異常扱いしないこと。**
	MultiPV int
}

// Score は評価値。**先手視点に直してある。**
//
// USI の評価値は**エンジンの手番側から見た値**なので、そのまま出すと
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

// Line は候補手 1 本ぶん（MultiPV の 1 行）。
//
// ⚠️ **「最善手 1 個」に畳まないこと。** 次善手を辿るのが構想の中心で、
// ここが複数本になれることが Phase 5（検討ツリー）の前提。
type Line struct {
	// Rank は候補の順位（1 が最善）。MultiPV を宣言しないエンジンでは常に 1。
	Rank int `json:"rank"`
	// Score は先手視点の評価値。
	Score Score `json:"score"`
	// Moves は読み筋（USI 表記）。
	//
	// ⚠️ **自作 `engine` は 1 手しか返さない**（engine/TODO.md の 2）。
	// **深い読み筋があるかのように出さないこと。**
	Moves []string `json:"moves"`
}

// Progress は反復深化の 1 段ぶんの途中経過。**深さが 1 つ終わるたびに届く。**
//
// 探索は数秒かかるので、終わるまで何も出さないと固まったように見える。
type Progress struct {
	Depth int   `json:"depth"`
	Nodes int64 `json:"nodes"`
	// ElapsedMS は解析を始めてからの経過ミリ秒。**こちらで測った値**
	// （`info time` を出さないエンジンがあるため。engine/TODO.md の 2）。
	ElapsedMS int64 `json:"elapsedMs"`
	// Lines は候補手（Rank の昇順）。
	Lines []Line `json:"lines"`
}

// Result は解析の結末。
type Result struct {
	Progress
	// Bestmove は `bestmove` の手（USI 表記。"resign" / "win" もありうる）。
	Bestmove string `json:"bestmove"`
	// Turn は解析した局面の手番（"b" / "w"）。評価値の符号を読むときの手掛かり。
	Turn string `json:"turn"`
	// Stopped は時間切れ・キャンセルで打ち切ったか。**失敗ではない。**
	Stopped bool `json:"stopped"`
	// Engine は答えたエンジンの名前（`id name`）。
	//
	// **何が出した評価値なのかは残すこと。** 繋ぎ先を差し替えられる以上、
	// 評価値だけ見せて出所を伏せると比べようがない。
	Engine string `json:"engine"`
}

// Session はエンジン 1 つとの対話を持ち回す。
//
// **接続は使い回す。** 局面ごとに繋ぎ直すと、外部エンジン（Step 2）では毎回
// プロセスの起動と `isready` を待つことになる。
//
// **同時に 1 つの解析しか流せない**（USI がそういう作り）。検討ツリーで複数の枝を
// 並べたくなったら Session を増やす。
type Session struct {
	// open はエンジンを開く関数。**Step 2 ではここを `client.Exec` に差し替えるだけ。**
	open func(context.Context) (*client.Session, error)

	mu  sync.Mutex
	eng *client.Session
}

// NewLocalSession は同梱の `engine` を USI で話す相手として使うセッションを作る
// （Step 1）。**接続は最初の解析まで開かない。**
func NewLocalSession() *Session { return &Session{open: localusi.Local} }

// EngineName は繋がっているエンジンの名前を返す（未接続なら空）。
func (s *Session) EngineName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eng == nil {
		return ""
	}
	return s.eng.ID
}

// Close はエンジンとの接続を閉じる。
func (s *Session) Close() error {
	s.mu.Lock()
	eng := s.eng
	s.eng = nil
	s.mu.Unlock()
	if eng == nil {
		return nil
	}
	return eng.Close()
}

// Analyze は局面を解析する。
//
// positionSFEN は**局面全体の SFEN**（盤面・手番・持ち駒・手数）。
// `position.Position.SFEN()` が返すものをそのまま渡す。
//
// info は深さが 1 つ完走するたびに呼ばれる（nil 可）。**エンジンの読み取り
// goroutine から呼ばれる**ので、UI へ流すならイベント経由にすること。
//
// ctx をキャンセルすると `stop` を送り、**それまでに届いた結果**を返す（設計原則3）。
func (s *Session) Analyze(ctx context.Context, positionSFEN string, opt Options, info func(Progress)) (Result, error) {
	fields, err := checkSFEN(positionSFEN)
	if err != nil {
		return Result{}, err
	}
	black := fields[1] == "b"

	eng, err := s.engine(ctx)
	if err != nil {
		return Result{}, err
	}

	movetime := opt.Movetime
	if movetime <= 0 {
		movetime = DefaultMovetime
	}

	started := time.Now()
	acc := &accumulator{black: black, started: started}
	res, err := eng.Analyze(ctx, strings.Join(fields, " "),
		client.GoOptions{Movetime: movetime, MultiPV: opt.MultiPV},
		func(in coreusi.Info) {
			if p, ok := acc.add(in); ok && info != nil {
				info(p)
			}
		})
	if err != nil {
		// 通信が切れたら接続を捨てる（次の解析で開き直す）。
		s.drop(eng)
		return Result{}, err
	}

	final := acc.snapshot()
	final.ElapsedMS = time.Since(started).Milliseconds()
	return Result{
		Progress: final,
		Bestmove: res.Bestmove,
		Turn:     fields[1],
		Stopped:  res.Stopped,
		Engine:   eng.ID,
	}, nil
}

// engine は接続を返す（無ければ開く）。
func (s *Session) engine(ctx context.Context) (*client.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eng != nil {
		return s.eng, nil
	}
	eng, err := s.open(ctx)
	if err != nil {
		return nil, fmt.Errorf("エンジンに繋げませんでした: %w", err)
	}
	s.eng = eng
	return eng, nil
}

// drop は壊れた接続を捨てる。**別の接続に差し替わっていたら何もしない。**
func (s *Session) drop(eng *client.Session) {
	s.mu.Lock()
	if s.eng == eng {
		s.eng = nil
	}
	s.mu.Unlock()
	_ = eng.Close()
}

// accumulator は info 行を候補手ごとに畳んで Progress にする。
//
// **MultiPV の各順位は別々の info 行で来る**ので、順位ごとに最新を覚えておいて
// まとめて出す。深さが変わったら前の深さの候補は捨てる（混ざると読み筋が食い違う）。
type accumulator struct {
	black   bool
	started time.Time

	mu    sync.Mutex
	depth int
	nodes int64
	lines map[int]Line
}

// add は info 行 1 本を取り込む。表に出す価値がある更新なら ok=true。
func (a *accumulator) add(in coreusi.Info) (Progress, bool) {
	// 評価値も読み筋も無い行（`info string` や currmove）は表示を動かさない。
	if !in.HasScore && !in.HasMate {
		return Progress{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if in.Depth > 0 && in.Depth != a.depth {
		a.depth = in.Depth
		a.lines = nil
	}
	if a.lines == nil {
		a.lines = map[int]Line{}
	}
	if in.HasNodes {
		a.nodes = in.Nodes
	}
	rank := in.MultiPV
	if rank <= 0 {
		rank = 1
	}
	a.lines[rank] = Line{Rank: rank, Score: newScore(in, a.black), Moves: in.PV}
	return a.progressLocked(), true
}

func (a *accumulator) snapshot() Progress {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.progressLocked()
}

// progressLocked はロックを取った状態で呼ぶこと。
func (a *accumulator) progressLocked() Progress {
	lines := make([]Line, 0, len(a.lines))
	for rank := 1; rank <= len(a.lines); rank++ {
		if l, ok := a.lines[rank]; ok {
			lines = append(lines, l)
		}
	}
	return Progress{
		Depth:     a.depth,
		Nodes:     a.nodes,
		ElapsedMS: time.Since(a.started).Milliseconds(),
		Lines:     lines,
	}
}

// newScore は USI の（手番側視点の）評価値を先手視点の Score にする。
func newScore(in coreusi.Info, black bool) Score {
	sign := 1
	if !black {
		sign = -1
	}
	if in.HasMate {
		plies := in.ScoreMate * sign
		return Score{Mate: plies, Label: mateLabel(plies)}
	}
	// ⚠️ `score mate` を返さないエンジン向けの推定（自作 engine が今それ）。
	if abs(in.ScoreCP) > engineMateScore-mateMargin {
		plies := engineMateScore - abs(in.ScoreCP)
		if plies < 1 {
			plies = 1
		}
		if in.ScoreCP < 0 {
			plies = -plies
		}
		plies *= sign
		return Score{Mate: plies, Label: mateLabel(plies)}
	}
	v := in.ScoreCP * sign
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

// checkSFEN は局面が解析にかけられる形かを確かめ、正規化した各フィールドを返す。
func checkSFEN(positionSFEN string) ([]string, error) {
	if strings.TrimSpace(positionSFEN) == "" {
		return nil, fmt.Errorf("局面がありません")
	}
	fields := strings.Fields(positionSFEN)
	if len(fields) < 4 {
		return nil, fmt.Errorf("局面が確定していません（手番・持ち駒・手数まで揃った SFEN が要ります）: %s", positionSFEN)
	}
	if fields[1] != "b" && fields[1] != "w" {
		return nil, fmt.Errorf("手番が読めません: %s", fields[1])
	}
	if err := ensurePlayable(fields[0]); err != nil {
		return nil, err
	}
	return fields, nil
}

// ensurePlayable はエンジンに渡せる局面かを確かめる。
//
// ⚠️ **玉が欠けた局面は渡せない。** 合法手生成が玉の位置を前提にしているエンジンが
// あり（自作 `engine` がそう）、渡すと落ちる。**外部エンジンでも同じ**で、
// 手前で弾いておくほうが「相手のプロセスが黙って死ぬ」より扱いやすい。
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
