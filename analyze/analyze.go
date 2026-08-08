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
	// Movetime は考える時間。
	//
	// ⚠️ **0 なら「止めるまで考え続ける」**（`client.GoOptions` と同じ意味）。
	// 「ずっと解析していたい」はこれで表す —— プロセスの寿命は解析の寿命なので、
	// そのあいだエンジンも生きている。
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
	// StartupMS は起動から `readyok` までにかかったミリ秒。
	//
	// **解析のたびに払うコスト**（プロセスは 1 回の解析のあいだしか生きない）。
	// NNUE の評価関数を読むエンジンでは数秒になりうるので、見えるようにしてある。
	StartupMS int64 `json:"startupMs"`
}

// Session は「どのエンジンに繋ぐか」を持つ。**接続そのものは持たない。**
//
// ⚠️ **エンジンのプロセスは 1 回の解析のあいだだけ生きる**（2026-08-08 決定）。
// 解析を始めるときに起こし、終わったら `quit` して閉じる。
//
//	起動 → setoption → isready → position → go → bestmove → quit
//
// **「ずっと解析していたい」は時間無制限の解析 1 回**（`Options.Movetime = 0`）として
// 表す。止めるまで `go` が続くので、そのあいだプロセスも生きている。
// **アイドルタイマーも検討モードも要らない**のは、寿命が解析そのものと一致するから。
//
// この形にした理由は 2 つ:
//
//   - **exe を常駐させない。** 置換表は `isready` で確保されるので、待機中のプロセスが
//     `USI_Hash` ぶん（GB 級になりうる）のメモリを掴んだままになる
//   - **`setoption` は `isready` の前にしか効かない。** 毎回繋ぎ直すなら、
//     設定を変えた結果が次の解析にそのまま反映される（作り直しの判断が要らない）
//
// 引き換えに、**解析のたびに `isready` のコストを払う**（NNUE の評価関数を読む
// エンジンでは数秒かかりうる）。それがどれくらいかは `Result.StartupMS` に出る。
type Session struct {
	// open はエンジンを開く関数。**繋ぎ先が変わるのはここだけ。**
	open func(context.Context) (*client.Session, error)

	mu sync.Mutex
	// lastEngine は最後に繋がったエンジンの名前。**表示用**
	// （接続を持たないので、繋いでいないあいだも名前だけは出せるようにしておく）。
	lastEngine string
}

func newSession(open func(context.Context) (*client.Session, error)) *Session {
	return &Session{open: open}
}

// NewLocalSession は同梱の `engine` を USI で話す相手として使うセッションを作る
// （Step 1 の足場）。
func NewLocalSession() *Session { return newSession(localusi.Local) }

// NewExecSession は外部の USI エンジン（.exe）を起こして使うセッションを作る（Step 2）。
//
// **ここでは起動しない**ので、パスが間違っていても失敗しない
// （実際に繋ぐときに理由が出る）。
//
// options は接続時に `setoption` で送る値。**`isready` の前に送られる**
// （置換表の確保や評価関数の読み込みに間に合わせるため）。エンジンが `usi` で
// 宣言した option には、ここに書かれていなくても既定値が送られる。
func NewExecSession(path string, options map[string]string) *Session {
	return newSession(func(ctx context.Context) (*client.Session, error) {
		t, err := client.Exec(ctx, path)
		if err != nil {
			return nil, err
		}
		s, err := client.Open(ctx, t, options)
		if err != nil {
			// ハンドシェイクに失敗した時点でプロセスを片付ける
			// （Open は自分が開いた Transport を閉じるが、起こしたのはこちら）。
			if t.Close != nil {
				_ = t.Close()
			}
			return nil, err
		}
		return s, nil
	})
}

// EngineName は最後に繋がったエンジンの名前を返す（まだ繋いでいなければ空）。
func (s *Session) EngineName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastEngine
}

func (s *Session) rememberEngine(name string) {
	s.mu.Lock()
	s.lastEngine = name
	s.mu.Unlock()
}

// EngineInfo は繋がったエンジンの素性。
type EngineInfo struct {
	// Name は `id name`。
	Name string `json:"name"`
	// Author は `id author`。
	Author string `json:"author"`
	// Options はエンジンが `usi` で宣言した option の数。
	Options int `json:"options"`
	// Applied は `isready` の前に送った `setoption` の数（**既定値を含む**）。
	//
	// 宣言より少ないのが普通（button と、既定値が空のものは送らない）。
	Applied int `json:"applied"`
	// StartupMS は起動から `readyok` までの所要ミリ秒。
	//
	// **解析のたびにこれだけ待つ**ことになるので、繋ぎ先を選ぶ材料として見せる。
	StartupMS int64 `json:"startupMs"`
}

// Connect は繋がるかどうかだけを確かめる（設定タブの「接続を確認」）。
//
// **確かめたら閉じる。** 解析していないのにプロセスを残さない。
func (s *Session) Connect(ctx context.Context) (EngineInfo, error) {
	eng, startup, err := s.dial(ctx)
	if err != nil {
		return EngineInfo{}, err
	}
	defer eng.close()
	return EngineInfo{
		Name:      eng.ID,
		Author:    eng.Author,
		Options:   len(eng.Options),
		Applied:   len(eng.Applied),
		StartupMS: startup.Milliseconds(),
	}, nil
}

// Analyze は局面を解析する。**このあいだだけエンジンのプロセスが生きている。**
//
// positionSFEN は**局面全体の SFEN**（盤面・手番・持ち駒・手数）。
// `position.Position.SFEN()` が返すものをそのまま渡す。
//
// info は深さが 1 つ完走するたびに呼ばれる（nil 可）。**エンジンの読み取り
// goroutine から呼ばれる**ので、UI へ流すならイベント経由にすること。
//
// ctx をキャンセルすると `stop` を送り、**それまでに届いた結果**を返す（設計原則3）。
// そのあとエンジンは `quit` して終わる。
func (s *Session) Analyze(ctx context.Context, positionSFEN string, opt Options, info func(Progress)) (Result, error) {
	fields, err := checkSFEN(positionSFEN)
	if err != nil {
		return Result{}, err
	}
	black := fields[1] == "b"

	eng, startup, err := s.dial(ctx)
	if err != nil {
		return Result{}, err
	}
	defer eng.close()

	started := time.Now()
	acc := &accumulator{black: black, started: started}
	res, err := eng.Analyze(ctx, strings.Join(fields, " "),
		client.GoOptions{Movetime: opt.Movetime, MultiPV: opt.MultiPV},
		func(in coreusi.Info) {
			if p, ok := acc.add(in); ok && info != nil {
				info(p)
			}
		})
	if err != nil {
		return Result{}, err
	}

	final := acc.snapshot()
	final.ElapsedMS = time.Since(started).Milliseconds()
	return Result{
		Progress:  final,
		Bestmove:  res.Bestmove,
		Turn:      fields[1],
		Stopped:   res.Stopped,
		Engine:    eng.ID,
		StartupMS: startup.Milliseconds(),
	}, nil
}

// liveEngine は起動中のエンジン 1 つ。**close で必ず片付ける。**
type liveEngine struct {
	*client.Session
	// kill はプロセスを強制的に終わらせる（行儀の悪いエンジンへの保険）。
	kill context.CancelFunc
}

// close は `quit` を送って閉じ、そのあとプロセスを落とす。
func (e *liveEngine) close() {
	_ = e.Session.Close()
	e.kill()
}

// dial はエンジンを起こしてハンドシェイクまで済ませる。
//
// ⚠️ **プロセスの ctx は引数の ctx から切り離す**（`context.WithoutCancel`）。
// `client.Exec` は `exec.CommandContext` で起こすので、解析の ctx をそのまま渡すと
// **「停止」を押した瞬間にプロセスが死に、`stop` に対する bestmove を受け取れない**。
// 片付けは close 側（`quit` → kill）で行う。
func (s *Session) dial(ctx context.Context) (*liveEngine, time.Duration, error) {
	procCtx, kill := context.WithCancel(context.WithoutCancel(ctx))

	started := time.Now()
	eng, err := s.open(procCtx)
	if err != nil {
		kill()
		return nil, 0, fmt.Errorf("エンジンに繋げませんでした: %w", err)
	}
	s.rememberEngine(eng.ID)
	return &liveEngine{Session: eng, kill: kill}, time.Since(started), nil
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
