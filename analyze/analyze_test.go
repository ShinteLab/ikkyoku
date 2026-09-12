package analyze

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreusi "github.com/ShinteLab/core/usi"
	"github.com/ShinteLab/core/usi/client"
	localusi "github.com/ShinteLab/ikkyoku/usi"
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
	// ⚠️ **接続は解析をまたいで生き続ける**（2026-08-12）ので、後始末が要る。
	s := NewLocalSession()
	t.Cleanup(s.Close)
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

// ⚠️ **接続は解析をまたいで使い回す**（2026-08-12。以前は解析ごとに開き直していた）。
//
// **連続モードと全て解析がこれを要求する** —— 1 手ごとに解析し直すので、
// 開き直していると `isready`（NNUE の読み込み。数秒になりうる）を手数ぶん払う。
// **ここが 2 に戻ったら、151 手の棋譜の解析が待ち時間だらけになる。**
func TestSessionReusesConnection(t *testing.T) {
	var opens int32
	s := newSession(func(ctx context.Context) (*client.Session, error) {
		atomic.AddInt32(&opens, 1)
		return localusi.Local(ctx)
	})

	for i := range 2 {
		r, err := s.Analyze(context.Background(), startpos, Options{Movetime: testMovetime}, nil)
		if err != nil {
			t.Fatalf("%d 回目: %v", i+1, err)
		}
		// **2 回目は起動を払っていない。** `StartupMS` が 0 の理由がこれで、
		// 「速かった」と区別できないと画面で読めなくなる。
		if want := i > 0; r.Reused != want {
			t.Errorf("%d 回目の Reused = %v, want %v", i+1, r.Reused, want)
		}
	}
	if got := atomic.LoadInt32(&opens); got != 1 {
		t.Errorf("接続を開いた回数 = %d, want 1（解析をまたいで使い回す）", got)
	}

	// ⚠️ **閉じたら次で開き直すこと**（解析タブを離れて戻ってきた場合）。
	s.Close()
	if _, err := s.Analyze(context.Background(), startpos, Options{Movetime: testMovetime}, nil); err != nil {
		t.Fatalf("閉じたあとの解析: %v", err)
	}
	if got := atomic.LoadInt32(&opens); got != 2 {
		t.Errorf("閉じたあとに開き直していません: 開いた回数 = %d, want 2", got)
	}
	s.Close()

	// 繋いでいなくても、最後に繋がった名前は表示のために残す。
	if s.EngineName() == "" {
		t.Error("エンジン名が残っていません")
	}
}

// ⚠️ **1 接続 1 探索。** 使い回す以上、重ねて走らせられない
// （**前の bestmove が返る前に次の position を送ると噛み合わなくなる**）。
// 「止めて即次」を繰り返す連続モードと全て解析がまさにこれを踏む。
func TestSessionSerializesAnalyze(t *testing.T) {
	s := newTestSession(t)

	var running, maxRunning int32
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Analyze(context.Background(), startpos, Options{Movetime: testMovetime},
				func(Progress) {
					n := atomic.AddInt32(&running, 1)
					for {
						old := atomic.LoadInt32(&maxRunning)
						if n <= old || atomic.CompareAndSwapInt32(&maxRunning, old, n) {
							break
						}
					}
					atomic.AddInt32(&running, -1)
				})
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&maxRunning); got > 1 {
		t.Errorf("同時に走った探索 = %d, want 1（1 接続 1 探索）", got)
	}
}

// 起動にかかった時間が出ること（**最初の 1 回で払うコスト**なので見せる）。
func TestAnalyzeReportsStartupTime(t *testing.T) {
	s := newTestSession(t)
	r, err := s.Analyze(context.Background(), startpos, Options{Movetime: testMovetime}, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if r.StartupMS < 0 {
		t.Errorf("StartupMS = %d", r.StartupMS)
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
			if got := newScore(in, tt.black, 0); got.CP != tt.want {
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
		if s := newScore(coreusi.Info{ScoreMate: 5, HasMate: true}, true, 0); s.Mate != 5 {
			t.Errorf("Mate = %d, want 5", s.Mate)
		}
		// 後手番で「手番側が詰ます」= 先手が詰まされる。
		if s := newScore(coreusi.Info{ScoreMate: 5, HasMate: true}, false, 0); s.Mate != -5 {
			t.Errorf("Mate = %d, want -5", s.Mate)
		}
	})
	t.Run("score cp に詰みスコアを詰めるエンジン", func(t *testing.T) {
		in := coreusi.Info{ScoreCP: engineMateScore - 5, HasScore: true}
		s := newScore(in, true, 0)
		if s.Mate != 5 {
			t.Errorf("Mate = %d, want 5", s.Mate)
		}
		if s.CP != 0 {
			t.Errorf("詰みのとき CP は 0 のままにすること: %d", s.CP)
		}
	})
}

// 玉が揃っていない局面をエンジンに渡してよいかの線引き（2026-09-12）。
//
// ⚠️ **「攻方の玉が無い」＝詰将棋は、同梱エンジンなら渡す。** 実測で落ちず、
// 1 手詰を詰みスコアで見つける（攻方の玉を隅に置いた版と手もスコアも一致した）。
// ⚠️ **外部エンジンには渡さない** —— 両玉を前提にしているエンジンがあり、
// 落ちるか出鱈目を返す。**測っていないものは安全側に倒す。**
// ⚠️ **両方無い・2 枚ある局面はどちらにも渡さない**（詰将棋ではない）。
func TestEnsurePlayable(t *testing.T) {
	const (
		bothKings = "lnsgkgsnl/1r5b1/ppppppppp/9/9/9/PPPPPPPPP/1B5R1/LNSGKGSNL"
		noBlack   = "4k4/9/9/9/9/9/9/9/9" // 詰将棋（攻方の玉が無い）
		noKings   = "9/9/9/9/9/9/9/9/9"
		twoBlack  = "4k4/9/9/9/9/9/9/9/3KK4"
	)
	for _, tt := range []struct {
		name     string
		board    string
		kingless bool
		ok       bool
	}{
		{"両玉・同梱", bothKings, true, true},
		{"両玉・外部", bothKings, false, true},
		{"攻方の玉なし・同梱", noBlack, true, true},
		{"攻方の玉なし・外部", noBlack, false, false},
		{"両方なし・同梱", noKings, true, false},
		{"玉が 2 枚・同梱", twoBlack, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ensurePlayable(tt.board, tt.kingless)
			if tt.ok && err != nil {
				t.Fatalf("通るはずが断られました: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("断るはずが通りました")
			}
			if err != nil && !strings.Contains(err.Error(), "玉") {
				t.Errorf("理由が伝わりません: %v", err)
			}
		})
	}
}

// ⚠️ **攻方の玉が無い詰将棋を、同梱エンジンが解けること**（2026-09-12）。
//
// ここが通らなくなったら「詰将棋を並べる」は**並べるだけ**になる。
// 見ているのは 2 つ: **断られないこと**と、**詰みを詰みとして返すこと**
// （評価値そのものは詰将棋では意味を持たない —— 慣習で玉方が余り駒を全部
// 持つので、材料では常に玉方が大優勢に出る。**意味があるのは詰みかどうかだけ**）。
func TestAnalyzeMateProblemWithoutAttackerKing(t *testing.T) {
	// 1 手詰（▲5二金打まで）。後手玉 5一・先手歩 5三・先手の持駒は金 1 枚で、
	// 残りは全部 後手（玉方）の持駒という詰将棋の形。
	const mateInOne = "4k4/9/4P4/9/9/9/9/9/9 b G2r2b3g4s4n4l17p 1"

	s := newTestSession(t)
	r, err := s.Analyze(context.Background(), mateInOne, Options{Movetime: testMovetime}, nil)
	if err != nil {
		t.Fatalf("詰将棋が断られました: %v", err)
	}
	if len(r.Lines) == 0 {
		t.Fatal("読み筋が 1 つも返りません")
	}
	if got := r.Lines[0].Score.Mate; got == 0 {
		t.Errorf("詰みとして返りません: score=%+v bestmove=%s", r.Lines[0].Score, r.Bestmove)
	}
	if r.Bestmove != "G*5b" {
		t.Errorf("bestmove = %q, want %q", r.Bestmove, "G*5b")
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

	// ⚠️ **深さが進んでも候補は消えない。** 順位ごとに上書きするだけなので、
	// まだ更新されていない順位は 1 つ前の深さの値のまま残る。
	// **揃うまで待つと候補が消える**（それが以前の壊れ方だった）。
	live, ok := a.add(coreusi.Info{Depth: 4, MultiPV: 1, ScoreCP: 120, HasScore: true, PV: []string{"7g7f"}})
	if !ok {
		t.Fatal("更新が表に出ていません")
	}
	if len(live.Lines) != 2 {
		t.Fatalf("深さが進んだら候補が減りました: %+v", live.Lines)
	}
	if live.Depth != 4 {
		t.Errorf("深さ = %d, want 4", live.Depth)
	}
	// 更新された順位だけ新しい深さになっていること（**候補ごとに違ってよい**）。
	if live.Lines[0].Depth != 4 || live.Lines[1].Depth != 3 {
		t.Errorf("候補ごとの深さが違います: %+v", live.Lines)
	}
	if live.Lines[0].Score.CP != 120 || live.Lines[1].Score.CP != 50 {
		t.Errorf("順位ごとの上書きになっていません: %+v", live.Lines)
	}
}

// ⚠️ **MultiPV の info は順位の順にも深さの順にも並ばない。**
//
// 実測で `multipv 2` → `multipv 1` → `multipv 3` の順に、しかも深さがばらついて
// 届く。以前は「深さが変わったら全部捨てる」だったので、**そのたびに 1 本だけの
// 状態に戻り、最後に来た順位しか残らなかった**（「三番手の手だけが残る」）。
func TestAccumulatorKeepsAllRanksWhenInfoIsOutOfOrder(t *testing.T) {
	a := &accumulator{black: true, started: time.Now(), sfen: startpos}
	// 順位もばらばら、深さもばらばらに届く。
	a.add(coreusi.Info{Depth: 21, MultiPV: 2, ScoreCP: 50, HasScore: true, PV: []string{"2g2f"}})
	a.add(coreusi.Info{Depth: 20, MultiPV: 1, ScoreCP: 100, HasScore: true, PV: []string{"7g7f"}})
	a.add(coreusi.Info{Depth: 21, MultiPV: 3, ScoreCP: 10, HasScore: true, PV: []string{"5g5f"}})

	p := a.snapshot()
	if len(p.Lines) != 3 {
		t.Fatalf("候補が %d 本。3 本とも残るはず: %+v", len(p.Lines), p.Lines)
	}
	for i, want := range []int{1, 2, 3} {
		if p.Lines[i].Rank != want {
			t.Fatalf("順位で並んでいません: %+v", p.Lines)
		}
	}
	// 深さが下がった報告（順位 1 の depth 20）でも候補そのものは残ること。
	if p.Lines[0].Score.CP != 100 {
		t.Errorf("順位 1 が反映されていません: %+v", p.Lines[0])
	}
	// 全体の深さは一番深いところ（下がらない）。
	if p.Depth != 21 {
		t.Errorf("深さ = %d, want 21", p.Depth)
	}
}

// 古い深さの報告で新しい値を上書きしないこと。
func TestAccumulatorIgnoresStaleDepth(t *testing.T) {
	a := &accumulator{black: true, started: time.Now(), sfen: startpos}
	a.add(coreusi.Info{Depth: 20, MultiPV: 1, ScoreCP: 100, HasScore: true, PV: []string{"7g7f"}})
	a.add(coreusi.Info{Depth: 21, MultiPV: 1, ScoreCP: 200, HasScore: true, PV: []string{"2g2f"}})
	// 遅れて届いた深さ 20 の報告。**捨てること。**
	if _, ok := a.add(coreusi.Info{Depth: 20, MultiPV: 1, ScoreCP: 100, HasScore: true, PV: []string{"7g7f"}}); ok {
		t.Error("古い深さの報告で表示が更新されています")
	}
	p := a.snapshot()
	if p.Lines[0].Score.CP != 200 {
		t.Errorf("古い深さの値で上書きされました: %+v", p.Lines[0])
	}
}

// ⚠️ **順位 1 が届いていなくても候補を落とさないこと。**
//
// 以前は順位を「1 から本数まで」で回しており、**順位 1 が無いと候補が丸ごと消え**、
// 順位が本数を超えるものは黙って落ちていた（エンジンが順位 1 から順に送ってくるとは
// 限らない）。
func TestAccumulatorKeepsOutOfOrderRanks(t *testing.T) {
	a := &accumulator{black: true, started: time.Now(), sfen: startpos}
	a.add(coreusi.Info{Depth: 3, MultiPV: 2, ScoreCP: 50, HasScore: true, PV: []string{"2g2f"}})
	p := a.snapshot()
	if len(p.Lines) != 1 || p.Lines[0].Rank != 2 {
		t.Fatalf("順位 2 だけの候補が消えています: %+v", p.Lines)
	}

	a.add(coreusi.Info{Depth: 3, MultiPV: 3, ScoreCP: 10, HasScore: true, PV: []string{"5g5f"}})
	a.add(coreusi.Info{Depth: 3, MultiPV: 1, ScoreCP: 100, HasScore: true, PV: []string{"7g7f"}})
	p = a.snapshot()
	if len(p.Lines) != 3 {
		t.Fatalf("候補が %d 本。3 本とも出るはず: %+v", len(p.Lines), p.Lines)
	}
	for i, want := range []int{1, 2, 3} {
		if p.Lines[i].Rank != want {
			t.Errorf("順位で並んでいません: %+v", p.Lines)
			break
		}
	}
}

// ⚠️ **読み筋の無い更新で、既に出している読み筋を消さないこと。**
//
// エンジンは探索の終わり際に「評価値だけ更新して読み筋を書かない info」を出すことが
// ある（`stop` を受けた直後が特にそう）。そのまま上書きすると**候補手が画面から消え、
// 打ち切ったあとに何が最善だったのか分からなくなる**（実際にそうなっていた）。
func TestAccumulatorKeepsPVOnScoreOnlyUpdate(t *testing.T) {
	a := &accumulator{black: true, started: time.Now(), sfen: startpos}
	a.add(coreusi.Info{Depth: 3, MultiPV: 1, ScoreCP: 100, HasScore: true, PV: []string{"7g7f", "3c3d"}})
	// 読み筋の無い更新（評価値だけ）。
	a.add(coreusi.Info{Depth: 3, MultiPV: 1, ScoreCP: 110, HasScore: true})

	p := a.snapshot()
	if len(p.Lines) != 1 {
		t.Fatalf("候補が消えています: %+v", p.Lines)
	}
	if got := p.Lines[0].Score.CP; got != 110 {
		t.Errorf("評価値 = %d, want 110（新しい値を採ること）", got)
	}
	if len(p.Lines[0].Moves) != 2 {
		t.Errorf("読み筋が消えています: %+v", p.Lines[0].Moves)
	}
	if len(p.Lines[0].Text) != 2 || p.Lines[0].Text[0] != "▲７六歩" {
		t.Errorf("日本語表記が消えています: %+v", p.Lines[0].Text)
	}
}

// 読み筋は**日本語表記でも**返すこと。
//
// USI の手には駒種が書いていない（"8h2b+" のどこにも「角」が無い）ので、解析した
// 局面から 1 手ずつ盤を進めて割り出す。⚠️ **開始局面を渡し忘れると全部おかしくなる**
// のに、USI 表記のほうは正しいままなので画面を見ても気づきにくい。
func TestAccumulatorNamesPV(t *testing.T) {
	a := &accumulator{black: true, started: time.Now(), sfen: startpos}
	a.add(coreusi.Info{
		Depth: 3, MultiPV: 1, ScoreCP: 100, HasScore: true,
		PV: []string{"7g7f", "3c3d", "8h2b+", "3a2b"},
	})
	p := a.snapshot()
	if len(p.Lines) != 1 {
		t.Fatalf("候補が %d 本", len(p.Lines))
	}
	want := []string{"▲７六歩", "△３四歩", "▲２二角成", "△同　銀"}
	got := p.Lines[0].Text
	if len(got) != len(want) {
		t.Fatalf("Text = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Text[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// **USI 表記も残すこと**（手を辿るのに使うのはこちら）。
	if len(p.Lines[0].Moves) != len(want) {
		t.Errorf("Moves = %q（日本語にしたあとも USI を残すこと）", p.Lines[0].Moves)
	}
}

// 後手番の局面から始まる読み筋でも記号が正しく付くこと。
//
// ⚠️ **先手番だけを前提にすると、半分の局面で ▲△ が入れ替わる。**
func TestAccumulatorNamesPVFromWhite(t *testing.T) {
	a := &accumulator{black: false, started: time.Now(),
		sfen: "lnsgkgsnl/1r5b1/ppppppppp/9/9/2P6/PP1PPPPPP/1B5R1/LNSGKGSNL w - 2"}
	a.add(coreusi.Info{Depth: 3, MultiPV: 1, ScoreCP: 10, HasScore: true, PV: []string{"3c3d"}})
	p := a.snapshot()
	if got := p.Lines[0].Text; len(got) != 1 || got[0] != "△３四歩" {
		t.Errorf("Text = %q, want [△３四歩]", got)
	}
}

// 表記にできない手があっても読み筋を捨てないこと（設計原則3）。
//
// **評価値は正しく出ているのに、表記の都合で候補ごと消えるのが一番困る。**
func TestAccumulatorKeepsPVWhenNamingFails(t *testing.T) {
	a := &accumulator{black: true, started: time.Now(), sfen: startpos}
	// 5e は空マスなので名付けられない。
	a.add(coreusi.Info{Depth: 3, MultiPV: 1, ScoreCP: 100, HasScore: true,
		PV: []string{"7g7f", "5e5d"}})
	p := a.snapshot()
	if len(p.Lines) != 1 {
		t.Fatalf("候補が消えています: %+v", p.Lines)
	}
	got := p.Lines[0].Text
	if len(got) != 2 {
		t.Fatalf("Text = %q, want 2 件", got)
	}
	if got[0] != "▲７六歩" {
		t.Errorf("Text[0] = %q, want %q", got[0], "▲７六歩")
	}
	if got[1] != "5e5d" {
		t.Errorf("Text[1] = %q（読めない手は USI のまま出すこと）", got[1])
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

// 手を進めた局面を `position sfen <根> moves ...` として渡せること。
//
// **エンジンに渡すのは根 + 手順。** 局面を組み立て直して渡すと、千日手と
// 連続王手をエンジンが判定できなくなる。
func TestAnalyzeWithMoves(t *testing.T) {
	s := NewLocalSession()
	r, err := s.Analyze(context.Background(), startpos,
		Options{Movetime: testMovetime, Moves: []string{"7g7f", "3c3d"}}, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// **手番は解析する局面のもの**（2 手進んだので先手番のまま）。
	if r.Turn != "b" {
		t.Errorf("Turn = %q, want %q", r.Turn, "b")
	}
	if r.Bestmove == "" {
		t.Error("bestmove が返っていません")
	}
}

// ⚠️ **奇数手進めたら手番が入れ替わること。** ここを根の手番のままにすると、
// **奇数手進めたときだけ評価値の符号が逆に見える**（画面では気づけない）。
func TestAnalyzeMovesFlipTurn(t *testing.T) {
	s := NewLocalSession()
	r, err := s.Analyze(context.Background(), startpos,
		Options{Movetime: testMovetime, Moves: []string{"7g7f"}}, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if r.Turn != "w" {
		t.Errorf("Turn = %q, want %q（1 手進めたら後手番）", r.Turn, "w")
	}
}

// 読み筋の日本語表記は**進めたあとの局面から**始まること。
//
// ⚠️ **根からいきなり読み筋を流すと、別の盤の上で名付けることになる**
// （駒種も「同」も全部おかしくなるのに、USI のほうは正しいままなので
// 画面を見ても気づけない）。
func TestAccumulatorNamesPVAfterMoves(t *testing.T) {
	a := &accumulator{
		black: true, started: time.Now(), sfen: startpos,
		played: []string{"7g7f", "3c3d"},
	}
	a.add(coreusi.Info{Depth: 3, MultiPV: 1, ScoreCP: 100, HasScore: true,
		PV: []string{"8h2b+", "3a2b"}})
	p := a.snapshot()
	if len(p.Lines) != 1 {
		t.Fatalf("候補が %d 本", len(p.Lines))
	}
	want := []string{"▲２二角成", "△同　銀"}
	got := p.Lines[0].Text
	if len(got) != len(want) {
		t.Fatalf("Text = %q, want %q（指し終わったぶんを捨てること）", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Text[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// ⚠️ **`lowerbound` / `upperbound` の速報で読み筋を潰さないこと**（2026-08-13）。
//
// あれは探索の途中で「この手はこれ以上（以下）」と分かっただけの報告で、
// **評価値は確定値ではなく、読み筋も 1 手しか付かないのが普通**。取り込むと
// **画面の読み筋がその 1 手に化ける**（実際に「最善手の読み筋だけが消える」
// という形で出た。USI のやり取りには正しい読み筋が流れているのでログでは分からない）。
//
// ⚠️ **`lastPV`（読み筋の無い更新で消さない仕掛け）では救えない** ——
// 1 手だけの読み筋は「有る」ので通ってしまう。
func TestAccumulatorIgnoresBoundReports(t *testing.T) {
	a := &accumulator{black: true, started: time.Now(), sfen: startpos}
	a.add(coreusi.Info{
		Depth: 12, MultiPV: 1, ScoreCP: 100, HasScore: true,
		PV: []string{"7g7f", "3c3d", "2g2f"},
	})
	// 次の深さの速報（読み筋は 1 手だけ・評価値は確定値ではない）。
	if _, ok := a.add(coreusi.Info{
		Depth: 13, MultiPV: 1, ScoreCP: 237, HasScore: true, LowerBound: true,
		PV: []string{"9i9h"},
	}); ok {
		t.Error("速報を表示の更新として通しました")
	}
	if _, ok := a.add(coreusi.Info{
		Depth: 13, MultiPV: 1, ScoreCP: -80, HasScore: true, UpperBound: true,
		PV: []string{"9i9h"},
	}); ok {
		t.Error("速報を表示の更新として通しました")
	}

	p := a.snapshot()
	if len(p.Lines) != 1 {
		t.Fatalf("候補が消えています: %+v", p.Lines)
	}
	if len(p.Lines[0].Moves) != 3 {
		t.Fatalf("読み筋が速報で潰れました: %+v", p.Lines[0].Moves)
	}
	if got := p.Lines[0].Score.CP; got != 100 {
		t.Errorf("評価値 = %d, want 100（確定値だけを採ること）", got)
	}

	// 確定値が来たら、そちらは採ること。
	a.add(coreusi.Info{
		Depth: 13, MultiPV: 1, ScoreCP: 180, HasScore: true,
		PV: []string{"9i9h", "3c3d", "2g2f", "8c8d"},
	})
	p = a.snapshot()
	if p.Lines[0].Score.CP != 180 || len(p.Lines[0].Moves) != 4 {
		t.Errorf("確定値が採れていません: %+v", p.Lines[0])
	}
}

// 詰み探索にかけられる局面かの線引き（2026-09-12）。
//
// ⚠️ **`ensurePlayable` とは条件が違う。** 詰将棋は**攻方の玉が無いのが普通**なので、
// 要るのは**詰ませる相手（玉方の玉）**だけ。
func TestEnsureMateTarget(t *testing.T) {
	for _, tt := range []struct {
		name  string
		board string
		black bool // 攻方が先手か
		ok    bool
	}{
		{"攻方の玉なし・玉方あり", "4k4/9/4P4/9/9/9/9/9/9", true, true},
		{"両玉あり", "4k4/9/4P4/9/9/9/9/9/8K", true, true},
		{"玉方が居ない", "9/9/9/9/9/9/9/9/4K4", true, false},
		{"どちらも居ない", "9/9/9/9/9/9/9/9/9", true, false},
		{"攻方の玉が 2 枚", "4k4/9/9/9/9/9/9/9/3KK4", true, false},
		{"後手が攻方（玉方は先手）", "4K4/9/4p4/9/9/9/9/9/9", false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ensureMateTarget(tt.board, tt.black)
			if tt.ok && err != nil {
				t.Fatalf("通るはずが断られました: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("断るはずが通りました")
			}
		})
	}
}

// ⚠️ **同梱エンジンは詰み探索に対応していない**（`checkmate notimplemented`）。
//
// **それでも経路は通ること**を見ている —— `go mate` を送り、答えを読み、
// **「非対応」として返る**。ここが黙って固まると、詰将棋エンジンを繋ぐまで
// 画面が止まったままになる。
func TestMateNotImplementedOnBundledEngine(t *testing.T) {
	const mateInOne = "4k4/9/4P4/9/9/9/9/9/9 b G2r2b3g4s4n4l17p 1"

	s := newTestSession(t)
	r, err := s.Mate(context.Background(), mateInOne, MateOptions{Limit: 2 * time.Second}, nil)
	if err != nil {
		t.Fatalf("Mate: %v", err)
	}
	if r.Kind != MateNotImplemented {
		t.Errorf("Kind = %q, want %q（同梱エンジンは詰み探索を持たない）", r.Kind, MateNotImplemented)
	}
	if len(r.Moves) != 0 {
		t.Errorf("Moves = %v（非対応なのに手順が入っている）", r.Moves)
	}
}

// 詰ませる相手が居ない局面は、エンジンに渡す前に断ること。
func TestMateRejectsWithoutDefender(t *testing.T) {
	s := newTestSession(t)
	_, err := s.Mate(context.Background(), "9/9/9/9/9/9/9/9/4K4 b 2r2b4g4s4n4l18p 1",
		MateOptions{Limit: time.Second}, nil)
	if err == nil {
		t.Fatal("玉方が居ないのに通りました")
	}
}
