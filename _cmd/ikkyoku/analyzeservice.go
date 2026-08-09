package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ShinteLab/ikkyoku/analyze"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// AnalyzeService は確定した局面をエンジンに解析させる Service（Phase 4）。
//
// **エンジンは USI を話す相手**（`analyze` → `core/usi/client`）。同梱の `engine` でも
// 外部エンジンの exe でも `prokishi.exe` でも、ここは変わらない
// （`_docs/phase4-engine-usi.md`）。
//
// ⚠️ **エンジンのプロセスは 1 回の解析のあいだだけ生きる。** 解析を始めるときに
// 起こし、終わったら `quit` する。「ずっと解析していたい」は**時間無制限の解析**
// （考える秒数を「無制限」にする）として表すので、そのあいだは生きている。
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
// **同時に走るのは 1 本だけ。** 新しく始めると前の解析は打ち切る。検討ツリー
// （Phase 5）で複数の枝を並べて解析したくなったらここを増やすが、**そのときも
// 「今どの枝を見ているか」は UI 側の話**で、解析そのものは 1 局面ずつ独立している
// （設計原則1）。
//
// 途中経過はイベントで流す。反復深化は深さが 1 つ終わるたびに答えが更新されるので、
// 終わるまで黙っていると数秒間固まったように見える。
//
//	analyze:info    深さが 1 つ完走した（Progress）
//	analyze:done    解析が終わった（Result）
//	analyze:failed  始められなかった・エラーになった（理由の文字列）
type AnalyzeService struct {
	logger *slog.Logger
	// study は**解析タブが持っている確定局面**。⚠️ **PositionService（訂正タブ）
	// を直に見ないこと** —— あちらは訂正の途中の、まだ決めていない局面で、
	// その評価値には意味が無い。
	study    *StudyService
	settings *SettingsService
	app      *application.App

	mu sync.Mutex
	// cancel は走っている解析の打ち切り。走っていなければ nil。
	cancel context.CancelFunc
	// done は走っている解析が終わったことの通知（終了時に待つため）。
	done chan struct{}
	// seq は解析の世代。**打ち切った解析の途中経過が後から届く**ので、
	// フロントはこれで古いものを捨てる。
	seq int
	// lastEngine は最後に答えたエンジンの名前。**表示用**
	// （接続を持ち続けないので、繋いでいないあいだも名前だけは出せるようにする）。
	lastEngine string
}

func NewAnalyzeService(logger *slog.Logger, study *StudyService, settings *SettingsService) *AnalyzeService {
	return &AnalyzeService{logger: logger, study: study, settings: settings}
}

// close は走っている解析を打ち切り、エンジンが終わるまで待つ（アプリの終了時）。
//
// ⚠️ **待たないと外部エンジンのプロセスが残る。** 親が先に消えても、Windows では
// 子プロセスは道連れにならない。
func (s *AnalyzeService) close() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(engineShutdownWait):
		s.logger.Warn("エンジンの終了を待ちきれませんでした")
	}
}

// engineShutdownWait は終了時にエンジンの後始末を待つ上限。
//
// `stop` → `bestmove` → `quit` → プロセス終了、まで待つ。長すぎるとアプリが
// 閉じなくなるので、諦める線を引いておく。
const engineShutdownWait = 5 * time.Second

// session は今の設定に合ったセッションを作る。
//
// **毎回作ってよい。** Session は接続を持たない（繋ぐのは解析のあいだだけ）ので、
// 作るコストはほぼ無い。**設定を変えたときの繋ぎ直しを気にしなくてよくなった**のが、
// この寿命にした利点の 1 つ（以前は鍵を持って作り直しを判断していた）。
func (s *AnalyzeService) session() *analyze.Session {
	cfg := s.settings.engineConfig()
	if cfg.Path == "" {
		return analyze.NewLocalSession()
	}
	return analyze.NewExecSession(cfg.Path, cfg.Options)
}

// EngineCheck は「接続を確認」の結果。
//
// **エラーも値として返す**（error にしない）。設定タブに出す情報であって、
// 呼び出しが失敗したわけではない。エンジンを置く前に確かめるのは普通の使い方。
type EngineCheck struct {
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
	// **解析のたびにこれだけ待つ**（プロセスは 1 回の解析のあいだしか生きない）ので、
	// 繋ぎ先を選ぶ材料として出す。
	StartupMS int64 `json:"startupMs"`
	// Error は繋がらなかった理由（日本語）。
	Error string `json:"error"`
}

// CheckEngine は設定したエンジンに実際に繋いでみる（設定タブの「接続を確認」）。
//
// **確かめたら閉じる。** 解析していないのにプロセスを残さない。
// 起動にかかった時間も返すので、**解析のたびに払うコストがここで分かる。**
func (s *AnalyzeService) CheckEngine() EngineCheck {
	cfg := s.settings.engineConfig()
	out := EngineCheck{Path: cfg.Path, Builtin: cfg.Path == ""}

	ctx, cancel := context.WithTimeout(context.Background(), engineConnectTimeout)
	defer cancel()
	info, err := s.session().Connect(ctx)
	if err != nil {
		out.Error = err.Error()
		s.logger.Warn("エンジンに繋げませんでした", "path", cfg.Path, "error", err)
		return out
	}
	out.OK = true
	out.Name = info.Name
	out.Options = info.Options
	out.Applied = info.Applied
	out.StartupMS = info.StartupMS
	s.rememberEngine(info.Name)
	s.logger.Info("エンジンに繋がりました",
		"name", info.Name, "path", cfg.Path,
		"options", info.Options, "applied", info.Applied, "startupMs", info.StartupMS)
	return out
}

// engineConnectTimeout は接続の確認に使う上限。
//
// 評価関数の読み込みに時間のかかるエンジンがあるので、ハンドシェイクの上限
// （`client.HandshakeTimeout`）より長めに取る。
const engineConnectTimeout = 30 * time.Second

func (s *AnalyzeService) bind(app *application.App) { s.app = app }

// AnalyzeProgress はイベントで流す途中経過。
//
// **analyze.Progress をそのまま載せるだけ**にすること（評価値の符号も表示文字列も
// あちらが決めている）。ここで組み立て直すと、書式が 2 か所に散る。
// 埋め込みにしていないのは、bindings の生成でフィールドが平らになるかどうかに
// 依存しないため。
type AnalyzeProgress struct {
	// Seq は解析の世代。**これが今の世代と違うイベントは捨てる。**
	Seq      int              `json:"seq"`
	Progress analyze.Progress `json:"progress"`
	// Done は最後の 1 通か。
	Done bool `json:"done"`
	// StartupMS は起動から `readyok` までの所要ミリ秒（**done のときだけ入る**）。
	//
	// **解析のたびに払っているコスト**なので画面に出す。これが見えないと、
	// 遅いのが探索のせいなのか起動のせいなのか分からない。
	StartupMS int64 `json:"startupMs"`
}

// AnalyzeFailure は解析が失敗したことの通知。
type AnalyzeFailure struct {
	Seq   int    `json:"seq"`
	Error string `json:"error"`
}

// AnalyzeState は今解析中かどうか。フロントの初期表示と、開始・停止の応答に使う。
type AnalyzeState struct {
	Running bool `json:"running"`
	Seq     int  `json:"seq"`
	// SFEN は解析にかけた局面（開始時のみ入る）。何を評価した値なのかを示す。
	SFEN string `json:"sfen"`
	// Engine は繋がっているエンジンの名前（未接続なら空）。
	//
	// **何が出した評価値なのかは見せること。** 繋ぎ先を差し替えられる以上、
	// 評価値だけ出して出所を伏せると比べようがない。
	Engine string `json:"engine"`
}

// Start は今の局面の解析を始める。**ここでエンジンを起こす。**
//
// seconds は考える秒数。**0 以下なら「止めるまで考え続ける」**（＝そのあいだ
// エンジンも生きている）。時間で打ち切っても、それまでに完走した深さの評価値は出る
// （設計原則3）。
//
// ⚠️ **局面が確定していなければエラー。** 手番か駒台の先後が未決だと SFEN が
// 組み上がらない（決めていないことを勝手に決めない。設計原則5）。訂正 UI で
// 決めてもらう以外に手は無いので、ここは警告ではなくエラーにする。
func (s *AnalyzeService) Start(seconds int) (AnalyzeState, error) {
	sfen, err := s.study.positionSFEN()
	if err != nil {
		return AnalyzeState{}, err
	}

	s.mu.Lock()
	// 走っているものがあれば打ち切る。**待たない**（前の探索は自分で畳んで
	// analyze:done を出すが、seq が古いのでフロントが捨てる）。
	if s.cancel != nil {
		s.cancel()
	}
	s.seq++
	seq := s.seq
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	s.mu.Unlock()

	// **エンジンは解析のあいだだけ生きる。** 設定はここで読まれるので、
	// 変えた結果が次の解析にそのまま効く（繋ぎ直しの判断が要らない）。
	session := s.session()

	// ⚠️ **seconds が 0 以下なら「止めるまで考え続ける」。**
	// これが「ずっと解析していたい」の表し方で、そのあいだプロセスも生きている。
	opt := analyze.Options{}
	if seconds > 0 {
		opt.Movetime = time.Duration(seconds) * time.Second
	}

	go func() {
		defer close(done)
		defer cancel()
		defer s.finish(seq)

		res, err := session.Analyze(ctx, sfen, opt, func(p analyze.Progress) {
			s.emit("analyze:info", AnalyzeProgress{Seq: seq, Progress: p})
		})
		if err != nil {
			s.logger.Warn("解析できませんでした", "sfen", sfen, "error", err)
			s.emit("analyze:failed", AnalyzeFailure{Seq: seq, Error: err.Error()})
			return
		}
		s.rememberEngine(res.Engine)
		s.logger.Info("解析しました",
			"engine", res.Engine, "sfen", sfen, "depth", res.Depth,
			"best", res.Bestmove, "nodes", res.Nodes,
			"elapsedMs", res.ElapsedMS, "startupMs", res.StartupMS, "stopped", res.Stopped)
		s.emit("analyze:done", AnalyzeProgress{
			Seq: seq, Progress: res.Progress, Done: true, StartupMS: res.StartupMS,
		})
	}()

	return AnalyzeState{Running: true, Seq: seq, SFEN: sfen, Engine: s.engineNameForDisplay()}, nil
}

// Stop は走っている解析を打ち切る。**打ち切っても評価値は出る**ので、
// 「やめる」というより「ここまでで良い」に近い。
func (s *AnalyzeService) Stop() AnalyzeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	return AnalyzeState{Running: s.cancel != nil, Seq: s.seq, Engine: s.lastEngine}
}

// State は今の状態を返す（何も始めない）。フロントの初期表示用。
func (s *AnalyzeService) State() AnalyzeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return AnalyzeState{Running: s.cancel != nil, Seq: s.seq, Engine: s.lastEngine}
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

// rememberEngine は最後に答えたエンジンの名前を覚える（表示用）。
func (s *AnalyzeService) rememberEngine(name string) {
	if name == "" {
		return
	}
	s.mu.Lock()
	s.lastEngine = name
	s.mu.Unlock()
}

// engineNameForDisplay は最後に答えたエンジンの名前を返す（まだ無ければ空）。
// **公開しない**（Service の公開メソッドは bindings に出てフロントの API になる）。
func (s *AnalyzeService) engineNameForDisplay() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastEngine
}

func (s *AnalyzeService) emit(name string, data any) {
	if s.app == nil {
		return
	}
	s.app.Event.Emit(name, data)
}
