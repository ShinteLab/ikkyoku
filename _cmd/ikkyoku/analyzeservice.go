package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ShinteLab/ikkyoku/analyze"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// AnalyzeService は確定した局面をエンジンに解析させる Service（Phase 4）。
//
// **エンジンは USI を話す相手**（`analyze` → `ikkyoku/usi`）。今は同梱の `engine` を
// 同一プロセスで動かしているが（Step 1）、外部エンジンの exe や `prokishi.exe` に
// 差し替わってもここは変わらない（`_docs/phase4-engine-usi.md`）。
//
// **局面はここが持たない。** 解析するのは常に「今 PositionService が持っている局面」で、
// フロントから SFEN を受け取らない（フロントに局面の写しを持たせない、という
// PositionService の方針と揃える。渡してもらう形にすると、訂正した直後に古い局面を
// 解析する経路ができる）。
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
	logger   *slog.Logger
	pos      *PositionService
	settings *SettingsService
	app      *application.App

	// session はエンジンとの接続。**使い回す**（接続は最初の解析まで開かない）。
	//
	// ⚠️ **設定でエンジンを変えたら作り直す**（sessionKey）。使い回したままだと、
	// パスを変えても前のエンジンが答え続ける。
	session    *analyze.Session
	sessionKey string

	mu sync.Mutex
	// cancel は走っている解析の打ち切り。走っていなければ nil。
	cancel context.CancelFunc
	// seq は解析の世代。**打ち切った解析の途中経過が後から届く**ので、
	// フロントはこれで古いものを捨てる。
	seq int
}

func NewAnalyzeService(logger *slog.Logger, pos *PositionService, settings *SettingsService) *AnalyzeService {
	return &AnalyzeService{logger: logger, pos: pos, settings: settings}
}

// close はエンジンとの接続を閉じる（終了時に呼ぶ）。
//
// ⚠️ **外部エンジンはプロセスなので、閉じないと残る。**
func (s *AnalyzeService) close() {
	s.mu.Lock()
	session := s.session
	s.session = nil
	s.sessionKey = ""
	s.mu.Unlock()
	if session == nil {
		return
	}
	if err := session.Close(); err != nil {
		s.logger.Warn("エンジンとの接続を閉じられませんでした", "error", err)
	}
}

// engineSession は今の設定に合ったセッションを返す。
//
// **設定が変わっていたら作り直す。** 使い回したままだと、設定でエンジンを
// 差し替えても前のエンジンが答え続ける（一番気づきにくい壊れ方）。
// **ロックを取った状態で呼ぶこと。**
func (s *AnalyzeService) engineSession() *analyze.Session {
	cfg := s.settings.engineConfig()
	// パスと option をまとめた鍵。どちらが変わっても繋ぎ直す
	// （option は isready の前にしか送れないので、後から反映できない）。
	key := fmt.Sprintf("%s\x00%v", cfg.Path, cfg.Options)
	if s.session != nil && s.sessionKey == key {
		return s.session
	}
	if s.session != nil {
		old := s.session
		go func() {
			// 閉じるのに時間がかかることがある（プロセスの終了待ち）ので、
			// ロックを持ったまま待たない。
			if err := old.Close(); err != nil {
				s.logger.Warn("前のエンジンを閉じられませんでした", "error", err)
			}
		}()
	}
	if cfg.Path == "" {
		s.session = analyze.NewLocalSession()
		s.logger.Info("同梱のエンジンを使います")
	} else {
		s.session = analyze.NewExecSession(cfg.Path, cfg.Options)
		s.logger.Info("外部のエンジンを使います", "path", cfg.Path, "options", len(cfg.Options))
	}
	s.sessionKey = key
	return s.session
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
	// Error は繋がらなかった理由（日本語）。
	Error string `json:"error"`
}

// CheckEngine は設定したエンジンに実際に繋いでみる（設定タブの「接続を確認」）。
//
// **繋いだ接続はそのまま使う。** 確かめるためだけに起こして捨てると、
// 直後の解析でまた起動を待つことになる。
func (s *AnalyzeService) CheckEngine() EngineCheck {
	cfg := s.settings.engineConfig()
	out := EngineCheck{Path: cfg.Path, Builtin: cfg.Path == ""}

	s.mu.Lock()
	session := s.engineSession()
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), engineConnectTimeout)
	defer cancel()
	name, err := session.Connect(ctx)
	if err != nil {
		out.Error = err.Error()
		s.logger.Warn("エンジンに繋げませんでした", "path", cfg.Path, "error", err)
		return out
	}
	out.OK = true
	out.Name = name
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

// Start は今の局面の解析を始める。
//
// seconds は考える秒数（0 以下なら analyze の既定）。**時間で打ち切っても、
// それまでに完走した深さの評価値は出る**（設計原則3）。
//
// ⚠️ **局面が確定していなければエラー。** 手番か駒台の先後が未決だと SFEN が
// 組み上がらない（決めていないことを勝手に決めない。設計原則5）。訂正 UI で
// 決めてもらう以外に手は無いので、ここは警告ではなくエラーにする。
func (s *AnalyzeService) Start(seconds int) (AnalyzeState, error) {
	sfen, err := s.pos.positionSFEN()
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
	s.cancel = cancel
	// **設定が変わっていればここで繋ぎ直す。**
	session := s.engineSession()
	s.mu.Unlock()

	opt := analyze.Options{}
	if seconds > 0 {
		opt.Movetime = time.Duration(seconds) * time.Second
	}

	go func() {
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
		s.logger.Info("解析しました",
			"engine", res.Engine, "sfen", sfen, "depth", res.Depth,
			"best", res.Bestmove, "nodes", res.Nodes,
			"elapsedMs", res.ElapsedMS, "stopped", res.Stopped)
		s.emit("analyze:done", AnalyzeProgress{Seq: seq, Progress: res.Progress, Done: true})
	}()

	return AnalyzeState{Running: true, Seq: seq, SFEN: sfen, Engine: session.EngineName()}, nil
}

// Stop は走っている解析を打ち切る。**打ち切っても評価値は出る**ので、
// 「やめる」というより「ここまでで良い」に近い。
func (s *AnalyzeService) Stop() AnalyzeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	return AnalyzeState{Running: s.cancel != nil, Seq: s.seq, Engine: s.engineName()}
}

// State は今の状態を返す（何も始めない）。フロントの初期表示用。
func (s *AnalyzeService) State() AnalyzeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return AnalyzeState{Running: s.cancel != nil, Seq: s.seq, Engine: s.engineName()}
}

// finish は解析が終わったことを記録する。**自分より新しい解析が始まっていたら
// 何もしない**（打ち切られた古い解析が、走っている新しい解析を止めないように）。
func (s *AnalyzeService) finish(seq int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seq == seq {
		s.cancel = nil
	}
}

func (s *AnalyzeService) emit(name string, data any) {
	if s.app == nil {
		return
	}
	s.app.Event.Emit(name, data)
}

// engineName は繋がっているエンジンの名前を返す。**ロックを取った状態で呼ぶこと。**
// まだ繋いでいなければ空（表示のためだけなので、ここで繋ぎにいかない）。
func (s *AnalyzeService) engineName() string {
	if s.session == nil {
		return ""
	}
	return s.session.EngineName()
}
