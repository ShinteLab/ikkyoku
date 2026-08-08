package analyze

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	coreusi "github.com/ShinteLab/core/usi"
)

const startpos = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL b - 1"

// engine の USI 層は受け取った行・送った行を全部 slog.Info に出す。
// テストの出力が埋まるので黙らせる（engine/TODO.md の「ログの出力先」）。
func TestMain(m *testing.M) {
	// **テストではなく USI エンジンとして起動する経路**（exec_test.go）。
	// 外部エンジンを起こす経路を、実際の将棋エンジン無しで確かめるため。
	if os.Getenv(fakeEngineEnv) == "1" {
		runFakeEngine()
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// テストは短く切る。**深さではなく符号と経路を見ている**ので、長く考えさせる必要はない。
const testMovetime = 400 * time.Millisecond

func newTestSession(t *testing.T) *Session {
	t.Helper()
	s := NewLocalSession()
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// 平手の初期局面を解析できること。**USI のハンドシェイクから bestmove まで通るのが最低条件。**
func TestAnalyzeStartPos(t *testing.T) {
	s := newTestSession(t)
	r, err := s.Analyze(context.Background(), startpos, Options{Movetime: testMovetime}, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if r.Bestmove == "" {
		t.Error("bestmove が空です")
	}
	if r.Turn != "b" {
		t.Errorf("Turn = %q, want b", r.Turn)
	}
	if r.Engine == "" {
		t.Error("どのエンジンが答えたか（id name）が空です")
	}
	if len(r.Lines) == 0 {
		t.Fatal("候補手が 1 本もありません")
	}
	if r.Lines[0].Score.Label == "" {
		t.Error("Label が空です")
	}
	if r.Lines[0].Score.Mate != 0 {
		t.Errorf("初期局面が詰みになっています: %+v", r.Lines[0].Score)
	}
}

// **接続を使い回すこと。** 局面ごとに繋ぎ直すと、外部エンジン（Step 2）では
// 毎回プロセス起動と isready を待つことになる。
func TestSessionReusesEngine(t *testing.T) {
	s := newTestSession(t)
	if _, err := s.Analyze(context.Background(), startpos, Options{Movetime: testMovetime}, nil); err != nil {
		t.Fatalf("1 回目: %v", err)
	}
	name := s.EngineName()
	if name == "" {
		t.Fatal("1 回目のあとにエンジン名が取れません")
	}
	if _, err := s.Analyze(context.Background(), startpos, Options{Movetime: testMovetime}, nil); err != nil {
		t.Fatalf("2 回目: %v", err)
	}
	if s.EngineName() != name {
		t.Error("2 回目で別のエンジンに繋ぎ直しています")
	}
}

// ⚠️ **評価値は先手視点。** USI が返すのは手番側視点なので、ここを外すと
// 後手番のときだけ符号が逆に見える（画面を見ても気づきにくい）。
func TestScoreIsBlackOriented(t *testing.T) {
	tests := []struct {
		name  string
		cp    int
		black bool
		want  int
	}{
		{"先手番で有利", 300, true, 300},
		{"先手番で不利", -300, true, -300},
		{"後手番で有利(=先手不利)", 300, false, -300},
		{"後手番で不利(=先手有利)", -300, false, 300},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := coreusi.Info{ScoreCP: tt.cp, HasScore: true}
			if got := newScore(in, tt.black); got.CP != tt.want {
				t.Errorf("newScore(cp=%d, black=%v).CP = %d, want %d", tt.cp, tt.black, got.CP, tt.want)
			}
		})
	}
}

// ⚠️ **符号の実地確認。** 単体テストだけだと「USI が実は先手視点で返していた」と
// いった思い違いに気づけない。**駒得している側が有利に出ること**を手番を変えながら見る。
func TestAnalyzeScoreSideOnRealPositions(t *testing.T) {
	tests := []struct {
		name string
		sfen string
		// wantPositive は「先手有利（正の評価値）」を期待するか。
		wantPositive bool
	}{
		{"先手が飛車得・先手番", "lnsgkgsnl/7b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL b R 1", true},
		{"先手が飛車得・後手番", "lnsgkgsnl/7b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL w R 1", true},
		{"後手が飛車得・先手番", "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B7/LNSGKGSNL b r 1", false},
		{"後手が飛車得・後手番", "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B7/LNSGKGSNL w r 1", false},
	}
	s := newTestSession(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := s.Analyze(context.Background(), tt.sfen, Options{Movetime: testMovetime}, nil)
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if len(r.Lines) == 0 {
				t.Fatal("候補手が 1 本もありません")
			}
			sc := r.Lines[0].Score
			if got := sc.CP > 0; got != tt.wantPositive {
				t.Errorf("評価値 %d（%s）: 先手有利=%v, want %v", sc.CP, sc.Label, got, tt.wantPositive)
			}
		})
	}
}

// 詰みが手数として出ること（符号は先手視点）。
//
// `score mate` を返すエンジンと、**`score cp` に詰みスコアを詰めて返す自作 engine**の
// 両方を見る（engine/TODO.md の 2 が直るまで後者の推定が要る）。
func TestScoreMate(t *testing.T) {
	t.Run("score mate を返すエンジン", func(t *testing.T) {
		if s := newScore(coreusi.Info{ScoreMate: 5, HasMate: true}, true); s.Mate != 5 {
			t.Errorf("Mate = %d, want 5", s.Mate)
		}
		// 後手番で「手番側が詰ます」= 先手が詰まされる。
		if s := newScore(coreusi.Info{ScoreMate: 5, HasMate: true}, false); s.Mate != -5 {
			t.Errorf("Mate = %d, want -5", s.Mate)
		}
	})
	t.Run("score cp に詰みスコアを詰めるエンジン", func(t *testing.T) {
		in := coreusi.Info{ScoreCP: engineMateScore - 5, HasScore: true}
		s := newScore(in, true)
		if s.Mate != 5 {
			t.Errorf("Mate = %d, want 5", s.Mate)
		}
		if s.CP != 0 {
			t.Errorf("詰みのとき CP は 0 のままにすること: %d", s.CP)
		}
	})
}

// ⚠️ **玉の欠けた局面はエンジンに渡さない**（合法手生成が落ちる）。
// 訂正 UI 側では確定できてよい局面なので、止めるのはこの境界だけ。
func TestAnalyzeRejectsMissingKing(t *testing.T) {
	const noBlackKing = "4k4/9/9/9/9/9/9/9/9 b R2b4g4s4n4l18p 1"
	s := newTestSession(t)
	_, err := s.Analyze(context.Background(), noBlackKing, Options{Movetime: testMovetime}, nil)
	if err == nil {
		t.Fatal("玉が欠けているのにエラーになりませんでした")
	}
	if !strings.Contains(err.Error(), "玉") {
		t.Errorf("理由が伝わりません: %v", err)
	}
}

// 局面が確定していない SFEN は受け付けないこと。
// **盤面だけの SFEN を黙って先手番として解析しない**（設計原則5）。
func TestAnalyzeRejectsBoardOnlySFEN(t *testing.T) {
	const boardOnly = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"
	s := newTestSession(t)
	if _, err := s.Analyze(context.Background(), boardOnly, Options{}, nil); err == nil {
		t.Fatal("盤面だけの SFEN が通ってしまいました")
	}
}

// 途中経過が届くこと。**終わるまで何も出さないと固まって見える。**
func TestAnalyzeReportsProgress(t *testing.T) {
	s := newTestSession(t)
	var got []Progress
	if _, err := s.Analyze(context.Background(), startpos, Options{Movetime: testMovetime}, func(p Progress) {
		got = append(got, p)
	}); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("途中経過が 1 度も届きませんでした")
	}
	for _, p := range got {
		if len(p.Lines) == 0 {
			t.Fatal("途中経過に候補手が入っていません")
		}
	}
}

// 打ち切っても結果が返ること（設計原則3: 段階的に劣化する）。
// **打ち切りはエラーではない。**
func TestAnalyzeCanceledStillReturns(t *testing.T) {
	s := newTestSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r, err := s.Analyze(ctx, startpos, Options{Movetime: time.Minute}, nil)
	if err != nil {
		t.Fatalf("打ち切りをエラーにしないこと: %v", err)
	}
	if r.Bestmove == "" {
		t.Error("打ち切っても bestmove は返すこと")
	}
	if !r.Stopped {
		t.Error("打ち切ったのに Stopped が false です")
	}
}

// MultiPV の各順位は別々の info 行で来る。**順位ごとにまとめ、深さが変わったら捨てること**
// （混ざると、違う深さの候補が同じ一覧に並ぶ）。
func TestAccumulatorGroupsMultiPV(t *testing.T) {
	a := &accumulator{black: true, started: time.Now()}
	a.add(coreusi.Info{Depth: 3, MultiPV: 1, ScoreCP: 100, HasScore: true, PV: []string{"7g7f"}})
	a.add(coreusi.Info{Depth: 3, MultiPV: 2, ScoreCP: 50, HasScore: true, PV: []string{"2g2f"}})
	p := a.snapshot()
	if len(p.Lines) != 2 {
		t.Fatalf("候補が %d 本。2 本にまとまるはず: %+v", len(p.Lines), p.Lines)
	}
	if p.Lines[0].Rank != 1 || p.Lines[1].Rank != 2 {
		t.Errorf("順位で並んでいません: %+v", p.Lines)
	}

	// 深さが進んだら前の深さの候補は残さない。
	a.add(coreusi.Info{Depth: 4, MultiPV: 1, ScoreCP: 120, HasScore: true, PV: []string{"7g7f"}})
	p = a.snapshot()
	if len(p.Lines) != 1 || p.Depth != 4 {
		t.Errorf("深さが変わったのに前の候補が残っています: depth=%d lines=%+v", p.Depth, p.Lines)
	}
}

// 評価値も詰みも無い行（info string など）で表示を動かさないこと。
func TestAccumulatorIgnoresScorelessInfo(t *testing.T) {
	a := &accumulator{black: true, started: time.Now()}
	if _, ok := a.add(coreusi.Info{Text: "hello"}); ok {
		t.Error("info string で表示が更新されています")
	}
	if _, ok := a.add(coreusi.Info{Depth: 3, CurrMove: "7g7f"}); ok {
		t.Error("currmove だけの行で表示が更新されています")
	}
}
