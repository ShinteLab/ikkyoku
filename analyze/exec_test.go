package analyze

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
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
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
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
			if ready {
				continue // 間に合っていない。実際のエンジンと同じく無視する。
			}
		case line == "isready":
			ready = true
			say("readyok")
		case strings.HasPrefix(line, "go"):
			say("info depth 1 score cp 42 nodes 10 pv 7g7f",
				"info depth 2 score cp 55 nodes 99 pv 2g2f",
				"bestmove 2g2f")
		case line == "quit":
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

	s := NewExecSession(os.Args[0], map[string]string{"USI_Hash": "16"})
	t.Cleanup(func() { _ = s.Close() })

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
}

// 実行ファイルが無いときは、繋ぐ時点で理由が分かること。
// **セッションを作る時点では失敗しない**（パスを先に書いておく使い方があるため）。
func TestExecSessionReportsMissingBinary(t *testing.T) {
	s := NewExecSession("no-such-engine-binary.exe", nil)
	t.Cleanup(func() { _ = s.Close() })

	_, err := s.Connect(context.Background())
	if err == nil {
		t.Fatal("存在しない実行ファイルで繋がってしまいました")
	}
	if !strings.Contains(err.Error(), "エンジン") {
		t.Errorf("理由が伝わりません: %v", err)
	}
}
