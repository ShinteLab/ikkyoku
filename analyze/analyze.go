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
	// PonanzaConstant は評価値 → 勝率の変換に使う定数。**0 なら既定（1500）。**
	//
	// **勝率は評価値の見せ方であって、エンジンに渡すものではない**（`setoption` には
	// 出ていかない）。ここに置いてあるのは、**書式と同じで Go 側が 1 か所で
	// 組み立てるため**（`Score.WinRate`）。⚠️ **フロントで計算し直さないこと。**
	PonanzaConstant float64
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
	// Label は表示用の文字列（"+230" / "▲詰 5手"）。
	//
	// **書式を 1 か所にまとめるためにここに入れてある。** フロントで組み立て直さないこと。
	Label string `json:"label"`
	// WinRate は**先手の勝率**（0.0〜1.0）。`WinRate(s, k)` の結果を写したもの。
	//
	// **評価値と同じで先手視点。** 解析タブの勝率バーがこれを読む
	// （左が後手・右が先手）。⚠️ **フロントで計算し直さないこと** ——
	// ポナンザ定数は設定で変えられるので、式を 2 か所に持つと片方だけ古い定数で
	// 描くことになる（**画面では気づけない**）。
	WinRate float64 `json:"winRate"`
}

// Line は候補手 1 本ぶん（MultiPV の 1 行）。
//
// ⚠️ **「最善手 1 個」に畳まないこと。** 次善手を辿るのが構想の中心で、
// ここが複数本になれることが Phase 5（検討ツリー）の前提。
type Line struct {
	// Rank は候補の順位（1 が最善）。MultiPV を宣言しないエンジンでは常に 1。
	Rank int `json:"rank"`
	// Depth はこの候補が届いたときの深さ。
	//
	// ⚠️ **候補ごとに違うことがある。** MultiPV では順位ごとに別々の info が来て、
	// **順位が更新されるタイミングも深さもばらつく**。深い順位だけ先に進むのは
	// 正常な状態で、**揃うまで待つと候補が消える**（それが以前の壊れ方だった）。
	Depth int `json:"depth"`
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
	// NNUE の評価関数を読むエンジンでは数秒になりうるので、見えるようにしてある。
	// ⚠️ **接続を使い回したときは 0**（＝**払っていない**。「速かった」ではない）。
	StartupMS int64 `json:"startupMs"`
	// Reused は繋ぎっぱなしの接続を使い回したか。
	//
	// **`StartupMS` が 0 の理由がこれ。** 出さないと、起動が速かったのか払って
	// いないのかを区別できない —— あの値は「遅いのが探索のせいか起動のせいか」を
	// 見るためのものなので、0 の意味が 2 通りあると読めなくなる。
	Reused bool `json:"reused"`
}

// Session は 1 つのエンジンへの繋ぎ先と、**繋ぎっぱなしの接続**を持つ。
//
// ⚠️ **接続は解析をまたいで使い回す**（2026-08-12 に変えた。以前は
// 「プロセスは 1 回の解析のあいだだけ生きる」だった）。
//
//	起動 → setoption → isready ─┬→ usinewgame → position → go → bestmove ─┐
//	                            └──────────────────────────────────────────┘
//	                            Close（解析タブを離れた・アプリの終了）→ quit
//
// **変えた理由は連続モードと全て解析。** 1 手ごとに解析し直すので、
// **`isready`（NNUE の評価関数の読み込み。数秒になりうる）を手数ぶん払う**ことになり、
// 151 手の棋譜を通しで解析すると待ち時間の大半がそれになる。
//
// 引き換えに払うもの（**どれも手当て済み。外さないこと**）:
//
//   - ⚠️ **1 接続 1 探索なので直列化が要る**（`runMu`）。前の `bestmove` が返る前に
//     次の `position` を送ると噛み合わなくなる。**「止めて即次」を繰り返す
//     連続モードと全て解析がまさにこれ**
//   - ⚠️ **`setoption` は `isready` の前にしか効かない。** 設定を変えたら
//     **繋ぎ直さないと反映されない**（毎回起こしていた頃は自動で解決していた）。
//     繋ぎ直しの判断は呼び出し側（`AnalyzeService` が繋ぎ先の指紋で見る）
//   - ⚠️ **待機中も `USI_Hash` ぶんのメモリを掴む**（GB 級になりうる）。だから
//     **解析タブに居ないあいだは閉じる**（`Close`）
//   - ⚠️ **壊れた接続を掴み続けないこと。** 解析がエラーで終わったら捨てる（`drop`）
//
// **「ずっと解析していたい」は時間無制限の解析 1 回**（`Options.Movetime = 0`）として
// 表す、というのは変わらない。
type Session struct {
	// open はエンジンを開く関数。**繋ぎ先が変わるのはここだけ。**
	open func(context.Context) (*client.Session, error)

	// runMu は探索の直列化。
	//
	// ⚠️ **USI は 1 接続 1 探索。** 接続を使い回す以上、重ねて走らせられない
	// （**前の `bestmove` が返る前に次の `position` を送ると噛み合わなくなる**）。
	// 待ちは有界 —— `client.Session.Analyze` が `stop` のあと `BestmoveGrace` で
	// 見切りをつける。
	runMu sync.Mutex

	mu sync.Mutex
	// live は繋ぎっぱなしのエンジン（まだ繋いでいなければ nil）。
	//
	// ⚠️ **解析のたびに起こし直さない**（2026-08-12 に変えた）。評価関数の
	// 読み込みは数秒かかることがあり、**連続モードと全て解析はその数秒を
	// 1 手ごとに払う**ことになる。閉じるのは `Close`（解析タブを離れたとき・
	// アプリの終了時）。
	live *liveEngine
	// lastEngine は最後に繋がったエンジンの名前。**表示用**
	// （閉じたあとも名前だけは出せるようにしておく）。
	lastEngine string

	// kingless は**玉が片方だけの局面を渡してよいか**（詰将棋。2026-09-12）。
	//
	// **同梱エンジンだけ true。** 実測で、攻方の玉が無くても
	// **落ちないし 1 手詰を詰みスコアで見つける**ことを確かめてある
	// （攻方の玉を隅に置いた版と手もスコアも一致した）。
	//
	// ⚠️ **外部エンジンでは false のまま。** 両玉の存在を前提にしているエンジンが
	// あり、渡すと落ちるか出鱈目を返す（実物で測っていないので**安全側に倒す**）。
	// 手前で断るほうが「相手のプロセスが黙って死ぬ」より扱いやすい、という
	// `ensurePlayable` の元の判断はここでも生きている。
	kingless bool
}

func newSession(open func(context.Context) (*client.Session, error)) *Session {
	return &Session{open: open}
}

// NewLocalSession は同梱の `engine` を USI で話す相手として使うセッションを作る
// （Step 1 の足場）。
func NewLocalSession() *Session {
	s := newSession(localusi.Local)
	// ⚠️ **詰将棋（攻方の玉が無い局面）を読ませるのは同梱エンジンだけ**
	// （2026-09-12。上の `kingless` の注記）。
	s.kingless = true
	return s
}

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
	// Declared はエンジンが `usi` で宣言した option そのもの（宣言順）。
	//
	// **設定タブの入力欄はこれで組み立てる**（型・既定値・範囲・選択肢）。
	// ⚠️ **値の意味づけはしない**（`core/usi/client.Session.Options` の注記と同じ）。
	// どれを画面に出すか・どう入力させるかは呼び出し側が決める。
	//
	// ⚠️ **宣言順のまま渡すこと。** 前の option が後の option の意味を変える
	// エンジンがある（評価関数の種類を決めてからそのパスを渡す等）ので、
	// 並べ替えると画面の並びが実際の依存と食い違う。
	Declared []coreusi.Option `json:"-"`
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
// ⚠️ **確かめたら閉じる。ここでは接続を残さない**（解析の使い回しとは別）。
// 押すのは設定タブに居るときで、そこから解析に入るとは限らない。
// **解析していないのに `USI_Hash` ぶんのメモリを掴んだままにしないこと。**
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
		Declared:  eng.Options,
		Applied:   len(eng.Applied),
		StartupMS: startup.Milliseconds(),
	}, nil
}

// Analyze は局面を解析する。
//
// **初回はエンジンを起こし、2 回目からは繋ぎっぱなしの接続を使い回す。**
// ⚠️ **1 接続 1 探索なので、前の探索が畳まれるまでここで待つ**（`runMu`）。
//
// positionSFEN は**根の局面全体の SFEN**（盤面・手番・持ち駒・手数）。
// `position.Position.SFEN()` が返すものをそのまま渡す。手を進めているなら
// `opt.Moves` に手順を入れる（**局面を組み立て直して渡さないこと**）。
//
// info は深さが 1 つ完走するたびに呼ばれる（nil 可）。**エンジンの読み取り
// goroutine から呼ばれる**ので、UI へ流すならイベント経由にすること。
//
// ctx をキャンセルすると `stop` を送り、**それまでに届いた結果**を返す（設計原則3）。
// ⚠️ **そのあともエンジンは生きたまま**（次の解析で使い回す）。終わらせるのは `Close`。
func (s *Session) Analyze(ctx context.Context, positionSFEN string, opt Options, info func(Progress)) (Result, error) {
	fields, err := checkSFEN(positionSFEN, s.kingless)
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

	// ⚠️ **1 接続 1 探索なので、前の探索が畳まれるまで待つ**（上の runMu）。
	s.runMu.Lock()
	defer s.runMu.Unlock()
	// 待っているあいだに世代が進んでいたら、この結果はもう誰も読まない。
	// **待たされたぶんだけ無駄に探索しないこと**（連続モードで手を速く進めると、
	// ここに何本も積み上がる）。
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	eng, startup, reused, err := s.acquire(ctx)
	if err != nil {
		return Result{}, err
	}

	// ⚠️ **`position` の前に `usinewgame` を送る**（`readyok` の直後）。
	// これが無いと、**前の局面の探索結果を引きずったまま次を読む**エンジンがある。
	//
	// ⚠️ **接続を使い回すようになっても、解析ごとに送り続けること**（2026-08-12）。
	// 送らなければ隣り合う局面で置換表が効いて全て解析は速くなるが、
	// **前の解析の影響を受けた評価値**になる（設計原則1: 履歴に依存しない）。
	// 使い回しで消したかったのは `isready`（評価関数の読み込み）のほうで、
	// ここではない。
	if err := eng.NewGame(); err != nil {
		s.drop()
		return Result{}, fmt.Errorf("エンジンに usinewgame を送れませんでした: %w", err)
	}

	started := time.Now()
	acc := &accumulator{
		black: black, started: started, sfen: root, played: opt.Moves,
		ponanza: opt.PonanzaConstant,
	}
	res, err := eng.Analyze(ctx, cmd,
		client.GoOptions{Movetime: opt.Movetime, MultiPV: opt.MultiPV},
		func(in coreusi.Info) {
			if p, ok := acc.add(in); ok && info != nil {
				info(p)
			}
		})
	if err != nil {
		// ⚠️ **エラーが出たら接続は捨てること。** 通信が切れたときはもちろん、
		// 「`stop` を送ったのに `bestmove` が返らない」ときも**そのまま使い回すと
		// 次の探索ごと噛み合わなくなる**。捨てても次で開き直すだけ（数秒の損）で、
		// **壊れた接続を掴み続けるほうがはるかに高くつく。**
		s.drop()
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
		Reused:    reused,
	}, nil
}

// MateKind は詰み探索の答えの種類（**画面にそのまま出す文字列**）。
//
// ⚠️ **「詰みなし」と「時間切れ」を同じ扱いにしないこと。** 後者は**分からなかった
// だけ**で、時間を伸ばして投げ直せば答えが変わる。
type MateKind string

const (
	MateFound          MateKind = "mate"           // 詰みあり（Moves に手順）
	MateNone           MateKind = "nomate"         // 詰みなし
	MateTimeout        MateKind = "timeout"        // 時間内に解けなかった
	MateNotImplemented MateKind = "notimplemented" // 詰み探索に対応していない
)

// MateResult は詰み探索の結末。
type MateResult struct {
	Kind MateKind `json:"kind"`
	// Moves は詰み手順（USI 表記）。⚠️ **1 手しか返さないエンジンもある**ので、
	// **長さを「何手詰」として出さないこと。**
	Moves []string `json:"moves"`
	// Text は手順の日本語表記（`core/kifu`）。**画面に出すのはこちら。**
	Text []string `json:"text"`
	// Engine は答えたエンジンの名前。**何が出した答えかは残す。**
	Engine    string `json:"engine"`
	ElapsedMS int64  `json:"elapsedMs"`
	// Stopped はこちらから打ち切ったか。
	Stopped bool `json:"stopped"`
}

// MateOptions は詰み探索の条件。
type MateOptions struct {
	// Limit は考えさせる上限。0 なら既定（`DefaultMateLimit`）。
	Limit time.Duration
}

// DefaultMateLimit は詰み探索の既定の上限。
//
// **詰将棋は「解けるか解けないか」なので、通常の解析より長く待つ意味がある。**
// 短すぎると「時間切れ」ばかりになり、長すぎると画面が止まって見える。
const DefaultMateLimit = 10 * time.Second

// Mate は詰み探索（詰将棋を解かせる）。
//
// ⚠️ **通常の解析（`Analyze`）とは別の口。** 答えの形が違う（最善手と評価値 /
// 詰むか否かとその手順）し、**渡してよい局面の条件も違う**（下記）。
//
// ⚠️ **攻方の玉が無くてよい。** そこが詰将棋の普通の形で、**詰将棋エンジンは
// それを前提にしている**（KomoringHeights で実測）。⚠️ **玉方の玉は要る** ——
// 詰ませる相手が居ない局面は詰み探索にならない。
//
// ⚠️ **詰将棋エンジンを繋ぐこと。** 通常のエンジン（やねうら王系）は**攻方の玉が
// 無いと `go mate` にも答えない**（実測）。その場合は上限まで待って
// 「エンジンが checkmate を返しません」で返る（**黙って固まりはしない**）。
func (s *Session) Mate(ctx context.Context, positionSFEN string, opt MateOptions, info func(Progress)) (MateResult, error) {
	fields := strings.Fields(positionSFEN)
	if len(fields) < 4 {
		return MateResult{}, fmt.Errorf("局面が確定していません（手番・持ち駒・手数まで揃った SFEN が要ります）: %s", positionSFEN)
	}
	if fields[1] != "b" && fields[1] != "w" {
		return MateResult{}, fmt.Errorf("手番が読めません: %s", fields[1])
	}
	if err := ensureMateTarget(fields[0], fields[1] == "b"); err != nil {
		return MateResult{}, err
	}
	root := strings.Join(fields, " ")

	limit := opt.Limit
	if limit <= 0 {
		limit = DefaultMateLimit
	}

	// ⚠️ **1 接続 1 探索**（`Analyze` と同じ理由）。
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if err := ctx.Err(); err != nil {
		return MateResult{}, err
	}

	eng, _, _, err := s.acquire(ctx)
	if err != nil {
		return MateResult{}, err
	}
	if err := eng.NewGame(); err != nil {
		s.drop()
		return MateResult{}, fmt.Errorf("エンジンに usinewgame を送れませんでした: %w", err)
	}

	started := time.Now()
	// 途中経過は `Analyze` と同じ形で流す（画面の出し先が同じなので）。
	acc := &accumulator{black: fields[1] == "b", started: started, sfen: root}
	res, err := eng.Mate(ctx, root, client.MateOptions{Limit: limit}, func(in coreusi.Info) {
		if p, ok := acc.add(in); ok && info != nil {
			info(p)
		}
	})
	if err != nil {
		// ⚠️ **エラーが出たら接続は捨てる**（`Analyze` と同じ）。
		s.drop()
		return MateResult{}, err
	}

	out := MateResult{
		Moves:     res.Moves,
		Engine:    eng.ID,
		ElapsedMS: time.Since(started).Milliseconds(),
		Stopped:   res.Stopped,
	}
	switch res.Kind {
	case coreusi.CheckmateFound:
		out.Kind = MateFound
		// **手順の日本語表記は core/kifu**（読み筋と同じ作り方。`accumulator.moveText`）。
		// ⚠️ **エラーは握る** —— 読めない手があっても、USI の手順は返す（設計原則3）。
		texts, _ := kifu.FormatMoves(root, res.Moves)
		out.Text = make([]string, 0, len(texts))
		for _, t := range texts {
			out.Text = append(out.Text, t.Text)
		}
	case coreusi.CheckmateNone:
		out.Kind = MateNone
	case coreusi.CheckmateTimeout:
		out.Kind = MateTimeout
	default:
		out.Kind = MateNotImplemented
	}
	if out.Moves == nil {
		out.Moves = []string{}
	}
	if out.Text == nil {
		out.Text = []string{}
	}
	return out, nil
}

// ensureMateTarget は詰み探索にかけられる局面かを確かめる。
//
// ⚠️ **`ensurePlayable` とは条件が違う。** あちらは両玉を要求するが、詰将棋は
// **攻方の玉が無いのが普通**。ここで要るのは**詰ませる相手（玉方の玉）**だけ。
//
// black は攻方（手番）が先手か。**玉方は手番でない側。**
func ensureMateTarget(boardSFEN string, black bool) error {
	for _, v := range sfen.Inspect(boardSFEN, sfen.CheckKing).Filter(sfen.CheckKing) {
		// 攻方の玉は 0 枚でよい（詰将棋の普通の形）。
		if v.Black == black && v.Count == 0 {
			continue
		}
		if v.Black == black {
			return fmt.Errorf("攻方の玉が多すぎます: %s", v.Detail)
		}
		return fmt.Errorf("詰ませる相手が居ません: %s", v.Detail)
	}
	return nil
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
// しかも**接続を使い回すようになった今は、1 回目の「停止」で接続ごと失う。**
// 片付けは `Close` / `drop`（`quit` → kill）で行う。
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

// acquire は使える接続を返す（無ければ起こす）。reused は使い回したかどうか。
//
// **起動にかかった時間は、実際に起こしたときだけ返る。** 使い回したときは 0 で、
// それは「速かった」ではなく「払っていない」の意味 —— **画面ではこの 2 つを
// 区別して出すこと**（`Result.Reused`）。
func (s *Session) acquire(ctx context.Context) (*liveEngine, time.Duration, bool, error) {
	s.mu.Lock()
	live := s.live
	s.mu.Unlock()
	if live != nil {
		return live, 0, true, nil
	}

	eng, startup, err := s.dial(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	s.mu.Lock()
	s.live = eng
	s.mu.Unlock()
	return eng, startup, false, nil
}

// drop は今の接続を捨てる（**壊れたと分かったとき**）。次の解析で開き直す。
func (s *Session) drop() {
	s.mu.Lock()
	live := s.live
	s.live = nil
	s.mu.Unlock()
	if live != nil {
		live.close()
	}
}

// Close はエンジンを終わらせる。
//
// **呼ぶのは「解析タブを離れたとき」と「アプリの終了時」**（`AnalyzeService`）。
// ⚠️ **待機中のエンジンは `USI_Hash` ぶん（GB 級になりうる）のメモリを掴んだまま**
// なので、**解析タブに居ないあいだまで生かしておかないこと。**
//
// ⚠️ **走っている探索が畳まれるのを待つ。** 探索の途中で `quit` を送ると、
// `stop` に対する `bestmove` を受け取る前にプロセスが消える。
func (s *Session) Close() {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.drop()
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
	// ponanza は評価値 → 勝率の変換に使う定数（0 なら既定。`PonanzaConstantOr`）。
	ponanza float64

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
	// ⚠️ **`lowerbound` / `upperbound` は捨てる**（2026-08-13）。
	//
	// これは**探索の途中で「この手はこれ以上（以下）だと分かった」という速報**で、
	// **評価値は確定値ではなく、読み筋も 1 手しか付かないのが普通**。取り込むと
	// **画面の読み筋がその 1 手に化ける**（実際に「最善手の読み筋だけが消える」
	// という形で出た。しかも USI のやり取りには正しい読み筋が流れているので、
	// ログを見ても原因が分からない）。
	//
	// ⚠️ **`lastPV` で救われないこと。** あの仕掛けは「読み筋の**無い** info」で
	// 上書きしないためのもので、**1 手だけの読み筋は「有る」**ので通ってしまう。
	// **ここで止めるしかない。**
	//
	// 捨てても損は無い —— **確定値は同じ深さのすぐあとに必ず来る**（来なければ
	// 1 つ前の深さの答えが残る。将棋 UI の一般的な挙動でもある）。
	if in.LowerBound || in.UpperBound {
		return Progress{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	// ⚠️ **深さが変わっても候補を消さないこと。**
	//
	// 以前は「深さが変わったら全部捨てる」だったが、**MultiPV では順位ごとに
	// 別々の info が来るうえ、順位の順にも深さの順にも並ばない**。実測で
	// `multipv 2` → `multipv 1` → `multipv 3` の順に、しかも深さがばらついて届く。
	// 全消しすると**そのたびに 1 本だけの状態に戻り、最後に来た順位しか残らない**
	// （「三番手の手だけが残る」がこれ）。
	//
	// 代わりに **順位ごとに上書きし、古い深さの報告だけ捨てる**。順位が更新されない
	// あいだは 1 つ前の深さの値が残るが、**候補が消えるよりはるかにまし**で、
	// どの深さの答えかは `Line.Depth` に出る（将棋 UI の一般的な挙動でもある）。
	if in.Depth > a.depth {
		a.depth = in.Depth
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

	// ⚠️ **捨てるのは「その順位が既に持っている答えより浅い報告」だけ。**
	// **全体の深さと比べて捨てないこと** —— 順位ごとに進み方が違うので、
	// ある順位の**最初の報告**が他より浅いことは普通にある。全体と比べると
	// その候補を丸ごと落とすことになる（「三番手しか残らない」の一因）。
	prev, seen := a.lines[rank]
	if seen && in.Depth > 0 && prev.Depth > in.Depth {
		return Progress{}, false
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

	depth := in.Depth
	if depth <= 0 {
		depth = a.depth // 深さを書かないエンジン向け
	}
	a.lines[rank] = Line{
		Rank:  rank,
		Depth: depth,
		Score: newScore(in, a.black, a.ponanza),
		Moves: moves,
		Text:  text,
	}
	return a.progressLocked(), true
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
// ⚠️ **打ち切っても候補は減らない。** 深さの途中で止められても、順位ごとの値は
// 消さずに持っているため（上の add）。ここで拾い直す仕掛けは要らない。
func (a *accumulator) snapshot() Progress {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.progressLocked()
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
//
// ponanza は勝率に直すときの定数（0 なら既定）。⚠️ **勝率もここで入れておくこと** ——
// 評価値・表示文字列・勝率が同じ 1 か所で決まっていれば、食い違いようがない。
func newScore(in coreusi.Info, black bool, ponanza float64) Score {
	sign := 1
	if !black {
		sign = -1
	}
	withRate := func(s Score) Score {
		s.WinRate = WinRate(s, ponanza)
		return s
	}
	if in.HasMate {
		plies := in.ScoreMate * sign
		return withRate(Score{Mate: plies, Label: mateLabel(plies)})
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
		return withRate(Score{Mate: plies, Label: mateLabel(plies)})
	}
	v := in.ScoreCP * sign
	return withRate(Score{CP: v, Label: cpLabel(v)})
}

func cpLabel(v int) string {
	if v > 0 {
		return fmt.Sprintf("+%d", v)
	}
	return fmt.Sprintf("%d", v)
}

// mateLabel は詰みの表示（"▲詰 14手"）。
//
// ⚠️ **短く保つこと**（2026-08-12 に「先手の詰み 14手」から詰めた）。候補手の行の
// 評価値の欄は**幅を px で固定してある**（`--analyze-score-w`。桁が変わっても
// 一の位が揃うように）ので、**溢れると隣の「次の 1 手」まで押し出す**。
// 先後は▲△で足りる —— 読み筋の表記でも同じ記号を使っている。
func mateLabel(plies int) string {
	if plies > 0 {
		return fmt.Sprintf("▲詰 %d手", plies)
	}
	return fmt.Sprintf("△詰 %d手", -plies)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// checkSFEN は局面が解析にかけられる形かを確かめ、正規化した各フィールドを返す。
func checkSFEN(positionSFEN string, kingless bool) ([]string, error) {
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
	if err := ensurePlayable(fields[0], kingless); err != nil {
		return nil, err
	}
	return fields, nil
}

// ensurePlayable はエンジンに渡せる局面かを確かめる。
//
// **訂正 UI 側でこれを禁止しないこと。** 詰将棋のように玉が 1 枚しかない局面も
// 「正しい局面」として確定できるのが訂正 UI の要件（CLAUDE.md）。ここで止めるのは
// **エンジンに渡す瞬間だけ**で、盤を見ることも手を進めることも学習に送ることもできる。
//
// ⚠️ **「玉が片方だけ」は渡せることがある**（2026-09-12。詰将棋）。kingless が
// true（＝同梱エンジン）なら通す。**実測で、攻方の玉が無くても落ちず、1 手詰を
// 詰みスコアで見つける**（攻方の玉を隅に置いた版と手もスコアも一致した）。
// ⚠️ **外部エンジンでは通さない** —— 両玉を前提にしているエンジンがあり、
// 落ちるか出鱈目を返す。**測っていないものは安全側に倒す。**
//
// ⚠️ **通すのは「片側だけが 0 枚」のときだけ。** 両方無い局面は攻める相手が
// 居らず、玉が 2 枚ある局面は**駒数がそもそもおかしい**（どちらも詰将棋ではない）。
func ensurePlayable(boardSFEN string, kingless bool) error {
	vs := sfen.Inspect(boardSFEN, sfen.CheckKing).Filter(sfen.CheckKing)
	if len(vs) == 0 {
		return nil
	}
	if kingless && len(vs) == 1 && vs[0].Count == 0 {
		// 片側の玉だけが無い＝詰将棋の形。同梱エンジンは読める。
		return nil
	}
	msgs := make([]string, 0, len(vs))
	for _, v := range vs {
		msgs = append(msgs, v.Detail)
	}
	// ⚠️ **盤を見ることも手を進めることもできている**ので、断られるのが
	// ここだけであることが分かる言い方にする。
	hint := "（盤を動かすことはできます）"
	if !kingless && len(vs) == 1 && vs[0].Count == 0 {
		// 詰将棋なのに外部エンジンを選んでいる、が一番ありうる。**次の一手を書く。**
		hint = "（詰将棋のように玉が片方だけの局面は、同梱エンジンなら読めます。" +
			"設定でエンジンを同梱のものに切り替えてください）"
	}
	return fmt.Errorf("エンジンは玉の揃った局面しか扱えません: %s%s",
		strings.Join(msgs, " / "), hint)
}
