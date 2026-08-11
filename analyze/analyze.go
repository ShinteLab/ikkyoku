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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ShinteLab/core/kifu"
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
	// Moves は根の局面から指した手（USI）。**`position sfen <根> moves ...` の moves。**
	//
	// 解析タブで手を進めたぶんがここに入る。**局面を組み立て直して渡さないこと** ——
	// 千日手と連続王手は手順が無いとエンジンに判定できないので、手順があるなら渡す。
	//
	// ⚠️ **これは設計原則1（履歴に依存しない）に反しない。** 根の 1 局面だけでも
	// 解析は成立していて（Moves が空でよい）、ここに入るのは**ユーザーが自分で
	// 伸ばした手順**であって、中継を最初から観ていないと得られない情報ではない。
	Moves []string
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
	// Text は読み筋の日本語表記（"▲２二角成" "△同　銀" …）。**画面に出すのはこちら。**
	//
	// 駒種は USI の手文字列に書いていない（"8h2b+" のどこにも「角」が無い）ので、
	// **解析した局面から 1 手ずつ盤を進めて割り出している**（`core/kifu.FormatMoves`）。
	// ⚠️ **フロントで組み立て直さないこと** —— 盤が要る変換なので、フロントには材料が無い。
	//
	// **Moves と必ず同じ長さ。** 変換できなかった手はその USI がそのまま入る
	// （読み筋を丸ごと捨てないため。設計原則3）。
	Text []string `json:"text"`
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
// positionSFEN は**根の局面全体の SFEN**（盤面・手番・持ち駒・手数）。
// `position.Position.SFEN()` が返すものをそのまま渡す。手を進めているなら
// `opt.Moves` に手順を入れる（**局面を組み立て直して渡さないこと**）。
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
	root := strings.Join(fields, " ")
	// ⚠️ **評価値の符号を決めるのは「解析する局面の」手番**であって、根の手番ではない。
	// 手を進めていれば 1 手ごとに入れ替わる。**ここを根のままにすると、
	// 奇数手進めたときだけ符号が逆に見える**（画面を見ても気づけない壊れ方）。
	black := fields[1] == "b"
	if len(opt.Moves)%2 == 1 {
		black = !black
	}
	turn := "b"
	if !black {
		turn = "w"
	}
	// `position sfen <根> moves ...`。手順があるなら**組み立て直さずにそのまま渡す**
	// （千日手と連続王手は手順が無いとエンジンに判定できない）。
	cmd := root
	if len(opt.Moves) > 0 {
		cmd += " moves " + strings.Join(opt.Moves, " ")
	}

	eng, startup, err := s.dial(ctx)
	if err != nil {
		return Result{}, err
	}
	defer eng.close()

	// ⚠️ **`position` の前に `usinewgame` を送る**（`readyok` の直後）。
	// これが無いと、**前の局面の探索結果を引きずったまま次を読む**エンジンがある。
	//
	// ここでは局面ごとに 1 回になる（プロセスが 1 回の解析で終わるため）。
	// **それが正しい** —— ikkyoku が渡すのは履歴を持たない独立した局面で、
	// 前の解析と繋がっていない（設計原則1）。
	if err := eng.NewGame(); err != nil {
		return Result{}, fmt.Errorf("エンジンに usinewgame を送れませんでした: %w", err)
	}

	started := time.Now()
	acc := &accumulator{black: black, started: started, sfen: root, played: opt.Moves}
	res, err := eng.Analyze(ctx, cmd,
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
		Turn:      turn,
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
	// sfen は**根の**局面（全体の SFEN）。**読み筋を日本語にするのに要る。**
	// USI の手には駒種が書いていないので、ここから 1 手ずつ盤を進めて割り出す。
	sfen string
	// played は根から解析対象の局面までの手（USI）。
	//
	// ⚠️ **読み筋を名付けるには、まずここまで盤を進める必要がある。** 根から
	// いきなり読み筋を流すと**別の盤の上で名付ける**ことになり、駒種も「同」も
	// 全部おかしくなる（しかも USI のほうは正しいままなので画面では気づけない）。
	played []string

	mu    sync.Mutex
	depth int
	nodes int64
	lines map[int]Line
	// lastPV は順位ごとの**最後に届いた読み筋**。⚠️ **深さが変わっても捨てない。**
	//
	// エンジンは探索の終わり際に「評価値だけ更新して読み筋を書かない info」を
	// 出すことがある（`stop` を受けた直後が特にそう）。そのまま上書きすると
	// **画面から候補手が消え、打ち切ったあとに何が最善だったのか分からなくなる**。
	lastPV map[int]pvLine
	// kept は**これまでで一番良かった結果**（読み筋つきで、候補が一番多いもの）。
	//
	// ⚠️ **打ち切りは深さの途中で起きる。** 新しい深さに入ってすぐ止められると、
	// その深さの候補は 1 本も揃っていないことがある。**一度画面に出した答えより
	// 貧しい結果を最終結果にしない**ための拠り所。
	kept Progress
}

// pvLine は読み筋 1 本（USI と日本語表記）。
type pvLine struct {
	moves []string
	text  []string
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

	// ⚠️ **読み筋の無い更新で、既に出している読み筋を消さないこと。**
	// エンジンは探索の終わり際に評価値だけの info を出すことがあり、そのまま
	// 上書きすると**候補手が画面から消える**（実際にそうなっていた）。
	moves, text := in.PV, a.moveText(in.PV)
	if len(moves) == 0 {
		if prev, ok := a.lastPV[rank]; ok {
			moves, text = prev.moves, prev.text
		}
	} else {
		if a.lastPV == nil {
			a.lastPV = map[int]pvLine{}
		}
		a.lastPV[rank] = pvLine{moves: moves, text: text}
	}

	a.lines[rank] = Line{
		Rank:  rank,
		Score: newScore(in, a.black),
		Moves: moves,
		Text:  text,
	}
	p := a.progressLocked()
	a.keepLocked(p)
	return p, true
}

// keepLocked は「これまでで一番良かった結果」を覚える。ロックを取った状態で呼ぶこと。
//
// **良い＝読み筋が付いていて、候補が今まで以上に揃っている。** 候補の本数は
// MultiPV の指定で決まるので探索中は変わらない。**少ないのは「その深さがまだ
// 途中」という意味**なので、そこで打ち切られた結果を最終結果にしない。
func (a *accumulator) keepLocked(p Progress) {
	if len(p.Lines) < len(a.kept.Lines) {
		return
	}
	for _, l := range p.Lines {
		if len(l.Moves) > 0 {
			a.kept = p
			return
		}
	}
}

// moveText は読み筋を日本語表記にする。
//
// ⚠️ **エラーは握り潰す。** `kifu.FormatMoves` は読めなかった手をその USI のまま
// 返してくれるので、**表記が作れなくても読み筋は必ず出る**（設計原則3）。
// ここで解析そのものを失敗させるのは割に合わない ——
// 評価値は正しく出ているのに、表記の都合で捨てることになる。
func (a *accumulator) moveText(pv []string) []string {
	if len(pv) == 0 {
		return nil
	}
	// **根から通しで変換して、指し終わっているぶんを捨てる。** 途中の局面の SFEN を
	// 別に持って渡す手もあるが、そうすると「同」の判定に要る直前の移動先が落ちる。
	all := make([]string, 0, len(a.played)+len(pv))
	all = append(all, a.played...)
	all = append(all, pv...)
	texts, _ := kifu.FormatMoves(a.sfen, all)
	if len(texts) <= len(a.played) {
		return nil
	}
	out := make([]string, 0, len(pv))
	for _, t := range texts[len(a.played):] {
		out = append(out, t.Text)
	}
	return out
}

// snapshot は最終結果を返す。
//
// ⚠️ **一度画面に出した答えより貧しい結果を返さない。** 打ち切りは深さの途中で
// 起きるので、そのままだと「秒数が過ぎた瞬間に候補手が消える」ことがある
// （実際にそうなっていた）。**最終結果が読めないのが一番困る**ので、
// 今の深さが揃っていなければ、直前に揃っていた答えを返す。
//
// **ノード数だけは最新にする**（累計なので、途中で止めても数えたぶんは正しい）。
func (a *accumulator) snapshot() Progress {
	a.mu.Lock()
	defer a.mu.Unlock()

	p := a.progressLocked()
	if a.thinnerThanKeptLocked(p) {
		kept := a.kept
		kept.ElapsedMS = p.ElapsedMS
		kept.Nodes = p.Nodes
		return kept
	}
	return p
}

// thinnerThanKeptLocked は p が kept より貧しいかを返す。ロックを取った状態で呼ぶこと。
func (a *accumulator) thinnerThanKeptLocked(p Progress) bool {
	if len(a.kept.Lines) == 0 {
		return false // 比べる相手が無い
	}
	if len(p.Lines) < len(a.kept.Lines) {
		return true
	}
	for _, l := range p.Lines {
		if len(l.Moves) > 0 {
			return false
		}
	}
	return true // 候補はあるが読み筋が 1 本も無い（＝何を指すのか分からない）
}

// progressLocked はロックを取った状態で呼ぶこと。
//
// ⚠️ **順位は「1 から本数まで」で回さないこと。** 以前そうしていて、
// **順位 1 が届いていないと候補が丸ごと消え**、順位が本数を超えるものは
// 黙って落ちていた（エンジンが順位 1 から順に送ってくるとは限らない）。
// 実際にある順位を並べ替えて返す。
func (a *accumulator) progressLocked() Progress {
	ranks := make([]int, 0, len(a.lines))
	for rank := range a.lines {
		ranks = append(ranks, rank)
	}
	sort.Ints(ranks)
	lines := make([]Line, 0, len(ranks))
	for _, rank := range ranks {
		lines = append(lines, a.lines[rank])
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
