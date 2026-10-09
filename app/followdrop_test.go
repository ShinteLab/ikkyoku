package app

// 打った駒の種類が決まらないとき（`followdrop.go`。2026-10-09）の歯止め。
//
// ⚠️ **ここが守っているのは 4 つ。**
//   - **打つ駒の種類だけで分かれたら、盤上の同じ側の駒と見た目を比べるよう頼むこと**（中継で香を「歩」と読み、香・桂・銀が
//     同点のまま 2 分待った）
//   - **見た目で決まった種類で、2 枚続けて同じ答えのときに足すこと**（1 枚では足さない、は崩さない）
//   - **決まらなければ人に聞き、選ばれた手は推測の印を付けずに足すこと**（決まらないまま 1 本選ばない）
//   - **比べる駒が盤上に無い種類があれば、比べずに人に聞くこと**（その種類を外す根拠が無い）

import (
	"slices"
	"testing"
)

// dropFrame は 3六 に先手の駒が打たれ、認識器がそれを「先手の歩」と読んだ 1 枚（玉と盤上の香・桂・銀ははっきり読めている）。
const dropFrameBoard = "4k4/9/9/9/9/6P2/9/9/LNS1K4"

func dropFrameConf() []float64 {
	conf := make([]float64, 81)
	for _, i := range []int{4, 51, 72, 73, 74, 76} { // 5一・3六・9九・8九・7九・5九
		conf[i] = 1
	}
	return conf
}

// dropEvidence は頼まれた組を、盤上の駒の種類ごとの近さで測ったことにする（9九=香・8九=桂・7九=銀）。
func dropEvidence(pairs []CellPair, sims map[int]float64) []PromoEvidence {
	out := []PromoEvidence{}
	for _, p := range pairs {
		out = append(out, PromoEvidence{From: p.From, To: p.To, Sim: sims[p.From], Same: p.Same})
	}
	return out
}

func dropStudy(t *testing.T) *StudyService {
	t.Helper()
	return studyAt(t, "4k4/9/9/9/9/9/9/9/LNS1K4 b LNS 1")
}

// followDropOnce は 1 枚を、フロントと同じく「比べるよう頼まれたら測ってもう一度」で渡す。
func followDropOnce(t *testing.T, s *StudyService, sims map[int]float64) FollowAuto {
	t.Helper()
	a, err := s.FollowAuto(dropFrameBoard, dropFrameConf(), nil, nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if len(a.Compare) == 0 {
		return a
	}
	for _, p := range a.Compare {
		if !p.Same {
			t.Fatalf("打った駒の比べは今の 1 枚どうしで頼むこと: %+v", a.Compare)
		}
	}
	a, err = s.FollowAuto(dropFrameBoard, dropFrameConf(), nil, dropEvidence(a.Compare, sims))
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	return a
}

func TestFollowDropDecidedByLooks(t *testing.T) {
	s := dropStudy(t)
	a, err := s.FollowAuto(dropFrameBoard, dropFrameConf(), nil, nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if len(a.Compare) == 0 || a.Applied || a.Pending {
		t.Fatalf("打つ駒の種類で分かれたのに、見た目を比べるよう頼んでいません: %+v", a)
	}
	looksLance := map[int]float64{72: 0.95, 73: 0.55, 74: 0.60}
	a = followDropOnce(t, s, looksLance)
	if !a.Pending || a.Applied {
		t.Fatalf("1 枚目で控えていないか、足しました: %+v", a)
	}
	a = followDropOnce(t, s, looksLance)
	if !a.Applied || !slices.Equal(a.Moves, []string{"L*3f"}) {
		t.Fatalf("2 枚目で ▲3六香打 を足していません: %+v", a)
	}
}

func TestFollowDropAsksWhenLooksUndecided(t *testing.T) {
	s := dropStudy(t)
	a := followDropOnce(t, s, map[int]float64{72: 0.70, 73: 0.68, 74: 0.66}) // 差が付かない
	if a.Applied || a.Pending || len(a.DropChoices) != 3 {
		t.Fatalf("決まらないのに足したか、人に聞いていません: %+v", a)
	}
	var lance []string
	for _, c := range a.DropChoices {
		if c.Moves[0] == "L*3f" {
			lance = c.Moves
		}
	}
	got, err := s.FollowChoose(lance)
	if err != nil || !got.Applied || got.Guess {
		t.Fatalf("選んだ手を印なしで足していません: %+v (%v)", got, err)
	}
	if line := mainLine(s); !slices.Equal(line, []string{"L*3f"}) {
		t.Fatalf("本譜 = %v", line)
	}
	s.mu.Lock()
	tail := s.study.MainGuessTail()
	s.mu.Unlock()
	if len(tail) != 0 {
		t.Errorf("人が選んだ手に推測の印が付いています: %v", tail)
	}
	// 2 度目は断る（聞いた候補はもう使った）。
	if _, err := s.FollowChoose(lance); err == nil {
		t.Error("聞いていない手を足しました")
	}
}

func TestFollowDropAsksWithoutReferencePiece(t *testing.T) {
	// 盤上に先手の銀が無い（7九 が空き）。銀を外す根拠が無いので、比べずに人に聞く。
	s := studyAt(t, "4k4/9/9/9/9/9/9/9/LN2K4 b LNS 1")
	board := "4k4/9/9/9/9/6P2/9/9/LN2K4"
	a, err := s.FollowAuto(board, dropFrameConf(), nil, nil)
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if len(a.Compare) > 0 || len(a.DropChoices) != 3 {
		t.Fatalf("比べる駒の無い種類があるのに比べようとしたか、人に聞いていません: %+v", a)
	}
}
