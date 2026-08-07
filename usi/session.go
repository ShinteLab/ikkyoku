// Package usi は「USI を話す相手」とのセッションを管理する層。
//
// **ikkyoku は将棋 UI として振る舞う**（ShogiGUI / ShogiHome と同じ立ち位置）。
// 繋ぎ先が「USI を話すプロセス」でありさえすれば、やねうら王でも水匠でも
// `prokishi.exe`（リモート実行）でも自作 `engine` でも**ここから先の差は無い**。
// 検討の経緯は `_docs/phase4-engine-usi.md`。
//
// ⚠️ **「best を問い合わせる同期 API」に丸めないこと。** そうすると Phase 5 の中核が
// 最初から取れない:
//
//   - **MultiPV が出せない。** 次善手を辿るのが構想の中心で、bestmove 1 個では足りない
//   - **info の逐次ストリームを捨てることになる。** 検討 UI の本体はこれ
//   - **stop で途中まで読んだ結果を使う**という検討ツールの基本操作が表現できない
//
// USI がステートフル（position → go → info… → bestmove）なのは面倒に見えるが、
// **その面倒さがそのまま欲しい機能**なので、素直に USI のまま扱う。
//
// **プロトコルの語彙は core/usi。** info 行の読み取りも手表記もあちらで、
// ここが持つのは「いつ送っていつ待つか」だけ。
package usi

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	coreusi "github.com/ShinteLab/core/usi"
)

// handshakeTimeout は usiok / readyok を待つ上限。
//
// 相手が USI を話さないプロセスだった場合、ここで待ち続けると
// アプリが黙って固まる。**必ず時間で切ること。**
const handshakeTimeout = 10 * time.Second

// Transport は USI を話す相手との入出力。
//
// Step 1（同一プロセスの `engine`）は `io.Pipe`、Step 2（外部エンジンの exe）は
// `os/exec` のパイプが入る。**セッション側はどちらかを知らない**ので、
// 差し替えてもここから上は変わらない。
type Transport struct {
	// In はエンジンの標準出力（こちらが読む側）。
	In io.Reader
	// Out はエンジンの標準入力（こちらが書く側）。
	Out io.Writer
	// Close は後始末（プロセスの終了・パイプのクローズ）。nil 可。
	Close func() error
}

// Session は起動済みのエンジン 1 つとの対話。
//
// **1 つの Session に同時に 1 つの探索しか流せない**（USI がそういう作り）。
// 検討ツリーで複数の枝を並べたくなったら Session を増やす（＝エンジンを増やす）。
type Session struct {
	t Transport

	// ID は `id name` で名乗った名前。
	ID     string
	Author string

	// lines はエンジンからの行。読み取り goroutine が流す。
	lines chan string
	// readErr は読み取りが終わった理由（EOF・プロセス終了）。
	readErr chan error

	// mu は送信の直列化。**探索中でも stop を送れる**必要があるので、
	// 送信だけを守り、待ちの側は握らない。
	mu     sync.Mutex
	closed bool
}

// Open はエンジンとの対話を始める（`usi` → `usiok` → `isready` → `readyok`）。
//
// ハンドシェイクが終わるまで返らない。**相手が USI を話さなければここで失敗する**
// ので、以降のコードは「相手は USI を話す」と思ってよい。
func Open(ctx context.Context, t Transport) (*Session, error) {
	s := &Session{
		t:       t,
		lines:   make(chan string, 64),
		readErr: make(chan error, 1),
	}
	go s.read()

	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()

	if err := s.send("usi"); err != nil {
		s.Close()
		return nil, fmt.Errorf("エンジンに usi を送れませんでした: %w", err)
	}
	if err := s.await(ctx, "usiok", func(line string) {
		if name, ok := strings.CutPrefix(line, "id name "); ok {
			s.ID = strings.TrimSpace(name)
		}
		if author, ok := strings.CutPrefix(line, "id author "); ok {
			s.Author = strings.TrimSpace(author)
		}
	}); err != nil {
		s.Close()
		return nil, fmt.Errorf("エンジンが usiok を返しませんでした: %w", err)
	}

	if err := s.send("isready"); err != nil {
		s.Close()
		return nil, fmt.Errorf("エンジンに isready を送れませんでした: %w", err)
	}
	if err := s.await(ctx, "readyok", nil); err != nil {
		s.Close()
		return nil, fmt.Errorf("エンジンが readyok を返しませんでした: %w", err)
	}
	return s, nil
}

// GoOptions は 1 回の探索の指定。
type GoOptions struct {
	// Movetime はこの局面を考える時間。0 なら `go infinite`（stop まで考える）。
	//
	// ⚠️ **自作 engine は今のところ `go movetime` を解釈しない**（engine/TODO.md）。
	// そのため Movetime を指定しても向こうは無制限に読み続けるので、
	// **ctx 側の期限で stop を送って打ち切る**（下記 Analyze）。
	Movetime time.Duration
	// MultiPV は候補手をいくつ出させるか。0/1 なら最善手だけ。
	//
	// ⚠️ **対応していないエンジンでは無視される**（自作 engine が今それ。
	// setoption 自体は受け取るが MultiPV という option を持っていない）。
	MultiPV int
}

// Result は 1 回の探索の結末。
type Result struct {
	// Bestmove は `bestmove` の手（"7g7f" / "resign" / "win"）。
	Bestmove string
	// Ponder は `bestmove ... ponder <手>`。
	Ponder string
	// Stopped は時間切れ・キャンセルで打ち切ったか。
	//
	// **打ち切りは失敗ではない**（設計原則3）。そこまでの info は届いている。
	Stopped bool
}

// Analyze は 1 局面を解析する（`position` → `go` → info… → `bestmove`）。
//
// sfen は**局面全体の SFEN**（盤面・手番・持ち駒・手数）。
// **履歴（moves）は渡さない** ——「どの層も履歴に依存しない」（設計原則1）。
// 引き換えに千日手・連続王手はエンジンが判定できないが、それは仕様として受け入れる。
//
// info はエンジンが info 行を出すたびに呼ばれる（nil 可）。
// **読み取り goroutine から呼ばれる**ので、UI へ流すならイベント経由にすること。
//
// ctx をキャンセルするか Movetime が過ぎると `stop` を送り、**エンジンが返す
// bestmove を待って**返る（設計原則3。そこまでの結果は使える）。
func (s *Session) Analyze(ctx context.Context, sfen string, opt GoOptions, info func(coreusi.Info)) (Result, error) {
	if opt.MultiPV > 1 {
		// **対応していないエンジンは黙って無視する**ので、送るだけ送ってよい。
		if err := s.send(fmt.Sprintf("setoption name MultiPV value %d", opt.MultiPV)); err != nil {
			return Result{}, err
		}
	}
	if err := s.send("position sfen " + sfen); err != nil {
		return Result{}, err
	}

	// ⚠️ **go movetime に頼らない。** 自作 engine が解釈しないので、
	// 時間の管理はこちら側（ctx）で持ち、期限が来たら stop を送る。
	// 外部エンジンに繋ぐときも「こちらが時間を握る」ほうが揃っていて分かりやすい。
	if opt.Movetime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opt.Movetime)
		defer cancel()
	}
	// **常に infinite で投げ、打ち切りはこちらの stop で行う。**
	// エンジンが自分で読み切って先に bestmove を返すのは正常（固定深さのエンジンなど）。
	if err := s.send("go infinite"); err != nil {
		return Result{}, err
	}

	stopped := false
	for {
		select {
		case <-ctx.Done():
			if stopped {
				// 既に stop 済みなのに bestmove が来ない。**待ち続けない**
				// （相手が壊れている可能性がある）。
				return Result{Stopped: true}, fmt.Errorf("エンジンが bestmove を返しません")
			}
			stopped = true
			if err := s.send("stop"); err != nil {
				return Result{Stopped: true}, err
			}
			// bestmove を待つための猶予。ここで返ってしまうと、次の position が
			// 前の探索の bestmove と混ざる。
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), handshakeTimeout)
			defer cancel()

		case err := <-s.readErr:
			return Result{Stopped: stopped}, fmt.Errorf("エンジンとの通信が切れました: %w", err)

		case line := <-s.lines:
			if in, ok := coreusi.ParseInfo(line); ok {
				if info != nil {
					info(in)
				}
				continue
			}
			if move, ponder, ok := coreusi.ParseBestmove(line); ok {
				return Result{Bestmove: move, Ponder: ponder, Stopped: stopped}, nil
			}
			// 知らない行（`id name` の再送など）は読み飛ばす。
		}
	}
}

// Close はエンジンを終わらせる（`quit` を送ってから後始末）。
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	// 返事は待たない（既に死んでいることもある）。
	_ = s.write("quit")
	if s.t.Close != nil {
		return s.t.Close()
	}
	return nil
}

// read はエンジンの出力を 1 行ずつ流す。
func (s *Session) read() {
	sc := bufio.NewScanner(s.t.In)
	// 読み筋が長いエンジンがあるので、既定の 64KB より広く取る。
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		s.lines <- strings.TrimSpace(sc.Text())
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	s.readErr <- err
}

// await は目的の行が来るまで読み進める。each には途中の行が渡る。
func (s *Session) await(ctx context.Context, want string, each func(string)) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-s.readErr:
			return err
		case line := <-s.lines:
			if line == want {
				return nil
			}
			if each != nil {
				each(line)
			}
		}
	}
}

// send は 1 行送る。**閉じた後は送らない。**
func (s *Session) send(line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("エンジンとの接続は閉じています")
	}
	return s.write(line)
}

func (s *Session) write(line string) error {
	_, err := io.WriteString(s.t.Out, line+"\n")
	return err
}
