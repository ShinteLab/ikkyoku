package app

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ShinteLab/ikkyoku/followcam"
)

// fakeCamera は中継セッションの撮る側の偽物（台本どおりに 1 枚ずつ返す）。台本が尽きたら「変わっていない」を返し続ける。
type fakeCamera struct {
	mu        sync.Mutex
	anchor    followcam.Shot
	anchorErr error
	shots     []followcam.Shot
	// hold が nil でなければ、CaptureQuiet はそれが閉じるまで返らない（走っている 1 周を作る）。
	hold      chan struct{}
	entered   chan struct{}
	following []bool
	ended     int
	misses    []string
	frames    int
}

func (c *fakeCamera) AnchorBoard() (followcam.Shot, error) { return c.anchor, c.anchorErr }
func (c *fakeCamera) CaptureQuiet() (followcam.Shot, error) {
	c.mu.Lock()
	hold, entered := c.hold, c.entered
	c.mu.Unlock()
	if hold != nil {
		if entered != nil {
			close(entered)
			c.mu.Lock()
			c.entered = nil
			c.mu.Unlock()
		}
		<-hold
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.shots) == 0 {
		return followcam.Shot{Skipped: "unchanged"}, nil
	}
	s := c.shots[0]
	c.shots = c.shots[1:]
	return s, nil
}
func (c *fakeCamera) RecognizeQuiet() (followcam.Shot, error) {
	return followcam.Shot{}, errors.New("読み直す 1 枚がありません")
}
func (c *fakeCamera) ReadCells([]int) ([]followcam.CellPiece, error) { return nil, nil }
func (c *fakeCamera) ComparePieces(pairs []followcam.CellPair) []float64 {
	return make([]float64, len(pairs))
}
func (c *fakeCamera) Judge(followcam.Verdict)             {}
func (c *fakeCamera) SaveFollowFrame(int, []string, bool) { c.mu.Lock(); c.frames++; c.mu.Unlock() }
func (c *fakeCamera) SaveFollowMiss(_ int, kind string) {
	c.mu.Lock()
	c.misses = append(c.misses, kind)
	c.mu.Unlock()
}
func (c *fakeCamera) BeginFollow() {
	c.mu.Lock()
	c.following = append(c.following, true)
	c.mu.Unlock()
}
func (c *fakeCamera) EndFollow() {
	c.mu.Lock()
	c.following = append(c.following, false)
	c.ended++
	c.mu.Unlock()
}

// sessionUpdates は `follow:update` を溜める。
type sessionUpdates struct {
	mu  sync.Mutex
	all []FollowUpdate
}

func (u *sessionUpdates) emit(name string, data any) {
	if name != "follow:update" {
		return
	}
	u.mu.Lock()
	u.all = append(u.all, data.(FollowUpdate))
	u.mu.Unlock()
}

func (u *sessionUpdates) last() FollowUpdate {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.all) == 0 {
		return FollowUpdate{}
	}
	return u.all[len(u.all)-1]
}

func (u *sessionUpdates) any(f func(FollowUpdate) bool) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.ContainsFunc(u.all, f)
}

// sessionFollowing は追従を始められる解析タブ（平手）と偽の撮る側・様子の受け手を作る。
func sessionFollowing(t *testing.T, cam *fakeCamera) (*StudyService, *sessionUpdates) {
	t.Helper()
	s, _ := following(t)
	u := &sessionUpdates{}
	s.Emit = u.emit
	s.Camera = cam
	t.Cleanup(s.StopFollow)
	return s, u
}

// waitFor は cond が真になるまで待つ（2 秒で諦める）。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s まで待ちましたが、そうなりませんでした", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ⚠️ **中継セッションは、押したときの 1 枚を最初の 1 周に流し、次の 1 枚が変わっていなければ確かめて足すこと**
// （2026-10-11。1 周の段取りを画面から Go へ移した。`followsession.go`）。止めたら追う盤を忘れ、撮る側の入り切りを戻すこと。
func TestFollowSessionAddsAfterTwoFrames(t *testing.T) {
	board := boardAfter(t, "7g7f")
	cam := &fakeCamera{anchor: followcam.Shot{SFEN: board, CellConfidence: realConf(board, 0.9)}}
	s, u := sessionFollowing(t, cam)

	if err := s.StartFollow(); err != nil {
		t.Fatalf("StartFollow: %v", err)
	}
	if !u.last().On {
		t.Fatalf("始めたのに追っている様子が流れていません: %+v", u.last())
	}
	waitFor(t, "▲7六歩 が足される", func() bool { return slices.Equal(mainLine(s), []string{"7g7f"}) })
	waitFor(t, "足したあとの様子が流れる", func() bool {
		return u.any(func(x FollowUpdate) bool { return x.On && x.Status == followOk && x.Waiting == 2 })
	})

	s.StopFollow()
	if last := u.last(); last.On || last.Notice == nil || last.Notice.Text != "中継の追跡を止めました" || !last.Notice.Fade {
		t.Fatalf("止めた様子が流れていません: %+v", last)
	}
	cam.mu.Lock()
	defer cam.mu.Unlock()
	if cam.ended != 1 || !slices.Equal(cam.following, []bool{true, false}) {
		t.Fatalf("撮る側の持ち物を捨てていないか、入り切りが戻っていません: ended=%d following=%v", cam.ended, cam.following)
	}
	if cam.frames != 1 {
		t.Errorf("手を決めた 1 枚を録画していません: frames=%d", cam.frames)
	}
}

// ⚠️ **繋がらない周が続いたら「見失っています」と言うこと**（`followLostLimit`。1 周では言わない）。
func TestFollowSessionSaysLostAfterLimit(t *testing.T) {
	// 歩が一気に 2 マス進んだ盤（どの手順でも繋がらない。費用表が無いので修復もしない）。
	far := strings.Replace(hirateBoard, "PPPPPPPPP", "PPPPPP1PP", 1)
	far = strings.Replace(far, "/9/9/9/", "/9/9/2P6/", 1)
	cam := &fakeCamera{anchor: followcam.Shot{SFEN: hirateBoard}}
	for i := 0; i < followLostLimit+1; i++ {
		cam.shots = append(cam.shots, followcam.Shot{SFEN: far})
	}
	s, u := sessionFollowing(t, cam)
	if err := s.StartFollow(); err != nil {
		t.Fatalf("StartFollow: %v", err)
	}
	waitFor(t, "見失っていると言う", func() bool {
		return u.any(func(x FollowUpdate) bool {
			return strings.HasPrefix(x.Status, "中継の手順を見失っています") && x.StatusKind == "warn"
		})
	})
	if got := mainLine(s); len(got) != 0 {
		t.Fatalf("繋がらない盤から手を足しました: %v", got)
	}
}

// ⚠️ **追う盤が決まらなければ、理由を出して止まること**（黙って始めない）。
func TestFollowSessionStopsWhenAnchorFails(t *testing.T) {
	cam := &fakeCamera{anchorErr: errors.New("枠の中に盤が映っていません")}
	s, u := sessionFollowing(t, cam)
	if err := s.StartFollow(); err != nil {
		t.Fatalf("StartFollow: %v", err)
	}
	waitFor(t, "止まる", func() bool { return !u.last().On && u.last().Notice != nil })
	if last := u.last(); last.Notice.Text != "枠の中に盤が映っていません" || last.Notice.Kind != "error" || last.Notice.Fade {
		t.Fatalf("理由を出して止まっていません: %+v", last.Notice)
	}
}

// ⚠️ **`StopFollow` は走っている 1 周が終わるまで待ってから返ること**（2026-10-11）。画面はこれを待ってから根を入れ替える
// 操作を通す（`followguard.ts`）—— 待たないと、走っている 1 周が入れ替えた局面へ手を足す。
func TestFollowSessionStopWaitsForCycle(t *testing.T) {
	cam := &fakeCamera{anchor: followcam.Shot{Skipped: "unchanged"}, hold: make(chan struct{}), entered: make(chan struct{})}
	entered := cam.entered
	s, _ := sessionFollowing(t, cam)
	if err := s.StartFollow(); err != nil {
		t.Fatalf("StartFollow: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatalf("1 周目が撮りに来ません")
	}
	stopped := make(chan struct{})
	go func() {
		s.StopFollow()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatalf("走っている 1 周が終わる前に StopFollow が返りました")
	case <-time.After(100 * time.Millisecond):
	}
	close(cam.hold)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatalf("1 周が終わっても StopFollow が返りません")
	}
}

// ⚠️ **撮る側が無ければ始めないこと**（`Camera` が nil。ほかは今までどおり動く）。
func TestFollowSessionNeedsCamera(t *testing.T) {
	s, _ := following(t)
	if err := s.StartFollow(); err == nil {
		t.Fatalf("撮る側が無いのに始めました")
	}
}

// ⚠️ **答えの控えは追い始めと止めたときに捨てること**（2026-10-11。`follower.resetLocked`）。前の回の控えを、次の回の
// 最初の「変わっていない」1 枚で足さない。
func TestFollowSessionDropsHoldsAtStartAndStop(t *testing.T) {
	board := boardAfter(t, "7g7f")
	cam := &fakeCamera{anchor: followcam.Shot{Skipped: "unchanged"}}
	s, _ := sessionFollowing(t, cam)
	// 追っていないあいだに 1 枚目の答えを控えた（前の回の名残）。
	if a, err := s.FollowAuto(board, realConf(board, 0.9), nil, nil); err != nil || !a.Pending {
		t.Fatalf("前提: 1 枚目で控えるはず: %+v（%v）", a, err)
	}
	held := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.follow.pending != nil
	}
	if err := s.StartFollow(); err != nil {
		t.Fatalf("StartFollow: %v", err)
	}
	if held() {
		t.Fatalf("追い始めても前の回の控えが残っています")
	}
	time.Sleep(300 * time.Millisecond) // 「変わっていない」1 枚が何周か回る
	if got := mainLine(s); len(got) != 0 {
		t.Fatalf("前の回の控えを足しました: %v", got)
	}
	s.StopFollow()
	if held() {
		t.Fatalf("止めたのに控えが残っています")
	}
}
