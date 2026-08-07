package analyze

import (
	"context"
	"strings"
	"testing"
	"time"
)

const startpos = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL b - 1"

// 平手の初期局面を浅く解析できること。**評価値と最善手が出るのが最低条件。**
func TestAnalyzeStartPos(t *testing.T) {
	r, err := Analyze(context.Background(), startpos, Options{Depth: 2}, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if r.Best == "" {
		t.Error("最善手が空です")
	}
	if r.Turn != "b" {
		t.Errorf("Turn = %q, want b", r.Turn)
	}
	if r.Score.Label == "" {
		t.Error("Label が空です")
	}
	// 初期局面は互角なので、大きく振れていたら符号か評価の扱いを疑う。
	if r.Score.Mate != 0 {
		t.Errorf("初期局面が詰みになっています: %+v", r.Score)
	}
}

// ⚠️ **評価値は先手視点。** engine が返すのは手番側視点の negamax 値なので、
// ここを外すと後手番のときだけ符号が逆に見える（画面を見ても気づきにくい）。
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
			if got := newScore(tt.cp, tt.black); got.CP != tt.want {
				t.Errorf("newScore(%d, %v).CP = %d, want %d", tt.cp, tt.black, got.CP, tt.want)
			}
		})
	}
}

// ⚠️ **符号の実地確認。** newScore の単体テストだけだと「engine が実は先手視点で
// 返していた」といった思い違いに気づけない。**駒得している側が有利に出ること**を
// 手番を変えながら確かめる（後手番のときだけ逆になる、が一番ありがちな壊れ方）。
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Analyze(context.Background(), tt.sfen, Options{Depth: 2}, nil)
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if got := r.Score.CP > 0; got != tt.wantPositive {
				t.Errorf("評価値 %d（%s）: 先手有利=%v, want %v",
					r.Score.CP, r.Score.Label, got, tt.wantPositive)
			}
		})
	}
}

// 詰みスコアが手数として出ること（符号は先手視点）。
func TestScoreMate(t *testing.T) {
	const mateScore = 1 << 20
	s := newScore(mateScore-5, true)
	if s.Mate != 5 {
		t.Errorf("Mate = %d, want 5", s.Mate)
	}
	if s.CP != 0 {
		t.Errorf("詰みのとき CP は 0 のままにすること: %d", s.CP)
	}
	// 後手番で「手番側が詰ます」= 先手が詰まされる。
	if s := newScore(mateScore-5, false); s.Mate != -5 {
		t.Errorf("Mate = %d, want -5", s.Mate)
	}
}

// ⚠️ **玉の欠けた局面はエンジンに渡さない**（合法手生成が落ちる）。
// 訂正 UI 側では確定できてよい局面なので、止めるのはこの境界だけ。
func TestAnalyzeRejectsMissingKing(t *testing.T) {
	// 後手玉だけの詰将棋のような局面。
	const noBlackKing = "4k4/9/9/9/9/9/9/9/9 b R2b4g4s4n4l18p 1"
	_, err := Analyze(context.Background(), noBlackKing, Options{Depth: 1}, nil)
	if err == nil {
		t.Fatal("玉が欠けているのにエラーになりませんでした")
	}
	if !strings.Contains(err.Error(), "玉") {
		t.Errorf("理由が伝わりません: %v", err)
	}
}

// 局面が確定していない（手番・持ち駒が無い）SFEN は受け付けないこと。
// **盤面だけの SFEN を黙って先手番として解析しない**（設計原則5）。
func TestAnalyzeRejectsBoardOnlySFEN(t *testing.T) {
	const boardOnly = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"
	if _, err := Analyze(context.Background(), boardOnly, Options{Depth: 1}, nil); err == nil {
		t.Fatal("盤面だけの SFEN が通ってしまいました")
	}
}

// 途中経過が深さごとに届くこと。**終わるまで何も出さないと固まって見える。**
func TestAnalyzeReportsProgress(t *testing.T) {
	var depths []int
	if _, err := Analyze(context.Background(), startpos, Options{Depth: 3}, func(p Progress) {
		depths = append(depths, p.Depth)
	}); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(depths) == 0 {
		t.Fatal("途中経過が 1 度も届きませんでした")
	}
	for i, d := range depths {
		if d != i+1 {
			t.Errorf("深さが 1 から順に来ていません: %v", depths)
			break
		}
	}
}

// 打ち切っても結果が返ること（設計原則3: 段階的に劣化する）。
func TestAnalyzeCanceledStillReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := Analyze(ctx, startpos, Options{Depth: 12, Movetime: time.Second}, nil)
	if err != nil {
		t.Fatalf("打ち切りをエラーにしないこと: %v", err)
	}
	if r.Best == "" {
		t.Error("打ち切っても最善手は返すこと")
	}
}
