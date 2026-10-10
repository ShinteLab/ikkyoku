package analyze

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeEngineEnv が "1" なら、このテストバイナリは**テストではなく USI エンジンとして**
// 起動する（TestMain が振り分ける）。
//
// **実際の将棋エンジンの実行ファイルをテストで要求しない**ため。手元にやねうら王が
// あるかどうかでテストの結果が変わると、壊れたときに切り分けられない。
// 自分自身を起こせば「本当に別プロセスと標準入出力で話せるか」だけを確かめられる。
const fakeEngineEnv = "IKKYOKU_TEST_USI_ENGINE"

// quitMarkEnv に指すファイルがあれば、エンジンは `quit` を受け取ったときに
// **受け取ったコマンドの並び**をそこに書き残す。
//
// ⚠️ 2 つのことを外から観測するために要る。どちらも呼び出し側からは見えない:
//
//   - **「exe が残らない」**（プロセスの生死）。この設計の目的そのもの
//   - **送っている手順が USI として正しいか**（usi → setoption → isready →
//     usinewgame → position → go の並び）
const quitMarkEnv = "IKKYOKU_TEST_USI_QUITMARK"

// multiPVMarkEnv に指すファイルがあれば、エンジンは `setoption name MultiPV` を
// 受け取ったときにその値をそこに書き残す。
//
// ⚠️ **quitMark の並びとは別に持つ。** あちらは「語」だけを順に記録するので、
// 値までは見えない。**MultiPV は値が届かないと意味が無い**（候補が 1 本のままになる）
// のに、画面では「このエンジンは MultiPV 非対応」と見分けが付かない。
const multiPVMarkEnv = "IKKYOKU_TEST_USI_MULTIPVMARK"

// hangEnv は**止まらないエンジン**を演じさせる（2026-10-10。中継中に実機で踏んだ）。
//
//   - "stop": `go` のあと `stop` にも `quit` にも標準入力の EOF にも応えない（探索が止まらない）
//   - "deaf": `readyok` のあと標準入力を読まなくなる（こちらの書き込みがパイプで詰まる）
//
// どちらも**自分では終わらない**ので、片付くのは ikkyoku が kill したときだけ。
const hangEnv = "IKKYOKU_TEST_USI_HANG"

// hangForever は自分では終わらない（`select {}` だとランタイムがデッドロックと見なして落ちる）。
func hangForever() {
	for {
		time.Sleep(time.Hour)
	}
}

// runFakeEngine は最低限の USI エンジンとして振る舞う。
func runFakeEngine() {
	sc := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	say := func(lines ...string) {
		for _, l := range lines {
			fmt.Fprintln(out, l)
		}
		out.Flush()
	}
	// ⚠️ **isready のあとに来た setoption は受け取らない。**
	// 実際のエンジンでも間に合っていない（置換表の確保も評価関数の読み込みも
	// isready で走る）ので、遅れて届いたものは無かったことにする。
	//
	// 送られる行そのものの検証は core 側（client の TestOpenSendsDeclaredDefaults）。
	// ここで見るのは「別プロセス相手でも同じ手順が通るか」。
	ready := false
	var log []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// 手順の検証用に、届いた順で覚えておく（値は落として語だけ）。
		log = append(log, strings.Fields(line + " ")[0])
		switch {
		case line == "usi":
			say("id name Fake External Engine", "id author test",
				"option name USI_Hash type spin default 256 min 1 max 1024",
				"option name Threads type spin default 1 min 1 max 8",
				// button と空の既定値は送られてこないはず。
				"option name Clear Hash type button",
				"option name BookDir type string default <empty>",
				"usiok")
		case strings.HasPrefix(line, "setoption "):
			// MultiPV は**探索ごとに変わる**ので、isready のあとに来るのが正しい。
			if f := strings.Fields(line); len(f) == 5 && f[2] == "MultiPV" {
				if mark := os.Getenv(multiPVMarkEnv); mark != "" {
					_ = os.WriteFile(mark, []byte(f[4]), 0o644)
				}
				continue
			}
			if ready {
				continue // 間に合っていない。実際のエンジンと同じく無視する。
			}
		case line == "isready":
			ready = true
			say("readyok")
			if os.Getenv(hangEnv) == "deaf" {
				hangForever()
			}
		case strings.HasPrefix(line, "go") && os.Getenv(hangEnv) == "stop":
			say("info depth 1 score cp 42 nodes 10 pv 7g7f")
			hangForever()
		case strings.HasPrefix(line, "go"):
			say("info depth 1 score cp 42 nodes 10 pv 7g7f",
				"info depth 2 score cp 55 nodes 99 pv 2g2f",
				"bestmove 2g2f")
		case line == "quit":
			if mark := os.Getenv(quitMarkEnv); mark != "" {
				_ = os.WriteFile(mark, []byte(strings.Join(log, " ")), 0o644)
			}
			return
		}
	}
}

// ⚠️ **外部エンジンを本当に起こせること**（Step 2 の要）。
//
// io.Pipe の同一プロセス版が通っていても、`os/exec` の経路は別物
// （プロセス起動・作業ディレクトリ・標準入出力の繋ぎ・終了処理）。
func TestExecSessionRunsExternalEngine(t *testing.T) {
	// 子プロセス（= このテストバイナリ）がエンジンとして起動するようにする。
	t.Setenv(fakeEngineEnv, "1")
	mark := filepath.Join(t.TempDir(), "quit.mark")
	t.Setenv(quitMarkEnv, mark)

	s := NewExecSession(os.Args[0], map[string]string{"USI_Hash": "16"})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	info, err := s.Connect(ctx)
	if err != nil {
		t.Fatalf("外部エンジンに繋げませんでした: %v", err)
	}
	if info.Name != "Fake External Engine" {
		t.Errorf("id name = %q", info.Name)
	}
	// ⚠️ **宣言された option には既定値を送る**（button と空の既定値は除く）。
	// USI_Hash は設定で上書きしているので、送るのは 2 件。
	if info.Options != 4 {
		t.Errorf("宣言された option = %d, want 4", info.Options)
	}
	if info.Applied != 2 {
		t.Errorf("送った setoption = %d, want 2（USI_Hash と Threads）", info.Applied)
	}
	// ⚠️ **確認しただけならプロセスを残さない。**
	if _, err := os.Stat(mark); err != nil {
		t.Errorf("接続を確認したあとにエンジンが終わっていません: %v", err)
	}
	if err := os.Remove(mark); err != nil {
		t.Fatalf("印を消せませんでした: %v", err)
	}

	r, err := s.Analyze(ctx, startpos, Options{Movetime: 3 * time.Second}, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if r.Bestmove != "2g2f" {
		t.Errorf("bestmove = %q", r.Bestmove)
	}
	if r.Engine != "Fake External Engine" {
		t.Errorf("どのエンジンが答えたかが残っていません: %q", r.Engine)
	}
	// 先手番なので、エンジンが返した +55 がそのまま先手視点。
	if len(r.Lines) == 0 || r.Lines[0].Score.CP != 55 {
		t.Errorf("評価値が違います: %+v", r.Lines)
	}
	if len(r.Lines) > 0 && len(r.Lines[0].Moves) != 1 {
		t.Errorf("読み筋が違います: %+v", r.Lines[0].Moves)
	}
	// ⚠️ **解析が終わってもプロセスは生きている**（2026-08-12。接続を使い回す）。
	// **ここで終わっていたら、1 手ごとに `isready` を払う元の作りに戻っている。**
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("解析が終わっただけでエンジンが終わっています（接続を使い回していない）")
	}

	// ⚠️ **終わらせるのは Close。** 呼ばなければプロセスが残る
	// （Windows では親が消えても子は道連れにならない）。
	s.Close()
	got, err := os.ReadFile(mark)
	if err != nil {
		t.Fatalf("Close のあとにエンジンが終わっていません: %v", err)
	}
	// ⚠️ **USI の手順どおりに送れていること。**
	// `usinewgame` を落としていると、前の局面の探索結果を引きずるエンジンがある
	// （実機の ShogiHome もこの並び）。
	const want = "usi setoption setoption isready usinewgame position go quit"
	if string(got) != want {
		t.Errorf("送った手順が違います:\n got = %s\nwant = %s", got, want)
	}
	// 起動にかかった時間が出ること（解析のたびに払うコスト）。
	if r.StartupMS < 0 {
		t.Errorf("StartupMS = %d", r.StartupMS)
	}
}

// 実行ファイルが無いときは、繋ぐ時点で理由が分かること。
// **セッションを作る時点では失敗しない**（パスを先に書いておく使い方があるため）。
func TestExecSessionReportsMissingBinary(t *testing.T) {
	s := NewExecSession("no-such-engine-binary.exe", nil)

	_, err := s.Connect(context.Background())
	if err == nil {
		t.Fatal("存在しない実行ファイルで繋がってしまいました")
	}
	if !strings.Contains(err.Error(), "エンジン") {
		t.Errorf("理由が伝わりません: %v", err)
	}
}

// ⚠️ **候補手の本数（MultiPV）がエンジンまで届くこと。**
//
// 届かないと候補は 1 本のままだが、**画面では「このエンジンは MultiPV 非対応」と
// 見分けが付かない**（対応していないエンジンでも 1 本しか返らないのが正常なので）。
// 値まで観測しないと、黙って効いていない状態に気づけない。
//
// **`isready` のあとに送るのが正しい**（探索ごとに変えてよい option なので、
// 初期化に効く option と違って間に合う）。
func TestAnalyzeSendsMultiPV(t *testing.T) {
	t.Setenv(fakeEngineEnv, "1")
	mark := filepath.Join(t.TempDir(), "multipv.mark")
	t.Setenv(multiPVMarkEnv, mark)

	s := NewExecSession(os.Args[0], nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := s.Analyze(ctx, startpos, Options{Movetime: 3 * time.Second, MultiPV: 3}, nil); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	got, err := os.ReadFile(mark)
	if err != nil {
		t.Fatalf("MultiPV が送られていません: %v", err)
	}
	if string(got) != "3" {
		t.Errorf("MultiPV = %q, want %q", got, "3")
	}
}

// 本数を指定しなければ MultiPV は送らない（1 本でよいときに余計な option を送らない）。
func TestAnalyzeOmitsMultiPVWhenNotAsked(t *testing.T) {
	t.Setenv(fakeEngineEnv, "1")
	mark := filepath.Join(t.TempDir(), "multipv.mark")
	t.Setenv(multiPVMarkEnv, mark)

	s := NewExecSession(os.Args[0], nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := s.Analyze(ctx, startpos, Options{Movetime: 3 * time.Second}, nil); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Error("本数を指定していないのに MultiPV を送っています")
	}
}

// shortenStopWaits は止まらないエンジンを待つ時間をテスト用に縮める。
func shortenStopWaits(t *testing.T, s *Session) {
	t.Helper()
	oldQuit, oldKill := quitWait, stopKillAfter
	quitWait, stopKillAfter = 300*time.Millisecond, 2*time.Second
	t.Cleanup(func() { quitWait, stopKillAfter = oldQuit, oldKill })
	s.grace = 300 * time.Millisecond
}

// analyzeWithin は Analyze が limit 以内に返ることを確かめる（返らなければ固まったとして落とす）。
func analyzeWithin(t *testing.T, limit time.Duration, s *Session, ctx context.Context, opt Options) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := s.Analyze(ctx, startpos, opt, nil)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("Analyze が %s 経っても返りません（止まらないエンジンに固まっています）", limit)
		return nil
	}
}

// ⚠️ **`stop` にも `quit` にも応えないエンジンで固まらないこと**（2026-10-10。中継中に実機で踏んだ）。
//
// 以前は `client` が bestmove を見切ったあとの後始末（`liveEngine.close`）が `cmd.Wait` で
// **プロセスが自分で終わるのを無制限に待ち**、後ろの kill に届かなかった。しかも `runMu` を
// 握ったままなので、**そのエンジンの解析が全部後ろに積まれ、「停止」も効かなくなった**。
func TestAnalyzeKillsEngineIgnoringStop(t *testing.T) {
	t.Setenv(fakeEngineEnv, "1")
	t.Setenv(hangEnv, "stop")
	s := NewExecSession(os.Args[0], nil)
	shortenStopWaits(t, s)

	err := analyzeWithin(t, 10*time.Second, s, context.Background(), Options{Movetime: 300 * time.Millisecond})
	if err == nil {
		t.Fatal("bestmove を返さないエンジンなのに成功しています")
	}
	// **落としたので、次に使うときは開き直す**（runMu も空いている）。
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close が返りません（runMu を握ったままです）")
	}
}

// ⚠️ **標準入力を読まなくなったエンジンでも固まらないこと**（2026-10-10）。
//
// 書き込みがパイプで詰まると、送っていたゴルーチンが送信の鍵を握ったまま残り、
// **後始末の `quit` もそこで待つ**。kill して書き込みを失敗させない限り抜けない。
func TestAnalyzeKillsDeafEngine(t *testing.T) {
	t.Setenv(fakeEngineEnv, "1")
	t.Setenv(hangEnv, "deaf")
	s := NewExecSession(os.Args[0], nil)
	shortenStopWaits(t, s)

	// パイプのバッファ（Windows は数 KB・Linux は 64KB）より長い position を送らせる。
	moves := make([]string, 40000)
	for i := range moves {
		moves[i] = "7g7f"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := analyzeWithin(t, 10*time.Second, s, ctx, Options{Moves: moves}); err == nil {
		t.Fatal("標準入力を読まないエンジンなのに成功しています")
	}
}

// ⚠️ **見張りは「畳まれるべき」になってから数えること**（打ち切る前の長考で落とさない）、
// **畳まれたら何もしないこと**。
func TestWatchStop(t *testing.T) {
	t.Run("畳まれなければ落とす", func(t *testing.T) {
		stopped, finished := make(chan struct{}), make(chan struct{})
		killed := make(chan struct{})
		go watchStop(stopped, finished, 50*time.Millisecond, func() { close(killed) })
		select {
		case <-killed:
			t.Fatal("打ち切る前に落としています")
		case <-time.After(150 * time.Millisecond):
		}
		close(stopped)
		select {
		case <-killed:
		case <-time.After(2 * time.Second):
			t.Fatal("打ち切っても畳まれないのに落としません")
		}
	})
	t.Run("畳まれたら落とさない", func(t *testing.T) {
		stopped, finished := make(chan struct{}), make(chan struct{})
		var killed atomic.Bool
		done := make(chan struct{})
		go func() {
			watchStop(stopped, finished, 50*time.Millisecond, func() { killed.Store(true) })
			close(done)
		}()
		close(stopped)
		close(finished)
		<-done
		time.Sleep(100 * time.Millisecond)
		if killed.Load() {
			t.Error("畳まれたのに落としています")
		}
	})
}
