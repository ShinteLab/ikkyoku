package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	coreusi "github.com/ShinteLab/core/usi"
	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/analyze"
	"github.com/ShinteLab/ikkyoku/log"
)

// AnalyzeService は確定した局面をエンジンに解析させる Service（Phase 4）。
//
// **エンジンは USI を話す相手**（`analyze` → `core/usi/client`）。同梱の `engine` でも
// 外部エンジンの exe でも `prokishi.exe` でも、ここは変わらない
// （`_docs/phase4-engine-usi.md`）。
//
// ⚠️ **エンジンのプロセスは解析タブに居るあいだ生きる**（2026-08-12 に変えた。
// 以前は「1 回の解析のあいだだけ」だった）。接続は登録 ID ごとに持ち回り、
// **終わらせるのは `Release`（解析タブを離れた）とアプリの終了時。**
//
// 変えた理由は連続モードと全て解析 —— 1 手ごとに解析し直すので、
// **`isready`（NNUE の読み込み。数秒になりうる）を手数ぶん払っていた。**
// 引き換えの手当ては `analyze.Session` の注記にまとめてある（直列化・
// 設定を変えたときの繋ぎ直し・待機中のメモリ・壊れた接続の始末）。
//
// **局面はここが持たない。** 解析するのは常に「今 StudyService が持っている確定局面」で、
// フロントから SFEN を受け取らない（フロントに局面の写しを持たせない、という
// PositionService の方針と揃える。渡してもらう形にすると、採り直した直後に古い局面を
// 解析する経路ができる）。
//
// ⚠️ **訂正タブの局面（PositionService）を解析しない。** 駒を自由に動かせる状態の
// 盤は「まだ決めていない局面」で、その評価値には意味が無い。以前は訂正モードかどうかを
// フロントが見てボタンを止めていたが、**タブを分けたことで構造上そこに手が届かなくなった**
// （2026-08-10）。
//
// **解析する局面は常に 1 つ、エンジンは複数**（2026-08-11）。設定で「解析に使う」を
// 付けたエンジンが**同時に走り、結果が並ぶ**。エンジンが違えば同じ局面の評価が
// 食い違うのが普通で、**その食い違いこそ見たいもの**（どれが正しいかは局面による）。
//
// ⚠️ **1 エンジン 1 プロセス・1 接続。** USI は 1 接続で 1 探索なので、束ねる方法は
// 「エンジンの数だけ起こす」以外に無い。**エンジンをまたいで結果を合成しないこと**
// （平均も多数決も取らない。並べて人が読む）。
//
// **新しく始めると前の解析は全部打ち切る。** 検討ツリー（Phase 5）で複数の枝を
// 並べて解析したくなったらここを増やすが、**そのときも「今どの枝を見ているか」は
// UI 側の話**で、解析そのものは 1 局面ずつ独立している（設計原則1）。
//
// 途中経過はイベントで流す。反復深化は深さが 1 つ終わるたびに答えが更新されるので、
// 終わるまで黙っていると数秒間固まったように見える。
//
//	analyze:info    深さが 1 つ完走した（Progress）
//	analyze:done    そのエンジンの解析が終わった（Result）
//	analyze:failed  そのエンジンが始められなかった・エラーになった（理由の文字列）
//
// ⚠️ **どのイベントにも engineId が載る。** 複数のエンジンが同時に喋るので、
// **seq だけでは行き先を決められない**（フロントはエンジンごとに表示を持つ）。
type AnalyzeService struct {
	// study は**解析タブが持っている確定局面**。⚠️ **PositionService（訂正タブ）
	// を直に見ないこと** —— あちらは訂正の途中の、まだ決めていない局面で、
	// その評価値には意味が無い。
	study    *StudyService
	settings *SettingsService
	// Emit は途中経過をフロントへ流す口。**`_cmd/ikkyoku` が差し込む。**
	//
	// ⚠️ **ここで Wails を import しないこと**（SettingsService.PickFile と同じ）。
	// nil なら黙って捨てる —— **解析そのものは進む**（設計原則3）。
	Emit EventEmitter

	mu sync.Mutex
	// cancel は走っている解析の打ち切り。走っていなければ nil。
	// **エンジンをまたいで 1 つ**（止めるときは全部止める）。
	cancel context.CancelFunc
	// done は走っている解析が**全部**終わったことの通知（終了時に待つため）。
	done chan struct{}
	// seq は解析の世代。**打ち切った解析の途中経過が後から届く**ので、
	// フロントはこれで古いものを捨てる。**エンジンごとではなく解析ごと**の番号。
	seq int
	// engines は今の世代で走らせたエンジン（表示用。登録順）。
	engines []AnalyzeEngine
	// lastEngine は登録 ID → 最後に名乗ったエンジン名。**表示用**
	// （閉じたあとも名前だけは出せるようにする）。
	lastEngine map[string]string
	// sessions は登録 ID → 繋ぎっぱなしの接続（2026-08-12）。
	//
	// ⚠️ **解析のたびに作り直さないこと。** それでは接続を使い回す意味が無い
	// （`isready` を毎回払う）。作り直すのは**繋ぎ先が変わったとき**だけで、
	// 判断は `engineKey` の指紋。
	sessions map[string]*engineSlot
}

// AnalyzeEngine は解析に参加しているエンジン 1 つ（フロントの表示の単位）。
type AnalyzeEngine struct {
	// ID は設定の登録 ID。**イベントの振り分けはこれ。**
	ID string `json:"id"`
	// Label は画面に出す名前（`EngineEntry.DisplayName`）。
	//
	// **繋ぐ前から出せる名前。** 起動を待っているあいだの見出しに要る。
	// 人が付けた名前 → **前に繋いだときの名乗り** → ファイル名 → 「同梱エンジン」。
	Label string `json:"label"`
	// Name はエンジンが名乗った名前（`id name`。まだ繋いでいなければ空）。
	Name string `json:"name"`
	// MultiPV は候補手を何本出させるか（**このエンジンの設定**）。
	//
	// ⚠️ **エンジンごとに違ってよい**（2026-08-15。以前は解析の行に共通の欄が
	// 1 つあった）。速いエンジンは多めに、重いエンジンは 1 本、という使い分けが
	// できないと、**複数を同時に走らせる意味が薄れる**。
	MultiPV int `json:"multiPv"`
	// Custom は Label を人が付けたか（設定タブの名前欄）。
	//
	// ⚠️ **名乗った名前を見出しに足すかどうかの判断**（2026-08-15）。
	// 人が名前を付けているなら、**そう呼びたくて付けた名前**なので足さない
	// （prokishi 越しだと `id name` にプラグイン名まで並んで長い）。
	//
	// ⚠️ **一度繋いだあとは `Label` 自体が名乗りになる**（`DisplayName`）ので、
	// そのときも足さない —— 同じ名前が括弧で 2 回並ぶ。判断はフロントの
	// 「`Label` と違うときだけ」で、**`Custom` と合わせて 2 つとも要る**
	// （`Custom` は「人が付けたか」という設定の事実で、一致は別の話）。
	//
	// ⚠️ **フロントで `label === name` を見て判断しないこと** —— 名前を付けたかどうかは
	// 設定が持っている事実で、たまたま一致したかどうかとは別物。
	Custom bool `json:"custom"`
	// Builtin は同梱のエンジンか。
	Builtin bool `json:"builtin"`
}

func NewAnalyzeService(study *StudyService, settings *SettingsService) *AnalyzeService {
	return &AnalyzeService{study: study, settings: settings}
}

// close は走っている解析を打ち切り、エンジンが終わるまで待つ（アプリの終了時）。
//
// ⚠️ **待たないと外部エンジンのプロセスが残る。** 親が先に消えても、Windows では
// 子プロセスは道連れにならない。
//
// ⚠️ **`//wails:ignore` を外さないこと。** これは `_cmd/ikkyoku` が起動・終了で
// 呼ぶための口で、**フロントの API ではない**（外すと bindings に出てしまう）。
//
//wails:ignore
func (s *AnalyzeService) Close() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(engineShutdownWait):
			log.Warn("エンジンの終了を待ちきれませんでした")
		}
	}
	// ⚠️ **打ち切っただけではプロセスは終わらない**（接続を使い回すので、
	// 解析が終わってもエンジンは生きている）。**ここで閉じないと残る。**
	s.closeSessions()
}

// engineShutdownWait は終了時にエンジンの後始末を待つ上限。
//
// `stop` → `bestmove` → `quit` → プロセス終了、まで待つ。長すぎるとアプリが
// 閉じなくなるので、諦める線を引いておく。
const engineShutdownWait = 5 * time.Second

// newSession は登録 1 件ぶんのセッションを作る。
//
// ⚠️ **接続を使い回すようになったので、作り直しは「繋ぎ先が変わったとき」だけ**
// （2026-08-12）。判断は `engineKey` の指紋で行う（`sessionFor`）。
func newSession(e ikkyoku.EngineEntry) *analyze.Session {
	if e.Path == "" {
		return analyze.NewLocalSession()
	}
	return analyze.NewExecSession(e.Path, e.Options)
}

// engineKey は繋ぎ先の指紋（実行ファイルと `setoption` の中身）。
//
// ⚠️ **options を含めること。** `setoption` は `isready` の前にしか効かないので、
// **値を変えたら繋ぎ直さないと反映されない**（毎回起こしていた頃は自動で解決して
// いた）。しかもエンジンは黙って古い値のまま動くので、**画面では気づけない。**
//
// ⚠️ **MultiPV だけは外す**（2026-08-15）。あれは**探索ごとに送る option**で
// （`client.Session.Analyze` が `go` の前に送る）、`isready` を跨ぐ必要が無い。
// 指紋に入れると、**候補手の本数を変えるたびにエンジンを起こし直す**ことになり、
// 評価関数の読み込みを毎回払う（解析タブで気軽に変える値なので特に効く）。
//
// ⚠️ **ただし詰将棋エンジンは別**（2026-09-12）。**KomoringHeights は `isready` の
// 前に送らないと MultiPV が効かない**（実測。後から送っても候補は 1 本のまま）ので、
// あちらでは指紋に入れて**繋ぎ直させる**。詰将棋エンジンは評価関数を読まないので
// 起こし直しが安い。
func engineKey(e ikkyoku.EngineEntry) string {
	names := make([]string, 0, len(e.Options))
	for k := range e.Options {
		// ⚠️ **詰将棋エンジンでは MultiPV も指紋に入れる**（2026-09-12 に実測）。
		// **KomoringHeights は `isready` の前に送らないと MultiPV が効かない**
		// （後から `setoption` を送っても無視され、候補は 1 本のまま）。
		// 繋ぎ直しは安い（評価関数を読まないので 1 秒ほど）ので、
		// **効かないより繋ぎ直すほうがよい。**
		if k == ikkyoku.MultiPVOption && !e.Mate {
			continue
		}
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(e.Path)
	for _, k := range names {
		b.WriteString("\x00")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(e.Options[k])
	}
	return b.String()
}

// sessionFor は登録 ID ごとに接続を持ち回る。**繋ぎ先が変わっていたら捨てて作り直す。**
func (s *AnalyzeService) sessionFor(e ikkyoku.EngineEntry) *analyze.Session {
	key := engineKey(e)

	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]*engineSlot{}
	}
	slot, ok := s.sessions[e.ID]
	if ok && slot.key == key {
		s.mu.Unlock()
		return slot.session
	}
	next := &engineSlot{key: key, session: newSession(e)}
	s.sessions[e.ID] = next
	s.mu.Unlock()

	// **古い接続は鍵の外で閉じる**（`Close` は走っている探索が畳まれるのを待つので、
	// ロックを持ったまま呼ぶと解析中に設定を変えたときに固まる）。
	if ok {
		slot.session.Close()
	}
	return next.session
}

// engineSlot は登録 1 件ぶんの接続と、その繋ぎ先の指紋。
type engineSlot struct {
	key     string
	session *analyze.Session
}

// closeSessions は持っている接続を全部終わらせる。
//
// **呼ぶのは「解析タブを離れたとき」（`Release`）と「アプリの終了時」。**
// ⚠️ **待機中のエンジンは `USI_Hash` ぶん（GB 級になりうる）のメモリを掴んだまま**
// なので、解析タブに居ないあいだまで生かしておかない。
func (s *AnalyzeService) closeSessions() {
	s.mu.Lock()
	slots := s.sessions
	s.sessions = nil
	s.mu.Unlock()
	for _, slot := range slots {
		slot.session.Close()
	}
}

// Release は走っている解析を打ち切って、エンジンのプロセスを終わらせる。
//
// **フロントが解析タブを離れるときに呼ぶ。** 接続を使い回す代わりに、
// **タブを離れたら手放す**というのがこの作りの寿命の決め方
// （アイドルタイマーを持たずに済むように、境界を目に見える操作に置いてある）。
//
// ⚠️ **走っている解析も止まる。** 全て解析の途中でタブを移ると止まるのは
// そういう約束で、**そこまでの評価値は残る**（設計原則3）。
func (s *AnalyzeService) Release() {
	s.Close()
}

// EngineCheck は「接続を確認」の結果。
//
// **エラーも値として返す**（error にしない）。設定タブに出す情報であって、
// 呼び出しが失敗したわけではない。エンジンを置く前に確かめるのは普通の使い方。
type EngineCheck struct {
	// ID は確かめた登録の ID。**どの行の結果なのかを示す**（設定タブには
	// エンジンが並んでいるので、結果を返す先が分からないと出せない）。
	ID string `json:"id"`
	// Label は設定タブで付けた名前。
	Label string `json:"label"`
	// Path は確かめた実行ファイル（同梱なら空）。
	Path string `json:"path"`
	// Builtin は同梱のエンジンか。
	Builtin bool `json:"builtin"`
	// OK は繋がったか。
	OK bool `json:"ok"`
	// Name は繋がったエンジンの名前（`id name`）。
	Name string `json:"name"`
	// Options はエンジンが宣言した option の数。
	Options int `json:"options"`
	// Applied は `isready` の前に送った `setoption` の数（**既定値を含む**）。
	//
	// **送ったことが見えないと、効いているか確かめようがない**（option には
	// 応答が返らない）。宣言より少ないのが普通（button と空の既定値は送らない）。
	Applied int `json:"applied"`
	// StartupMS は起動から `readyok` までの所要ミリ秒。
	//
	// **解析タブで最初に解析するときに 1 回だけ待つ**（そのあとは接続を使い回す）。
	// 繋ぎ先を選ぶ材料として出す。
	StartupMS int64 `json:"startupMs"`
	// Error は繋がらなかった理由（日本語）。
	Error string `json:"error"`
}

// CheckEngine は登録したエンジン 1 つに実際に繋いでみる（設定タブの「接続を確認」）。
//
// **1 つずつ確かめる。** まとめて起こすと、どれが遅いのか・どれが落ちたのかが
// 分からなくなる（複数を同時に起こすのは解析のときだけ）。
//
// **確かめたら閉じる。** 解析していないのにプロセスを残さない。
// 起動にかかった時間も返すので、**解析のたびに払うコストがここで分かる。**
func (s *AnalyzeService) CheckEngine(id string) EngineCheck {
	entry, ok := s.settings.engineEntry(id)
	if !ok {
		return EngineCheck{ID: id, Error: "そのエンジンの登録が見つかりません"}
	}
	out := EngineCheck{
		ID:      entry.ID,
		Label:   entry.DisplayName(),
		Path:    entry.Path,
		Builtin: entry.Path == "",
	}

	ctx, cancel := context.WithTimeout(context.Background(), engineConnectTimeout)
	defer cancel()
	info, err := newSession(entry).Connect(ctx)
	if err != nil {
		out.Error = err.Error()
		log.Warn("エンジンに繋げませんでした", "id", entry.ID, "path", entry.Path, "error", err)
		return out
	}
	out.OK = true
	out.Name = info.Name
	out.Options = info.Options
	out.Applied = info.Applied
	out.StartupMS = info.StartupMS
	// ⚠️ **宣言を設定ファイルに控える。** これが無いと、設定タブで option を
	// 出すたびにエンジンを起こすことになる（評価関数の読み込みで数秒かかるものがある）。
	// **控える入口はここだけ** —— 宣言は繋いで初めて分かるので、繋ぐ操作と
	// 結び付いているのが素直（保存の操作では繋がない、という線引きは変えていない）。
	s.settings.setEngineOptionSpecs(entry.ID, engineOptions(info.Declared))
	s.rememberEngine(entry.ID, info.Name)
	log.Info("エンジンに繋がりました",
		"id", entry.ID, "name", info.Name, "path", entry.Path,
		"options", info.Options, "applied", info.Applied, "startupMs", info.StartupMS)
	return out
}

// engineOptions は `core/usi` の宣言を設定ファイルに書く形へ写す。
//
// ⚠️ **写す場所はここ 1 か所。** `core/usi.Option` をそのまま設定ファイルに
// 埋めると、プロトコルの語彙の変更が**保存形式の変更**になってしまう
// （`ikkyoku.EngineOption` の注記）。
func engineOptions(declared []coreusi.Option) []ikkyoku.EngineOption {
	out := make([]ikkyoku.EngineOption, 0, len(declared))
	for _, o := range declared {
		out = append(out, ikkyoku.EngineOption{
			Name: o.Name, Type: o.Type, Default: o.Default,
			Min: o.Min, Max: o.Max, HasMin: o.HasMin, HasMax: o.HasMax, Vars: o.Vars,
		})
	}
	return out
}

// engineConnectTimeout は接続の確認に使う上限。
//
// 評価関数の読み込みに時間のかかるエンジンがあるので、ハンドシェイクの上限
// （`client.HandshakeTimeout`）より長めに取る。
const engineConnectTimeout = 30 * time.Second

// cancelGrace は**前の解析が畳まれるのを待つ上限**（`Start`）。
//
// ⚠️ **無制限に待たないこと。** `stop` に応えないエンジンが居ると、そこで
// 「解析」を押しても何も起きないアプリになる。見切ったら重なったまま始める。
const cancelGrace = 3 * time.Second

// interruptSlack は「頼んだ秒数を使い切った」と見なす遊び（`Interrupted`）。
//
// **打ち切りを送るのはこちら**（`go infinite` + `stop`）なので、往復のぶんだけ
// 頼んだ秒数より手前で終わる。⚠️ **狭くしすぎないこと** —— 普通に考え切った
// 解析まで「外から止められた」と読むと、**連続解析が 1 手目で止まる。**
const interruptSlack = 500 * time.Millisecond

// EventEmitter はフロントへイベントを流す口。
type EventEmitter func(name string, data any)

// AnalyzeProgress はイベントで流す途中経過。
//
// **analyze.Progress をそのまま載せるだけ**にすること（評価値の符号も表示文字列も
// あちらが決めている）。ここで組み立て直すと、書式が 2 か所に散る。
// 埋め込みにしていないのは、bindings の生成でフィールドが平らになるかどうかに
// 依存しないため。
type AnalyzeProgress struct {
	// Seq は解析の世代。**これが今の世代と違うイベントは捨てる。**
	Seq int `json:"seq"`
	// EngineID はどのエンジンが喋ったか（設定の登録 ID）。
	//
	// ⚠️ **複数のエンジンが同時に喋る。** seq だけでは行き先を決められないので、
	// フロントはこれで表示先を選ぶ。
	EngineID string `json:"engineId"`
	// EngineName はそのエンジンが名乗った名前（`id name`）。
	EngineName string           `json:"engineName"`
	Progress   analyze.Progress `json:"progress"`
	// Done は最後の 1 通か。
	Done bool `json:"done"`
	// StartupMS は起動から `readyok` までの所要ミリ秒（**done のときだけ入る**）。
	//
	// **払ったコスト**なので画面に出す。これが見えないと、遅いのが探索のせいなのか
	// 起動のせいなのか分からない。⚠️ **接続を使い回したときは 0**（下の Reused）。
	StartupMS int64 `json:"startupMs"`
	// Reused は繋ぎっぱなしの接続を使い回したか。
	//
	// **`StartupMS` が 0 の理由がこれ。** 「速かった」と「払っていない」を
	// 画面で区別するために要る。
	Reused bool `json:"reused"`
	// Interrupted は**考える時間を使い切る前に外から止められた**か（done のときだけ）。
	//
	// ⚠️ **「打ち切られた」（`Stopped`）とは違う。** 時間切れも `stop` を送って
	// 終わるので、あちらは**普通に考え切ったときも立つ**。ここが立つのは
	// **頼んだ秒数に届いていない**ときだけで、原因は外から来た `Stop` か、
	// **別の解析が始まって世代ごと打ち切られた**か。
	//
	// ⚠️ **連続解析はこれを見て止まる**（`sidepane.ts`）。打ち切られた解析も
	// `analyze:done` を出す（設計原則3: そこまでの評価値は残る）ので、
	// **これが無いと「1 手ぶん終わった」と読んで次の手へ進み、手数ぶんが
	// 数秒で流れる**（2026-09-09 に踏んだ）。
	//
	// ⚠️ **「無制限」（`Movetime` が 0）では立たない** —— 止めて終わるのが
	// 普通の終わり方なので、区別のしようが無い。
	Interrupted bool `json:"interrupted"`
}

// AnalyzeFailure は解析が失敗したことの通知。
//
// ⚠️ **1 つのエンジンが落ちても、他のエンジンの解析は続く**（設計原則3）。
// これは「そのエンジンの」失敗であって、解析全体の失敗ではない。
type AnalyzeFailure struct {
	Seq      int    `json:"seq"`
	EngineID string `json:"engineId"`
	Error    string `json:"error"`
}

// AnalyzeState は今解析中かどうか。フロントの初期表示と、開始・停止の応答に使う。
type AnalyzeState struct {
	Running bool `json:"running"`
	Seq     int  `json:"seq"`
	// SFEN は解析にかけた局面（開始時のみ入る）。何を評価した値なのかを示す。
	SFEN string `json:"sfen"`
	// Engines は解析に参加しているエンジン（登録順）。
	//
	// **何が出した評価値なのかは見せること。** 繋ぎ先を差し替えられる以上、
	// 評価値だけ出して出所を伏せると比べようがない。**複数走るなら尚更**で、
	// フロントはこの並びのぶんだけ結果の枠を作る（起動を待つあいだも見出しが出る）。
	Engines []AnalyzeEngine `json:"engines"`
}

// Start は今の局面の解析を始める。**ここでエンジンを起こす。**
//
// seconds は考える秒数。**0 以下なら「止めるまで考え続ける」**（＝そのあいだ
// エンジンも生きている）。時間で打ち切っても、それまでに完走した深さの評価値は出る
// （設計原則3）。
//
// ⚠️ **候補手の本数（MultiPV）はここでは受け取らない**（2026-08-15。以前は引数
// だった）。**エンジンごとの設定**（`EngineEntry.MultiPV`）を `runOne` が読む ——
// 速いエンジンは多めに、重いエンジンは 1 本、という使い分けができないと、
// **複数を同時に走らせる意味が薄れる**。⚠️ **対応していないエンジンでは無視される**
// （自作 `engine` が今それ。engine/TODO.md の 1）ので、
// **1 本しか返らないことを異常扱いしないこと。**
//
// ⚠️ **局面が確定していなければエラー。** 手番か駒台の先後が未決だと SFEN が
// 組み上がらない（決めていないことを勝手に決めない。設計原則5）。訂正 UI で
// 決めてもらう以外に手は無いので、ここは警告ではなくエラーにする。
// ⚠️ **設定で「解析に使う」が 1 つも無ければエラー。** 何も起きないより、
// 設定を直す先が分かるほうがよい。
func (s *AnalyzeService) Start(seconds int) (AnalyzeState, error) {
	// ⚠️ **根と手順を分けて受け取る。** 組み立て直した 1 つの SFEN を渡すと、
	// 千日手と連続王手をエンジンが判定できない（`position sfen <根> moves ...`）。
	target, err := s.study.analyzeTarget()
	if err != nil {
		return AnalyzeState{}, err
	}
	// **エンジンは解析のあいだだけ生きる。** 設定はここで読まれるので、
	// 変えた結果が次の解析にそのまま効く（繋ぎ直しの判断が要らない）。
	entries := s.settings.enabledEngines()
	if len(entries) == 0 {
		return AnalyzeState{}, fmt.Errorf("解析に使うエンジンが 1 つも選ばれていません（設定タブで選んでください）")
	}

	// 走っているものがあれば打ち切る。
	//
	// ⚠️ **畳まれるまで待つこと**（2026-09-09。以前は待っていなかった）。
	// 待たないと、**前の世代の `runOne` が `runMu` を掴んだまま**新しい世代が
	// 積み上がり、1 つの接続に対して**局面の違う探索が何本も並ぶ**。
	// フロント側は世代で捨てるので画面は破綻しないが、**エンジンは前の局面を
	// 読み続けたまま次の `position` を待つ**ことになり、連続解析では
	// 「他の手も同時に解析している」「そのうち止まる」という形で出る。
	//
	// ⚠️ **待ちは有界にすること。** 行儀の悪いエンジンが `bestmove` を返さないと
	// ここで止まる（`client.Session` の猶予も含めて `cancelGrace` で見切る）。
	// **見切ったら諦めて始める** —— 始められないより、重なるほうがまし（設計原則3）。
	s.mu.Lock()
	prev, prevCancel := s.done, s.cancel
	s.mu.Unlock()
	if prevCancel != nil {
		prevCancel()
	}
	if prev != nil {
		select {
		case <-prev:
		case <-time.After(cancelGrace):
			log.Warn("前の解析が畳まれるのを待ちきれませんでした。重なったまま始めます")
		}
	}

	s.mu.Lock()
	s.seq++
	seq := s.seq
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	engines := make([]AnalyzeEngine, 0, len(entries))
	for _, e := range entries {
		engines = append(engines, AnalyzeEngine{
			ID:      e.ID,
			Label:   e.DisplayName(),
			Name:    s.lastEngine[e.ID],
			MultiPV: e.MultiPV(),
			Custom:  e.Name != "",
			Builtin: e.Path == "",
		})
	}
	s.engines = engines
	s.mu.Unlock()

	// ⚠️ **seconds が 0 以下なら「止めるまで考え続ける」。**
	// これが「ずっと解析していたい」の表し方で、そのあいだプロセスも生きている。
	// ⚠️ **勝率の定数もここで読む。** 設定は解析を始めるたびに読まれるので、
	// 変えた結果が次の解析にそのまま効く（`setoption` と違って繋ぎ直しは要らない ——
	// **エンジンに渡す値ではなく、評価値の見せ方だから**）。
	// ⚠️ **MultiPV はここでは入れない**（エンジンごとに違う。`runOne` が入れる）。
	opt := analyze.Options{
		Moves:           target.Moves,
		PonanzaConstant: s.settings.ponanzaConstant(),
	}
	if seconds > 0 {
		opt.Movetime = time.Duration(seconds) * time.Second
	}

	// ⚠️ **エンジンごとに 1 プロセス・1 goroutine。** USI は 1 接続 1 探索なので
	// 束ねようがなく、束ねる意味も無い（結果は並べて人が読む）。
	var wg sync.WaitGroup
	for _, entry := range entries {
		wg.Add(1)
		go func(entry ikkyoku.EngineEntry) {
			defer wg.Done()
			s.runOne(ctx, seq, entry, target, opt)
		}(entry)
	}
	go func() {
		wg.Wait()
		// **全部終わってから畳む。** 1 つ落ちただけで解析が終わったことにしない
		// （設計原則3: 残りのエンジンの評価値は出る）。
		s.finish(seq)
		cancel()
		close(done)
	}()

	// ⚠️ **SFEN には「解析する局面」を入れる**（根ではない）。手を進めても根は
	// 変わらないので、根を入れるとフロントが「局面が変わった」に気づけず、
	// **前の手の評価値が今の盤の上に残る。**
	return AnalyzeState{Running: true, Seq: seq, SFEN: target.Current, Engines: engines}, nil
}

// runOne はエンジン 1 つぶんの解析。**他のエンジンの成否に影響しない。**
func (s *AnalyzeService) runOne(
	ctx context.Context, seq int, entry ikkyoku.EngineEntry, target analyzeTarget, opt analyze.Options,
) {
	label := entry.DisplayName()
	// ⚠️ **候補手の本数はエンジンごと**（`opt` は値渡しなので、ここで入れてよい）。
	// 探索ごとに送る option なので、繋ぎ直しは要らない（`engineKey` の注記）。
	opt.MultiPV = entry.MultiPV()
	// 評価値グラフに残すのは**最善手（順位 1）の評価値**。
	//
	// ⚠️ **途中経過のたびに書く**（`done` だけにしない）。時間で打ち切っても
	// 「無制限」で止めても、**そこまでで一番深い答えが残っているのが正しい**
	// （設計原則3）。同じ手数に上書きしていくだけなので、書き込みは安い。
	//
	// ⚠️ **記録先の判断は `StudyService` に任せる**（世代が合わなければ捨てられる）。
	// ここで「まだ同じ局面か」を確かめようとしないこと —— 手順を持っていないので、
	// 確かめようがない。
	record := func(p analyze.Progress) {
		if len(p.Lines) == 0 {
			return
		}
		best := p.Lines[0] // Lines は Rank の昇順（analyze.Progress）
		s.study.recordEval(target.Epoch, target.NodeID, entry.ID, label, best.Score, best.Depth)
	}
	res, err := s.sessionFor(entry).Analyze(ctx, target.Root, opt, func(p analyze.Progress) {
		record(p)
		s.emit("analyze:info", AnalyzeProgress{
			Seq: seq, EngineID: entry.ID, EngineName: s.engineName(entry.ID), Progress: p,
		})
	})
	if err != nil {
		log.Warn("解析できませんでした",
			"engine", label, "id", entry.ID, "sfen", target.Root, "error", err)
		s.emit("analyze:failed", AnalyzeFailure{Seq: seq, EngineID: entry.ID, Error: err.Error()})
		return
	}
	record(res.Progress)
	s.rememberEngine(entry.ID, res.Engine)

	// ⚠️ **debug で出す**（2026-08-14。以前は Info）。**連続モードでは 1 手ごとに、
	// 連続解析では手数ぶん**出るので、151 手の棋譜を通すとこの行だけでログが埋まる
	// （エンジンを 2 つ有効にすればその倍）。**消したのではなく黙らせただけ**なので、
	// 追うときはハンドラの Level を debug にすること。
	log.Debug("解析しました",
		"engine", res.Engine, "id", entry.ID, "sfen", target.Root, "depth", res.Depth,
		"best", res.Bestmove, "nodes", res.Nodes,
		"elapsedMs", res.ElapsedMS, "startupMs", res.StartupMS, "reused", res.Reused,
		"stopped", res.Stopped)
	s.emit("analyze:done", AnalyzeProgress{
		Seq: seq, EngineID: entry.ID, EngineName: res.Engine,
		Progress: res.Progress, Done: true, StartupMS: res.StartupMS, Reused: res.Reused,
		Interrupted: interruptedRun(opt.Movetime, res.Progress.ElapsedMS, res.Stopped),
	})
}

// MateSolution は詰み探索の結末（画面に出す形）。
type MateSolution struct {
	// Kind は "mate" / "nomate" / "timeout" / "notimplemented"。
	//
	// ⚠️ **「詰みなし」と「時間切れ」を混ぜないこと。** 後者は分からなかっただけで、
	// 時間を伸ばせば答えが変わる（画面の文言もそう出し分ける）。
	Kind string `json:"kind"`
	// Moves は詰み手順（USI）。**手順ツリーへ足すのに使う。**
	Moves []string `json:"moves"`
	// Text は日本語表記（画面に出すのはこちら）。
	Text []string `json:"text"`
	// Progress は**詰み手順の候補**（`MultiPV`。解析の候補手と同じ形）。
	//
	// ⚠️ **いきなり手順に足さない**（2026-09-12）。**候補として出して、足すのは人が
	// 選んでから** —— 解析の候補手と同じ道具（右クリック）で足せるように、
	// **形を揃えて返す**。詰将棋では「他の詰み」＝余詰そのものなので、
	// **1 本目だけ見せて終わりにしない。**
	Progress analyze.Progress `json:"progress"`
	// SFEN は解いた局面。**「どの局面の答えか」が分かるように返す。**
	SFEN string `json:"sfen"`
	// Engine は答えたエンジンの表示名。
	Engine string `json:"engine"`
	// EngineID は登録 ID。**手順ツリーへ足すときの「誰が言った手か」**に使う
	// （`StudyService.AddLine`）。
	EngineID string `json:"engineId"`
	// MultiPV はこのエンジンに設定されている候補手の本数。
	//
	// ⚠️ **返ってきた候補の数ではない**（詰みが 1 つしか無ければ 1 本しか返らない）。
	// 画面のエンジンの見出しに出す「候補 n」の選択肢がこの値で、
	// **入口は解析タブのその欄だけ**（設定タブには出していない。編集口を 2 つにしない）。
	MultiPV   int   `json:"multiPv"`
	ElapsedMS int64 `json:"elapsedMs"`
}

// SolveMate は今見ている局面の詰みを解く（詰将棋。2026-09-12）。
//
// ⚠️ **通常の解析とは別の口**（`Start` は使わない）。答えの形が違ううえ、
// **エンジンも別**（`Config.MateEngine`）。詰将棋エンジンは通常の `go` に
// 答えないことがあるので、**同じ一覧に混ぜて使い回さない。**
//
// ⚠️ **手順は渡さない**（`position sfen <今の局面>` だけ）。詰将棋は
// **その局面だけで完結する**問題で、そこへ至る手順は答えに関係しない。
//
// seconds は考えさせる上限（0 なら既定）。⚠️ **詰み探索では時間切れも答えの 1 つ**
// なので、**エンジン自身に時間を伝える**（`analyze.MateOptions`）。
func (s *AnalyzeService) SolveMate(seconds int) (MateSolution, error) {
	target, err := s.study.analyzeTarget()
	if err != nil {
		return MateSolution{}, err
	}
	entry, ok := s.settings.mateEngine()
	if !ok {
		return MateSolution{}, fmt.Errorf(
			"詰将棋エンジンが登録されていません（設定タブでエンジンを足し、「詰将棋エンジン」を入れてください）")
	}

	limit := time.Duration(seconds) * time.Second
	// ⚠️ **候補の本数はエンジンの登録から**（通常の解析と同じ入口。設定タブの
	// エンジンの行で変える）。**詰み探索用に別の欄を作らないこと。**
	res, err := s.sessionFor(entry).Mate(context.Background(), target.Current,
		analyze.MateOptions{Limit: limit, MultiPV: entry.MultiPV()}, nil)
	if err != nil {
		log.Warn("詰み探索に失敗しました",
			"engine", entry.DisplayName(), "sfen", target.Current, "error", err)
		return MateSolution{}, err
	}
	s.rememberEngine(entry.ID, res.Engine)
	log.Info("詰み探索が終わりました",
		"engine", res.Engine, "kind", string(res.Kind), "moves", len(res.Moves),
		"elapsedMs", res.ElapsedMS, "sfen", target.Current)

	return MateSolution{
		Kind:      string(res.Kind),
		Progress:  res.Progress,
		Moves:     res.Moves,
		Text:      res.Text,
		SFEN:      target.Current,
		Engine:    s.engineName(entry.ID),
		EngineID:  entry.ID,
		MultiPV:   entry.MultiPV(),
		ElapsedMS: res.ElapsedMS,
	}, nil
}

// interruptedRun は**考える時間を使い切る前に外から止められたか**を返す。
//
// ⚠️ **`stopped` だけでは区別できない。** 時間切れもこちらから `stop` を送って
// 終わるので、**普通に考え切ったときも立つ**。見るのは**頼んだ秒数に届いたか。**
//
// ⚠️ **`stopped` が false なら常に false** —— エンジンが自分で `bestmove` を
// 返したとき（詰み・固定深さのエンジン）は、早く終わっても**正常な終わり方**。
// ここを落とすと、**詰みの局面で連続解析が止まる。**
//
// ⚠️ **「無制限」（movetime が 0）では判断しない。** 止めて終わるのが普通の
// 終わり方なので、区別のしようが無い。
func interruptedRun(movetime time.Duration, elapsedMS int64, stopped bool) bool {
	if movetime <= 0 || !stopped {
		return false
	}
	return elapsedMS+interruptSlack.Milliseconds() < movetime.Milliseconds()
}

// Stop は走っている解析を打ち切る。**打ち切っても評価値は出る**ので、
// 「やめる」というより「ここまでで良い」に近い。
//
// ⚠️ **止めるときは全部のエンジンを止める。** 同じ局面を読ませている以上、
// 片方だけ生かしておく意味が無い（比べるための同時解析なので）。
func (s *AnalyzeService) Stop() AnalyzeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	return AnalyzeState{Running: s.cancel != nil, Seq: s.seq, Engines: s.engines}
}

// State は今の状態を返す（何も始めない）。フロントの初期表示用。
func (s *AnalyzeService) State() AnalyzeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return AnalyzeState{Running: s.cancel != nil, Seq: s.seq, Engines: s.engines}
}

// finish は解析が終わったことを記録する。**自分より新しい解析が始まっていたら
// 何もしない**（打ち切られた古い解析が、走っている新しい解析を止めないように）。
func (s *AnalyzeService) finish(seq int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seq == seq {
		s.cancel, s.done = nil, nil
	}
}

// rememberEngine は登録ごとに、最後に名乗ったエンジンの名前を覚える（表示用）。
//
// ⚠️ **設定ファイルにも控える**（`rememberEngineName`）。ここだけで持つと
// **アプリを閉じた時点で消える**ので、次の起動では設定タブも解析タブも
// exe のファイル名に戻ってしまう（名乗りは繋がないと分からないため、
// 起動しただけでは二度と出てこない）。
//
// ⚠️ **向こうは変わっていなければ書かない。** これは解析が終わるたびに呼ばれる。
func (s *AnalyzeService) rememberEngine(id, name string) {
	if id == "" || name == "" {
		return
	}
	s.settings.rememberEngineName(id, name)
	s.mu.Lock()
	if s.lastEngine == nil {
		s.lastEngine = map[string]string{}
	}
	s.lastEngine[id] = name
	for i := range s.engines {
		if s.engines[i].ID == id {
			s.engines[i].Name = name
		}
	}
	s.mu.Unlock()
}

// engineName は登録が最後に名乗った名前を返す（まだ繋いでいなければ空）。
// **公開しない**（Service の公開メソッドは bindings に出てフロントの API になる）。
func (s *AnalyzeService) engineName(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastEngine[id]
}

func (s *AnalyzeService) emit(name string, data any) {
	if s.Emit == nil {
		return
	}
	s.Emit(name, data)
}
